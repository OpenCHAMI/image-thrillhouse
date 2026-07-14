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

Some images assign ids above the range the rootless builder maps into the build,
so a `chown user:group` against them fails with:

```
chown: changing ownership of '/run/dnsmasq/': Invalid argument
```

The most common offender is Debian/Ubuntu's `nogroup` (gid **65534**). The right
fix depends on the buildah **isolation mode** (`BUILDAH_ISOLATION`), because the
two rootless modes map ids very differently.

**chroot isolation (the image default — the common case)**

Two nested user namespaces are in play. `podman run` creates the *outer* one from
your host's `/etc/subuid`; then the tool re-execs into an *inner* one built from
the **container's** `/etc/subuid` (`builder:2000:50000`), and buildah's chroot run
identity-maps that inner namespace. So the ceiling is the container's
`/etc/subuid` range — and that range must also fit inside the outer namespace.
Reaching a high id therefore needs **both**:

1. **Widen the container's `/etc/subuid`** so the id falls inside it — the
   `SUBID_COUNT` build arg, or a runtime bind-mount over `/etc/subuid` and
   `/etc/subgid`:

   ```bash
   # build time
   podman build --build-arg SUBID_COUNT=65536 -t image-thrillhouse:dev -f Dockerfile .

   # or, against the published image, at run time:
   printf 'builder:2000:65536\n' > /tmp/subid
   #   ... -v /tmp/subid:/etc/subuid:ro -v /tmp/subid:/etc/subgid:ro ...
   ```

2. **Widen the host's `/etc/subuid`/`/etc/subgid`** so the outer namespace can
   contain that wider range, then `podman system migrate` (podman caches the old
   size otherwise):

   ```bash
   grep "^$USER:" /etc/subuid /etc/subgid   # e.g. youruser:100000:1200000
   podman system migrate
   podman unshare cat /proc/self/uid_map    # should show the full range
   ```

With the stock `builder:2000:50000`, ids above 49999 (e.g. `nogroup` at 65534)
have no mapping no matter how large the *host* range is — you must widen the
*container* range too. The container range is deliberately narrow by default
because a wider one can collide with host uid/gid allocations, so it's opt-in.

`THRILLHOUSE_EXTRA_UIDS` / `_GIDS` (below) are **ignored** under chroot: a sparse
override can't be applied to chroot's direct id-map write, and isn't needed once
the range above is wide enough.

**OCI-rootless isolation (`BUILDAH_ISOLATION` unset)**

> Note: OCI-rootless generally does not work *inside* a container today, so this
> path mostly applies to running the tool directly on a host.

OCI-rootless maps ids through `newuidmap`/`newgidmap` from `/etc/subuid`, so it
can honor a **sparse** map that adds just the high ids a build needs — without
widening the whole range. Splice them in at run time:

```bash
export BUILDAH_ISOLATION=oci
export THRILLHOUSE_EXTRA_GIDS=65534
image-thrillhouse build --config my-image.yaml
```

Both variables accept a comma-separated list of ids and inclusive `lo-hi` ranges,
e.g. `THRILLHOUSE_EXTRA_GIDS=65534,65530-65533`. Unset, the mapping is byte-for-byte
the historical default. (The `SUBID_COUNT` build arg works here too, if you'd
rather widen the contiguous block than name individual ids.)
