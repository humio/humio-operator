#!/usr/bin/env bash

set -x -o pipefail

source hack/functions.sh

# We skip the helpers package as those tests assumes the environment variable USE_CERT_MANAGER is not set.
USE_CERTMANAGER=true TEST_USE_EXISTING_CLUSTER=true DUMMY_LOGSCALE_IMAGE=true ginkgo --label-filter=dummy -timeout 1h -nodes=$GINKGO_NODES --no-color --skip-package helpers -v ./controllers/suite/... -covermode=count -coverprofile cover.out -progress | tee /proc/1/fd/1
