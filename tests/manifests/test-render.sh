#!/bin/bash
# Integration test: `image-build render`
#
# Exercises templating end-to-end without invoking buildah:
#   - var-file substitution
#   - --var CLI override priority
#   - --output to file
#   - missing-key fail-loud behaviour
#
# Render is a pure read/parse/template/write op so it doesn't need the
# privileged container the build tests use; we still run it through the
# same image-build:test image to keep the runner uniform.

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUTPUT_DIR="${SCRIPT_DIR}/test-output/manifests-render"
mkdir -p "$OUTPUT_DIR"

echo "════════════════════════════════════════════════════════════════"
echo "Manifests Test: Template render"
echo "Tests: --var-file, --var precedence, --output, missingkey=error"
echo "════════════════════════════════════════════════════════════════"
echo ""

TOTAL_TESTS=0
PASSED_TESTS=0
FAILED_TESTS=0

# Run image-build in the test container. We only need read-only access to
# tests/ — no /dev/fuse, no caps. Stdout of `render` (sans flags) is the
# rendered YAML; we tee the run log to OUTPUT_DIR and the rendered output
# to a second file for easier post-mortem.
run_render() {
    local test_name="$1"
    shift
    local out_file="${OUTPUT_DIR}/${test_name}.yaml"
    local log_file="${OUTPUT_DIR}/${test_name}.log"

    podman run --rm \
        -v "${SCRIPT_DIR}/tests:/tests:Z" \
        image-build:test \
        image-build --log-level error render "$@" \
        > "$out_file" 2> "$log_file"
}

# Assert the rendered file contains every needle; fails the test on the
# first miss.
assert_contains() {
    local test_name="$1"
    local file="$2"
    shift 2
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    echo "[$TOTAL_TESTS] $test_name"
    for needle in "$@"; do
        if ! grep -qF "$needle" "$file"; then
            echo "  ✗ FAILED: expected to find '$needle' in $file"
            FAILED_TESTS=$((FAILED_TESTS + 1))
            return 1
        fi
    done
    echo "  ✓ PASSED"
    PASSED_TESTS=$((PASSED_TESTS + 1))
}

# Assert no Go text/template directives survived the render — catches
# missing-var bugs that fail to error out for some reason.
assert_no_template_markers() {
    local test_name="$1"
    local file="$2"
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    echo "[$TOTAL_TESTS] $test_name"
    if grep -qE '\{\{|\}\}' "$file"; then
        echo "  ✗ FAILED: leftover template markers in $file:"
        grep -nE '\{\{|\}\}' "$file" | sed 's/^/    /'
        FAILED_TESTS=$((FAILED_TESTS + 1))
        return 1
    fi
    echo "  ✓ PASSED"
    PASSED_TESTS=$((PASSED_TESTS + 1))
}

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

echo "════════════════════════════════════════════════════════════════"
echo "Phase 1: --var-file substitution"
echo "════════════════════════════════════════════════════════════════"
echo ""

run_render "rocky-base-x86_64" \
    --config /tests/rocky/templates/rocky-base.yaml \
    --var-file /tests/rocky/templates/x86_64.yaml
assert_contains "rocky-base x86_64 substitutes arch/repobase" \
    "${OUTPUT_DIR}/rocky-base-x86_64.yaml" \
    "BaseOS/x86_64/os" \
    "https://dl.rockylinux.org/pub/rocky/9/BaseOS" \
    "/etc/image-build/yum.repos.d/rocky-baseos.repo"
assert_no_template_markers "rocky-base x86_64 no leftover markers" \
    "${OUTPUT_DIR}/rocky-base-x86_64.yaml"

run_render "rocky-base-aarch64" \
    --config /tests/rocky/templates/rocky-base.yaml \
    --var-file /tests/rocky/templates/aarch64.yaml
assert_contains "rocky-base aarch64 substitutes arch" \
    "${OUTPUT_DIR}/rocky-base-aarch64.yaml" \
    "BaseOS/aarch64/os"

run_render "suse-base-x86_64" \
    --config /tests/zypper/templates/suse-base.yaml \
    --var-file /tests/zypper/templates/x86_64.yaml
assert_contains "suse-base x86_64 substitutes leap_version" \
    "${OUTPUT_DIR}/suse-base-x86_64.yaml" \
    "openSUSE Leap 15.6" \
    "/etc/zypp/repos.d/opensuse-oss.repo"

run_render "suse-base-aarch64" \
    --config /tests/zypper/templates/suse-base.yaml \
    --var-file /tests/zypper/templates/aarch64.yaml
assert_contains "suse-base aarch64 uses ports/ repobase" \
    "${OUTPUT_DIR}/suse-base-aarch64.yaml" \
    "download.opensuse.org/ports/aarch64"

echo ""
echo "════════════════════════════════════════════════════════════════"
echo "Phase 2: --var CLI override priority"
echo "════════════════════════════════════════════════════════════════"
echo ""

# CLI --var must win over a value present in --var-file.
run_render "rocky-base-cli-override" \
    --config /tests/rocky/templates/rocky-base.yaml \
    --var-file /tests/rocky/templates/x86_64.yaml \
    --var "releasever=10"
assert_contains "CLI --var releasever overrides var-file" \
    "${OUTPUT_DIR}/rocky-base-cli-override.yaml" \
    "BaseOS/x86_64/os" \
    "RPM-GPG-KEY-Rocky-10"

echo ""
echo "════════════════════════════════════════════════════════════════"
echo "Phase 3: missingkey=error"
echo "════════════════════════════════════════════════════════════════"
echo ""

# Rendering without a var-file should fail loudly because the template
# references {{ .reposdir }} etc. with no value.
TOTAL_TESTS=$((TOTAL_TESTS + 1))
echo "[$TOTAL_TESTS] missing var triggers a render error"
if podman run --rm \
        -v "${SCRIPT_DIR}/tests:/tests:Z" \
        image-build:test \
        image-build render --config /tests/rocky/templates/rocky-base.yaml \
        > "${OUTPUT_DIR}/missing-var.yaml" 2> "${OUTPUT_DIR}/missing-var.log"
then
    echo "  ✗ FAILED: render with no vars should have errored"
    FAILED_TESTS=$((FAILED_TESTS + 1))
else
    echo "  ✓ PASSED (render exited non-zero as expected)"
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
echo "Rendered output: $OUTPUT_DIR/*.yaml"
echo "Logs:            $OUTPUT_DIR/*.log"
echo ""

if [ $FAILED_TESTS -eq 0 ]; then
    echo "✓ All render tests passed!"
    exit 0
else
    echo "✗ Some render tests failed"
    exit 1
fi
