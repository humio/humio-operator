#!/usr/bin/env bash

set -x -o pipefail

source hack/functions.sh

# We skip the helpers package as those tests assumes the environment variable USE_CERT_MANAGER is not set.
DUMMY_LOGSCALE_IMAGE=true ginkgo run --label-filter=dummy -timeout 90m -procs=1 --no-color --skip-package helpers --skip-package pfdrenderservice -v -progress ${SUITE:+./internal/controller/suite/$SUITE/...} | tee /proc/1/fd/1
