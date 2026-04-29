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

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y \
    buildah \
    mmdebstrap \
    dnf \
    fakeroot \
    fakechroot \
    fuse-overlayfs \
    libcap2-bin \
    vim \
    rpm \
    zypper \
    curl \
    squashfs-tools \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /src/image-build /usr/local/bin/image-build

RUN chcon -t container_runtime_exec_t /usr/local/bin/image-build 2>/dev/null || true

RUN touch /etc/subgid /etc/subuid \
	setcap cap_setuid=ep "$(command -v newuidmap)" && \
    	setcap cap_setgid=ep "$(command -v newgidmap)" &&\
    	chmod 0755 "$(command -v newuidmap)" && \
    	chmod 0755 "$(command -v newgidmap)" && \
	chmod g=u /etc/subgid /etc/subuid /etc/passwd &&\
	echo builder:10000:65536 > /etc/subuid &&\
	echo builder:10000:65536 > /etc/subgid

RUN useradd -m --uid 1001 builder && \
    chown -R builder /home/builder

RUN mkdir -p /etc/containers /run/containers/storage /var/lib/containers/storage && \
    cat > /etc/containers/storage.conf << EOF
[storage]
driver = "overlay"
runroot = "/run/containers/storage"
graphroot = "/var/lib/containers/storage"

[storage.options]
mount_program = "/usr/bin/fuse-overlayfs"
EOF

RUN echo "user_allow_other" >> /etc/fuse.conf

USER builder
WORKDIR /home/builder
