%global debug_package %{nil}

Name:           image-build
Version:        0.1.0
Release:        1%{?dist}
Summary:        Go-based image builder wrapping buildah
License:        MIT
URL:            https://github.com/travisbcotton/image-build
Source0:        %{name}-%{version}.tar.gz

# BuildRequires intentionally left empty - Go is installed via GitHub Actions
Requires:       buildah
Requires:       gpgme-devel
Requires:       device-mapper-devel
Requires:       btrfs-progs-devel
Recommends:     squashfs-tools
Suggests:       podman

%description
A Go-based image builder that wraps buildah to create layered OS images
with multiple package manager support. This is the next-generation
replacement for the Python-based image-builder tool used by OpenCHAMI.

Features:
 - Multiple Package Managers: Support for DNF, Zypper, APT and mmdebstrap
 - Scratch & Parent Builds: Build from scratch or layer on top of existing images
 - Flexible Configuration: YAML-based declarative configuration
 - Multiple Publishers: Local container storage, SquashFS, Container Registry, S3
 - Structured Logging: JSON and text logging formats with configurable levels
 - Config Validation: Validate configurations without building

%prep
%setup -q -n %{name}-%{version}

%build
go build -v -o %{name} ./cmd/image-build

%install
install -D -m 0755 %{name} %{buildroot}%{_bindir}/%{name}

%files
%{_bindir}/%{name}

%changelog
* Tue Jun 02 2026 Travis Cotton <travis@example.com> - 0.1.0-1
- Initial release
- Go-based image builder wrapping buildah
- Support for multiple package managers (DNF, Zypper, APT, mmdebstrap)
- Support for scratch and parent builds
- YAML-based declarative configuration
- Multiple publishers: local, SquashFS, container registry, S3
- Structured logging with JSON and text formats
- Configuration validation
- OpenSCAP security scanning support
- Package removal support for minimal images
- GPG key import for repositories
