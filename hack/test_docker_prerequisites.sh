#!/usr/bin/env bash
# Test: Docker prerequisite checks (Task 4 Validation)
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TARGET="$SCRIPT_DIR/run-local-kind.sh"

test_count=0
pass_count=0

check() {
  local desc="$1"
  local cmd="$2"

  test_count=$((test_count + 1))
  echo -n "Test $test_count: $desc... "

  if eval "$cmd" >/dev/null 2>&1; then
    echo "PASS"
    pass_count=$((pass_count + 1))
  else
    echo "FAIL"
  fi
}

echo "Docker Prerequisite Validation Tests"
echo "======================================"

check "docker info check exists" "grep -q '\\\${docker} info' '$TARGET'"
check "daemon error message exists" "grep -q 'Docker daemon is not running' '$TARGET'"
check "not installed message exists" "grep -q 'not installed' '$TARGET'"
check "check before cluster creation" "test \$(grep -n '\\\${docker} info' '$TARGET' | head -1 | cut -d: -f1) -lt \$(grep -n 'start_kind_cluster' '$TARGET' | head -1 | cut -d: -f1)"

echo ""
echo "Results: $pass_count/$test_count passed"

if [ "$pass_count" -eq "$test_count" ]; then
  echo "Status: GREEN - Implementation correct"
  exit 0
else
  echo "Status: RED - Some checks failed"
  exit 1
fi
