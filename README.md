# Image Builder (Go based)

A Go-based image builder that wraps `buildah` to create layered OS images with multiple package manager support. This is the next-generation replacement for the Python-based image-builder tool used by OpenCHAMI.

## Features

- **Multiple Package Managers**: Support for DNF, Zypper, APT (parent builds only) and mmdebstrap (scratch builds only)
- **Scratch & Parent Builds**: Build from scratch or layer on top of existing images
- **Flexible Configuration**: YAML-based declarative configuration
- **Multiple Publishers**: 
  - Local container storage
  - SquashFS filesystem images
  - Container Registry
  - S3
- **Structured Logging**: JSON and text logging formats with configurable levels
- **Config Validation**: Validate configurations without building

## Prerequisites

For using pre-built containers (recommended):
- Podman or Docker
- FUSE device support for container builds

For building from source:
- Go 1.21 or later
- Buildah
- Root or rootless container capabilities
- `mksquashfs` (for SquashFS publishing)

## Installation & Usage

### Using Pre-built Container (Recommended)

The easiest way to use the image-builder is with the pre-built container image:

```bash
# Pull the latest image
podman pull ghcr.io/openchami/image-builder:latest

# Build an image using your config
podman run --rm \
  --device /dev/fuse \
  --cap-add=SYS_ADMIN \
  --cap-add=SETUID \
  --cap-add=SETGID \
  --security-opt seccomp=unconfined \
  --security-opt label=disable \
  -v $(pwd)/my-image.yaml:/config.yaml:Z \
  -v $(pwd)/output:/output:Z \
  ghcr.io/openchami/image-builder:latest \
  image-build build --config /config.yaml --log-level info
```

**Note**: If you're building images that will publish to S3, add credentials:
```bash
-e S3_ACCESS=<your-access-key> \
-e S3_SECRET=<your-secret-key> \
```

### Building from Source (Development)

Only needed if you're developing or modifying the tool:

First, clone the repository: 
```bash
git clone https://github.com/OpenCHAMI/image-builder.git
cd image-build
```

Building on bare metal:
```bash
go build -o image-build ./cmd/image-build
sudo mv image-build /usr/local/bin/
```

Or build a local container:
```bash
podman build -t image-builder:dev -f Dockerfile .
```

## Quick Start

### Basic Configuration

Create a config file `rocky-example.yaml`:

```yaml
meta:
  name: rocky-base
  tag: "9.5"
  from: scratch

layer:
  manager:
    name: dnf
    config: |
      [main]
      gpgcheck=1
      reposdir=/etc/image-build/yum.repos.d
  repos: 
    - path: /etc/image-build/yum.repos.d/rocky-baseos.repo
      content: |
        [rocky-baseos]
        name=rocky-baseos
        baseurl=https://dl.rockylinux.org/pub/rocky/9/BaseOS/x86_64/os
        enabled=1
        gpgcheck=1
        gpgkey=https://dl.rockylinux.org/pub/rocky/RPM-GPG-KEY-Rocky-9
  actions:
    install:
      packages:
        - kernel
        - systemd
      groups:
        - Minimal Install
    commands:
      - run: systemctl disable firewalld

publish:
  - type: local
```

### Build the Image

```bash
image-build build --config my-image.yaml --log-level info
```

### Validate Configuration

```bash
image-build validate --config my-image.yaml
```

## Configuration Reference

The configuration file is divided into three main sections:

### 1. Meta Section

Defines image metadata and the base image to build from.

```yaml
meta:
  name: my-image           # Image name (required)
  tag: "1.0"               # Image tag (required)
  from: scratch            # Base image: 'scratch' or 'registry.io/image:tag'
```

### 2. Layer Section

Defines how to build the image layer.

#### Package Manager Configuration

```yaml
layer:
  manager:
    name: dnf              # Package manager: dnf, zypper, apt, mmdebstrap
    config: |              # Optional: package manager config file content
      [main]
      gpgcheck=1
    options:               # Optional: backend-specific options
      key: value
```

#### Repository Configuration

```yaml
  repos:
    - path: /etc/yum.repos.d/my-repo.repo
      content: |           # Inline content (preferred)
        [my-repo]
        name=My Repository
        baseurl=https://...
    - path: /etc/yum.repos.d/other.repo
      src: ./local-file.repo       # Copy from local file
    - path: /etc/yum.repos.d/remote.repo
      url: https://example.com/repo.repo  # Download from URL
```

**Note**: Use exactly one of: `content`, `src`, or `url` per repo entry.

#### Files Configuration

Add custom files to the image:

```yaml
  files:
    - path: /etc/custom-config
      content: |           # Inline content
        key=value
    - path: /usr/local/bin/script.sh
      src: ./scripts/script.sh     # Copy from local file
    - path: /etc/downloaded-config
      url: https://example.com/config  # Download from URL
```

#### Actions

Define installation and commands:

```yaml
  actions:
    install:
      packages:            # Individual packages
        - vim
        - wget
      groups:              # Package groups (DNF only)
        - Development Tools
      modules:             # DNF modules (DNF only)
        - name: nodejs
          stream: "18"
          action: install  # enable, install, disable
    commands:
      - run: systemctl enable myservice    # Simple command
      - script: |                          # Multi-line script
          #!/bin/bash
          echo "Setting up..."
          dnf clean all
```

### 3. Publish Section

Define where to publish the built image:

```yaml
publish:
  - type: local            # Commit to local container storage
  
  - type: squashfs         # Create SquashFS image
    path: /output/images   # Output directory
    
  # Registry publishing (planned)
  - type: registry
    url: registry.example.com/myorg
    
  # S3 publishing (planned)
  - type: s3
    url: https://s3.example.com
    bucket: boot-images
    prefix: compute/
```

## Package Manager Support

### DNF (Red Hat, Rocky, AlmaLinux, Fedora)

- ✅ Scratch builds (installroot)
- ✅ Parent image builds
- ✅ Package groups
- ✅ Modules (enable/install/disable)
- ✅ Repository management

### Zypper (openSUSE, SLES)

- ✅ Scratch builds
- ✅ Parent image builds
- ✅ Repository management
- ⚠️ No package group support

### APT (Debian, Ubuntu)

- ❌ Scratch builds not supported (use mmdebstrap)
- ✅ Parent image builds
- ✅ Repository management

### mmdebstrap (Debian, Ubuntu)

- ✅ Scratch builds (Debian bootstrap)
- ❌ Parent image builds not supported
- ✅ Suite-based installation

## Running in Container

### Production Usage (Pre-built Container)

The recommended way to run image-build is using the pre-built container from GitHub Container Registry:

```bash
# Basic build
podman run --rm \
  --device /dev/fuse \
  --cap-add=SYS_ADMIN \
  --cap-add=SETUID \
  --cap-add=SETGID \
  --security-opt seccomp=unconfined \
  --security-opt label=disable \
  -v $(pwd)/my-image.yaml:/config.yaml:Z \
  -v $(pwd)/output:/output:Z \
  ghcr.io/openchami/image-build-go:latest \
  image-build build --config /config.yaml --log-level info
```

**With S3 credentials:**
```bash
podman run --rm \
  --device /dev/fuse \
  --cap-add=SYS_ADMIN \
  --cap-add=SETUID \
  --cap-add=SETGID \
  --security-opt seccomp=unconfined \
  --security-opt label=disable \
  -e S3_ACCESS=${S3_ACCESS} \
  -e S3_SECRET=${S3_SECRET} \
  -v $(pwd)/my-image.yaml:/config.yaml:Z \
  -v $(pwd)/output:/output:Z \
  ghcr.io/openchami/image-build-go:latest \
  image-build build --config /config.yaml --log-level info
```

**Available images:**
- `ghcr.io/openchami/image-build-go:latest` - Latest build from main branch
- `ghcr.io/openchami/image-build-go:v0.1.0` - Specific version (when tagged)

### Development Usage (Local Build)

If you've built a local container for development:

```bash
podman run --rm \
  --device /dev/fuse \
  --cap-add=SYS_ADMIN \
  --security-opt seccomp=unconfined \
  --security-opt label=disable \
  -v $(pwd)/my-image.yaml:/config.yaml:Z \
  -v $(pwd)/output:/output:Z \
  image-build-go:dev \
  image-build build --config /config.yaml --log-level info
```

### Understanding the Flags

- `--device /dev/fuse`: Required for buildah to use FUSE for container filesystems
- `--cap-add=SYS_ADMIN`: Allows mounting filesystems
- `--cap-add=SETUID/SETGID`: For user namespace mapping in rootless mode
- `--security-opt seccomp=unconfined`: Relaxes security for buildah operations
- `--security-opt label=disable`: Disables SELinux confinement
- `-v $(pwd)/config.yaml:/config.yaml:Z`: Mounts your config (`:Z` for SELinux relabeling)
- `-v $(pwd)/output:/output:Z`: Mounts output directory for SquashFS images


## Command Line Options

### Global Flags

- `--log-level`: Set log level (debug, info, warn, error). Default: `info`
- `--log-format`: Set log format (json, text). Default: `json`

### Build Command

```bash
image-build build --config <path> [flags]
```

- `-c, --config`: Path to YAML configuration file (required)

### Validate Command

```bash
image-build validate --config <path> [flags]
```

Validates the configuration without building the image.

### Version Command

```bash
image-build version
```

Prints version information.

## Examples

See the `tests/` directory for complete examples:

- `tests/rocky/rocky-base-x86_64.yaml` - Rocky Linux base image from scratch
- `tests/rocky/rocky-compute-x86_64.yaml` - Compute node image with additional packages
- `tests/debian/bookworm-base.yaml` - Debian base using mmdebstrap
- `tests/opensuse/suse-base.yaml` - openSUSE base with Zypper

## Architecture

```
image-build/
├── cmd/image-build/           # Main application entry point
│   └── main.go
├── internal/
│   ├── backend/               # Package manager implementations
│   │   ├── backend.go         # Backend interface
│   │   ├── dnf/               # DNF backend
│   │   ├── zypper/            # Zypper backend
│   │   ├── apt/               # APT backend
│   │   └── mmdebstrap/        # mmdebstrap backend
│   ├── buildah/               # Buildah container management
│   │   ├── container.go       # Container operations
│   │   └── store.go           # Storage management
│   ├── builder/               # Build orchestration
│   │   └── builder.go         # Main build logic
│   ├── config/                # Configuration parsing & validation
│   │   ├── config.go          # Config structures
│   │   └── validate.go        # Validation logic
│   ├── container/             # Container abstractions
│   │   ├── container.go       # Container interface
│   │   └── logwriter.go       # Logging utilities
│   └── publisher/             # Image publishing
│       ├── publisher.go       # Publisher interface
│       ├── local/             # Local storage publisher
│       ├── squashfs/          # SquashFS publisher
│       └── registry/          # Registry publisher (planned)
└── tests/                     # Example configurations
```

## Development

### Building from Source

```bash
go build -o image-build ./cmd/image-build
```

### Running Tests

```bash
go test ./...
```

### Adding a New Backend

1. Create a new package in `internal/backend/mymanager/`
2. Implement the `Backend` interface from `internal/backend/backend.go`
3. Register it in `cmd/image-build/main.go` in the `newBackend()` function

### Adding a New Publisher

1. Create a new package in `internal/publisher/mypublisher/`
2. Implement the `Publisher` interface from `internal/publisher/publisher.go`
3. Register it in `cmd/image-build/main.go` in the `newPublishers()` function

## Migration from Python Version

This Go version is a rewrite of the Python-based image-builder. Key differences:

### Configuration Format

**Python** uses a flatter structure:
```yaml
options:
  layer_type: base
  name: my-image
  pkg_manager: dnf
  parent: scratch
  publish_local: true
```

**Go** uses a structured format:
```yaml
meta:
  name: my-image
  tag: latest
  from: scratch
layer:
  manager:
    name: dnf
publish:
  - type: local
```

### Feature Parity Status

- ✅ Base layer builds
- ✅ Multiple package managers (expanded)
- ✅ Local publishing
- ✅ SquashFS publishing
- ⚠️ Registry publishing (in development)
- ❌ Ansible layer support (planned)
- ❌ S3 publishing (planned)
- ❌ OpenSCAP scanning (planned)
- ❌ Image labels/metadata (planned)

## Troubleshooting

### Permission Errors

If you encounter permission errors, ensure you're running with appropriate privileges:

```bash
# Rootless (recommended)
image-build build --config my-image.yaml

# Root
sudo image-build build --config my-image.yaml
```

### Buildah Not Found

Install buildah:
```bash
# Fedora/RHEL/Rocky
sudo dnf install buildah

# Ubuntu/Debian
sudo apt install buildah

# openSUSE
sudo zypper install buildah
```

### Package Manager Fails

- Ensure your repository URLs are accessible
- Check GPG key configuration
- For scratch builds, verify the package manager is installed on the host
- Check logs with `--log-level debug`

### SquashFS Creation Fails

Install squashfs-tools:
```bash
# RHEL/Rocky/Fedora
sudo dnf install squashfs-tools

# Ubuntu/Debian
sudo apt install squashfs-tools

# openSUSE
sudo zypper install squashfs
```

## Contributing

Contributions are welcome! Areas needing development:

1. **Registry Publisher**: Implement OCI registry push functionality
2. **S3 Publisher**: Add S3 upload with kernel/initramfs extraction
3. **OpenSCAP**: Security scanning and compliance checking
4. **Ansible Support**: Ansible playbook execution against containers
5. **Image Labels**: Automatic metadata label generation
6. **Testing**: Expand test coverage

## License

See LICENSE file for details.

## Related Projects

- [Buildah](https://buildah.io/) - Container building tool
- [Podman](https://podman.io/) - Container runtime
- [OpenChami](https://openchami.org/) - HPC cluster management (original use case)

## Support

For issues, questions, or contributions, please open an issue in the repository.

## LLM Use Acknowledgement & Statement
Majority of the documentation and some of the code was written by Claude Sonnet 4.5 (Training cutoff date: September 29, 2025). All the generated content has been verified and tested.

