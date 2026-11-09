#!/usr/bin/env bash

# Test suite for run-local-kind.sh banner validation
# Part of DATAPLANE-5462 Task 6

set -euo pipefail

SCRIPT_PATH="hack/run-local-kind.sh"
TESTS_PASSED=0
TESTS_FAILED=0

test_banner_contains_ready_message() {
  if grep -q "LOCAL KIND CLUSTER READY" "$SCRIPT_PATH"; then
    echo "PASS: Banner contains 'LOCAL KIND CLUSTER READY'"
    TESTS_PASSED=$((TESTS_PASSED + 1))
  else
    echo "FAIL: Banner missing 'LOCAL KIND CLUSTER READY'"
    TESTS_FAILED=$((TESTS_FAILED + 1))
  fi
}

test_banner_contains_get_pods_command() {
  if grep -q "kubectl get pods -A" "$SCRIPT_PATH"; then
    echo "PASS: Banner contains 'kubectl get pods -A' command"
    TESTS_PASSED=$((TESTS_PASSED + 1))
  else
    echo "FAIL: Banner missing 'kubectl get pods -A' command"
    TESTS_FAILED=$((TESTS_FAILED + 1))
  fi
}

test_banner_contains_get_humiocluster_command() {
  if grep -q "kubectl get humiocluster" "$SCRIPT_PATH"; then
    echo "PASS: Banner contains 'kubectl get humiocluster' command"
    TESTS_PASSED=$((TESTS_PASSED + 1))
  else
    echo "FAIL: Banner missing 'kubectl get humiocluster' command"
    TESTS_FAILED=$((TESTS_FAILED + 1))
  fi
}

test_banner_contains_logs_command() {
  if grep -q "kubectl logs" "$SCRIPT_PATH"; then
    echo "PASS: Banner contains 'kubectl logs' command"
    TESTS_PASSED=$((TESTS_PASSED + 1))
  else
    echo "FAIL: Banner missing 'kubectl logs' command"
    TESTS_FAILED=$((TESTS_FAILED + 1))
  fi
}

test_banner_shows_preserve_cluster_value() {
  if grep -q 'PRESERVE_KIND_CLUSTER=\$preserve_kind_cluster' "$SCRIPT_PATH" || \
     grep -q 'PRESERVE_KIND_CLUSTER=.*preserve_kind_cluster' "$SCRIPT_PATH"; then
    echo "PASS: Banner shows current PRESERVE_KIND_CLUSTER value"
    TESTS_PASSED=$((TESTS_PASSED + 1))
  else
    echo "FAIL: Banner does not show PRESERVE_KIND_CLUSTER value"
    TESTS_FAILED=$((TESTS_FAILED + 1))
  fi
}

# Run all tests
echo "==> Running banner validation tests for $SCRIPT_PATH"
echo ""

test_banner_contains_ready_message
test_banner_contains_get_pods_command
test_banner_contains_get_humiocluster_command
test_banner_contains_logs_command
test_banner_shows_preserve_cluster_value

echo ""
echo "==> Test Results:"
echo "    PASSED: $TESTS_PASSED"
echo "    FAILED: $TESTS_FAILED"
echo ""

if [[ $TESTS_FAILED -gt 0 ]]; then
  echo "FAIL: $TESTS_FAILED test(s) failed"
  exit 1
else
  echo "PASS: All tests passed"
  exit 0
fi
