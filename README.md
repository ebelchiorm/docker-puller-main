# Docker Puller

A lightweight container update service that automatically pulls and updates Docker containers from your private registry.


## Features

- **Registry Authentication**: Secure authentication with private Docker registries
- **Selective Updates**: Update only containers with specific labels
- **Automatic Cleanup**: Optional removal of old images
- **Configurable Intervals**: Customizable check intervals for updates
- **Minimal Dependencies**: Written in Go with only Docker SDK dependencies

## Usage

### Docker Compose

```yaml
services:
  puller:
    image: registry.example.com/docker-puller:latest
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
    environment:
      - REGISTRY_URL=registry.example.com
      - REGISTRY_USERNAME=${REGISTRY_USER}
      - REGISTRY_PASSWORD=${REGISTRY_PASSWORD}
    command: --interval 30 --cleanup --label-enable
    restart: unless-stopped

  your-service:
    image: registry.example.com/your-service:latest
    labels:
      - "puller.update.enable=true"
```

### Configuration

#### Environment Variables

- `REGISTRY_URL`: Your Docker registry URL
- `REGISTRY_USERNAME`: Registry username
- `REGISTRY_PASSWORD`: Registry password
- `NOTIFICATION_URL`: Optional URL to send notifications about updates and errors

#### Command Line Flags

- `--interval`: Check interval in seconds (default: 30)
- `--cleanup`: Remove old images after pulling (default: false)
- `--label-enable`: Only update containers with enable label (default: false)

#### Container Labels

To enable automatic updates when using `--label-enable`:
```yaml
labels:
  - "puller.update.enable=true"
```

## Building

```bash
go mod download
go build -o puller
```

### Docker Build

```bash
docker build -t docker-puller .
```

## CI/CD Integration

Place in `.github/workflows/puller.yml`:

```yaml
name: Docker Puller CI

on:
  push:
    branches: [ main ]
    paths:
      - 'puller/**'

env:
  CI_REGISTRY: ${{ vars.CI_REGISTRY }}
  CI_REGISTRY_USER: ${{ secrets.CI_REGISTRY_USER }}
  CI_REGISTRY_PASSWORD: ${{ secrets.CI_REGISTRY_PASSWORD }}

jobs:
  build:
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/checkout@v4
      - uses: docker/setup-buildx-action@v3
      - uses: docker/login-action@v3
        with:
          registry: ${{ env.CI_REGISTRY }}
          username: ${{ env.CI_REGISTRY_USER }}
          password: ${{ env.CI_REGISTRY_PASSWORD }}
      - uses: docker/build-push-action@v5
        with:
          context: ./puller
          push: true
          tags: |
            ${{ env.CI_REGISTRY }}/docker-puller:latest
            ${{ env.CI_REGISTRY }}/docker-puller:${{ github.sha }}
```

## Security

- Requires Docker socket access
- Uses basic authentication for registry
- Updates only specified containers when label filtering is enabled
- No external dependencies beyond Docker SDK