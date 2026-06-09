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
- Re-run with `--log-level debug` for more detail

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
