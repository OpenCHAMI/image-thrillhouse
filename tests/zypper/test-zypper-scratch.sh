#!/bin/bash
# Zypper Test #1: Scratch Build
# Tests all scratch-build functionality for openSUSE/SLES
#
# NOTE: These tests require Linux due to buildah unshare's dependency on
# user namespaces. macOS/Podman Machine has limitations with nested user
# namespaces that prevent buildah unshare from working properly.

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUTPUT_DIR="${SCRIPT_DIR}/test-output/zypper-scratch"
mkdir -p "$OUTPUT_DIR"

echo "════════════════════════════════════════════════════════════════"
echo "Zypper Test #1: Scratch Build"
echo "Tests: Zypper scratch builds with all scratch-specific features"
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

# Validate the scratch build config
validate_config "suse-scratch-validation" "zypper/suse-scratch.yaml"

echo ""
echo "════════════════════════════════════════════════════════════════"
echo "Phase 2: Zypper Scratch Build Tests"
echo "════════════════════════════════════════════════════════════════"
echo ""

# Test 1: Basic zypper scratch build
run_test "suse-scratch-basic" "zypper/suse-scratch.yaml"

# Test 2: Scratch build with zypper options
run_test "suse-scratch-options" "zypper/suse-scratch-options.yaml"

# Test 3: Scratch build with repos, files, commands
run_test "suse-scratch-full" "zypper/suse-scratch-full.yaml"

# Test 4: Scratch build with patterns (SUSE's equivalent of groups)
run_test "suse-scratch-patterns" "zypper/suse-scratch-patterns.yaml"

# Test 5: Scratch build with auto-agree-with-licenses option
run_test "suse-scratch-with-licenses" "zypper/suse-scratch-with-licenses.yaml"

echo ""
echo "════════════════════════════════════════════════════════════════"
echo "Phase 3: Error Handling Tests"
echo "════════════════════════════════════════════════════════════════"
echo ""

# Test 6: Invalid zypper option (should fail validation)
TOTAL_TESTS=$((TOTAL_TESTS + 1))
echo "[$TOTAL_TESTS] Testing: invalid-zypper-option"
if podman run --rm \
    -v "${SCRIPT_DIR}/tests:/tests:Z" \
    image-thrillhouse:test \
    image-thrillhouse validate --config "/tests/opensuse/invalid-zypper-test.yaml" > "${OUTPUT_DIR}/invalid-option.log" 2>&1; then
    echo "  ✗ FAILED (should have rejected invalid option)"
    FAILED_TESTS=$((FAILED_TESTS + 1))
else
    echo "  ✓ PASSED (correctly rejected invalid option)"
    PASSED_TESTS=$((PASSED_TESTS + 1))
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
