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
RUN go mod tidy && go build -o image-thrillhouse ./cmd/image-thrillhouse/

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y \
    buildah \
    mmdebstrap \
    dnf \
    fakeroot \
    fakechroot \
    fuse-overlayfs \
    libcap2-bin \
    uidmap \
    vim \
    rpm \
    zypper \
    curl \
    squashfs-tools \
    libgpgme11 \
    libdevmapper1.02.1 \
    && rm -rf /var/lib/apt/lists/*

# Debian's rpm defaults %_dbpath to ~/.rpmdb for non-root users, which puts the
# rpm database at <rootPath>/home/builder/.rpmdb during scratch bootstraps and
# breaks dependency tracking across runs. Pin it to /var/lib/rpm here so every
# host-side rpm/dnf invocation lands the DB in the canonical location.
RUN printf '%%_dbpath /var/lib/rpm\n%%_dbpath_rebuild /var/lib/rpm\n' \
      > /etc/rpm/macros.dbpath

COPY --from=builder /src/image-thrillhouse /usr/local/bin/image-thrillhouse

RUN chcon -t container_runtime_exec_t /usr/local/bin/image-thrillhouse 2>/dev/null || true

# Create builder user first
RUN useradd -m --uid 1001 builder

# Set up subuid/subgid for the builder user
RUN touch /etc/subgid /etc/subuid && \
    echo builder:2000:50000 > /etc/subuid && \
    echo builder:2000:50000 > /etc/subgid && \
    chmod 644 /etc/subuid /etc/subgid

# Set capabilities on newuidmap/newgidmap
RUN setcap cap_setuid=ep "$(command -v newuidmap)" && \
    setcap cap_setgid=ep "$(command -v newgidmap)" && \
    chmod 0755 "$(command -v newuidmap)" && \
    chmod 0755 "$(command -v newgidmap)"

# Set proper ownership
RUN chown -R builder /home/builder

RUN mkdir -p /etc/containers /run/containers/storage /var/lib/containers/storage && \
    printf '%s\n' \
    '[storage]' \
    'driver = "overlay"' \
    'runroot = "/run/containers/storage"' \
    'graphroot = "/var/lib/containers/storage"' \
    '' \
    '[storage.options]' \
    'mount_program = "/usr/bin/fuse-overlayfs"' \
    > /etc/containers/storage.conf && \
    chown -R builder:builder /home/builder /run/containers /var/lib/containers

RUN echo "user_allow_other" >> /etc/fuse.conf

ENV BUILDAH_ISOLATION=chroot

USER builder
WORKDIR /home/builder

