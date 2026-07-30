#!/usr/bin/env bash
# Stub script for Task 9: CRD manifest generation and verification
#
# This script is invoked by the test suite to verify CRD generation.
# It is a placeholder to demonstrate the test workflow (RED phase).
#
# In the GREEN phase, the actual `make generate manifests` command will be executed
# and the CRD manifest at config/crd/bases/core.humio.com_humionodepools.yaml
# will contain the correct scale subresource paths.

set -euo pipefail

echo "STUB: CRD manifest generation not yet implemented"
echo "Expected behavior:"
echo "  - Run: make generate manifests"
echo "  - Verify: config/crd/bases/core.humio.com_humionodepools.yaml"
echo "    contains specReplicasPath: .spec.nodeCount (flat, not nested)"
echo "  - Verify: config/rbac/role.yaml contains humionodepools permissions"
exit 1
