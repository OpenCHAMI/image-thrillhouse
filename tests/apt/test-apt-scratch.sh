#!/bin/bash
# APT/mmdebstrap Test #1: Scratch Build
# Tests all scratch-build functionality for Debian/Ubuntu using mmdebstrap

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUTPUT_DIR="${SCRIPT_DIR}/test-output/apt-scratch"
mkdir -p "$OUTPUT_DIR"

echo "════════════════════════════════════════════════════════════════"
echo "APT/mmdebstrap Test #1: Scratch Build"
echo "Tests: mmdebstrap scratch builds with all scratch-specific features"
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

echo "Building image-build container for APT (if needed)..."
if ! podman image exists image-build:test-apt && ! podman image exists localhost/image-build:test-apt; then
    cd "${SCRIPT_DIR}" && podman build -t image-build:test-apt -f Dockerfile . > "${OUTPUT_DIR}/container-build.log" 2>&1
    echo "✓ Container built"
else
    echo "✓ Container already exists"
fi
echo ""

echo "════════════════════════════════════════════════════════════════"
echo "Phase 1: Configuration Validation"
echo "════════════════════════════════════════════════════════════════"
echo ""

# Validate the scratch build config
validate_config "debian-scratch-validation" "apt/debian-scratch.yaml"

echo ""
echo "════════════════════════════════════════════════════════════════"
echo "Phase 2: mmdebstrap Scratch Build Tests"
echo "════════════════════════════════════════════════════════════════"
echo ""

# Test 1: Basic mmdebstrap scratch build (bookworm)
run_test "debian-scratch-basic" "apt/debian-scratch.yaml"

# Test 2: Scratch build with mmdebstrap options (variant, mode)
run_test "debian-scratch-options" "apt/debian-scratch-options.yaml"

# Test 3: Scratch build with files and commands
run_test "debian-scratch-full" "apt/debian-scratch-full.yaml"

# Test 4: Scratch build with custom mirror
run_test "debian-scratch-mirror" "apt/debian-scratch-mirror.yaml"

echo ""
echo "════════════════════════════════════════════════════════════════"
echo "Phase 3: Error Handling Tests"
echo "════════════════════════════════════════════════════════════════"
echo ""

# Test 5: Missing required option (suite)
((TOTAL_TESTS++))
echo "[$TOTAL_TESTS] Testing: missing-suite-option"
if podman run --rm \
    -v "${SCRIPT_DIR}/tests:/tests:Z" \
    image-build:test-apt \
    image-build validate --config "/tests/apt/invalid-no-suite.yaml" > "${OUTPUT_DIR}/invalid-no-suite.log" 2>&1; then
    echo "  ✗ FAILED (should have rejected config without suite)"
    ((FAILED_TESTS++))
else
    echo "  ✓ PASSED (correctly rejected missing suite)"
    ((PASSED_TESTS++))
fi

# Test 6: Invalid mmdebstrap option
((TOTAL_TESTS++))
echo "[$TOTAL_TESTS] Testing: invalid-mmdebstrap-option"
if podman run --rm \
    -v "${SCRIPT_DIR}/tests:/tests:Z" \
    image-build:test-apt \
    image-build validate --config "/tests/apt/invalid-option.yaml" > "${OUTPUT_DIR}/invalid-option.log" 2>&1; then
    echo "  ✗ FAILED (should have rejected invalid option)"
    ((FAILED_TESTS++))
else
    echo "  ✓ PASSED (correctly rejected invalid option)"
    ((PASSED_TESTS++))
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
