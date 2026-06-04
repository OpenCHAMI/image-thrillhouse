#!/bin/bash
# Integration test: `image-build build --manifest --layer`
#
# Drives a manifest layer through buildah and asserts:
#   1. building the base layer with the manifest-mode flags produces an
#      image committed to local storage under the computed hash tag
#   2. building the compute layer (which depends on the base) resolves
#      `from:` against the base's hash — i.e. parent_tag injection makes
#      it all the way to the buildah pull
#   3. re-running both builds with --skip-if-exists is a no-op (the
#      Exists check on the local publisher actually returns true)
#
# Per the original design, manifest mode builds a SINGLE named layer at a
# time. Ordering across layers is the user's responsibility, so this
# script does it explicitly (base first, then compute) rather than asking
# the tool to walk the DAG.
#
# image-build doesn't cross-compile, so we only ever build for the host
# arch. Maps `uname -m` to one of {x86_64, aarch64}; anything else is a
# hard fail with a clear message.
#
# Storage persistence:
#   Each `podman run --rm` would normally throw away the container's
#   image store on exit, which kills any locally-committed image as soon
#   as the build finishes. Two things working together fix this:
#
#     1. A named podman volume ($STORAGE_VOLUME, default
#        image-build-test-storage) mounted on /var/lib/containers/storage
#        carries the store across --rm boundaries.
#
#     2. CONTAINERS_STORAGE_CONF=/etc/containers/storage.conf passed to
#        the container. The image runs as a non-root user, so rootless
#        buildah would otherwise ignore the system storage.conf and put
#        the store under $HOME — which is not covered by the volume.
#        Pointing at the system config restores graphroot to
#        /var/lib/containers/storage where the volume is mounted.
#
#   The volume persists between test runs too — set RESET_STORAGE=1 to
#   start from an empty volume.
#
# Deliberately no `set -e`: each phase must be allowed to fail and report
# independently, otherwise the first podman failure aborts the suite with
# no diagnostic output.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUTPUT_DIR="${SCRIPT_DIR}/test-output/manifests-build"
# /opt/images is where the compute layer's squashfs publisher writes. The
# container runs as USER builder (uid 1001) and can't mkdir /opt/images
# itself, so we mount a writable host dir over the path. SQUASHFS_DIR is
# host-side; mounted as /opt/images inside the container in run_image_build.
SQUASHFS_DIR="${OUTPUT_DIR}/squashfs"
mkdir -p "$OUTPUT_DIR" "$SQUASHFS_DIR"
# Be permissive with the squashfs dir: rootless podman maps container uid
# 1001 to a high host uid (100000+1000), which has no claim on a dir
# created by the host user. World-writable lets the build write here
# without the user having to wrangle uid mappings.
chmod 0777 "$SQUASHFS_DIR"

HOST_ARCH_RAW=$(uname -m)
case "$HOST_ARCH_RAW" in
    x86_64|amd64)  HOST_ARCH=x86_64  ;;
    aarch64|arm64) HOST_ARCH=aarch64 ;;
    *)
        echo "✗ Unsupported host architecture: $HOST_ARCH_RAW"
        echo "  image-build doesn't cross-compile; this suite only runs on x86_64 or aarch64."
        exit 1
        ;;
esac

BASE_LAYER="rocky-base-${HOST_ARCH}"
COMPUTE_LAYER="rocky-compute-${HOST_ARCH}"
MANIFEST="/tests/manifests/rocky-multiarch.yaml"

echo "════════════════════════════════════════════════════════════════"
echo "Manifests Test: build --manifest --layer (single-layer builds)"
echo "  host arch:      $HOST_ARCH_RAW ($HOST_ARCH)"
echo "  manifest:       $MANIFEST"
echo "  base layer:     $BASE_LAYER"
echo "  compute layer:  $COMPUTE_LAYER"
echo "  storage volume: ${STORAGE_VOLUME:-image-build-test-storage} (RESET_STORAGE=1 to clear)"
echo "  squashfs dir:   $SQUASHFS_DIR"
echo "════════════════════════════════════════════════════════════════"
echo ""

TOTAL_TESTS=0
PASSED_TESTS=0
FAILED_TESTS=0

pass() {
    echo "  ✓ PASSED: $1"
    PASSED_TESTS=$((PASSED_TESTS + 1))
}
fail() {
    echo "  ✗ FAILED: $1"
    FAILED_TESTS=$((FAILED_TESTS + 1))
}

dump_log() {
    local log_file="$1"
    if [ -s "$log_file" ]; then
        echo "    --- last 20 lines of $log_file ---"
        tail -20 "$log_file" | sed 's/^/    /'
    fi
}

# The container's local image storage lives at /var/lib/containers/storage
# (see Dockerfile's storage.conf). With `podman run --rm`, that directory
# is destroyed at container exit, which means a base image committed in
# one invocation is gone before the next invocation can pull it as a
# parent. The dnf/zypper/apt parent tests dodge this by sourcing parents
# from a remote registry (`from: docker://rockylinux:9`) — but our
# rocky-compute layer's `from:` is `localhost/rocky-base:<computed-hash>`,
# which only ever exists in local storage.
#
# Mount a named podman volume on /var/lib/containers/storage so the
# storage tree survives across --rm boundaries. The volume persists
# between test runs too, which incidentally gives Phase 3
# (--skip-if-exists) cached state to work against. Set RESET_STORAGE=1
# to clear it before the run.
STORAGE_VOLUME="${STORAGE_VOLUME:-image-build-test-storage}"

if [ "${RESET_STORAGE:-0}" = "1" ]; then
    if podman volume exists "$STORAGE_VOLUME" 2>/dev/null; then
        echo "RESET_STORAGE=1: removing existing volume $STORAGE_VOLUME"
        podman volume rm "$STORAGE_VOLUME" >/dev/null
    fi
fi
# `podman volume create` is idempotent if the volume already exists.
podman volume create "$STORAGE_VOLUME" >/dev/null

# Run image-build inside the test container with the full privileged set
# (build needs buildah, which needs user namespaces / fuse). Two mounts
# carry state across --rm boundaries:
#
#   1. The storage volume on /var/lib/containers/storage carries the
#      buildah image store, so a layer committed in one invocation
#      survives to be the parent of the next invocation.
#
#   2. CONTAINERS_STORAGE_CONF points buildah at the system storage.conf
#      baked into the image. The container runs as USER builder (uid
#      1001), so buildah runs rootless and would otherwise IGNORE
#      /etc/containers/storage.conf and use $HOME/.local/share/containers/
#      storage — which is NOT covered by the volume mount above. Without
#      this env, Phase 1's image is written to /home/builder/... and
#      vanishes on --rm; Phase 2 then can't find the parent and buildah
#      falls back to treating localhost/rocky-base:<tag> as a remote
#      docker registry ref. CONTAINERS_STORAGE_CONF forces rootless
#      buildah to honor the system config, which graphroots at the
#      volume-mounted path.
run_image_build() {
    podman run --rm \
        --device /dev/fuse \
        --cap-add=SYS_ADMIN \
        --cap-add=SETUID \
        --cap-add=SETGID \
        --security-opt seccomp=unconfined \
        --security-opt label=disable \
        -v "${SCRIPT_DIR}/tests:/tests:Z" \
        -v "${OUTPUT_DIR}:/output:Z" \
        -v "${SQUASHFS_DIR}:/opt/images:Z" \
        -v "${STORAGE_VOLUME}:/var/lib/containers/storage" \
        -e BUILDAH_ISOLATION=chroot \
        -e CONTAINERS_STORAGE_CONF=/etc/containers/storage.conf \
        image-build:test \
        image-build --log-level info "$@"
}

echo "Preparing image-build container (if needed)..."
NEEDS_BUILD=0
if [ "${REBUILD_IMAGE:-0}" = "1" ]; then
    NEEDS_BUILD=1
elif ! podman image exists image-build:test && ! podman image exists localhost/image-build:test; then
    NEEDS_BUILD=1
fi
if [ "$NEEDS_BUILD" = "1" ]; then
    if ! (cd "${SCRIPT_DIR}" && podman build -t image-build:test -f Dockerfile . \
            > "${OUTPUT_DIR}/container-build.log" 2>&1); then
        echo "✗ Container build FAILED. See ${OUTPUT_DIR}/container-build.log"
        tail -30 "${OUTPUT_DIR}/container-build.log" | sed 's/^/    /'
        exit 1
    fi
    echo "✓ Container built"
else
    echo "✓ Container already exists (set REBUILD_IMAGE=1 to force rebuild)"
fi
echo ""

echo "════════════════════════════════════════════════════════════════"
echo "Phase 1: build the base layer from clean state"
echo "════════════════════════════════════════════════════════════════"
echo ""

TOTAL_TESTS=$((TOTAL_TESTS + 1))
echo "[$TOTAL_TESTS] build --manifest --layer $BASE_LAYER"
if run_image_build build --manifest "$MANIFEST" --layer "$BASE_LAYER" \
       > "${OUTPUT_DIR}/build-base.log" 2>&1
then
    if grep -q "computed tag.*${BASE_LAYER}" "${OUTPUT_DIR}/build-base.log"; then
        pass "base layer built and tag was computed"
    else
        fail "no 'computed tag' log line for ${BASE_LAYER}"
        dump_log "${OUTPUT_DIR}/build-base.log"
    fi
else
    fail "build base exited non-zero"
    dump_log "${OUTPUT_DIR}/build-base.log"
fi

echo ""
echo "════════════════════════════════════════════════════════════════"
echo "Phase 2: build the compute layer (parent_tag must resolve)"
echo "════════════════════════════════════════════════════════════════"
echo ""

TOTAL_TESTS=$((TOTAL_TESTS + 1))
echo "[$TOTAL_TESTS] build --manifest --layer $COMPUTE_LAYER"
if run_image_build build --manifest "$MANIFEST" --layer "$COMPUTE_LAYER" \
       > "${OUTPUT_DIR}/build-compute.log" 2>&1
then
    # The compute template's `from: localhost/rocky-base:{{ .parent_tag }}`
    # must render to a concrete tag that exists in local storage. If
    # parent_tag injection were broken, buildah would fail to pull the
    # base image and the build would error before getting here.
    if grep -q "computed tag.*${COMPUTE_LAYER}" "${OUTPUT_DIR}/build-compute.log"; then
        pass "compute layer built (parent_tag resolved to a real base image)"
    else
        fail "no 'computed tag' log line for ${COMPUTE_LAYER}"
        dump_log "${OUTPUT_DIR}/build-compute.log"
    fi
else
    fail "build compute exited non-zero"
    dump_log "${OUTPUT_DIR}/build-compute.log"
fi

# rocky-compute.yaml has `publish: - type: squashfs` writing to
# /opt/images/rocky-base-<arch>.squashfs. Confirm the file actually
# appeared in the host-side mount — end-to-end check that the squashfs
# publisher's Publish() ran without silently swallowing an error.
TOTAL_TESTS=$((TOTAL_TESTS + 1))
echo "[$TOTAL_TESTS] squashfs publisher wrote /opt/images/rocky-base-${HOST_ARCH}.squashfs"
SQUASHFS_FILE="${SQUASHFS_DIR}/rocky-base-${HOST_ARCH}.squashfs"
if [ -s "$SQUASHFS_FILE" ]; then
    bytes=$(stat -c %s "$SQUASHFS_FILE" 2>/dev/null || stat -f %z "$SQUASHFS_FILE")
    pass "squashfs file exists (${bytes} bytes)"
else
    fail "squashfs file not found at $SQUASHFS_FILE"
    if [ -d "$SQUASHFS_DIR" ]; then
        echo "    --- contents of $SQUASHFS_DIR ---"
        ls -la "$SQUASHFS_DIR" | sed 's/^/    /'
    fi
fi

echo ""
echo "════════════════════════════════════════════════════════════════"
echo "Phase 3: --skip-if-exists short-circuits warm rebuilds"
echo "════════════════════════════════════════════════════════════════"
echo ""

TOTAL_TESTS=$((TOTAL_TESTS + 1))
echo "[$TOTAL_TESTS] re-build base with --skip-if-exists is a no-op"
if run_image_build build --manifest "$MANIFEST" --layer "$BASE_LAYER" \
       --skip-if-exists \
       > "${OUTPUT_DIR}/skip-base.log" 2>&1
then
    if grep -q 'skipping build, image already exists' "${OUTPUT_DIR}/skip-base.log"; then
        pass "base reported skipped"
    else
        fail "expected 'skipping build, image already exists' log line"
        dump_log "${OUTPUT_DIR}/skip-base.log"
    fi
else
    fail "build base --skip-if-exists exited non-zero"
    dump_log "${OUTPUT_DIR}/skip-base.log"
fi

TOTAL_TESTS=$((TOTAL_TESTS + 1))
echo "[$TOTAL_TESTS] re-build compute with --skip-if-exists is a no-op"
if run_image_build build --manifest "$MANIFEST" --layer "$COMPUTE_LAYER" \
       --skip-if-exists \
       > "${OUTPUT_DIR}/skip-compute.log" 2>&1
then
    if grep -q 'skipping build, image already exists' "${OUTPUT_DIR}/skip-compute.log"; then
        pass "compute reported skipped"
    else
        fail "expected 'skipping build, image already exists' log line"
        dump_log "${OUTPUT_DIR}/skip-compute.log"
    fi
else
    fail "build compute --skip-if-exists exited non-zero"
    dump_log "${OUTPUT_DIR}/skip-compute.log"
fi

echo ""
echo "════════════════════════════════════════════════════════════════"
echo "Test Results Summary"
echo "════════════════════════════════════════════════════════════════"
echo ""
echo "Total Tests:  $TOTAL_TESTS"
echo "Passed:       $PASSED_TESTS"
echo "Failed:       $FAILED_TESTS"
echo ""
echo "Output: $OUTPUT_DIR"
echo ""

if [ $FAILED_TESTS -eq 0 ]; then
    echo "✓ All build --manifest tests passed!"
    exit 0
else
    echo "✗ Some build --manifest tests failed"
    exit 1
fi
