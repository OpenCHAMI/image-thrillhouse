# Running in a Container

The pre-built unified container at `ghcr.io/openchami/image-thrillhouse:latest` includes DNF, Zypper, APT, and mmdebstrap. It can build images for any supported distribution.

## Basic invocation

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
  ghcr.io/openchami/image-thrillhouse:latest \
  image-thrillhouse build --config /config.yaml --log-level info
```

For S3 publish targets, add credentials:

```bash
  -e S3_ACCESS=<your-access-key> \
  -e S3_SECRET=<your-secret-key> \
```

## Available tags

- `ghcr.io/openchami/image-thrillhouse:latest` — Unified image with all package managers
- `ghcr.io/openchami/image-thrillhouse:v0.1.0` — Specific version

## Flag explanations

| Flag | Why it's needed |
| --- | --- |
| `--device /dev/fuse` | Buildah uses FUSE for container filesystems |
| `--cap-add=SYS_ADMIN` | Mount filesystems |
| `--cap-add=SETUID` / `SETGID` | User namespace mapping in rootless mode |
| `--security-opt seccomp=unconfined` | Relaxes seccomp for buildah operations |
| `--security-opt label=disable` | Disables SELinux confinement |
| `-v ...:/config.yaml:Z` | Mounts the config (`:Z` for SELinux relabeling) |
| `-v ...:/output:Z` | Mounts the output directory for SquashFS images |

## Multi-version DNF builds

The unified image can build any RHEL-family version by setting `releasever` on the manager:

```yaml
meta:
  name: rocky-9-base
  from: scratch

layer:
  manager:
    name: dnf
    options:
      releasever: "9"            # 9 / 10 for RHEL/Rocky/Alma; 40 for Fedora 40
    config: |
      [main]
      gpgcheck=1
      reposdir=/etc/image-thrillhouse/yum.repos.d
  repos:
    - path: /etc/image-thrillhouse/yum.repos.d/rocky-baseos.repo
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

`releasever` is passed to DNF as `--releasever` so a single builder image creates images for any version.

## Why a unified image?

When building from scratch (`from: scratch`), the package manager runs on the **host** with `--installroot` to bootstrap a new filesystem. Bundling every package manager into one image means you can:

- Build images for any distribution without switching base images
- Maintain a single image for CI/CD
- Reduce storage and maintenance overhead

Different distributions have subtle differences in package metadata formats, GPG key handling, dependency resolution, and default configs. Having the **native** package manager available maximises compatibility for scratch builds.

For **parent builds** (layering on top of an existing image) the package manager runs inside the container, so the native tools in the parent image are used.

## Building a local container

For development you can build the image from the repo:

```bash
podman build -t image-thrillhouse:dev -f Dockerfile .
```

Then swap `ghcr.io/openchami/image-thrillhouse:latest` for `image-thrillhouse:dev` in the run command above.
