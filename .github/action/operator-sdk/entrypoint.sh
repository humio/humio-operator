#!/bin/bash

set -eu

declare -r project_root="/go/src/github.com/${GITHUB_REPOSITORY}"
declare -r repo_root="$(dirname $project_root)"

mkdir -p "${repo_root}"
ln -s "$GITHUB_WORKSPACE" "${project_root}"
cd "${project_root}"

"$@"
