#!/bin/bash

set -e

# Script configuration
declare -r script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
declare -r project_root="$(cd "$script_dir/.." && pwd)"
dry_run=false
remote_name="origin"

# Usage function
usage() {
    cat << EOF
Usage: $0 [OPTIONS] [VERSION]

Create release chart changes for Humio Operator.

This script creates required helm chart changes for a release.

OPTIONS:
    -h, --help          Show this help message

ARGUMENTS:
    VERSION             Explicit release version (e.g., 1.2.3)

EXAMPLES:
    $0 1.2.3                    # Use explicit version 1.2.3

EOF
}

validate_version() {
    local version="$1"
    if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-rc.([0-9]+))?$ ]]; then
        echo "Invalid version format: $version. Expected format: x.y.z (e.g., 1.2.3 , 1.2.3-rc.1)"
        exit 1
    fi
}

create_operator_release() {
    local version="$1"
    local chart_file="$project_root/charts/humio-operator/Chart.yaml"
    
    # Update VERSION file
    echo "$version" > "$project_root/VERSION"
    echo "Updated VERSION file to: $version"
    
    # Update Chart.yaml
    sed -i.bak "s/^version: .*/version: $version/" "$chart_file"
    sed -i.bak "s/^appVersion: .*/appVersion: $version/" "$chart_file"
    rm "$chart_file.bak"
    echo "Updated Chart.yaml version and appVersion to: $version"

    # Run manifests generation
    cd "$project_root"
    make manifests
    echo "Generated manifests"
    
    # Stage and commit changes (only if there are changes)
    git add VERSION charts/humio-operator/Chart.yaml config/crd/bases/ charts/humio-operator/crds/
    if ! git diff --staged --quiet; then
        git commit -m "Bump operator version to $version"
        echo "Committed changes for operator version $version"
    else
        echo "No changes to commit for operator version $version"
    fi
}

# Main function
main() {
    local version=""
    
    # Parse command line arguments
    while [[ $# -gt 0 ]]; do
        case $1 in
            -h|--help)
                usage
                exit 0
                ;;
            -*)
                echo "Unknown option: $1"
                usage
                exit 1
                ;;
            *)
                if [[ -z "$version" ]]; then
                    version="$1"
                else
                    echo "Too many arguments"
                    usage
                    exit 1
                fi
                shift
                ;;
        esac
    done
    
    # Change to project root
    cd "$project_root"
    
    # Determine version to use
    if [[ -z "$version" ]]; then
        echo "No version specified, exiting..."
        exit 1
    else
        validate_version "$version"
        echo "Using explicit version: $version"
    fi
    
    echo "Starting release process for version: $version"
    create_operator_release $version
}

# Run main function with all arguments
main "$@"