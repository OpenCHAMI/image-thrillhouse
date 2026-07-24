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

echo "Parent tests use public open-source base images (Ubuntu, Debian)"
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
validate_config "debian-parent-validation" "apt/debian-parent.yaml"

# Negative test: a repo file with a .repo extension is placed where apt will
# never read it, so validation must REJECT it (a passing validate is a failure).
TOTAL_TESTS=$((TOTAL_TESTS + 1))
echo "[$TOTAL_TESTS] Testing: reject-bad-apt-repo-path"
if podman run --rm \
    -v "${SCRIPT_DIR}/tests:/tests:Z" \
    image-thrillhouse:test \
    image-thrillhouse validate --config "/tests/apt/invalid-repo-path.yaml" > "${OUTPUT_DIR}/invalid-repo-path.log" 2>&1; then
    echo "  ✗ FAILED (should have rejected .repo path for apt)"
    FAILED_TESTS=$((FAILED_TESTS + 1))
else
    echo "  ✓ PASSED (correctly rejected apt repo path apt would ignore)"
    PASSED_TESTS=$((PASSED_TESTS + 1))
fi

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

# Test 5: Parent build that adds a repo file (.list) with a pinned signed-by
run_test "ubuntu-parent-repo" "apt/debian-parent-repo.yaml"

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
