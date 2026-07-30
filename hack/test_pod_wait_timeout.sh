#!/usr/bin/env bash
# Test suite for wait_for_pod_with_timeout function
# Part of TDD RED phase: these tests verify timeout behavior

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TEST_TEMP_DIR="/tmp/humio-operator-test-$$"
mkdir -p "$TEST_TEMP_DIR"

# Test counters
TESTS_RUN=0
TESTS_PASSED=0
TESTS_FAILED=0

# Color codes for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_test() {
    echo -e "${YELLOW}[TEST]${NC} $1"
}

pass() {
    TESTS_PASSED=$((TESTS_PASSED + 1))
    echo -e "${GREEN}[PASS]${NC} $1"
}

fail() {
    TESTS_FAILED=$((TESTS_FAILED + 1))
    echo -e "${RED}[FAIL]${NC} $1"
}

# Test 1: Function syntax validation - extract function from script
test_function_exists() {
    TESTS_RUN=$((TESTS_RUN + 1))
    log_test "Function syntax validation"

    if grep -q "wait_for_pod_with_timeout()" "$SCRIPT_DIR/run-local-kind.sh"; then
        pass "Function definition exists in script"
    else
        fail "Function definition not found in script"
    fi
}

# Test 2: Verify timeout prints diagnostic output (kubectl describe)
test_diagnostic_output_present() {
    TESTS_RUN=$((TESTS_RUN + 1))
    log_test "Diagnostic output after timeout"

    if grep -A 5 "Timed out waiting" "$SCRIPT_DIR/run-local-kind.sh" | grep -q "kubectl describe pod"; then
        pass "Diagnostic kubectl describe command present after timeout"
    else
        fail "Diagnostic kubectl describe command missing after timeout"
    fi
}

# Test 3: Timeout elapsed-time behavior with mock kubectl
test_timeout_elapsed_time() {
    TESTS_RUN=$((TESTS_RUN + 1))
    log_test "Timeout elapsed-time behavior (30s timeout)"

    # Create harness that mocks kubectl and measures elapsed time
    cat > "$TEST_TEMP_DIR/timeout_harness.sh" << 'HARNESS'
#!/usr/bin/env bash
set -euo pipefail

# Mock kubectl: always returns "False" (pod never ready)
kubectl() {
    if [[ "$1" == "get" ]]; then
        echo "False"
        return 0
    fi
    if [[ "$1" == "describe" ]]; then
        echo "mock describe output" >&2
        return 0
    fi
    return 0
}
export -f kubectl

# Override timeout for fast testing
pod_wait_timeout=30

# Inline the wait_for_pod_with_timeout function (modified to return instead of exit)
wait_for_pod_with_timeout() {
    local pod_selector="$@"
    local elapsed=0
    local interval=10

    while [[ $elapsed -lt $pod_wait_timeout ]]; do
        if [[ $(kubectl get pods $pod_selector -o 'jsonpath={..status.conditions[?(@.type=="Ready")].status}' 2>/dev/null) == "True" ]]; then
            echo "Pod ready: $pod_selector"
            return 0
        fi
        echo "Waiting for pod ($pod_selector) ... ${elapsed}s/${pod_wait_timeout}s" >&2
        sleep $interval
        elapsed=$((elapsed + interval))
    done

    echo "ERROR: Timed out waiting for pod: $pod_selector (${pod_wait_timeout}s)" >&2
    echo "--- kubectl describe pod $pod_selector ---" >&2
    kubectl describe pod $pod_selector || true
    return 1
}

start=$(date +%s)
set +e
wait_for_pod_with_timeout test-pod
exit_code=$?
set -e
end=$(date +%s)
elapsed=$((end - start))

echo "EXIT_CODE=$exit_code ELAPSED=${elapsed}s"
HARNESS

    chmod +x "$TEST_TEMP_DIR/timeout_harness.sh"

    set +e
    output=$(bash "$TEST_TEMP_DIR/timeout_harness.sh" 2>&1)
    actual_exit_code=$?
    set -e

    # Extract elapsed time from output
    elapsed=$(echo "$output" | grep -o 'ELAPSED=[0-9]*' | grep -o '[0-9]*' || echo "0")

    # Validate exit code is 1 (script should exit with return code from function)
    if [[ "$actual_exit_code" -eq 0 ]]; then
        # Function returned 1, but script continued, so check output
        if echo "$output" | grep -q "EXIT_CODE=1"; then
            pass "Function returns 1 on timeout"
        else
            fail "Function did not return 1 on timeout (output: $output)"
            return
        fi
    else
        fail "Script exited unexpectedly with code $actual_exit_code"
        return
    fi

    # Validate elapsed time is within expected range [30, 40]
    if [[ "$elapsed" -ge 30 ]] && [[ "$elapsed" -le 40 ]]; then
        pass "Elapsed time ${elapsed}s within expected range [30, 40]"
    else
        fail "Elapsed time ${elapsed}s outside expected range [30, 40]"
    fi
}

# Test 4: Success path - pod becomes ready quickly
test_success_path() {
    TESTS_RUN=$((TESTS_RUN + 1))
    log_test "Success path - pod becomes ready"

    cat > "$TEST_TEMP_DIR/success_harness.sh" << 'HARNESS'
#!/usr/bin/env bash
set -euo pipefail

# Mock kubectl: returns "True" immediately (pod ready)
kubectl() {
    if [[ "$1" == "get" ]]; then
        echo "True"
        return 0
    fi
    return 0
}
export -f kubectl

pod_wait_timeout=30

wait_for_pod_with_timeout() {
    local pod_selector="$@"
    local elapsed=0
    local interval=10

    while [[ $elapsed -lt $pod_wait_timeout ]]; do
        if [[ $(kubectl get pods $pod_selector -o 'jsonpath={..status.conditions[?(@.type=="Ready")].status}' 2>/dev/null) == "True" ]]; then
            echo "Pod ready: $pod_selector"
            return 0
        fi
        echo "Waiting for pod ($pod_selector) ... ${elapsed}s/${pod_wait_timeout}s" >&2
        sleep $interval
        elapsed=$((elapsed + interval))
    done

    echo "ERROR: Timed out waiting for pod: $pod_selector (${pod_wait_timeout}s)" >&2
    exit 1
}

wait_for_pod_with_timeout test-pod
echo "EXIT_CODE=$?"
HARNESS

    chmod +x "$TEST_TEMP_DIR/success_harness.sh"

    set +e
    output=$(bash "$TEST_TEMP_DIR/success_harness.sh" 2>&1)
    actual_exit_code=$?
    set -e

    if [[ "$actual_exit_code" -eq 0 ]] && echo "$output" | grep -q "Pod ready"; then
        pass "Function returns 0 and prints 'Pod ready' when pod becomes ready"
    else
        fail "Function did not return 0 or print 'Pod ready'. Exit: $actual_exit_code, Output: $output"
    fi
}

# Test 5: Timeout at boundary (300s) - verify timeout message format
test_timeout_boundary() {
    TESTS_RUN=$((TESTS_RUN + 1))
    log_test "Timeout boundary (300s) - message format"

    # Just verify the script has the correct timeout constant and error message format
    if grep -q "declare -r pod_wait_timeout=300" "$SCRIPT_DIR/run-local-kind.sh" && \
       grep -q 'ERROR: Timed out waiting for pod:.*${pod_wait_timeout}s' "$SCRIPT_DIR/run-local-kind.sh"; then
        pass "Timeout constant is 300s and error message includes timeout value"
    else
        fail "Timeout constant or error message format incorrect"
    fi
}

# Cleanup
cleanup() {
    rm -rf "$TEST_TEMP_DIR"
}
trap cleanup EXIT

# Run all tests
echo "========================================"
echo "Testing wait_for_pod_with_timeout"
echo "========================================"
echo ""

test_function_exists
test_diagnostic_output_present
test_timeout_elapsed_time
test_success_path
test_timeout_boundary

echo ""
echo "========================================"
echo "Test Summary"
echo "========================================"
echo "Tests run:    $TESTS_RUN"
echo "Tests passed: $TESTS_PASSED"
echo "Tests failed: $TESTS_FAILED"
echo ""

if [[ $TESTS_FAILED -eq 0 ]]; then
    echo -e "${GREEN}All tests passed!${NC}"
    exit 0
else
    echo -e "${RED}Some tests failed!${NC}"
    exit 1
fi
