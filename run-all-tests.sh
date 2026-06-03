#!/bin/bash
# Master Test Runner
# Runs all test scripts in the correct order

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

echo "════════════════════════════════════════════════════════════════"
echo "             Image Builder - Complete Test Suite               "
echo "════════════════════════════════════════════════════════════════"
echo ""

# Parse arguments
PARALLEL_SCRATCH=false
PACKAGE_MANAGER=""
ONLY_MANIFESTS=false
SKIP_MANIFESTS=false

while [[ $# -gt 0 ]]; do
    case $1 in
        --parallel)
            PARALLEL_SCRATCH=true
            shift
            ;;
        --dnf|--apt|--zypper)
            PACKAGE_MANAGER="${1#--}"
            shift
            ;;
        --manifests)
            ONLY_MANIFESTS=true
            shift
            ;;
        --no-manifests)
            SKIP_MANIFESTS=true
            shift
            ;;
        -h|--help)
            cat << EOF
Usage: $0 [OPTIONS]

Options:
  --parallel        Run scratch tests in parallel (faster)
  --dnf             Run only DNF backend tests
  --apt             Run only APT backend tests
  --zypper          Run only Zypper backend tests
  --manifests       Run only manifest tests (render + build-all)
  --no-manifests    Skip manifest tests (run only per-backend)
  -h, --help        Show this help

Examples:
  $0                    # Run all tests sequentially (backends + manifests)
  $0 --parallel         # Run scratch tests in parallel
  $0 --dnf              # Run only DNF backend tests
  $0 --manifests        # Run only the manifest/template/build-all suite
  $0 --parallel --apt   # Run APT tests with parallel scratch

EOF
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            echo "Use --help for usage information"
            exit 1
            ;;
    esac
done

# Determine which tests to run
if [ -n "$PACKAGE_MANAGER" ]; then
    case $PACKAGE_MANAGER in
        dnf)
            TESTS=("dnf")
            ;;
        apt)
            TESTS=("apt")
            ;;
        zypper)
            TESTS=("zypper")
            ;;
    esac
else
    TESTS=("dnf" "apt" "zypper")
fi

echo "Test configuration:"
echo "  Package managers: ${TESTS[*]}"
echo "  Parallel scratch: $PARALLEL_SCRATCH"
echo ""

# Track results
TOTAL_PASSED=0
TOTAL_FAILED=0

# Function to run a per-backend test script (scratch / parent).
run_test() {
    local pkg=$1
    local type=$2
    local script="${SCRIPT_DIR}/tests/${pkg}/test-${pkg}-${type}.sh"

    echo ""
    echo -e "${BLUE}════════════════════════════════════════════════════════════════${NC}"
    echo -e "${BLUE}Running: ${pkg} ${type} tests${NC}"
    echo -e "${BLUE}════════════════════════════════════════════════════════════════${NC}"
    echo ""

    if [ ! -f "$script" ]; then
        echo -e "${RED}✗ Script not found: $script${NC}"
        TOTAL_FAILED=$((TOTAL_FAILED + 1))
        return 1
    fi

    if bash "$script"; then
        echo ""
        echo -e "${GREEN}✓ ${pkg} ${type} tests PASSED${NC}"
        TOTAL_PASSED=$((TOTAL_PASSED + 1))
        return 0
    else
        echo ""
        echo -e "${RED}✗ ${pkg} ${type} tests FAILED${NC}"
        TOTAL_FAILED=$((TOTAL_FAILED + 1))
        return 1
    fi
}

# Function to run a manifest test script (render / build-all). These live
# under tests/manifests/ and don't follow the per-backend naming, so they
# get their own runner.
run_manifest_test() {
    local kind=$1
    local script="${SCRIPT_DIR}/tests/manifests/test-${kind}.sh"

    echo ""
    echo -e "${BLUE}════════════════════════════════════════════════════════════════${NC}"
    echo -e "${BLUE}Running: manifests ${kind} tests${NC}"
    echo -e "${BLUE}════════════════════════════════════════════════════════════════${NC}"
    echo ""

    if [ ! -f "$script" ]; then
        echo -e "${RED}✗ Script not found: $script${NC}"
        TOTAL_FAILED=$((TOTAL_FAILED + 1))
        return 1
    fi

    if bash "$script"; then
        echo ""
        echo -e "${GREEN}✓ manifests ${kind} tests PASSED${NC}"
        TOTAL_PASSED=$((TOTAL_PASSED + 1))
        return 0
    else
        echo ""
        echo -e "${RED}✗ manifests ${kind} tests FAILED${NC}"
        TOTAL_FAILED=$((TOTAL_FAILED + 1))
        return 1
    fi
}

# Manifests-only short-circuit: skip every per-backend test, just run the
# manifest suite. Useful for iterating on templating / build-all changes.
if [ "$ONLY_MANIFESTS" = true ]; then
    run_manifest_test "render"    || true
    run_manifest_test "build-all" || true
    echo ""
    echo "════════════════════════════════════════════════════════════════"
    echo "                      Final Results                             "
    echo "════════════════════════════════════════════════════════════════"
    echo ""
    echo "Test Suites Passed: $TOTAL_PASSED"
    echo "Test Suites Failed: $TOTAL_FAILED"
    [ $TOTAL_FAILED -eq 0 ] && exit 0 || exit 1
fi

# Run tests
if [ "$PARALLEL_SCRATCH" = true ]; then
    echo "════════════════════════════════════════════════════════════════"
    echo "Phase 1: Running scratch tests in parallel"
    echo "════════════════════════════════════════════════════════════════"
    echo ""

    # Run scratch tests in parallel. Backgrounded run_test runs in a subshell,
    # so any counter changes it makes are lost — we tally results here in the
    # parent shell based on each child's exit code.
    PIDS=()
    PIDS_PKG=()
    for pkg in "${TESTS[@]}"; do
        run_test "$pkg" "scratch" &
        PIDS+=($!)
        PIDS_PKG+=("$pkg")
    done

    SCRATCH_FAILED=0
    for i in "${!PIDS[@]}"; do
        if wait "${PIDS[$i]}"; then
            TOTAL_PASSED=$((TOTAL_PASSED + 1))
        else
            SCRATCH_FAILED=$((SCRATCH_FAILED + 1))
            TOTAL_FAILED=$((TOTAL_FAILED + 1))
        fi
    done

    if [ $SCRATCH_FAILED -gt 0 ]; then
        echo ""
        echo -e "${RED}✗ Some scratch tests failed. Stopping.${NC}"
        exit 1
    fi

    echo ""
    echo "════════════════════════════════════════════════════════════════"
    echo "Phase 2: Running parent tests sequentially"
    echo "════════════════════════════════════════════════════════════════"
    echo ""

    # Run parent tests sequentially
    for pkg in "${TESTS[@]}"; do
        run_test "$pkg" "parent" || true
    done
else
    echo "════════════════════════════════════════════════════════════════"
    echo "Running all tests sequentially"
    echo "════════════════════════════════════════════════════════════════"
    echo ""
    
    # Run sequentially
    for pkg in "${TESTS[@]}"; do
        run_test "$pkg" "scratch" || true
        run_test "$pkg" "parent" || true
    done
fi

# Manifest tests after per-backend tests (skipped when --no-manifests or
# when a single --pkg flag narrowed the run).
if [ "$SKIP_MANIFESTS" = false ] && [ -z "$PACKAGE_MANAGER" ]; then
    echo ""
    echo "════════════════════════════════════════════════════════════════"
    echo "Phase: Manifests (templating / build-all / skip-if-exists)"
    echo "════════════════════════════════════════════════════════════════"
    echo ""
    run_manifest_test "render"    || true
    run_manifest_test "build-all" || true
fi

# Summary
echo ""
echo "════════════════════════════════════════════════════════════════"
echo "                      Final Results                             "
echo "════════════════════════════════════════════════════════════════"
echo ""
echo "Test Suites Passed: $TOTAL_PASSED"
echo "Test Suites Failed: $TOTAL_FAILED"
echo ""

if [ $TOTAL_FAILED -eq 0 ]; then
    echo -e "${GREEN}✓ All test suites passed!${NC}"
    exit 0
else
    echo -e "${RED}✗ Some test suites failed${NC}"
    echo ""
    echo "Check individual test logs in test-output/ for details"
    exit 1
fi
