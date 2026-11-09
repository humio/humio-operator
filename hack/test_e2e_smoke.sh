#!/usr/bin/env bash

# End-to-end smoke test for make local-kind (Task 7)
# Part of DATAPLANE-5462
#
# This test validates what can be mechanically tested without requiring
# a full Docker/Kind/Kubernetes environment. For full integration validation,
# see the manual validation steps at the end of this file.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$SCRIPT_DIR/.."
cd "$PROJECT_ROOT"

TESTS_RUN=0
TESTS_PASSED=0
TESTS_FAILED=0

# Color codes
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'

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

# Test 1: Makefile target exists and can be parsed
test_makefile_target_exists() {
    TESTS_RUN=$((TESTS_RUN + 1))
    log_test "Makefile target 'local-kind' exists"

    if make -n local-kind >/dev/null 2>&1; then
        pass "Target 'local-kind' is defined in Makefile"
    else
        fail "Target 'local-kind' not found in Makefile"
    fi
}

# Test 2: Verify make -n shows correct dependency chain
test_makefile_dependency_chain() {
    TESTS_RUN=$((TESTS_RUN + 1))
    log_test "Makefile 'local-kind' has correct dependencies"

    output=$(make -n local-kind 2>&1 || true)

    # Should depend on: manifests, generate, fmt, vet
    # Then execute: hack/run-local-kind.sh
    if echo "$output" | grep -q "hack/run-local-kind.sh"; then
        pass "Dependency chain includes hack/run-local-kind.sh"
    else
        fail "Dependency chain missing hack/run-local-kind.sh"
    fi
}

# Test 3: Script exists and is executable
test_script_exists_and_executable() {
    TESTS_RUN=$((TESTS_RUN + 1))
    log_test "hack/run-local-kind.sh exists and is executable"

    if [[ -f "$SCRIPT_DIR/run-local-kind.sh" && -x "$SCRIPT_DIR/run-local-kind.sh" ]]; then
        pass "Script exists and is executable"
    else
        fail "Script missing or not executable"
    fi
}

# Test 4: Script has no syntax errors
test_script_syntax() {
    TESTS_RUN=$((TESTS_RUN + 1))
    log_test "Script has valid bash syntax"

    if bash -n "$SCRIPT_DIR/run-local-kind.sh" 2>/dev/null; then
        pass "Script syntax is valid"
    else
        fail "Script has syntax errors"
    fi
}

# Test 5: Script has EXIT trap registered
test_exit_trap_present() {
    TESTS_RUN=$((TESTS_RUN + 1))
    log_test "Script registers EXIT trap"

    if grep -q "trap cleanup EXIT" "$SCRIPT_DIR/run-local-kind.sh"; then
        pass "EXIT trap is registered"
    else
        fail "EXIT trap not found"
    fi
}

# Test 6: Cleanup function handles PRESERVE_KIND_CLUSTER
test_cleanup_respects_preserve_flag() {
    TESTS_RUN=$((TESTS_RUN + 1))
    log_test "Cleanup function respects PRESERVE_KIND_CLUSTER flag"

    script_content=$(cat "$SCRIPT_DIR/run-local-kind.sh")

    if echo "$script_content" | grep -q "preserve_kind_cluster.*true" && \
       echo "$script_content" | grep -q "kind delete cluster"; then
        pass "Cleanup function has conditional logic for PRESERVE_KIND_CLUSTER"
    else
        fail "Cleanup function missing PRESERVE_KIND_CLUSTER handling"
    fi
}

# Test 7: Cleanup function handles SETUP_COMPLETE flag
test_cleanup_respects_setup_complete() {
    TESTS_RUN=$((TESTS_RUN + 1))
    log_test "Cleanup function respects SETUP_COMPLETE flag"

    script_content=$(cat "$SCRIPT_DIR/run-local-kind.sh")

    if echo "$script_content" | grep -q "SETUP_COMPLETE.*true"; then
        pass "Cleanup function checks SETUP_COMPLETE flag"
    else
        fail "Cleanup function missing SETUP_COMPLETE check"
    fi
}

# Test 8: Wait loop is present for interactive use
test_wait_loop_present() {
    TESTS_RUN=$((TESTS_RUN + 1))
    log_test "Script has wait loop for interactive use"

    if grep -q "while true; do" "$SCRIPT_DIR/run-local-kind.sh" && \
       grep -q "Cluster still running" "$SCRIPT_DIR/run-local-kind.sh"; then
        pass "Wait loop present with heartbeat"
    else
        fail "Wait loop or heartbeat missing"
    fi
}

# Test 9: Banner is printed after setup
test_banner_present() {
    TESTS_RUN=$((TESTS_RUN + 1))
    log_test "Connection info banner is present"

    if grep -q "LOCAL KIND CLUSTER READY" "$SCRIPT_DIR/run-local-kind.sh"; then
        pass "Banner is present in script"
    else
        fail "Banner missing from script"
    fi
}

# Test 10: SETUP_COMPLETE is set before wait loop
test_setup_complete_set() {
    TESTS_RUN=$((TESTS_RUN + 1))
    log_test "SETUP_COMPLETE is set to true after deployment"

    script_content=$(cat "$SCRIPT_DIR/run-local-kind.sh")

    # SETUP_COMPLETE=true should appear before the wait loop
    setup_line=$(grep -n "SETUP_COMPLETE=true" "$SCRIPT_DIR/run-local-kind.sh" | cut -d: -f1)
    wait_line=$(grep -n "while true; do" "$SCRIPT_DIR/run-local-kind.sh" | tail -1 | cut -d: -f1)

    if [[ -n "$setup_line" && -n "$wait_line" && "$setup_line" -lt "$wait_line" ]]; then
        pass "SETUP_COMPLETE is set before wait loop"
    else
        fail "SETUP_COMPLETE not set correctly before wait loop"
    fi
}

# Run all tests
echo "=========================================="
echo "End-to-End Smoke Test Suite"
echo "Task 7: make local-kind integration"
echo "=========================================="
echo ""

test_makefile_target_exists
test_makefile_dependency_chain
test_script_exists_and_executable
test_script_syntax
test_exit_trap_present
test_cleanup_respects_preserve_flag
test_cleanup_respects_setup_complete
test_wait_loop_present
test_banner_present
test_setup_complete_set

echo ""
echo "=========================================="
echo "Test Results"
echo "=========================================="
echo "Tests run:    $TESTS_RUN"
echo "Tests passed: $TESTS_PASSED"
echo "Tests failed: $TESTS_FAILED"
echo ""

if [[ $TESTS_FAILED -gt 0 ]]; then
    echo -e "${RED}FAIL: $TESTS_FAILED test(s) failed${NC}"
    echo ""
    exit 1
else
    echo -e "${GREEN}PASS: All automated tests passed${NC}"
    echo ""
    echo "=========================================="
    echo "Manual Integration Validation Required"
    echo "=========================================="
    echo ""
    echo "The automated tests above validate the structure and logic"
    echo "of the make local-kind implementation. To validate the full"
    echo "end-to-end behavior, perform these manual steps:"
    echo ""
    echo "1. Dry-run validation:"
    echo "   $ make -n local-kind"
    echo "   Expected: Shows full recipe chain"
    echo ""
    echo "2. Full execution (requires Docker):"
    echo "   $ PRESERVE_KIND_CLUSTER=true make local-kind"
    echo "   Expected: Cluster created, operator deployed, banner printed"
    echo ""
    echo "3. Test Ctrl-C cleanup with PRESERVE_KIND_CLUSTER=false:"
    echo "   $ make local-kind"
    echo "   Press Ctrl-C during wait loop"
    echo "   $ kind get clusters"
    echo "   Expected: Empty output (no 'kind' cluster)"
    echo ""
    echo "4. Test Ctrl-C cleanup with PRESERVE_KIND_CLUSTER=true:"
    echo "   $ PRESERVE_KIND_CLUSTER=true make local-kind"
    echo "   Press Ctrl-C during wait loop"
    echo "   $ kind get clusters"
    echo "   Expected: Output contains 'kind'"
    echo "   $ kubectl get ns default"
    echo "   Expected: Exit code 0 (cluster accessible)"
    echo ""
    echo "5. Test cluster re-use:"
    echo "   $ PRESERVE_KIND_CLUSTER=true make local-kind"
    echo "   Expected: Reuses existing cluster, redeploys operator"
    echo ""
    echo "=========================================="
    echo ""
    exit 0
fi
