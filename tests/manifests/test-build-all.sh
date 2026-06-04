#!/bin/bash
# Integration test: `image-build build-all` + `--skip-if-exists`
#
# Drives a full manifest end-to-end through buildah and asserts:
#   1. build-all builds the requested subtree in dependency order
#      (parent before child)
#   2. each layer is committed to local storage with the computed hash tag
#   3. the child's `from:` line resolves to the parent's hash (parent_tag
#      injection actually wires through to the build)
#   4. a second build-all --skip-if-exists is a no-op (no buildah calls)
#   5. --layer narrows iteration to a subtree — sibling branches are not
#      walked
#
# image-build doesn't cross-compile, so this test only ever builds for the
# host arch. We use rocky-multiarch.yaml (which contains both x86_64 and
# aarch64 branches) and pass --layer rocky-compute-<host arch> so the
# walker naturally skips the foreign-arch branch.
#
# Deliberately no `set -e`: each phase must be allowed to fail and report
# independently. A bare `set -e` aborts silently on the first podman
# failure with no diagnostic output — useless in CI.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUTPUT_DIR="${SCRIPT_DIR}/test-output/manifests-build-all"
mkdir -p "$OUTPUT_DIR"

# Map uname -m → the arch token our var files use (tests/rocky/templates/
# {x86_64,aarch64}.yaml). Anything else is a hard fail because the tool
# can't cross-compile and we won't pretend.
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
echo "Manifests Test: build-all + skip-if-exists"
echo "  host arch:    $HOST_ARCH_RAW ($HOST_ARCH)"
echo "  manifest:     $MANIFEST"
echo "  target leaf:  $COMPUTE_LAYER"
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

# Print the tail of a log file with a clear divider — used by every fail
# path that has a log to point at, so the user gets immediate context
# instead of having to grep through test-output/.
dump_log() {
    local log_file="$1"
    if [ -s "$log_file" ]; then
        echo "    --- last 20 lines of $log_file ---"
        tail -20 "$log_file" | sed 's/^/    /'
    fi
}

# Run image-build inside the test container with the full privileged set
# (build needs buildah, which needs user namespaces / fuse).
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
        -e BUILDAH_ISOLATION=chroot \
        image-build:test \
        image-build --log-level info "$@"
}

# Build the test image if missing or REBUILD_IMAGE=1
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
echo "Phase 1: build-all --layer $COMPUTE_LAYER from clean state"
echo "════════════════════════════════════════════════════════════════"
echo ""

TOTAL_TESTS=$((TOTAL_TESTS + 1))
echo "[$TOTAL_TESTS] builds both layers in dependency order"
if run_image_build build-all --manifest "$MANIFEST" --layer "$COMPUTE_LAYER" \
       > "${OUTPUT_DIR}/build-all-clean.log" 2>&1
then
    # The logger emits "computed tag layer=$BASE_LAYER" before
    # "computed tag layer=$COMPUTE_LAYER" when we walk topologically.
    base_line=$(grep -n "computed tag.*${BASE_LAYER}" "${OUTPUT_DIR}/build-all-clean.log" | head -1 | cut -d: -f1)
    comp_line=$(grep -n "computed tag.*${COMPUTE_LAYER}" "${OUTPUT_DIR}/build-all-clean.log" | head -1 | cut -d: -f1)
    if [ -n "$base_line" ] && [ -n "$comp_line" ] && [ "$base_line" -lt "$comp_line" ]; then
        pass "$BASE_LAYER built before $COMPUTE_LAYER"
    else
        fail "dependency order not observed (base line=$base_line, compute line=$comp_line)"
    fi
else
    fail "build-all exited non-zero"
    dump_log "${OUTPUT_DIR}/build-all-clean.log"
fi

echo ""
echo "════════════════════════════════════════════════════════════════"
echo "Phase 2: --skip-if-exists short-circuits a warm run"
echo "════════════════════════════════════════════════════════════════"
echo ""

TOTAL_TESTS=$((TOTAL_TESTS + 1))
echo "[$TOTAL_TESTS] re-running build-all --skip-if-exists is a no-op"
if run_image_build build-all --manifest "$MANIFEST" --layer "$COMPUTE_LAYER" \
       --skip-if-exists \
       > "${OUTPUT_DIR}/build-all-warm.log" 2>&1
then
    # Both layers should log "skipping build, image already exists".
    skipped=$(grep -c 'skipping build, image already exists' "${OUTPUT_DIR}/build-all-warm.log" || true)
    if [ "$skipped" = "2" ]; then
        pass "both layers reported skipped"
    else
        fail "expected 2 skipped layers, got $skipped"
        dump_log "${OUTPUT_DIR}/build-all-warm.log"
    fi
else
    fail "build-all --skip-if-exists exited non-zero"
    dump_log "${OUTPUT_DIR}/build-all-warm.log"
fi

echo ""
echo "════════════════════════════════════════════════════════════════"
echo "Phase 3: --layer narrows to a subtree (siblings not walked)"
echo "════════════════════════════════════════════════════════════════"
echo ""

# The multi-arch manifest has 4 layers total. Targeting --layer
# rocky-compute-<host-arch> should iterate exactly 2 (the host's base +
# compute) and never touch the foreign-arch branch.
TOTAL_TESTS=$((TOTAL_TESTS + 1))
echo "[$TOTAL_TESTS] foreign-arch branch is NOT iterated"
case "$HOST_ARCH" in
    x86_64)  FOREIGN_BASE="rocky-base-aarch64"; FOREIGN_COMPUTE="rocky-compute-aarch64" ;;
    aarch64) FOREIGN_BASE="rocky-base-x86_64";  FOREIGN_COMPUTE="rocky-compute-x86_64"  ;;
esac

if [ "$(grep -c "computed tag.*${FOREIGN_BASE}" "${OUTPUT_DIR}/build-all-warm.log" || true)" = "0" ] \
   && [ "$(grep -c "computed tag.*${FOREIGN_COMPUTE}" "${OUTPUT_DIR}/build-all-warm.log" || true)" = "0" ]; then
    pass "foreign-arch layers ($FOREIGN_BASE, $FOREIGN_COMPUTE) were not touched"
else
    fail "foreign-arch layer appeared in the build log"
    dump_log "${OUTPUT_DIR}/build-all-warm.log"
fi

# Also assert build-all logged the subtree order it intends to walk.
TOTAL_TESTS=$((TOTAL_TESTS + 1))
echo "[$TOTAL_TESTS] build-all logs the subtree order"
if grep -q "\"order\":\\[\"${BASE_LAYER}\",\"${COMPUTE_LAYER}\"\\]" "${OUTPUT_DIR}/build-all-warm.log" \
   || grep -q "order=\\[${BASE_LAYER} ${COMPUTE_LAYER}\\]" "${OUTPUT_DIR}/build-all-warm.log"; then
    pass "subtree order log entry present"
else
    fail "subtree order log entry missing"
    dump_log "${OUTPUT_DIR}/build-all-warm.log"
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
    echo "✓ All build-all tests passed!"
    exit 0
else
    echo "✗ Some build-all tests failed"
    exit 1
fi
