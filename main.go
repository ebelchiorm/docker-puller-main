package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

var (
	interval    = flag.Int("interval", 30, "Check interval in seconds")
	cleanup     = flag.Bool("cleanup", false, "Remove old images after pulling")
	labelEnable = flag.Bool("label-enable", false, "Only update containers with enable label")
	verbose     = flag.Bool("verbose", false, "Enable verbose logging")
	quiet       = flag.Bool("quiet", false, "Reduce logging to minimum (only errors and updates)")
	enableLabel = "puller.update.enable"
)

// Logging helpers
func logInfo(format string, v ...interface{}) {
	if !*quiet {
		log.Printf("[INFO] "+format, v...)
	}
}
func logVerbose(format string, v ...interface{}) {
	if *verbose && !*quiet {
		log.Printf("[VERBOSE] "+format, v...)
	}
}
func logWarn(format string, v ...interface{}) {
	log.Printf("[WARN] "+format, v...)
}
func logError(format string, v ...interface{}) {
	log.Printf("[ERROR] "+format, v...)
}
func logUpdate(format string, v ...interface{}) {
	log.Printf("[UPDATE] "+format, v...)
}

func main() {
	flag.Parse()

	registryUser := os.Getenv("REGISTRY_USERNAME")
	registryPass := os.Getenv("REGISTRY_PASSWORD")
	registryURL := os.Getenv("REGISTRY_URL")
	registryTag := os.Getenv("REGISTRY_TAG")
	notificationURL := os.Getenv("NOTIFICATION_URL")

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatalf("Error creating Docker client: %v", err)
	}
	defer cli.Close()

	logInfo("Starting puller service with interval: %ds", *interval)
	logInfo("Cleanup enabled: %v", *cleanup)
	logInfo("Label filtering enabled: %v", *labelEnable)
	if *verbose {
		logInfo("Verbose logging enabled")
	}
	if *quiet {
		logInfo("Quiet mode enabled - only errors and updates will be shown")
	}
	if notificationURL != "" {
		logInfo("Notifications enabled: %s", notificationURL)
	}
	if registryTag != "" {
		logInfo("Additional registry tag to check: %s", registryTag)
	}

	ticker := time.NewTicker(time.Duration(*interval) * time.Second)
	defer ticker.Stop()

	if err := checkContainers(cli, registryURL, registryUser, registryPass, registryTag, notificationURL); err != nil {
		logError("Error in initial check: %v", err)
		notify(notificationURL, "Error in initial check: "+err.Error())
	}

	for range ticker.C {
		if err := checkContainers(cli, registryURL, registryUser, registryPass, registryTag, notificationURL); err != nil {
			logError("Error in check cycle: %v", err)
			notify(notificationURL, "Error in check cycle: "+err.Error())
		}
	}
}

func notify(url, message string) {
	if url == "" {
		return
	}
	resp, err := http.Post(url, "text/plain", strings.NewReader(message))
	if err != nil {
		logError("Error sending notification: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		logWarn("Notification failed with status: %d", resp.StatusCode)
	} else {
		logVerbose("Notification sent successfully")
	}
}

func checkContainers(cli *client.Client, registryURL, user, pass, registryTag, notificationURL string) error {
	ctx := context.Background()

	opts := types.ContainerListOptions{All: true}
	if *labelEnable {
		opts.Filters = filters.NewArgs(filters.Arg("label", enableLabel+"=true"))
	}

	containers, err := cli.ContainerList(ctx, opts)
	if err != nil {
		return fmt.Errorf("error listing containers: %v", err)
	}

	eligibleContainers := 0
	for _, c := range containers {
		imageName := c.Image

		if strings.HasPrefix(imageName, "sha256:") {
			imgInspect, _, err := cli.ImageInspectWithRaw(ctx, c.ImageID)
			if err == nil && len(imgInspect.RepoTags) > 0 {
				imageName = imgInspect.RepoTags[0]
				logVerbose("Resolved digest %s to tag %s", c.Image, imageName)
			}
		}

		if strings.Contains(imageName, registryURL) || strings.Contains(imageName, user) {
			eligibleContainers++
		}
	}

	logVerbose("Found %d total containers, %d eligible for updates", len(containers), eligibleContainers)
	if eligibleContainers == 0 {
		logVerbose("No eligible containers found, skipping check")
		return nil
	}

	var authConfig types.AuthConfig
	if user != "" && pass != "" {
		authConfig = types.AuthConfig{
			Username:      user,
			Password:      pass,
			ServerAddress: registryURL,
		}
		if registryURL == "" || registryURL == "docker.io" || registryURL == "https://docker.io" {
			authConfig.ServerAddress = "https://index.docker.io/v1/"
		}
	} else {
		authConfig = types.AuthConfig{}
	}

	updatedContainers := 0
	skippedContainers := 0

	for _, c := range containers {
		image := c.Image
		name := strings.TrimPrefix(c.Names[0], "/")

		if strings.HasPrefix(image, "sha256:") {
			imgInspect, _, err := cli.ImageInspectWithRaw(ctx, c.ImageID)
			if err == nil && len(imgInspect.RepoTags) > 0 {
				image = imgInspect.RepoTags[0]
				logVerbose("Resolved digest %s to tag %s", c.Image, image)
			}
		}

		imgInspect, _, err := cli.ImageInspectWithRaw(ctx, c.ImageID)
		if err != nil {
			logError("Error inspecting image for %s: %v", name, err)
			continue
		}
		platform := fmt.Sprintf("%s/%s", imgInspect.Os, imgInspect.Architecture)

		tagsToCheck := []string{"latest"}
		if registryTag != "" {
			tagsToCheck = append(tagsToCheck, registryTag)
		}

		needsUpdate := false
		for _, tag := range tagsToCheck {
			imageWithTag := image
			if !strings.Contains(image, ":") {
				imageWithTag = image + ":" + tag
			} else {
				parts := strings.Split(image, ":")
				imageWithTag = parts[0] + ":" + tag
			}

			logVerbose("Checking container %s with tag %s", name, tag)
			updated, err := pullImageAndCheckUpdate(cli, ctx, imageWithTag, authConfig, platform, name, notificationURL, c.ImageID)
			if err != nil {
				logError("Error pulling %s (%s): %v", name, tag, err)
				continue
			}
			if updated {
				needsUpdate = true

				if registryTag != "" && tag == registryTag {
					baseRepo := strings.Split(imageWithTag, ":")[0]

					err := cli.ImageTag(ctx, imageWithTag, baseRepo+":latest")
					if err != nil {
						logWarn("Failed to retag %s as latest: %v", imageWithTag, err)
					} else {
						logUpdate("Retagged %s as latest", imageWithTag)

						_, err := cli.ImageRemove(ctx, imageWithTag, types.ImageRemoveOptions{Force: true, PruneChildren: true})
						if err != nil {
							logWarn("Failed to remove old tag %s: %v", imageWithTag, err)
						} else {
							logUpdate("Removed old tag %s", imageWithTag)
						}
					}
				}
				break
			}
		}

		if !needsUpdate {
			logVerbose("No updates needed for %s", name)
			continue
		}

		logUpdate("Updating container %s with new image", name)

		if err := recreateContainer(cli, ctx, c.ID, name, notificationURL); err != nil {
			logError("Error recreating container %s: %v", name, err)
			continue
		}

		msg := fmt.Sprintf("Successfully updated %s", name)
		logUpdate(msg)
		notify(notificationURL, msg)
		updatedContainers++

		if *cleanup {
			logVerbose("Cleaning up old images")
			pruned, err := cli.ImagesPrune(ctx, filters.NewArgs())
			if err != nil {
				msg := fmt.Sprintf("Error pruning old images: %v", err)
				logWarn(msg)
				notify(notificationURL, msg)
			} else if len(pruned.ImagesDeleted) > 0 {
				logInfo("Cleaned up %d images, reclaimed %d bytes", len(pruned.ImagesDeleted), pruned.SpaceReclaimed)
			}
		}
	}

	if eligibleContainers > 0 {
		if updatedContainers > 0 {
			logInfo("Check completed: %d containers updated, %d skipped", updatedContainers, skippedContainers)
		} else {
			logVerbose("Check completed: no updates needed for %d containers", eligibleContainers)
		}
	}

	return nil
}

func recreateContainer(cli *client.Client, ctx context.Context, containerID, name, notificationURL string) error {
	inspect, err := cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return fmt.Errorf("inspect failed: %w", err)
	}

	timeout := 10
	if err := cli.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &timeout}); err != nil {
		return fmt.Errorf("stop failed: %w", err)
	}

	if err := cli.ContainerRemove(ctx, containerID, types.ContainerRemoveOptions{}); err != nil {
		return fmt.Errorf("remove failed: %w", err)
	}

	resp, err := cli.ContainerCreate(
		ctx,
		inspect.Config,
		inspect.HostConfig,
		&network.NetworkingConfig{EndpointsConfig: inspect.NetworkSettings.Networks},
		nil,
		name,
	)
	if err != nil {
		return fmt.Errorf("create failed: %w", err)
	}

	if err := cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("start failed: %w", err)
	}

	return nil
}

func pullImageAndCheckUpdate(cli *client.Client, ctx context.Context, image string, authConfig types.AuthConfig, platform, name, notificationURL string, currentImgID string) (bool, error) {
	// primeiro inspeciona imagem remota (puxa metadata mas não força update)
	opts := types.ImagePullOptions{}
	if authConfig.Username != "" && authConfig.Password != "" {
		opts.RegistryAuth = encodeAuth(authConfig)
	}
	opts.Platform = platform

	resp, err := cli.ImagePull(ctx, image, opts)
	if err != nil {
		return false, fmt.Errorf("error pulling image: %v", err)
	}
	defer resp.Close()
	_, _ = io.Copy(io.Discard, resp)

	newImg, _, err := cli.ImageInspectWithRaw(ctx, image)
	if err != nil {
		return false, fmt.Errorf("inspect pulled image: %w", err)
	}

	localImg, _, err := cli.ImageInspectWithRaw(ctx, currentImgID)
	if err != nil {
		logWarn("Failed to inspect current image: %v", err)
	}

	// Digest diferente
	if newImg.ID != currentImgID {
		// só troca se a data remota for mais nova
		if localImg.Created != "" && newImg.Created != "" {
			localTime, err1 := time.Parse(time.RFC3339Nano, localImg.Created)
			remoteTime, err2 := time.Parse(time.RFC3339Nano, newImg.Created)

			if err1 == nil && err2 == nil {
				if remoteTime.After(localTime) {
					logVerbose("Image %s has newer push date (remote: %s > local: %s)", name, remoteTime, localTime)
					return true, nil
				}
				logVerbose("Remote image for %s is not newer (remote: %s <= local: %s)", name, remoteTime, localTime)
				return false, nil
			}
		}
	}

	return false, nil
}

func encodeAuth(auth types.AuthConfig) string {
	authJSON, _ := json.Marshal(auth)
	return base64.URLEncoding.EncodeToString(authJSON)
}
