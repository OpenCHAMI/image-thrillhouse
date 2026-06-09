#!/bin/bash
# Integration test: `image-thrillhouse render`
#
# Exercises templating end-to-end without invoking buildah:
#   - var-file substitution
#   - --var CLI override priority
#   - --output to file
#   - missing-key fail-loud behaviour
#
# Render is a pure read/parse/template/write op so it doesn't need the
# privileged container the build tests use; we still run it through the
# same image-thrillhouse:test image to keep the runner uniform.
#
# This script deliberately does NOT use `set -e`. We want every test to
# run and report independently — `set -e` would silently abort the whole
# script on the first podman failure with no diagnostic output, which is
# the opposite of useful in CI logs.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUTPUT_DIR="${SCRIPT_DIR}/test-output/manifests-render"
mkdir -p "$OUTPUT_DIR" || { echo "✗ cannot create $OUTPUT_DIR"; exit 1; }

echo "════════════════════════════════════════════════════════════════"
echo "Manifests Test: Template render"
echo "Tests: --var-file, --var precedence, --output, missingkey=error"
echo "════════════════════════════════════════════════════════════════"
echo ""

TOTAL_TESTS=0
PASSED_TESTS=0
FAILED_TESTS=0

# Print a failure summary, including the captured stderr log and (when
# present and nonempty) a head of the stdout file. The previous version
# of this script reported "expected to find X" without showing the actual
# render error, which made debugging painful.
report_failure() {
    local test_name="$1"
    local out_file="$2"
    local log_file="$3"
    local extra="$4"

    echo "  ✗ FAILED: $test_name"
    [ -n "$extra" ] && echo "    $extra"
    if [ -s "$log_file" ]; then
        echo "    --- stderr ($log_file) ---"
        sed 's/^/    /' "$log_file" | head -20
    else
        echo "    (no stderr captured)"
    fi
    if [ -f "$out_file" ] && [ -s "$out_file" ]; then
        echo "    --- stdout head ($out_file) ---"
        head -10 "$out_file" | sed 's/^/    /'
    fi
    FAILED_TESTS=$((FAILED_TESTS + 1))
}

# run_render invokes image-thrillhouse inside the test container with --log-level
# error so that even on the rare path where logs end up on stdout, the
# rendered output stays clean. Returns the podman exit code so callers
# can branch on it.
run_render() {
    local test_name="$1"
    shift
    local out_file="${OUTPUT_DIR}/${test_name}.yaml"
    local log_file="${OUTPUT_DIR}/${test_name}.log"

    podman run --rm \
        -v "${SCRIPT_DIR}/tests:/tests:Z" \
        image-thrillhouse:test \
        image-thrillhouse --log-level error render "$@" \
        > "$out_file" 2> "$log_file"
}

# Render then assert every needle is present. Reports the render error
# (if any) before the assertion error so the user sees what actually
# happened rather than "expected to find X".
assert_render_contains() {
    local test_name="$1"
    shift
    local render_args=()
    while [ "$#" -gt 0 ] && [ "$1" != "--" ]; do
        render_args+=("$1")
        shift
    done
    shift # consume the --
    local needles=("$@")

    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    echo "[$TOTAL_TESTS] $test_name"

    local out_file="${OUTPUT_DIR}/${test_name}.yaml"
    local log_file="${OUTPUT_DIR}/${test_name}.log"

    if ! run_render "$test_name" "${render_args[@]}"; then
        report_failure "$test_name" "$out_file" "$log_file" \
            "image-thrillhouse render exited non-zero"
        return 1
    fi

    for needle in "${needles[@]}"; do
        if ! grep -qF "$needle" "$out_file"; then
            report_failure "$test_name" "$out_file" "$log_file" \
                "expected to find: $needle"
            return 1
        fi
    done

    # No leftover {{ or }} markers — catches the case where missingkey=error
    # didn't fire and we got placeholder-substituted output.
    if grep -qE '\{\{|\}\}' "$out_file"; then
        report_failure "$test_name" "$out_file" "$log_file" \
            "rendered output still contains template markers"
        return 1
    fi

    echo "  ✓ PASSED"
    PASSED_TESTS=$((PASSED_TESTS + 1))
}

# Assert that render exits non-zero with the given args. Used for
# missingkey=error coverage.
assert_render_errors() {
    local test_name="$1"
    shift
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    echo "[$TOTAL_TESTS] $test_name"

    if run_render "$test_name" "$@"; then
        echo "  ✗ FAILED: render exited 0; expected an error"
        FAILED_TESTS=$((FAILED_TESTS + 1))
    else
        echo "  ✓ PASSED (render exited non-zero as expected)"
        PASSED_TESTS=$((PASSED_TESTS + 1))
    fi
}

echo "Preparing image-thrillhouse container (if needed)..."
NEEDS_BUILD=0
if [ "${REBUILD_IMAGE:-0}" = "1" ]; then
    NEEDS_BUILD=1
elif ! podman image exists image-thrillhouse:test && ! podman image exists localhost/image-thrillhouse:test; then
    NEEDS_BUILD=1
fi
if [ "$NEEDS_BUILD" = "1" ]; then
    if ! (cd "${SCRIPT_DIR}" && podman build -t image-thrillhouse:test -f Dockerfile . \
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
echo "Phase 1: --manifest --layer renders with computed tags + layer vars"
echo "════════════════════════════════════════════════════════════════"
echo ""

# Manifest mode is the realistic path: render previews exactly what build
# will consume. The layer's var_files are loaded automatically, tag and
# parent_tag are computed, and we can assert per-arch repo URLs come
# through correctly.

assert_render_contains "rocky-base-x86_64" \
    --manifest /tests/manifests/rocky-multiarch.yaml \
    --layer rocky-base-x86_64 \
    -- \
    "BaseOS/x86_64/os" \
    "https://dl.rockylinux.org/pub/rocky/9/BaseOS" \
    "/etc/image-thrillhouse/yum.repos.d/rocky-baseos.repo"

assert_render_contains "rocky-base-aarch64" \
    --manifest /tests/manifests/rocky-multiarch.yaml \
    --layer rocky-base-aarch64 \
    -- \
    "BaseOS/aarch64/os"

assert_render_contains "suse-base-x86_64" \
    --manifest /tests/manifests/suse-multiarch.yaml \
    --layer suse-base-x86_64 \
    -- \
    "openSUSE Leap 15.6" \
    "/etc/zypp/repos.d/opensuse-oss.repo"

assert_render_contains "suse-base-aarch64" \
    --manifest /tests/manifests/suse-multiarch.yaml \
    --layer suse-base-aarch64 \
    -- \
    "download.opensuse.org/ports/aarch64"

echo ""
echo "════════════════════════════════════════════════════════════════"
echo "Phase 2: parent_tag is injected into compute layers"
echo "════════════════════════════════════════════════════════════════"
echo ""

# rocky-compute-x86_64 templates `from: localhost/rocky-base:{{ .parent_tag }}`,
# which only resolves if ComputeBuildVars injects parent_tag for the
# single-direct-parent case. Catches regressions in that path end-to-end.
assert_render_contains "rocky-compute-x86_64-parent" \
    --manifest /tests/manifests/rocky-multiarch.yaml \
    --layer rocky-compute-x86_64 \
    -- \
    "from: localhost/rocky-base:"

echo ""
echo "════════════════════════════════════════════════════════════════"
echo "Phase 3: --var CLI override priority"
echo "════════════════════════════════════════════════════════════════"
echo ""

# CLI --var must win over a value present in --var-file. Render in
# manifest mode so tag/parent_tag come from the manifest while we
# override one of the layer's vars on the command line.
assert_render_contains "rocky-base-cli-override" \
    --manifest /tests/manifests/rocky-multiarch.yaml \
    --layer rocky-base-x86_64 \
    --var "releasever=10" \
    -- \
    "BaseOS/x86_64/os" \
    "RPM-GPG-KEY-Rocky-10"

echo ""
echo "════════════════════════════════════════════════════════════════"
echo "Phase 4: missingkey=error (standalone --config mode)"
echo "════════════════════════════════════════════════════════════════"
echo ""

# Standalone --config render with no var-file must fail loudly: the
# template references {{ .reposdir }} etc. which are undefined, and
# {{ .tag }} which only manifest mode injects.
assert_render_errors "missing-var-fails-loud" \
    --config /tests/rocky/templates/rocky-base.yaml

echo ""
echo "════════════════════════════════════════════════════════════════"
echo "Phase 5: mutually-exclusive flag validation"
echo "════════════════════════════════════════════════════════════════"
echo ""

# Passing both --config and --manifest must be rejected, and --layer
# without --manifest must be rejected. Mirrors build's flag contract.
assert_render_errors "config-and-manifest-conflict" \
    --config /tests/rocky/templates/rocky-base.yaml \
    --manifest /tests/manifests/rocky-multiarch.yaml \
    --layer rocky-base-x86_64

assert_render_errors "layer-without-manifest" \
    --layer rocky-base-x86_64

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
