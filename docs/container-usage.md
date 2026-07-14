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

### High uids/gids in the target image (`chown: Invalid argument`)

The builder runs rootless, so container uids/gids are mapped through a
subordinate range defined in the image's `/etc/subuid` and `/etc/subgid`
(`builder:2000:50000` by default). Only container ids `0..49999` exist inside the
build, so an image that assigns an id above that ceiling has no mapping, and any
`chown user:group` against it fails with:

```
chown: changing ownership of '/run/dnsmasq/': Invalid argument
```

The most common offender is Debian/Ubuntu's `nogroup` (gid **65534**).

**Recommended — sparse mapping (`THRILLHOUSE_EXTRA_GIDS` / `_UIDS`)**

Set these at **run** time to splice single-id mappings for the specific high ids a
build needs. They borrow ids from the existing subordinate block, so they work on
a stock host with no `/etc/subuid` change:

```bash
podman run --rm \
  --device /dev/fuse --cap-add=SYS_ADMIN --cap-add=SETUID --cap-add=SETGID \
  --security-opt seccomp=unconfined --security-opt label=disable \
  -e THRILLHOUSE_EXTRA_GIDS=65534 \
  -v $(pwd)/my-image.yaml:/config.yaml:Z \
  -v $(pwd)/output:/output:Z \
  ghcr.io/openchami/image-thrillhouse:latest \
  image-thrillhouse build --config /config.yaml
```

Both variables accept a comma-separated list of ids and inclusive `lo-hi` ranges,
e.g. `THRILLHOUSE_EXTRA_GIDS=65534,65530-65533`. Unset, the mapping is byte-for-byte
the historical default.

**Alternative — widen the whole range (`SUBID_START` / `SUBID_COUNT` build args)**

If you'd rather map a contiguous block up to the high id, widen it at **build**
time. This only works if your host's subordinate allocation is large enough to
contain the range (the default range is deliberately narrow because a wider one
can collide with host uid/gid allocations):

```bash
podman build --build-arg SUBID_COUNT=65536 -t image-thrillhouse:dev -f Dockerfile .
```

Note the outer podman user namespace caps this: with a stock host allocation of
65536, `start=2000` cannot reach gid 65534 no matter the count — you'd first need
a larger host `/etc/subuid`/`/etc/subgid` allocation (and a `podman system migrate`
so podman picks up the change). The sparse mapping above sidesteps that ceiling,
which is why it's preferred for hitting isolated high ids like `nogroup`.
