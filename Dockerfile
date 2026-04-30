# Single builder stage: compiles the Go binary once for all targets
FROM golang:1.26-bookworm AS builder
RUN apt-get update && apt-get install -y \
    libgpgme-dev \
    libassuan-dev \
    btrfs-progs \
    libbtrfs-dev \
    libdevmapper-dev \
    pkg-config \
    gcc
WORKDIR /src
COPY . .
RUN go build -o image-build ./cmd/image-build/

# Zypper stage: for building SUSE/openSUSE images with native Zypper
FROM opensuse/leap:latest AS zypper
RUN zypper install -y \
    buildah \
    fuse-overlayfs \
    vim \
    curl \
    squashfs \
    shadow \
    gpgme \
    device-mapper \
    && zypper clean --all

COPY --from=builder /src/image-build /usr/local/bin/image-build

RUN useradd -m --uid 1001 builder

RUN touch /etc/subgid /etc/subuid && \
    echo builder:10000:65536 > /etc/subuid && \
    echo builder:10000:65536 > /etc/subgid && \
    chmod 644 /etc/subuid /etc/subgid

RUN mkdir -p /etc/containers /run/containers/storage /var/lib/containers/storage && \
    printf '[storage]\ndriver = "vfs"\nrunroot = "/run/containers/storage"\ngraphroot = "/var/lib/containers/storage"\n' > /etc/containers/storage.conf && \
    chown -R builder:builder /home/builder /run/containers /var/lib/containers

USER builder
WORKDIR /home/builder

# APT stage: for building Debian/Ubuntu images with APT/mmdebstrap
FROM debian:bookworm-slim AS apt
RUN apt-get update && apt-get install -y \
    buildah \
    mmdebstrap \
    fakeroot \
    fakechroot \
    fuse-overlayfs \
    libcap2-bin \
    uidmap \
    vim \
    curl \
    squashfs-tools \
    libgpgme11 \
    libdevmapper1.02.1 \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /src/image-build /usr/local/bin/image-build

RUN chcon -t container_runtime_exec_t /usr/local/bin/image-build 2>/dev/null || true

RUN useradd -m --uid 1001 builder

RUN touch /etc/subgid /etc/subuid && \
	setcap cap_setuid=ep "$(command -v newuidmap)" && \
    	setcap cap_setgid=ep "$(command -v newgidmap)" && \
    	chmod 0755 "$(command -v newuidmap)" && \
    	chmod 0755 "$(command -v newgidmap)" && \
	chmod 644 /etc/subgid /etc/subuid && \
	echo builder:10000:65536 > /etc/subuid && \
	echo builder:10000:65536 > /etc/subgid

RUN mkdir -p /etc/containers /run/containers/storage /var/lib/containers/storage && \
    printf '[storage]\ndriver = "overlay"\nrunroot = "/run/containers/storage"\ngraphroot = "/var/lib/containers/storage"\n\n[storage.options]\nmount_program = "/usr/bin/fuse-overlayfs"\n' > /etc/containers/storage.conf && \
    chown -R builder:builder /home/builder /run/containers /var/lib/containers

RUN echo "user_allow_other" >> /etc/fuse.conf

USER builder
WORKDIR /home/builder

# DNF/RHEL 9 stage: for building RHEL 9/Rocky 9/Alma 9 images (default)
FROM almalinux:9 AS dnf
# curl doesn't like curl-minimum hence allowerasing
RUN dnf install -y --allowerasing \
    buildah \
    dnf \
    dnf-plugins-core \
    fuse-overlayfs \
    vim \
    curl \
    squashfs-tools \
    shadow-utils \
    gpgme \
    device-mapper-libs \
    && dnf clean all

COPY --from=builder /src/image-build /usr/local/bin/image-build

RUN useradd -m --uid 1001 builder

RUN touch /etc/subgid /etc/subuid && \
    echo builder:10000:65536 > /etc/subuid && \
    echo builder:10000:65536 > /etc/subgid && \
    chmod 644 /etc/subuid /etc/subgid

RUN mkdir -p /etc/containers /run/containers/storage /var/lib/containers/storage && \
    printf '[storage]\ndriver = "vfs"\nrunroot = "/run/containers/storage"\ngraphroot = "/var/lib/containers/storage"\n' > /etc/containers/storage.conf && \
    chown -R builder:builder /home/builder /run/containers /var/lib/containers

USER builder
WORKDIR /home/builder

# DNF/RHEL 10 stage: for building RHEL 10/Rocky 10/Alma 10 images
FROM almalinux:10 AS dnf10
RUN dnf install -y \
    buildah \
    dnf \
    dnf-plugins-core \
    fuse-overlayfs \
    vim \
    curl \
    squashfs-tools \
    shadow-utils \
    gpgme \
    device-mapper-libs \
    && dnf clean all

COPY --from=builder /src/image-build /usr/local/bin/image-build

RUN useradd -m --uid 1001 builder

RUN touch /etc/subgid /etc/subuid && \
    echo builder:10000:65536 > /etc/subuid && \
    echo builder:10000:65536 > /etc/subgid && \
    chmod 644 /etc/subuid /etc/subgid

RUN mkdir -p /etc/containers /run/containers/storage /var/lib/containers/storage && \
    printf '[storage]\ndriver = "vfs"\nrunroot = "/run/containers/storage"\ngraphroot = "/var/lib/containers/storage"\n' > /etc/containers/storage.conf && \
    chown -R builder:builder /home/builder /run/containers /var/lib/containers

USER builder
WORKDIR /home/builder

