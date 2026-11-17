# SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
#
# SPDX-License-Identifier: MIT

.PHONY: all build clean install test deb rpm

# Build variables
BINARY_NAME=image-thrillhouse
# The version comes from git (see scripts/version.sh): the release tag when
# building a tagged commit, otherwise the most recent tag plus "-debug".
# GIT_VERSION is the string stamped into the binary (e.g. v0.0.27-debug);
# VERSION is the package version derived from it (e.g. 0.0.27~debug), since
# RPM forbids '-' in Version and '~' sorts a debug build before its release.
# Override with `make GIT_VERSION=v1.2.3`.
GIT_VERSION ?= $(shell sh scripts/version.sh 2>/dev/null || echo v0.0.0-debug)
VERSION=$(subst -,~,$(patsubst v%,%,$(GIT_VERSION)))
GO=go
GOFLAGS=-v
# Build tags, kept identical to debian/rules and image-thrillhouse.spec so a
# locally built binary matches the one that ships. The graphdriver exclusions
# drop the btrfs and devicemapper backends, which would otherwise need
# libbtrfs-dev and libdevmapper-dev installed just to compile.
#
# This does NOT remove the gpgme/pkg-config requirement. On a host without the
# cgo dependencies (macOS, minimal CI runners), append the pure-Go signature
# backend — see docs/development.md:
#
#	make build BUILD_TAGS="$(BUILD_TAGS) containers_image_openpgp"
BUILD_TAGS=exclude_graphdriver_btrfs exclude_graphdriver_devicemapper
# Stamp the version into the binary so `image-thrillhouse version` reports
# the git-derived GIT_VERSION.
LDFLAGS=-ldflags "-X main.version=$(GIT_VERSION)"
BUILD_DIR=.
INSTALL_DIR=/usr/local/bin
RPMBUILD_DIR=$(HOME)/rpmbuild

# Default target
all: build

# Build the binary
build:
	$(GO) build $(GOFLAGS) -tags "$(BUILD_TAGS)" $(LDFLAGS) -o $(BINARY_NAME) ./cmd/image-thrillhouse

# Clean build artifacts
clean:
	rm -f $(BINARY_NAME)
	rm -rf debian/image-thrillhouse
	rm -rf debian/.debhelper
	rm -rf debian/files
	rm -rf debian/*.log
	rm -rf debian/*.substvars
	rm -rf debian/tmp
	rm -rf $(RPMBUILD_DIR)

# Install the binary
install: build
	install -D -m 0755 $(BINARY_NAME) $(DESTDIR)$(INSTALL_DIR)/$(BINARY_NAME)

# Run tests. Uses the same tags as build so the test binary and the shipped
# binary compile against the same code — previously `make test` passed no tags
# at all and failed to compile on any host lacking the btrfs/devicemapper
# development headers.
test:
	$(GO) test -tags "$(BUILD_TAGS)" -v ./...

# Build Debian package
deb:
	dpkg-buildpackage -us -uc -b

# Build source package
deb-source:
	dpkg-buildpackage -us -uc -S

# Build RPM package
rpm: rpm-prep
	rpmbuild -bb --define "app_version $(GIT_VERSION)" $(RPMBUILD_DIR)/SPECS/$(BINARY_NAME).spec

# Prepare RPM build environment
rpm-prep:
	mkdir -p $(RPMBUILD_DIR)/{BUILD,RPMS,SOURCES,SPECS,SRPMS}
	mkdir -p $(BINARY_NAME)-$(VERSION)
	cp -r * $(BINARY_NAME)-$(VERSION)/ 2>/dev/null || true
	tar czf $(RPMBUILD_DIR)/SOURCES/$(BINARY_NAME)-$(VERSION).tar.gz $(BINARY_NAME)-$(VERSION)
	rm -rf $(BINARY_NAME)-$(VERSION)
	sed "s/^Version:.*/Version:        $(VERSION)/" $(BINARY_NAME).spec > $(RPMBUILD_DIR)/SPECS/$(BINARY_NAME).spec

# Build RPM source package
rpm-source: rpm-prep
	rpmbuild -bs --define "app_version $(GIT_VERSION)" $(RPMBUILD_DIR)/SPECS/$(BINARY_NAME).spec

# Display help
help:
	@echo "Available targets:"
	@echo "  all         - Build the binary (default)"
	@echo "  build       - Build the binary"
	@echo "  clean       - Clean build artifacts"
	@echo "  install     - Install the binary to $(INSTALL_DIR)"
	@echo "  test        - Run Go tests"
	@echo "  deb         - Build Debian binary package"
	@echo "  deb-source  - Build Debian source package"
	@echo "  rpm         - Build RPM binary package"
	@echo "  rpm-source  - Build RPM source package"
	@echo "  rpm-prep    - Prepare RPM build environment"
	@echo "  help        - Display this help message"
