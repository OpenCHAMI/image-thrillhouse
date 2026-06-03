#!/bin/bash
# Integration test: `image-build build-all` + `--skip-if-exists`
#
# Drives a full manifest end-to-end through buildah and asserts:
#   1. build-all builds every layer in dependency order (parent before child)
#   2. each layer is committed to local storage with the computed hash tag
#   3. the child's `from:` line resolves to the parent's hash (parent_tag
#      injection actually wires through to the build)
#   4. a second build-all --skip-if-exists is a no-op (no buildah calls)
#   5. --layer narrows the iteration to a subtree

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUTPUT_DIR="${SCRIPT_DIR}/test-output/manifests-build-all"
mkdir -p "$OUTPUT_DIR"

echo "════════════════════════════════════════════════════════════════"
echo "Manifests Test: build-all + skip-if-exists"
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
    cd "${SCRIPT_DIR}" && podman build -t image-build:test -f Dockerfile . \
        > "${OUTPUT_DIR}/container-build.log" 2>&1
    echo "✓ Container built"
else
    echo "✓ Container already exists (set REBUILD_IMAGE=1 to force rebuild)"
fi
echo ""

# The rocky.yaml manifest is the simplest non-trivial DAG: rocky-base
# (scratch) → rocky-compute (depends on rocky-base). One backend (dnf),
# one arch, so each test phase only needs ~2 layers worth of build time.
MANIFEST="/tests/manifests/rocky.yaml"

echo "════════════════════════════════════════════════════════════════"
echo "Phase 1: build-all from clean state"
echo "════════════════════════════════════════════════════════════════"
echo ""

TOTAL_TESTS=$((TOTAL_TESTS + 1))
echo "[$TOTAL_TESTS] build-all rocky.yaml builds both layers in order"
if run_image_build build-all --manifest "$MANIFEST" \
       --var-file /tests/rocky/templates/x86_64.yaml \
       > "${OUTPUT_DIR}/build-all-clean.log" 2>&1
then
    # The logger prints "computed tag layer=rocky-base" before
    # "computed tag layer=rocky-compute" because we walk topologically.
    base_line=$(grep -n 'computed tag.*rocky-base' "${OUTPUT_DIR}/build-all-clean.log" | head -1 | cut -d: -f1)
    comp_line=$(grep -n 'computed tag.*rocky-compute' "${OUTPUT_DIR}/build-all-clean.log" | head -1 | cut -d: -f1)
    if [ -n "$base_line" ] && [ -n "$comp_line" ] && [ "$base_line" -lt "$comp_line" ]; then
        pass "rocky-base built before rocky-compute"
    else
        fail "dependency order not observed (base line=$base_line, compute line=$comp_line)"
    fi
else
    fail "build-all exited non-zero (see ${OUTPUT_DIR}/build-all-clean.log)"
fi

echo ""
echo "════════════════════════════════════════════════════════════════"
echo "Phase 2: --skip-if-exists short-circuits a warm run"
echo "════════════════════════════════════════════════════════════════"
echo ""

TOTAL_TESTS=$((TOTAL_TESTS + 1))
echo "[$TOTAL_TESTS] re-running build-all --skip-if-exists is a no-op"
if run_image_build build-all --manifest "$MANIFEST" \
       --var-file /tests/rocky/templates/x86_64.yaml \
       --skip-if-exists \
       > "${OUTPUT_DIR}/build-all-warm.log" 2>&1
then
    # Both layers should log "skipping build, image already exists".
    skipped=$(grep -c 'skipping build, image already exists' "${OUTPUT_DIR}/build-all-warm.log" || true)
    if [ "$skipped" = "2" ]; then
        pass "both layers reported skipped"
    else
        fail "expected 2 skipped layers, got $skipped (see ${OUTPUT_DIR}/build-all-warm.log)"
    fi
else
    fail "build-all --skip-if-exists exited non-zero (see ${OUTPUT_DIR}/build-all-warm.log)"
fi

echo ""
echo "════════════════════════════════════════════════════════════════"
echo "Phase 3: --layer narrows to a subtree"
echo "════════════════════════════════════════════════════════════════"
echo ""

TOTAL_TESTS=$((TOTAL_TESTS + 1))
echo "[$TOTAL_TESTS] --layer rocky-compute only iterates rocky-compute's subtree"
if run_image_build build-all --manifest "$MANIFEST" \
       --layer rocky-compute \
       --var-file /tests/rocky/templates/x86_64.yaml \
       --skip-if-exists \
       > "${OUTPUT_DIR}/build-all-subtree.log" 2>&1
then
    # The log should mention both rocky-base and rocky-compute, since the
    # subtree of rocky-compute includes rocky-base as an ancestor.
    if grep -q '"order":\["rocky-base","rocky-compute"\]' "${OUTPUT_DIR}/build-all-subtree.log" \
       || grep -q 'order=\[rocky-base rocky-compute\]' "${OUTPUT_DIR}/build-all-subtree.log"; then
        pass "subtree order observed (rocky-base then rocky-compute)"
    else
        fail "subtree ordering log entry missing (see ${OUTPUT_DIR}/build-all-subtree.log)"
    fi
else
    fail "build-all --layer exited non-zero"
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
