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
- Go 1.26 or later (see `go.mod`)
- Buildah
- Root or rootless container capabilities
- `mksquashfs` (for SquashFS publishing)
- C toolchain + `pkg-config` + `libgpgme-dev` + `libbtrfs-dev` + `libdevmapper-dev` (transitive cgo deps via containers/storage). On macOS these are not available, so the binary builds and tests in `internal/buildah` only work on Linux.

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
  tags:
    - "9.5"
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

This will check:
- YAML syntax
- Required fields
- Package manager support
- Backend options (validates unknown options and invalid values)
- Publisher configuration

## Configuration Reference

The configuration file is divided into three main sections:

### 1. Meta Section

Defines image metadata and the base image to build from.

```yaml
meta:
  name: my-image           # Image name (required)
  tags:                    # Image tags (required, can be multiple)
    - "1.0"
    - "latest"
  from: scratch            # Base image: 'scratch' or 'registry.io/image:tag'
  from-tls-verify: true    # Optional: verify TLS when pulling base image (default: true)
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
    options:               # Optional: backend-specific options (see below)
      install-weak-deps: "false"
      best: "true"
```

**Backend Options:**

All backends support configurable options to control package installation behavior:

**APT (Debian/Ubuntu):**
- `install-recommends`: `"true"` or `"false"` (default: `"false"`) - Install recommended packages
- `install-suggests`: `"true"` or `"false"` (default: `"false"`) - Install suggested packages  
- `allow-unauthenticated`: `"true"` or `"false"` (default: `"false"`) - Allow unsigned packages

**DNF (RHEL/Rocky/Fedora):**
- `install-weak-deps`: `"true"` or `"false"` (default: `"true"`) - Install weak dependencies
- `best`: `"true"` or `"false"` (default: `"true"`) - Use best package versions
- `skip-broken`: `"true"` or `"false"` (default: `"false"`) - Skip broken packages
- `allowerasing`: `"true"` or `"false"` (default: `"false"`) - Allow erasing packages for dependencies
- `nobest`: `"true"` or `"false"` (default: `"false"`) - Don't limit to best candidates
- `releasever`: string (optional) - Override the RHEL/distro release version (e.g., `"9"`, `"10"`, `"40"`) - **Required for scratch builds**

**Zypper (openSUSE/SLES):**
- `repopath`: Repository directory path (default: `"/etc/zypp/repos.d"`)
- `no-recommends`: `"true"` or `"false"` (default: `"false"`) - Don't install recommended packages
- `no-gpg-checks`: `"true"` or `"false"` (default: `"false"`) - Skip GPG verification
- `force-resolution`: `"true"` or `"false"` (default: `"false"`) - Auto-resolve conflicts

**mmdebstrap (Debian/Ubuntu scratch builds):**
- `suite`: Debian/Ubuntu release (e.g., `"bookworm"`) - **Required**
- `mirror`: Package mirror URL (e.g., `"http://deb.debian.org/debian"`) - **Required**
- `variant`: Bootstrap variant (default: `"minbase"`)
- `mode`: Execution mode (default: `"fakechroot"`)

#### Repository Configuration

```yaml
  repos:
    - path: /etc/yum.repos.d/my-repo.repo
      content: |           # Inline content (preferred)
        [my-repo]
        name=My Repository
        baseurl=https://...
        gpgcheck=1
        gpgkey=file:///etc/pki/rpm-gpg/RPM-GPG-KEY-myrepo
      gpg: https://example.com/RPM-GPG-KEY-myrepo  # Automatic GPG key import
    - path: /etc/yum.repos.d/other.repo
      src: ./local-file.repo       # Copy from local file
    - path: /etc/yum.repos.d/remote.repo
      url: https://example.com/repo.repo  # Download from URL
```

**GPG Key Import:**

The optional `gpg` field automatically imports a GPG key for repository verification. The key URL is fetched by the builder over HTTP (with a 60-second timeout, respecting cancellation) and the key bytes are then installed into the appropriate trust store for the backend:

- **RPM-based (dnf, zypper)**: imported via `rpm --import`
- **APT-based (apt, mmdebstrap)**: dearmored (if ASCII-armored) and placed in `/etc/apt/trusted.gpg.d/`
- **Scratch builds**: the trust store under the new root filesystem is targeted on the host
- **Parent builds**: the key is placed inside the container

The user-supplied URL is **never** interpolated into a shell command — the fetch happens in Go and the backend only sees a local file path. Per-repo key-import failures are logged as warnings; the build continues so a repo that works without GPG (or whose key the user has installed by other means) is not blocked.

If the `gpg` field is omitted, you must either:
- Set `gpgcheck=0` (or the equivalent) in the repository configuration, or
- Manually provide GPG keys through other means

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
      remove_packages:     # Packages to remove (for minimizing image size)
        - kernel-debug
        - man-pages
        - linux-firmware
    commands:
      - run: systemctl enable myservice    # Simple command
      - script: |                          # Multi-line script
          #!/bin/bash
          echo "Setting up..."
          dnf clean all
```

**Package Removal:**

The `remove_packages` option allows you to remove unnecessary packages after installation to minimize image size. This is useful for:
- Removing debug packages (kernel-debug, kernel-debug-core)
- Removing documentation (man-db, man-pages)
- Removing unused firmware (linux-firmware, iwl7260-firmware)
- Removing build tools after compilation

**Technical Details:**
- Uses `rpm -e --nodeps` for RPM-based systems (dnf, zypper)
- Uses `dpkg --remove --force-depends` for Debian-based systems (apt, mmdebstrap)
- Executed after all packages are installed
- For **scratch builds**, the command runs on the host targeting the mounted root (e.g. `rpm --root <mount> -e --nodeps …`) because a freshly-bootstrapped scratch root may not yet be able to exec the package manager.
- For **parent builds**, the command runs inside the container.
- **Failures fail the build.** A common mistake is listing a package that's not installed; the package manager will return non-zero and the build will stop with a clear error. List only packages you know are present after the install step.

**Example minimal image strategy:**
```yaml
layer:
  manager:
    name: dnf
    options:
      install-weak-deps: "false"  # Don't install weak dependencies
  actions:
    install:
      groups:
        - Minimal Install
      packages:
        - kernel
        - systemd
      remove_packages:
        # Debug packages
        - kernel-debug
        - kernel-debug-core
        # Documentation
        - man-db
        - man-pages
        # Unused firmware
        - linux-firmware
        # Boot rescue images
        - dracut-config-rescue
```

### 3. OpenSCAP Security Scanning (Optional)

Add security compliance checking and vulnerability assessment:

```yaml
layer:
  openscap:
    # Install OpenSCAP tools (openscap-utils, scap-security-guide, bzip2)
    install_scap: true
    
    # Run XCCDF security benchmark scan
    scap_benchmark: true
    profile: "xccdf_org.ssgproject.content_profile_stig"
    benchmark_path: "/usr/share/xml/scap/ssg/content/ssg-rl9-ds.xml"
    
    # Run OVAL vulnerability evaluation
    oval_eval: true
    oval_url: "https://www.redhat.com/security/data/oval/v2/RHEL9/rhel-9.oval.xml.bz2"
    
    # Optional: Custom result paths (defaults shown)
    results_path: "/root/scan.xml"
    remediate_path: "/root/remediate.sh"
    oval_result_path: "/root/vulnerabilities.xml"
```

**OpenSCAP Features:**
- **XCCDF Benchmarks**: Test system against security profiles (e.g., DISA STIG, CIS, PCI-DSS)
- **OVAL Evaluations**: Check for known vulnerabilities (CVEs) in installed packages
- **Remediation Scripts**: Automatically generate scripts to fix security findings
- **Compliance Reports**: Detailed XML reports saved in the container

**Common SCAP Profiles:**
- `xccdf_org.ssgproject.content_profile_stig` - DISA STIG (DoD requirements)
- `xccdf_org.ssgproject.content_profile_cis` - CIS Benchmarks
- `xccdf_org.ssgproject.content_profile_pci-dss` - PCI-DSS compliance
- `xccdf_org.ssgproject.content_profile_ospp` - OSPP/Common Criteria

Check available profiles: `oscap info /usr/share/xml/scap/ssg/content/ssg-rl9-ds.xml`

### 4. Publish Section

Define where to publish the built image:

```yaml
publish:
  - type: local            # Commit to local container storage
  
  - type: squashfs         # Create SquashFS image
    path: /output/images   # Output directory; file is written as
                           # <meta.name>-<meta.tags[0]>.squashfs
    
  - type: registry         # Push to container registry
    url: registry.example.com/myorg
    tls-verify: false      # Optional: disable TLS verification
    
  - type: s3               # Upload to S3-compatible storage
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
- ✅ Configurable options (6 options)

### Zypper (openSUSE, SLES)

- ✅ Scratch builds
- ✅ Parent image builds
- ✅ Repository management
- ✅ Configurable options (4 options)
- ⚠️ No package group support

### APT (Debian, Ubuntu)

- ❌ Scratch builds not supported (use mmdebstrap)
- ✅ Parent image builds
- ✅ Repository management
- ✅ Configurable options (3 options)

### mmdebstrap (Debian, Ubuntu)

- ✅ Scratch builds (Debian bootstrap)
- ❌ Parent image builds not supported
- ✅ Suite-based installation
- ✅ Required options (suite, mirror)

## Container Images

The Dockerfile provides a unified image based on Debian Bookworm that includes all supported package managers (DNF, Zypper, APT, mmdebstrap). A single Go binary is compiled and included in the image.

### Available Image

**Unified multi-package-manager image:**
- **Base:** Debian Bookworm (12)
- **Included package managers:** DNF, Zypper, APT, mmdebstrap
- **Capabilities:** Can build images for any supported distribution

**DNF (RHEL/Rocky/AlmaLinux/Fedora):**
- Use `options: { releasever: "9" }` in config for RHEL/Rocky/Alma 9
- Use `options: { releasever: "10" }` in config for RHEL/Rocky/Alma 10
- Use `options: { releasever: "40" }` in config for Fedora 40
- The `releasever` option tells DNF which release version to target

### Building the Image

```bash
# Build the unified image
podman build -t image-builder:latest .
```

### Multi-Version Support

The unified image can build images for any RHEL/Fedora version using the `releasever` option:

**Example: Building Rocky Linux 9**
```yaml
meta:
  name: rocky-9-base
  from: scratch

layer:
  manager:
    name: dnf
    options:
      releasever: "9"  # ← Specify target RHEL version
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
        - bash
        - systemd
```

**Example: Building Rocky Linux 10**
```yaml
meta:
  name: rocky-10-base
  from: scratch

layer:
  manager:
    name: dnf
    options:
      releasever: "10"  # ← Specify target RHEL version
    config: |
      [main]
      gpgcheck=1
      reposdir=/etc/image-build/yum.repos.d
  repos:
    - path: /etc/image-build/yum.repos.d/rocky-baseos.repo
      content: |
        [rocky-baseos]
        name=rocky-baseos
        baseurl=https://dl.rockylinux.org/pub/rocky/10/BaseOS/x86_64/os
        enabled=1
        gpgcheck=1
        gpgkey=https://dl.rockylinux.org/pub/rocky/RPM-GPG-KEY-Rocky-10
  actions:
    install:
      packages:
        - bash
        - systemd
```

The `releasever` option is passed to DNF as `--releasever` and tells it which release version to use when resolving dependencies and accessing repositories. This allows a single builder image to create images for any RHEL-family distribution version.

**Benefits:**
- ✅ **Single builder image** - One image includes all package managers
- ✅ **Simpler maintenance** - One Dockerfile, one build
- ✅ **Version flexibility** - Build any distribution from config
- ✅ **Smaller storage** - Only one image to store

### Architecture

```
Single Go Builder (golang:1.26-bookworm)
   └─> Unified Image (debian:bookworm-slim)
       ├─> dnf (supports all RHEL versions via releasever)
       ├─> zypper (for SUSE/openSUSE)
       ├─> apt (for Debian/Ubuntu)
       └─> mmdebstrap (for Debian/Ubuntu scratch builds)
```

The Go binary is compiled once and copied into the unified base image that includes all package managers.

### Why a Unified Image?

When building images **from scratch** (using `from: scratch` in config), the package manager runs on the **host** using `--installroot` to bootstrap a new filesystem. By including all package managers in a single image, you can:

- Build images for any distribution without switching container images
- Maintain a single image for your CI/CD pipelines
- Reduce storage and maintenance overhead

Different distributions have subtle differences in:

- Package metadata formats and repository structures
- GPG key handling and signature verification
- Dependency resolution algorithms
- Default configuration files

Having the **native package manager** available ensures maximum compatibility and reduces the risk of issues during scratch builds.

**For parent image builds** (building on top of an existing image), the package manager runs **inside** the container, so the native tools in the parent image are used.

## Running in Container

### Production Usage (Pre-built Container)

The recommended way to run image-build is using the pre-built container from GitHub Container Registry:

```bash
# For any distribution (unified image includes all package managers)
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

**Available image tags:**
- `ghcr.io/openchami/image-builder:latest` - Unified image with all package managers
- `ghcr.io/openchami/image-builder:v0.1.0` - Specific version (when tagged)

### Development Usage (Local Build)

If you've built a local container for development:

```bash
podman run --rm \
  --device /dev/fuse \
  --cap-add=SYS_ADMIN \
  --cap-add=SETUID \
  --cap-add=SETGID \
  --security-opt seccomp=unconfined \
  --security-opt label=disable \
  -v $(pwd)/my-image.yaml:/config.yaml:Z \
  -v $(pwd)/output:/output:Z \
  image-builder:latest \
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

See the `tests/` directory for complete examples. The config files double as the corpus that the integration test suite exercises.

**DNF (Rocky Linux):**
- `tests/dnf/rocky-scratch.yaml` - Minimal scratch build
- `tests/dnf/rocky-scratch-options.yaml` - Scratch with DNF backend options
- `tests/dnf/rocky-scratch-groups.yaml` - Scratch with package groups
- `tests/dnf/rocky-scratch-full.yaml` - Scratch with repos, files, commands
- `tests/dnf/rocky-parent.yaml` - Layered on a parent image
- `tests/dnf/rocky-parent-groups.yaml` - Parent build using groups
- `tests/dnf/rocky-parent-modules.yaml` - Parent build using DNF modules
- `tests/dnf/rocky-parent-commands.yaml` - Parent build with run/script commands

**APT / mmdebstrap (Debian / Ubuntu):**
- `tests/apt/debian-scratch.yaml` - mmdebstrap scratch build (apt scratch is not supported)
- `tests/apt/debian-scratch-options.yaml` - Scratch with mmdebstrap options
- `tests/apt/debian-scratch-mirror.yaml` - Scratch using a non-default mirror
- `tests/apt/debian-scratch-full.yaml` - Scratch with repos, files, commands
- `tests/apt/debian-parent.yaml` - apt parent build
- `tests/apt/debian-parent-files.yaml` - Parent build that adds files
- `tests/apt/debian-parent-commands.yaml` - Parent build with run/script commands
- `tests/apt/debian-parent-tasks.yaml` - Parent build combining install + tasks

**Zypper (openSUSE / SLES):**
- `tests/zypper/suse-scratch.yaml` - Minimal scratch build
- `tests/zypper/suse-scratch-options.yaml` - Scratch with Zypper backend options
- `tests/zypper/suse-scratch-patterns.yaml` - Scratch using SUSE patterns (groups)
- `tests/zypper/suse-scratch-full.yaml` - Scratch with repos, files, commands
- `tests/zypper/suse-parent.yaml` - Parent build
- `tests/zypper/suse-parent-files.yaml` - Parent build that adds files
- `tests/zypper/suse-parent-patterns.yaml` - Parent build using patterns
- `tests/zypper/suse-parent-commands.yaml` - Parent build with run/script commands

**Validation negatives** (intentionally invalid; used to verify the `validate` subcommand rejects them):
- `tests/rocky/invalid-dnf-test.yaml` - unknown DNF option
- `tests/rocky/conflicting-dnf-test.yaml` - `best: true` and `nobest: true` together
- `tests/opensuse/invalid-zypper-test.yaml` - unknown Zypper option
- `tests/apt/invalid-option.yaml` - unknown apt option
- `tests/apt/invalid-no-suite.yaml` - mmdebstrap missing required `suite`

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
│   ├── labels/                # Image label generation
│   │   └── labels.go          # Label creation logic
│   ├── oscap/                 # OpenSCAP security scanning
│   │   └── oscap.go           # SCAP operations
│   └── publisher/             # Image publishing
│       ├── publisher.go       # Publisher interface
│       ├── local/             # Local storage publisher
│       ├── squashfs/          # SquashFS publisher
│       ├── registry/          # Registry publisher
│       └── s3/                # S3 publisher
└── tests/                     # Example configurations
```

## Development

### Building from Source

```bash
go build -o image-build ./cmd/image-build
```

### Running Unit Tests

```bash
go test ./...
go test -v ./...          # Verbose output
go test -cover ./...      # With coverage
```

**Caveat:** `internal/buildah` depends transitively on cgo bindings (gpgme, btrfs, devicemapper) via `containers/storage`. On systems without those libraries — most notably macOS — `go test ./...` will fail to build that package. The pure-Go packages below test cleanly anywhere; the buildah-backed integration paths require Linux with the build deps from `Dockerfile`.

**Unit test coverage:**
- ✅ `internal/config` - YAML parsing, schema validation
- ✅ `internal/labels` - OCI label generation
- ✅ `internal/backend/apt` - APT backend
- ✅ `internal/backend/dnf` - DNF backend
- ✅ `internal/backend/zypper` - Zypper backend (including informational exit codes 102/103/107)
- ✅ `internal/backend/mmdebstrap` - mmdebstrap backend
- ⚠️ `internal/buildah`, `internal/builder`, `internal/container`, `internal/fetch`, `internal/publisher/*` - covered only by the integration suite below

See `TESTING.md` for the unit-testing guide.

### Running Integration Tests

The `tests/` directory holds shell-driven smoke tests that build a unified container image and exercise each backend against real distro repos. The top-level `run-all-tests.sh` runs everything in sequence, or in parallel when asked:

```bash
./run-all-tests.sh                  # all backends, sequential
./run-all-tests.sh --parallel       # scratch tests in parallel
./run-all-tests.sh --dnf            # only DNF
./run-all-tests.sh --apt            # only APT / mmdebstrap
./run-all-tests.sh --zypper         # only Zypper
```

Each backend script (`tests/<backend>/test-<backend>-{scratch,parent}.sh`) can also be run directly. All scripts share a single podman image tag, `image-build:test`, built from the repo's `Dockerfile`.

**Rebuilding the test image after a code change:**

The container guard skips the build if `image-build:test` already exists, so iterating on the Go binary requires forcing a rebuild. Set the env var:

```bash
REBUILD_IMAGE=1 ./run-all-tests.sh                    # rebuild and run everything
REBUILD_IMAGE=1 ./tests/zypper/test-zypper-scratch.sh # rebuild and run just one
```

Without `REBUILD_IMAGE=1`, the test will silently reuse a stale binary — if a test "still fails" after a fix you believe should help, this is the first thing to check.

Per-test logs land in `test-output/<backend>-<type>/`.

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
  tags:
    - latest
  from: scratch
layer:
  manager:
    name: dnf
publish:
  - type: local
```

### Feature Parity Status

- ✅ Base layer builds
- ✅ Multiple package managers (expanded: dnf, zypper, apt, mmdebstrap)
- ✅ Local publishing
- ✅ SquashFS publishing
- ✅ Registry publishing
- ✅ S3 publishing with kernel/initramfs extraction
- ✅ OpenSCAP security scanning (NEW)
- ✅ Package removal (NEW)
- ✅ GPG key import for repositories (NEW)
- ✅ Image labels/metadata
- ❌ Ansible layer support (intentionally not supported)

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

### OpenSCAP Installation Issues

If OpenSCAP packages are not found:
```bash
# Ensure AppStream repository is enabled (for RHEL/Rocky)
dnf config-manager --set-enabled appstream

# Manually install OpenSCAP
dnf install openscap-utils scap-security-guide bzip2
```

For Debian/Ubuntu:
```bash
apt-get install libopenscap8 ssg-debian ssg-debderived bzip2
```

## Contributing

Contributions are welcome! Areas for potential improvement:

1. **Additional Package Managers**: Add support for more package managers (pacman, apk, etc.)
2. **Additional Publishers**: Cloud-specific publishers (Azure, GCP, etc.)
3. **Testing**: Expand test coverage for new features
4. **Documentation**: Improve examples and use cases
5. **Performance**: Optimize build times for large images

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

