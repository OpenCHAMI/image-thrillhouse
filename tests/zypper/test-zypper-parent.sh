#!/bin/bash
# Zypper Test #2: Parent Image Build
# Tests parent image functionality for openSUSE/SLES (builds on scratch image from test #1)
#
# NOTE: These tests require Linux due to buildah unshare's dependency on
# user namespaces. macOS/Podman Machine has limitations with nested user
# namespaces that prevent buildah unshare from working properly.

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUTPUT_DIR="${SCRIPT_DIR}/test-output/zypper-parent"
mkdir -p "$OUTPUT_DIR"

echo "════════════════════════════════════════════════════════════════"
echo "Zypper Test #2: Parent Image Build"
echo "Tests: Zypper parent image builds (depends on test-zypper-scratch.sh)"
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
    
    ((TOTAL_TESTS++))
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
        image-build:test-zypper \
        image-build build --config "/tests/$config_file" --log-level info > "${OUTPUT_DIR}/${test_name}.log" 2>&1; then
        echo "  ✓ PASSED"
        ((PASSED_TESTS++))
        return 0
    else
        echo "  ✗ FAILED (see ${OUTPUT_DIR}/${test_name}.log)"
        ((FAILED_TESTS++))
        return 1
    fi
}

# Validate config
validate_config() {
    local test_name="$1"
    local config_file="$2"
    
    ((TOTAL_TESTS++))
    echo "[$TOTAL_TESTS] Validating: $test_name"
    
    if podman run --rm \
        -v "${SCRIPT_DIR}/tests:/tests:Z" \
        image-build:test-zypper \
        image-build validate --config "/tests/$config_file" > "${OUTPUT_DIR}/${test_name}-validate.log" 2>&1; then
        echo "  ✓ PASSED"
        ((PASSED_TESTS++))
        return 0
    else
        echo "  ✗ FAILED (see ${OUTPUT_DIR}/${test_name}-validate.log)"
        ((FAILED_TESTS++))
        return 1
    fi
}

echo "Parent tests use public open-source base images (openSUSE)"
echo ""

echo "Building image-build container for Zypper (if needed)..."
if ! podman image exists image-build:test-zypper && ! podman image exists localhost/image-build:test-zypper; then
    cd "${SCRIPT_DIR}" && podman build --target zypper -t image-build:test-zypper -f Dockerfile . > "${OUTPUT_DIR}/container-build.log" 2>&1
    echo "✓ Container built"
else
    echo "✓ Container already exists"
fi
echo ""

echo "════════════════════════════════════════════════════════════════"
echo "Phase 1: Configuration Validation"
echo "════════════════════════════════════════════════════════════════"
echo ""

# Validate the parent build configs
validate_config "suse-parent-validation" "zypper/suse-parent.yaml"

echo ""
echo "════════════════════════════════════════════════════════════════"
echo "Phase 2: Zypper Parent Image Build Tests"
echo "════════════════════════════════════════════════════════════════"
echo ""

# Test 1: Basic parent build
run_test "opensuse-parent-basic" "zypper/suse-parent.yaml"

# Test 2: Parent build with patterns
run_test "opensuse-parent-patterns" "zypper/suse-parent-patterns.yaml"

# Test 3: Parent build with files
run_test "opensuse-parent-files" "zypper/suse-parent-files.yaml"

# Test 4: Parent build with commands only
run_test "opensuse-parent-commands" "zypper/suse-parent-commands.yaml"

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
