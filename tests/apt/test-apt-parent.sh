#!/bin/bash
# APT Test #2: Parent Image Build
# Tests parent image functionality for Debian/Ubuntu using APT (not mmdebstrap)

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUTPUT_DIR="${SCRIPT_DIR}/test-output/apt-parent"
mkdir -p "$OUTPUT_DIR"

echo "════════════════════════════════════════════════════════════════"
echo "APT Test #2: Parent Image Build"
echo "Tests: APT parent image builds (builds on scratch image from test #1)"
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
        image-build:test-apt \
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
        image-build:test-apt \
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

echo "Parent tests use public open-source base images (Ubuntu, Debian)"
echo ""

echo "Building image-build container for APT (if needed)..."
if ! podman image exists image-build:test-apt && ! podman image exists localhost/image-build:test-apt; then
    cd "${SCRIPT_DIR}" && podman build --target apt -t image-build:test-apt -f Dockerfile . > "${OUTPUT_DIR}/container-build.log" 2>&1
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
validate_config "debian-parent-validation" "apt/debian-parent.yaml"

echo ""
echo "════════════════════════════════════════════════════════════════"
echo "Phase 2: APT Parent Image Build Tests"
echo "════════════════════════════════════════════════════════════════"
echo ""

# Test 1: Basic parent build with APT
run_test "ubuntu-parent-basic" "apt/debian-parent.yaml"

# Test 2: Parent build with Debian tasks
run_test "debian-parent-tasks" "apt/debian-parent-tasks.yaml"

# Test 3: Parent build with files
run_test "ubuntu-parent-files" "apt/debian-parent-files.yaml"

# Test 4: Parent build with commands only
run_test "debian-parent-commands" "apt/debian-parent-commands.yaml"

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
