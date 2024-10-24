#!/usr/bin/env bash

set -x -o pipefail

source hack/functions.sh

# We skip the helpers package as those tests assumes the environment variable USE_CERT_MANAGER is not set.
ginkgo --label-filter=real -timeout 120m -procs=$GINKGO_NODES --no-color --skip-package helpers -v ./controllers/suite/... -covermode=count -coverprofile cover.out -progress | tee /proc/1/fd/1
