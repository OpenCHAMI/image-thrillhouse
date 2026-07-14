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

## Rootless uid/gid ranges

The build runs rootless, so container uids/gids are mapped through a subordinate
range. If a target image assigns an id **above** that range, a `chown user:group`
against it fails with `chown: … Invalid argument` — most commonly on
Debian/Ubuntu's `nogroup` (gid **65534**). Understanding the mapping is the key to
fixing it.

### Two nested user namespaces

Inside the container, isolation is `chroot` (the image sets
`BUILDAH_ISOLATION=chroot`). Under chroot there are **two** nested user
namespaces:

1. **Outer** — `podman run` creates it from **your host's** `/etc/subuid` /
   `/etc/subgid` (the ranges assigned to the user running `podman`).
2. **Inner** — the tool re-execs into a second namespace built from the
   **container's** `/etc/subuid` (`builder:2000:50000`). buildah's chroot run then
   identity-maps this inner namespace, so **the inner namespace's range is the
   ceiling** for what a build can `chown` to.

With `builder:2000:50000`, the inner namespace owns container ids `0..49999`.
Anything above that (gid 65534) has no mapping — hence the error. The range is
deliberately narrow by default because a wider one can collide with host uid/gid
allocations, so widening it is opt-in.

Two things must both be true to reach a high id:

- the **container's** `/etc/subuid`/`/etc/subgid` must include it, **and**
- the **host's** `/etc/subuid`/`/etc/subgid` must be large enough that the outer
  namespace can contain the wider inner range.

### Widening the range for the published image

No rebuild is required — bind-mount a wider range over the container's
`/etc/subuid` and `/etc/subgid`, and make sure the host range can hold it.

**1. Enlarge the host range** (once, as the user who runs `podman`). To reach gid
65534 the inner range needs `2000 + 65536 = 67536` ids, so give the host user at
least that many — a generous round allocation is easiest:

```bash
# /etc/subuid and /etc/subgid on the host, e.g.:
#   youruser:100000:1200000
grep "^$USER:" /etc/subuid /etc/subgid

# podman caches the old namespace size, so it must re-read the files:
podman system migrate
podman unshare cat /proc/self/uid_map    # should now show the full range
```

**2. Bind-mount a wider container range** at run time. `builder:2000:65536` covers
container ids `0..65535`, which includes 65534:

```bash
printf 'builder:2000:65536\n' > /tmp/subid

podman run --rm \
  --device /dev/fuse \
  --cap-add=SYS_ADMIN --cap-add=SETUID --cap-add=SETGID \
  --security-opt seccomp=unconfined --security-opt label=disable \
  -v /tmp/subid:/etc/subuid:ro \
  -v /tmp/subid:/etc/subgid:ro \
  -v $(pwd)/my-image.yaml:/config.yaml:Z \
  -v $(pwd)/output:/output:Z \
  ghcr.io/openchami/image-thrillhouse:latest \
  image-thrillhouse build --config /config.yaml
```

If you build your own image instead, bake the wider range into the Dockerfile's
`echo builder:2000:… > /etc/subuid` lines rather than bind-mounting.

> **Note on the ceiling.** Because the inner range starts at 2000, it cannot reach
> gid 65534 unless the outer namespace holds at least ~67536 ids. On a host with
> the stock 65536-id allocation you will hit `newgidmap: … Operation not
> permitted` (the inner range won't fit) — step 1 is what lifts that ceiling.

> **OCI-rootless isolation** maps ids differently (via `newuidmap`/`newgidmap`
> directly, so a sparse single-id override is possible), but it generally does not
> work *inside* a container today, so the bind-mount approach above is the path for
> the published image.
