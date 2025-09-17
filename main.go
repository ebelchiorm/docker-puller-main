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

// Log levels
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
	notificationURL := os.Getenv("NOTIFICATION_URL")

	if registryUser == "" || registryPass == "" {
		log.Fatal("REGISTRY_USERNAME and REGISTRY_PASSWORD must be set")
	}

	cli, err := client.NewClientWithOpts(client.FromEnv)
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

	ticker := time.NewTicker(time.Duration(*interval) * time.Second)
	defer ticker.Stop()

	// Run once immediately, then on schedule
	if err := checkContainers(cli, registryURL, registryUser, registryPass, notificationURL); err != nil {
		logError("Error in initial check: %v", err)
		notify(notificationURL, "Error in initial check: "+err.Error())
	}

	for range ticker.C {
		if err := checkContainers(cli, registryURL, registryUser, registryPass, notificationURL); err != nil {
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

func checkContainers(cli *client.Client, registryURL, user, pass, notificationURL string) error {
	ctx := context.Background()

	opts := types.ContainerListOptions{All: true}
	if *labelEnable {
		opts.Filters = filters.NewArgs(filters.Arg("label", enableLabel))
	}

	containers, err := cli.ContainerList(ctx, opts)
	if err != nil {
		return fmt.Errorf("error listing containers: %v", err)
	}

	eligibleContainers := 0
	for _, c := range containers {
		if strings.Contains(c.Image, registryURL) {
			eligibleContainers++
		}
	}

	logVerbose("Found %d total containers, %d eligible for updates", len(containers), eligibleContainers)

	if eligibleContainers == 0 {
		logVerbose("No containers from registry %s found, skipping check", registryURL)
		return nil
	}

	authConfig := types.AuthConfig{
		Username:      user,
		Password:      pass,
		ServerAddress: registryURL,
	}

	updatedContainers := 0
	skippedContainers := 0

	for _, c := range containers {
		image := c.Image
		name := c.Names[0]

		if !strings.Contains(image, registryURL) {
			skippedContainers++
			logVerbose("Skipping %s (%s): not from our registry", name, image)
			continue
		}

		// Inspecionar a imagem para obter informações de plataforma
		imgInspect, _, err := cli.ImageInspectWithRaw(ctx, c.ImageID)
		if err != nil {
			logError("Error inspecting image for %s: %v", name, err)
			continue
		}

		// Obter a plataforma da imagem
		platform := fmt.Sprintf("%s/%s", imgInspect.Os, imgInspect.Architecture)
		logVerbose("Checking container %s (%s) with platform %s", name, image, platform)

		hasUpdate, err := pullImageAndCheckUpdate(cli, ctx, image, authConfig, platform, name, notificationURL)
		if err != nil {
			logError("Error checking updates for %s: %v", name, err)
			continue
		}

		if !hasUpdate {
			logVerbose("No updates needed for %s", name)
			continue
		}

		logUpdate("Restarting container %s with new image", name)
		seconds := 10
		err = cli.ContainerRestart(ctx, c.ID, container.StopOptions{Timeout: &seconds})
		if err != nil {
			msg := fmt.Sprintf("Error restarting %s: %v", name, err)
			logError(msg)
			notify(notificationURL, msg)
			continue
		}

		msg := fmt.Sprintf("Successfully updated %s", name)
		notify(notificationURL, msg)
		logUpdate(msg)
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

	// Summary log only if there were eligible containers
	if eligibleContainers > 0 {
		if updatedContainers > 0 {
			logInfo("Check completed: %d containers updated, %d skipped", updatedContainers, skippedContainers)
		} else {
			logVerbose("Check completed: no updates needed for %d containers", eligibleContainers)
		}
	}

	return nil
}

func pullImageAndCheckUpdate(cli *client.Client, ctx context.Context, image string, authConfig types.AuthConfig, platform, name, notificationURL string) (bool, error) {
	resp, err := cli.ImagePull(ctx, image, types.ImagePullOptions{
		RegistryAuth: encodeAuth(authConfig),
		Platform:     platform,
	})
	if err != nil {
		return false, fmt.Errorf("error pulling image: %v", err)
	}
	defer resp.Close()

	needsUpdate := false
	decoder := json.NewDecoder(resp)
	for {
		var status struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		}
		if err := decoder.Decode(&status); err != nil {
			if err == io.EOF {
				break
			}
			logVerbose("Error decoding pull status: %v", err)
			break
		}
		if status.Error != "" {
			logWarn("Pull error: %s", status.Error)

			// Se falhar com a plataforma específica, tenta com amd64 como fallback
			if strings.Contains(status.Error, "no matching manifest") {
				logVerbose("Trying fallback to linux/amd64 for %s", name)
				resp.Close()

				resp, err = cli.ImagePull(ctx, image, types.ImagePullOptions{
					RegistryAuth: encodeAuth(authConfig),
					Platform:     "linux/amd64",
				})
				if err != nil {
					return false, fmt.Errorf("error pulling image (fallback): %v", err)
				}
				defer resp.Close()
				// Reinicia o decoder para o novo response
				decoder = json.NewDecoder(resp)
			}
		} else if strings.Contains(status.Status, "Downloaded newer image") {
			needsUpdate = true
			logVerbose("New image downloaded for %s", name)
		}
	}

	return needsUpdate, nil
}

func encodeAuth(auth types.AuthConfig) string {
	authJSON, _ := json.Marshal(auth)
	return base64.URLEncoding.EncodeToString(authJSON)
}
