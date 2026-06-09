.PHONY: all build clean install test deb rpm

# Build variables
BINARY_NAME=image-thrillhouse
VERSION=0.1.0
GO=go
GOFLAGS=-v
BUILD_DIR=.
INSTALL_DIR=/usr/local/bin
RPMBUILD_DIR=$(HOME)/rpmbuild

# Default target
all: build

# Build the binary
build:
	$(GO) build $(GOFLAGS) -o $(BINARY_NAME) ./cmd/image-thrillhouse

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

# Run tests
test:
	$(GO) test -v ./...

# Build Debian package
deb:
	dpkg-buildpackage -us -uc -b

# Build source package
deb-source:
	dpkg-buildpackage -us -uc -S

# Build RPM package
rpm: rpm-prep
	rpmbuild -bb $(RPMBUILD_DIR)/SPECS/$(BINARY_NAME).spec

# Prepare RPM build environment
rpm-prep:
	mkdir -p $(RPMBUILD_DIR)/{BUILD,RPMS,SOURCES,SPECS,SRPMS}
	mkdir -p $(BINARY_NAME)-$(VERSION)
	cp -r * $(BINARY_NAME)-$(VERSION)/ 2>/dev/null || true
	tar czf $(RPMBUILD_DIR)/SOURCES/$(BINARY_NAME)-$(VERSION).tar.gz $(BINARY_NAME)-$(VERSION)
	rm -rf $(BINARY_NAME)-$(VERSION)
	cp $(BINARY_NAME).spec $(RPMBUILD_DIR)/SPECS/

# Build RPM source package
rpm-source: rpm-prep
	rpmbuild -bs $(RPMBUILD_DIR)/SPECS/$(BINARY_NAME).spec

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
