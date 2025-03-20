#!/usr/bin/env bash

set -x -o pipefail

source hack/functions.sh

# We skip the helpers package as those tests assumes the environment variable USE_CERT_MANAGER is not set.
DUMMY_LOGSCALE_IMAGE=true ginkgo run --label-filter=dummy -timeout 90m -procs=$GINKGO_NODES --no-color --skip-package helpers -v -progress ./internal/controller/suite/... | tee /proc/1/fd/1
