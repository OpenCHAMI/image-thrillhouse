#!/bin/bash
# DNF Test #2: Parent Image Build
# Tests parent image functionality for DNF (builds on scratch image from test #1)

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUTPUT_DIR="${SCRIPT_DIR}/test-output/dnf-parent"
mkdir -p "$OUTPUT_DIR"

echo "════════════════════════════════════════════════════════════════"
echo "DNF Test #2: Parent Image Build"
echo "Tests: DNF parent image builds (depends on test-dnf-scratch.sh)"
echo "════════════════════════════════════════════════════════════════"
echo ""

# Test counter
TOTAL_TESTS=0
PASSED_TESTS=0
FAILED_TESTS=0

# Function to run a test
run_test() {
    local test_name="$1"
    local config_file="$2"
    
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    echo "[$TOTAL_TESTS] Testing: $test_name"
    echo "  Config: $config_file"
    
    if podman run --rm \
        --device /dev/fuse \
        --cap-add=SYS_ADMIN \
        --cap-add=SETUID \
        --cap-add=SETGID \
        --security-opt seccomp=unconfined \
        --security-opt label=disable \
        -v "${SCRIPT_DIR}/tests:/tests:Z" \
        -v "${OUTPUT_DIR}:/output:Z" \
        -e BUILDAH_ISOLATION=chroot \
        image-thrillhouse:test \
        image-thrillhouse build --config "/tests/$config_file" --log-level info > "${OUTPUT_DIR}/${test_name}.log" 2>&1; then
        echo "  ✓ PASSED"
        PASSED_TESTS=$((PASSED_TESTS + 1))
        return 0
    else
        echo "  ✗ FAILED (see ${OUTPUT_DIR}/${test_name}.log)"
        FAILED_TESTS=$((FAILED_TESTS + 1))
        return 1
    fi
}

# Validate config
validate_config() {
    local test_name="$1"
    local config_file="$2"
    
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    echo "[$TOTAL_TESTS] Validating: $test_name"
    
    if podman run --rm \
        -v "${SCRIPT_DIR}/tests:/tests:Z" \
        image-thrillhouse:test \
        image-thrillhouse validate --config "/tests/$config_file" > "${OUTPUT_DIR}/${test_name}-validate.log" 2>&1; then
        echo "  ✓ PASSED"
        PASSED_TESTS=$((PASSED_TESTS + 1))
        return 0
    else
        echo "  ✗ FAILED (see ${OUTPUT_DIR}/${test_name}-validate.log)"
        FAILED_TESTS=$((FAILED_TESTS + 1))
        return 1
    fi
}

echo "Parent tests use public open-source base images (Rocky, Alma)"
echo ""

echo "Preparing image-thrillhouse container (if needed)..."
NEEDS_BUILD=0
if [ "${REBUILD_IMAGE:-0}" = "1" ]; then
    echo "REBUILD_IMAGE=1 set, forcing rebuild"
    NEEDS_BUILD=1
elif ! podman image exists image-thrillhouse:test && ! podman image exists localhost/image-thrillhouse:test; then
    NEEDS_BUILD=1
fi
if [ "$NEEDS_BUILD" = "1" ]; then
    cd "${SCRIPT_DIR}" && podman build -t image-thrillhouse:test -f Dockerfile . > "${OUTPUT_DIR}/container-build.log" 2>&1
    echo "✓ Container built"
else
    echo "✓ Container already exists (set REBUILD_IMAGE=1 to force rebuild)"
fi
echo ""

echo "════════════════════════════════════════════════════════════════"
echo "Phase 1: Configuration Validation"
echo "════════════════════════════════════════════════════════════════"
echo ""

# Validate the parent build configs
validate_config "rocky-parent-validation" "dnf/rocky-parent.yaml"

echo ""
echo "════════════════════════════════════════════════════════════════"
echo "Phase 2: Parent Image Build Tests"
echo "════════════════════════════════════════════════════════════════"
echo ""

# Test 1: Basic parent build (additional packages only)
run_test "rocky-parent-basic" "dnf/rocky-parent.yaml"

# Test 2: Parent build with package groups
run_test "rocky-parent-groups" "dnf/rocky-parent-groups.yaml"

# Test 3: Parent build with DNF modules
run_test "rocky-parent-modules" "dnf/rocky-parent-modules.yaml"

# Test 4: Parent build with commands only (no packages)
run_test "rocky-parent-commands" "dnf/rocky-parent-commands.yaml"

# Test 5: Parent build with layer.directories (recursive host->image copy
# with excludes; fixture tree lives under tests/dnf/fixtures/sample-tree/)
run_test "rocky-parent-directories" "dnf/rocky-parent-directories.yaml"

echo ""
echo "════════════════════════════════════════════════════════════════"
echo "Test Results Summary"
echo "════════════════════════════════════════════════════════════════"
echo ""
echo "Total Tests:  $TOTAL_TESTS"
echo "Passed:       $PASSED_TESTS"
echo "Failed:       $FAILED_TESTS"
echo ""
echo "Output directory: $OUTPUT_DIR"
echo "Logs: ${OUTPUT_DIR}/*.log"
echo ""

if [ $FAILED_TESTS -eq 0 ]; then
    echo "✓ All tests passed!"
    exit 0
else
    echo "✗ Some tests failed"
    exit 1
fi
