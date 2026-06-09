# Building Debian Package for image-thrillhouse

This directory contains the necessary configuration files to build a Debian package (.deb) for the image-thrillhouse tool.

## Prerequisites

On a Debian/Ubuntu system, install the following packages:

```bash
sudo apt-get update
sudo apt-get install -y \
    debhelper \
    dh-golang \
    golang-go \
    devscripts \
    build-essential \
    fakeroot
```

Note: This requires Go 1.26 or later. If your distribution doesn't have Go 1.26, you may need to:
1. Download Go 1.26+ from https://go.dev/dl/
2. Update the `debian/control` file to adjust the Go version requirement

## Building the Package

### Method 1: Using dpkg-buildpackage (Recommended)

From the repository root:

```bash
# Build binary package only (no source package)
dpkg-buildpackage -us -uc -b

# Or use the Makefile
make deb
```

This will create the `.deb` file in the parent directory (one level up from the repository root).

### Method 2: Using debuild

```bash
debuild -us -uc -b
```

### Method 3: Using the Makefile

```bash
# Build the package
make deb

# Or just build the binary
make build

# Install locally
sudo make install
```

## Package Files

The following files are created during the build:

- `../image-thrillhouse_0.1.0-1_amd64.deb` - The binary package
- `../image-thrillhouse_0.1.0-1_amd64.buildinfo` - Build information
- `../image-thrillhouse_0.1.0-1_amd64.changes` - Changes file

## Installing the Package

Once built, install the package with:

```bash
sudo dpkg -i ../image-thrillhouse_0.1.0-1_amd64.deb

# If there are dependency issues, run:
sudo apt-get install -f
```

## Package Information

- **Package name**: image-thrillhouse
- **Version**: 0.1.0-1
- **Architecture**: amd64 (or your system architecture)
- **Dependencies**: buildah, libgpgme-dev, libbtrfs-dev, libdevmapper-dev
- **Recommends**: squashfs-tools
- **Suggests**: podman

## Files Installed

The package installs:
- `/usr/bin/image-thrillhouse` - The main binary

## Testing on macOS

Since you're running on macOS, you won't be able to build or test the Debian package locally. You have several options:

### Option 1: Use Docker/Podman

```bash
# Create a Debian build container
podman run -it --rm -v $(pwd):/workspace debian:bookworm bash

# Inside the container:
cd /workspace
apt-get update
apt-get install -y debhelper dh-golang golang-go devscripts build-essential fakeroot
dpkg-buildpackage -us -uc -b
```

### Option 2: Use GitHub Actions or CI/CD

Set up a GitHub Actions workflow to build the package on Linux:

```yaml
name: Build Debian Package

on: [push, pull_request]

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - name: Install dependencies
        run: |
          sudo apt-get update
          sudo apt-get install -y debhelper dh-golang golang-go devscripts build-essential
      - name: Build package
        run: dpkg-buildpackage -us -uc -b
      - name: Upload artifact
        uses: actions/upload-artifact@v3
        with:
          name: debian-package
          path: ../*.deb
```

### Option 3: Use a Remote Linux Server

Copy the repository to a Linux server and build there:

```bash
# On your Mac
rsync -av . user@linux-server:/path/to/image-thrillhouse/

# On the Linux server
ssh user@linux-server
cd /path/to/image-thrillhouse
sudo apt-get install -y debhelper dh-golang golang-go devscripts build-essential
dpkg-buildpackage -us -uc -b
```

## Customization

### Updating the Version

Edit `debian/changelog` and update the version number. Follow the Debian changelog format:

```
image-thrillhouse (0.2.0-1) unstable; urgency=medium

  * New upstream release
  * Add new features...

 -- Travis Cotton <travis@example.com>  [date]
```

### Updating Dependencies

Edit `debian/control` to add or modify:
- Build dependencies in the `Build-Depends` field
- Runtime dependencies in the `Depends` field

### Modifying the Build Process

Edit `debian/rules` to customize how the package is built.

## Cleaning Up

To clean build artifacts:

```bash
make clean
# or
debian/rules clean
```

## Important Notes

1. **License**: The `debian/copyright` file currently has a placeholder. You should update it with the actual license from the project.

2. **Maintainer**: Update the maintainer email in:
   - `debian/control`
   - `debian/changelog`
   - `debian/copyright`

3. **Testing**: The package build skips tests by default (see `debian/rules`) since the tests require Linux-specific dependencies and a container runtime. You should test the installed package manually after installation.

4. **Go Dependencies**: The package uses `dh-golang` which handles Go dependencies automatically using `go.mod`.

## Troubleshooting

### Build fails with "Go version too old"

Your distribution may not have Go 1.26. Either:
1. Install a newer Go version manually from https://go.dev/dl/
2. Update `debian/control` to require an older Go version if the code is compatible

### Missing dependencies during build

Install the build dependencies listed in `debian/control`:

```bash
sudo apt-get install debhelper-compat dh-golang golang-go
```

### Package has unmet dependencies after install

Install dependencies with:

```bash
sudo apt-get install -f
```

Or install the dependencies manually before installing the package.

## References

- [Debian New Maintainers' Guide](https://www.debian.org/doc/manuals/maint-guide/)
- [Debian Policy Manual](https://www.debian.org/doc/debian-policy/)
- [dh-golang documentation](https://pkg.go.dev/github.com/Debian/dh-golang)
