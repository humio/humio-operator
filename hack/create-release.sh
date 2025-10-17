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

Create release branches and PRs for Humio Operator releases.

This script creates two separate release branches with the SAME version:
1. Operator container image release (updates VERSION file)
2. Helm chart release (updates Chart.yaml)

VERSION DETECTION:
    If no VERSION is specified, the script automatically increments:
    - Patch version (x.y.z+1) by default
    - Minor version (x.y+1.0) if CRD changes detected since last release

OPTIONS:
    -h, --help          Show this help message
    -d, --dry-run       Show what would be done without making changes
    -r, --remote NAME   Git remote name (default: origin)
    --minor             Force minor version bump (x.y+1.0)
    --patch             Force patch version bump (x.y.z+1)

ARGUMENTS:
    VERSION             Explicit release version (e.g., 1.2.3) - overrides auto-detection

EXAMPLES:
    $0                          # Auto-detect next version
    $0 --dry-run                # Show what would be done
    $0 --minor                  # Force minor version bump
    $0 1.2.3                    # Use explicit version 1.2.3

EOF
}

# Version functions
get_current_version() {
    if [[ -f "$project_root/VERSION" ]]; then
        cat "$project_root/VERSION" | tr -d '\n'
    else
        echo "VERSION file not found"
        exit 1
    fi
}

bump_patch_version() {
    local current="$1"
    local version_regex="^([0-9]+)\.([0-9]+)\.([0-9]+)$"
    if [[ ! "$current" =~ $version_regex ]]; then
        echo "Cannot parse current version: $current"
        exit 1
    fi
    
    local major="${BASH_REMATCH[1]}"
    local minor="${BASH_REMATCH[2]}"
    local patch="${BASH_REMATCH[3]}"
    
    echo "$major.$minor.$((patch + 1))"
}

bump_minor_version() {
    local current="$1"
    local version_regex="^([0-9]+)\.([0-9]+)\.([0-9]+)$"
    if [[ ! "$current" =~ $version_regex ]]; then
        echo "Cannot parse current version: $current"
        exit 1
    fi
    
    local major="${BASH_REMATCH[1]}"
    local minor="${BASH_REMATCH[2]}"
    
    echo "$major.$((minor + 1)).0"
}

check_crd_changes() {
    # Find the last commit that changed the VERSION file
    local last_version_commit=$(git log -1 --format="%H" -- VERSION)
    
    if [[ -z "$last_version_commit" ]]; then
        echo "No previous VERSION file changes found, assuming patch bump"
        return 1
    fi
    
    # Get the version from that commit for display
    local last_version=$(git show "$last_version_commit:VERSION" 2>/dev/null | tr -d '\n' || echo "unknown")
    
    echo "Checking for CRD changes since last VERSION update: $last_version_commit (version $last_version)"
    
    # Check for changes in API and CRD directories since the last VERSION update
    if git log --oneline "$last_version_commit"..HEAD -- api/ config/crd/bases/ | grep -q .; then
        echo "CRD changes detected since last release"
        return 0
    else
        echo "No CRD changes detected since last release"
        return 1
    fi
}

validate_version() {
    local version="$1"
    if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
        echo "Invalid version format: $version. Expected format: x.y.z (e.g., 1.2.3)"
        exit 1
    fi
}

check_git_status() {
    if [[ -n $(git status --porcelain) ]]; then
        echo "Working directory is not clean. Please commit or stash changes first."
        git status --short
        exit 1
    fi
}

check_git_remote() {
    local remote="$1"
    if ! git remote get-url "$remote" &>/dev/null; then
        echo "Git remote '$remote' not found."
        echo "Available remotes:"
        git remote -v
        exit 1
    fi
}

ensure_master_updated() {
    local remote="$1"
    
    echo "Ensuring we're on master branch and up to date..."
    
    if [[ "$dry_run" == "true" ]]; then
        echo "[DRY RUN] Would checkout master and pull from $remote"
        return
    fi
    
    git checkout master
    git pull "$remote" master
}

checkout_or_create_branch() {
    local branch_name="$1"
    local remote="$2"
    
    if git show-ref --verify --quiet refs/heads/"$branch_name"; then
        echo "Branch $branch_name already exists, switching to it and updating"
        git checkout "$branch_name"
        # Pull latest changes from remote if it exists there
        if git show-ref --verify --quiet refs/remotes/"$remote"/"$branch_name"; then
            git pull "$remote" "$branch_name"
        fi
        # Rebase on master to get latest changes
        git rebase master
    else
        echo "Creating new branch $branch_name from master"
        git checkout -b "$branch_name"
    fi
}

create_operator_release_branch() {
    local version="$1"
    local remote="$2"
    local branch_name="release-operator-$version"
    
    echo "Creating operator release branch: $branch_name"
    
    if [[ "$dry_run" == "true" ]]; then
        echo "[DRY RUN] Would create or update branch $branch_name"
        echo "[DRY RUN] Would update VERSION file to: $version"
        echo "[DRY RUN] Would run: make manifests"
        echo "[DRY RUN] Would commit and push changes"
        return
    fi
    
    # Handle branch creation/checkout
    checkout_or_create_branch "$branch_name" "$remote"
    
    # Update VERSION file
    echo "$version" > "$project_root/VERSION"
    echo "Updated VERSION file to: $version"
    
    # Run manifests generation
    cd "$project_root"
    make manifests
    echo "Generated manifests"
    
    # Stage and commit changes (only if there are changes)
    git add VERSION config/crd/bases/ charts/humio-operator/crds/
    if ! git diff --staged --quiet; then
        git commit -m "Bump operator version to $version"
        echo "Committed changes for operator version $version"
    else
        echo "No changes to commit for operator version $version"
    fi
    
    # Push branch
    git push "$remote" "$branch_name"
    echo "Pushed branch $branch_name to $remote"
    
    # Return to master
    git checkout master
}

create_chart_release_branch() {
    local version="$1"
    local remote="$2"
    local branch_name="release-chart-$version"
    local chart_file="$project_root/charts/humio-operator/Chart.yaml"
    
    echo "Creating Helm chart release branch: $branch_name"
    
    if [[ "$dry_run" == "true" ]]; then
        echo "[DRY RUN] Would create or update branch $branch_name"
        echo "[DRY RUN] Would update Chart.yaml version to: $version"
        echo "[DRY RUN] Would update Chart.yaml appVersion to: $version"
        echo "[DRY RUN] Would run: make manifests"
        echo "[DRY RUN] Would commit and push changes"
        return
    fi
    
    # Handle branch creation/checkout
    checkout_or_create_branch "$branch_name" "$remote"
    
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
    git add charts/humio-operator/Chart.yaml charts/humio-operator/crds/
    if ! git diff --staged --quiet; then
        git commit -m "Bump Helm chart version to $version"
        echo "Committed changes for Helm chart version $version"
    else
        echo "No changes to commit for Helm chart version $version"
    fi
    
    # Push branch
    git push "$remote" "$branch_name"
    echo "Pushed branch $branch_name to $remote"
    
    # Return to master
    git checkout master
}

display_next_steps() {
    local version="$1"
    local remote_url
    remote_url=$(git remote get-url "$remote_name")
    
    # Convert git URL to web URL format
    local web_url
    if [[ "$remote_url" =~ ^ssh://git@([^:]+):([0-9]+)/(.+)\.git$ ]]; then
        # SSH format with port: ssh://git@hostname:port/path/repo.git -> https://hostname/projects/PATH/repos/repo
        local hostname="${BASH_REMATCH[1]}"
        local repo_path="${BASH_REMATCH[3]}"
        # Extract project and repo from path like "hum/humio-operator"
        if [[ "$repo_path" =~ ^([^/]+)/(.+)$ ]]; then
            local project="${BASH_REMATCH[1]^^}"  # Convert to uppercase
            local repo="${BASH_REMATCH[2]}"
            web_url="https://$hostname/projects/$project/repos/$repo"
        else
            web_url="https://$hostname/$repo_path"
        fi
    elif [[ "$remote_url" =~ ^git@([^:]+):(.+)\.git$ ]]; then
        # SSH format: git@hostname:path/repo.git -> https://hostname/path/repo
        local hostname="${BASH_REMATCH[1]}"
        local repo_path="${BASH_REMATCH[2]}"
        web_url="https://$hostname/$repo_path"
    elif [[ "$remote_url" =~ ^https://(.+)\.git$ ]]; then
        # HTTPS format: https://hostname/path/repo.git -> https://hostname/path/repo
        web_url="https://${BASH_REMATCH[1]}"
    else
        # Fallback - use as is
        web_url="$remote_url"
    fi
    
    # Generate branch-specific URLs if not in dry run mode
    local operator_branch_info="Branch: release-operator-$version"
    local chart_branch_info="Branch: release-chart-$version"
    
    if [[ "$dry_run" != "true" ]]; then
        # Construct Bitbucket pull request creation URLs
        if [[ "$web_url" =~ bitbucket ]]; then
            local operator_pr_url="$web_url/pull-requests?create&sourceBranch=refs%2Fheads%2Frelease-operator-$version"
            local chart_pr_url="$web_url/pull-requests?create&sourceBranch=refs%2Fheads%2Frelease-chart-$version"
            
            operator_branch_info="Branch: release-operator-$version
   Create PR: $operator_pr_url"
            chart_branch_info="Branch: release-chart-$version
   Create PR: $chart_pr_url"
        elif [[ "$web_url" =~ github ]]; then
            operator_branch_info="Branch: release-operator-$version
   Create PR: $web_url/compare/release-operator-$version"
            chart_branch_info="Branch: release-chart-$version
   Create PR: $web_url/compare/release-chart-$version"
        else
            operator_branch_info="Branch: release-operator-$version
   URL: $web_url (check for branch links)"
            chart_branch_info="Branch: release-chart-$version
   URL: $web_url (check for branch links)"
        fi
    fi
    
    cat << EOF

========================================
Release branches created successfully!
========================================

Version: $version

Next Steps:

1. Create PR for Operator Release:
   $operator_branch_info
   Target: master
   Title: "Bump operator version to $version"
   
2. Create PR for Helm Chart Release:
   $chart_branch_info
   Target: master
   Title: "Bump Helm chart version to $version"

Repository URL: $web_url

After merging:
- Operator PR merge will trigger container image build and GitHub release
- Chart PR merge will trigger Helm chart release
- Consider updating documentation in docs2 repository

EOF
}

# Main function
main() {
    local version=""
    local force_minor=false
    local force_patch=false
    
    # Parse command line arguments
    while [[ $# -gt 0 ]]; do
        case $1 in
            -h|--help)
                usage
                exit 0
                ;;
            -d|--dry-run)
                dry_run=true
                shift
                ;;
            -r|--remote)
                remote_name="$2"
                shift 2
                ;;
            --minor)
                force_minor=true
                shift
                ;;
            --patch)
                force_patch=true
                shift
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
    
    # Pre-flight checks
    check_git_remote "$remote_name"
    if [[ "$dry_run" != "true" ]]; then
        check_git_status
    fi
    
    # Determine version to use
    if [[ -z "$version" ]]; then
        echo "No version specified, auto-detecting next version..."
        
        local current_version=$(get_current_version)
        echo "Current version: $current_version"
        
        if [[ "$force_minor" == "true" ]]; then
            version=$(bump_minor_version "$current_version")
            echo "Using forced minor bump: $version"
        elif [[ "$force_patch" == "true" ]]; then
            version=$(bump_patch_version "$current_version")
            echo "Using forced patch bump: $version"
        elif check_crd_changes; then
            version=$(bump_minor_version "$current_version")
            echo "CRD changes detected, using minor bump: $version"
        else
            version=$(bump_patch_version "$current_version")
            echo "No CRD changes, using patch bump: $version"
        fi
        
        echo "Auto-detected version: $version"
    else
        validate_version "$version"
        echo "Using explicit version: $version"
    fi
    
    echo "Starting release process for version: $version"
    if [[ "$dry_run" == "true" ]]; then
        echo "DRY RUN MODE - No changes will be made"
    fi
    
    # Ensure we're on updated master
    ensure_master_updated "$remote_name"
    
    # Create release branches
    create_operator_release_branch "$version" "$remote_name"
    create_chart_release_branch "$version" "$remote_name"
    
    # Display next steps
    display_next_steps "$version"
    
    if [[ "$dry_run" != "true" ]]; then
        echo "Release branches created and pushed successfully!"
    else
        echo "Dry run completed. Use without --dry-run to execute changes."
    fi
}

# Run main function with all arguments
main "$@"