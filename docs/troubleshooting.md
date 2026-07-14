# Troubleshooting

## Permission errors

Make sure you're running with appropriate privileges:

```bash
# Rootless (recommended)
image-thrillhouse build --config my-image.yaml

# Root
sudo image-thrillhouse build --config my-image.yaml
```

If you're running inside a container, double-check the capabilities and security flags in [container-usage.md](container-usage.md#flag-explanations).

## Buildah not found

```bash
# Fedora / RHEL / Rocky
sudo dnf install buildah

# Ubuntu / Debian
sudo apt install buildah

# openSUSE
sudo zypper install buildah
```

## Package manager fails

- Ensure repository URLs are reachable
- Check GPG key configuration (see [configuration.md](configuration.md#gpg-key-import))
- For scratch builds, verify the package manager is installed on the host (or use the pre-built container, which includes them all)
- Re-run with `--log-level debug` for more detail (full package lists, raw package-manager output, exact install commands)

## Storage, mount, or isolation errors

Errors mentioning overlay mounts, bind mounts, user namespaces, or
containers-storage come from the container runtime libraries, not from
image-thrillhouse itself. Their internal logging is suppressed by default —
even at `--log-level debug` — because it is far too verbose for normal use.
To see it, add `--container-debug`:

```bash
image-thrillhouse --log-level debug --container-debug build --config my.yaml
```

## "did not get container create message from subprocess: EOF"

This error comes from buildah's OCI runtime while it sets up a **private
network namespace** for the build container (via netavark/CNI). On a host
without a configured rootful netavark/CNI stack, that per-container network
setup fails and the runtime subprocess dies before the package manager runs.

image-thrillhouse defaults the build container to the **host** network
namespace, which skips netavark entirely — a build only needs outbound access
to package repositories, which the host already has. If you are seeing this
error you are likely on an older build; rebuilding from current `main` resolves
it.

To opt back into a private network namespace (requires a working netavark/CNI
setup on the host):

```bash
BUILDAH_HOST_NETWORK=false image-thrillhouse build --config my-image.yaml
```

As a fallback that avoids the OCI runtime altogether, chroot isolation also
forces host networking:

```bash
BUILDAH_ISOLATION=chroot image-thrillhouse build --config my-image.yaml
```

## "chown: … Invalid argument" during a build

A build step fails with something like:

```
chown: changing ownership of '/run/dnsmasq/': Invalid argument
```

This means the target image assigns a uid/gid **above** the rootless subordinate
range mapped into the build. The usual culprit is Debian/Ubuntu's `nogroup`
(gid **65534**), which sits above the default `builder:2000:50000` range
(container ids `0..49999`). `chown user` alone works — only the group (or a high
uid) fails — because the user's id is inside the range while the group's isn't.

Widen the range so the id is mapped. Under chroot isolation (the container
default) this takes two steps — a wider **container** `/etc/subuid` **and** a host
range large enough to hold it — covered in full, with commands, in
[container-usage.md](container-usage.md#rootless-uidgid-ranges).

If you instead see `newgidmap: … Operation not permitted` / `Falling back to
single mapping`, the wider range didn't fit in the outer namespace: enlarge the
host `/etc/subuid`/`/etc/subgid` and run `podman system migrate` (podman caches
the old size). Same section covers this.

## SquashFS creation fails

Install `squashfs-tools`:

```bash
# RHEL / Rocky / Fedora
sudo dnf install squashfs-tools

# Ubuntu / Debian
sudo apt install squashfs-tools

# openSUSE
sudo zypper install squashfs
```

## OpenSCAP installation issues

If OpenSCAP packages aren't found:

```bash
# RHEL / Rocky — ensure AppStream is enabled
dnf config-manager --set-enabled appstream
dnf install openscap-utils scap-security-guide bzip2
```

```bash
# Debian / Ubuntu
apt-get install libopenscap8 ssg-debian ssg-debderived bzip2
```

## Stale binary in integration tests

If a test still fails after a code change you believe should fix it, the container test image is probably stale. Force a rebuild:

```bash
REBUILD_IMAGE=1 ./run-all-tests.sh
```

See [development.md](development.md#rebuilding-the-test-image-after-a-code-change) for context.
