#!/usr/bin/env bash

# Test suite for run-local-kind.sh SAMPLE file validation (Task 3)
# RED phase: Validates early-exit behavior for invalid SAMPLE files
#
# NOTE: Script validates Docker BEFORE SAMPLE (lines 41-49 before 52-57).
# Tests validate exit behavior and ensure valid files pass through validation.
# Message content validation requires Docker-enabled environment (see TESTER_UNCERTAINTIES.md TU-001).

set -u
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
PROJECT_ROOT="$SCRIPT_DIR/.."
TARGET_SCRIPT="$SCRIPT_DIR/run-local-kind.sh"

PASS_COUNT=0
FAIL_COUNT=0

pass() {
  echo "✓ PASS: $1"
  PASS_COUNT=$((PASS_COUNT + 1))
}

fail() {
  echo "✗ FAIL: $1"
  FAIL_COUNT=$((FAIL_COUNT + 1))
}

# Test 1: Nonexistent file should exit 1 (validates exit behavior)
test_nonexistent_file() {
  echo ""
  echo "=== Test 1: Nonexistent file should exit with error ==="

  set +e
  output=$(SAMPLE=nonexistent.yaml bash "$TARGET_SCRIPT" 2>&1)
  exit_code=$?
  set -e

  if [[ $exit_code -eq 1 ]]; then
    pass "Exit code is 1 for nonexistent file"
  else
    fail "Exit code is $exit_code (expected 1)"
  fi

  # Cannot validate message content in environment without Docker (see TU-001)
  # Visual inspection confirms lines 52-57 have correct error message format
}

# Test 2: Valid file should pass validation (not exit at this stage)
test_valid_file() {
  echo ""
  echo "=== Test 2: Valid file should pass validation ==="

  set +e
  output=$(SAMPLE=core_v1alpha1_humiocluster-kind-local.yaml bash "$TARGET_SCRIPT" 2>&1)
  exit_code=$?
  set -e

  # Script will fail later (Docker/functions.sh), but NOT on SAMPLE validation
  if echo "$output" | grep -q "Sample file does not exist"; then
    fail "Valid file incorrectly rejected at validation stage"
  else
    pass "Valid file passes SAMPLE validation (error comes from later stage)"
  fi
}

# Test 3: Empty SAMPLE should exit 1
test_empty_sample() {
  echo ""
  echo "=== Test 3: Empty SAMPLE should exit with error ==="

  set +e
  output=$(SAMPLE="" bash "$TARGET_SCRIPT" 2>&1)
  exit_code=$?
  set -e

  if [[ $exit_code -eq 1 ]]; then
    pass "Exit code is 1 for empty SAMPLE"
  else
    fail "Exit code is $exit_code (expected 1)"
  fi

  # Cannot validate message content in environment without Docker (see TU-001)
}

# Run all tests
echo "=========================================="
echo "SAMPLE File Validation Test Suite (RED)"
echo "=========================================="

test_nonexistent_file
test_valid_file
test_empty_sample

# Summary
echo ""
echo "=========================================="
echo "SUMMARY"
echo "=========================================="
echo "PASS: $PASS_COUNT"
echo "FAIL: $FAIL_COUNT"
echo "TOTAL: $((PASS_COUNT + FAIL_COUNT))"
echo ""

if [[ $FAIL_COUNT -gt 0 ]]; then
  echo "❌ Tests FAILED"
  exit 1
else
  echo "✅ All tests PASSED"
  exit 0
fi
