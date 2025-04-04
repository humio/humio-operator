#!/bin/bash

set -x

trap "cleanup_helm_cluster" EXIT

run_test_suite() {
    trap "cleanup_upgrade" RETURN

    yq eval -o=j test-cases.yaml | jq -c '.test_scenarios[]' | while IFS= read -r scenario; do
        local name=$(echo "$scenario" | jq -r '.name')
        local from_version=$(echo $scenario | jq -r '.from_version')
        local to_version=$(echo $scenario | jq -r '.to_version')
        local cluster=$(echo $scenario | jq -r '.cluster')
        local expect_restarts=$(echo $scenario | jq -r '.expect_restarts')
        local description=$(echo $scenario | jq -r '.description')

        echo "Running test: $name"
        echo "Description: $description"

        # Run test
        if test_upgrade "$from_version" "$to_version" "$expect_restarts" "$cluster"; then
            echo "✅ Test passed: $name"
        else
            echo "❌ Test failed: $name"
            exit 1
        fi
    done
}

cleanup_helm_cluster() {
  cleanup_upgrade
  cleanup_humiocluster
}

source ./test-helm-upgrade.sh
run_test_suite