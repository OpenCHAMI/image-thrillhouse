.PHONY: all build clean install test deb

# Build variables
BINARY_NAME=image-build
VERSION=0.1.0
GO=go
GOFLAGS=-v
BUILD_DIR=.
INSTALL_DIR=/usr/local/bin

# Default target
all: build

# Build the binary
build:
	$(GO) build $(GOFLAGS) -o $(BINARY_NAME) ./cmd/image-build

# Clean build artifacts
clean:
	rm -f $(BINARY_NAME)
	rm -rf debian/image-build
	rm -rf debian/.debhelper
	rm -rf debian/files
	rm -rf debian/*.log
	rm -rf debian/*.substvars
	rm -rf debian/tmp

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
	@echo "  help        - Display this help message"
