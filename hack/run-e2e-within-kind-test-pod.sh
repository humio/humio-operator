#!/usr/bin/env bash

set -x -o pipefail

source hack/functions.sh

# Start log watcher in background to dump init container logs as they appear
./hack/watch-dependency-check-logs.sh | tee /proc/1/fd/1 &
LOG_WATCHER_PID=$!

# Build ginkgo flags
GINKGO_FLAGS="--label-filter=real --timeout 240m --procs=${GINKGO_NODES} --no-color --skip-package helpers -v"

# Add focus filter if specified
if [ -n "$GINKGO_FOCUS" ]; then
  GINKGO_FLAGS="$GINKGO_FLAGS --focus=$GINKGO_FOCUS"
fi

# We skip the helpers package as those tests assumes the environment variable USE_CERT_MANAGER is not set.
# If SUITE is set, run that specific suite, otherwise run all suites
if [ -n "$SUITE" ]; then
  ginkgo run $GINKGO_FLAGS ./internal/controller/suite/$SUITE/... | tee /proc/1/fd/1
  TEST_EXIT_CODE=${PIPESTATUS[0]}
else
  ginkgo run $GINKGO_FLAGS ./internal/controller/suite/... | tee /proc/1/fd/1
  TEST_EXIT_CODE=${PIPESTATUS[0]}
fi

# Stop the log watcher
kill $LOG_WATCHER_PID 2>/dev/null

# Exit with the test exit code
exit $TEST_EXIT_CODE
