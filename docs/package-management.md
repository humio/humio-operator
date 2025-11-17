# HumioPackage and HumioPackageRegistry User Guide

## Overview

The Humio Operator provides native Kubernetes support for managing LogScale packages through two custom resource types:

- **HumioPackageRegistry**: Configures connections to package repositories (Marketplace, GitLab, GitHub, etc.)
- **HumioPackage**: Defines specific packages to install and their target LogScale views

This allows you to manage LogScale package deployments using GitOps workflows and Kubernetes-native tooling.

## Quick Start

### 1. Create a Package Registry

First, configure a registry connection. Here's an example using the LogScale Marketplace:

```yaml
apiVersion: core.humio.com/v1alpha1
kind: HumioPackageRegistry
metadata:
  name: marketplace-registry
spec:
  managedClusterName: my-humio-cluster
  displayName: "LogScale Marketplace"
  registryType: marketplace
  enabled: true
  marketplace:
    url: https://packages.humio.com
```

Apply this configuration:
```bash
kubectl apply -f registry.yaml
```

### 2. Install a Package

Create a HumioPackage resource that references your registry:

```yaml
apiVersion: core.humio.com/v1alpha1
kind: HumioPackage
metadata:
  name: crowdstrike-fdr
spec:
  managedClusterName: my-humio-cluster
  packageName: "crowdstrike/fdr"
  packageVersion: "1.1.4"
  packageChecksum: "sha256:bf6d5929ae79f9b43dc5dd378a20a39e2325e4d4c54bdffef248e8d129886453"
  registryRef:
    name: marketplace-registry
  packageInstallTargets:
    - viewNames: ["security", "audit"]
  marketplace:
    scope: "crowdstrike"
    package: "fdr"
```

Apply the package:
```bash
kubectl apply -f package.yaml
```

### 3. Monitor Installation

Check the status of your package:
```bash
kubectl get humiopackages
kubectl describe humiopackage crowdstrike-fdr
```

## HumioPackageRegistry Configuration

### Core Fields

| Field | Description | Required |
|-------|-------------|----------|
| `managedClusterName` | Name of your HumioCluster resource | Yes (or `externalClusterName`) |
| `externalClusterName` | Name of your HumioExternalCluster resource | Yes (or `managedClusterName`) |
| `displayName` | Human-readable name for the registry | No |
| `registryType` | Type of registry (`marketplace`, `gitlab`, `github`, etc.) | Yes |
| `enabled` | Whether this registry is active (default: `true`) | No |

### Supported Registry Types

#### 1. LogScale Marketplace

The official LogScale package repository.

```yaml
apiVersion: core.humio.com/v1alpha1
kind: HumioPackageRegistry
metadata:
  name: marketplace-registry
spec:
  managedClusterName: my-cluster
  registryType: marketplace
  marketplace:
    url: https://packages.humio.com  # Default official marketplace
```

**Authentication**: Not required for public packages.

#### 2. GitLab Package Registry

For packages hosted in GitLab's Package Registry.

```yaml
apiVersion: core.humio.com/v1alpha1
kind: HumioPackageRegistry
metadata:
  name: gitlab-registry
spec:
  managedClusterName: my-cluster
  registryType: gitlab
  gitlab:
    url: "https://gitlab.com/api/v4"  # Or your GitLab instance
    project: "mygroup/myproject"      # Project path
    tokenRef:
      name: gitlab-token-secret
      key: token
---
apiVersion: v1
kind: Secret
metadata:
  name: gitlab-token-secret
type: Opaque
stringData:
  token: "glpat-xxxxxxxxxxxxxxxxxxxx"  # Your GitLab access token
```

**Required Token Permissions**: Read access to the project's Package Registry.

#### 3. GitHub Releases

For packages distributed through GitHub Releases.

```yaml
apiVersion: core.humio.com/v1alpha1
kind: HumioPackageRegistry
metadata:
  name: github-registry
spec:
  managedClusterName: my-cluster
  registryType: github
  github:
    url: "https://api.github.com"     # Or GitHub Enterprise API URL
    owner: "myorganization"           # GitHub username/organization
    tokenRef:
      name: github-token-secret
      key: token
---
apiVersion: v1
kind: Secret
metadata:
  name: github-token-secret
type: Opaque
stringData:
  token: "ghp_xxxxxxxxxxxxxxxxxxxx"   # GitHub Personal Access Token
```

**Required Token Scopes**: 
- Public repos: No scopes required
- Private repos: `repo` scope

#### 4. JFrog Artifactory

For packages stored in Artifactory generic repositories.

```yaml
apiVersion: core.humio.com/v1alpha1
kind: HumioPackageRegistry
metadata:
  name: artifactory-registry
spec:
  managedClusterName: my-cluster
  registryType: artifactory
  artifactory:
    url: "https://mycompany.jfrog.io/artifactory"
    repository: "generic-local"       # Repository name
    tokenRef:
      name: artifactory-token-secret
      key: token
---
apiVersion: v1
kind: Secret
metadata:
  name: artifactory-token-secret
type: Opaque
stringData:
  token: "your-artifactory-token"     # Artifactory access token
```

**Required Permissions**: Read access to the specified repository.

#### 5. AWS CodeArtifact

For packages stored in AWS CodeArtifact.

```yaml
apiVersion: core.humio.com/v1alpha1
kind: HumioPackageRegistry
metadata:
  name: aws-registry
spec:
  managedClusterName: my-cluster
  registryType: aws
  aws:
    region: "us-east-1"
    domain: "my-domain"
    repository: "my-repo"
    accessKeyRef:
      name: aws-access-key-secret
      key: access-key
    accessSecretRef:
      name: aws-secret-key-secret
      key: secret-key
---
apiVersion: v1
kind: Secret
metadata:
  name: aws-access-key-secret
type: Opaque
stringData:
  access-key: "AKIAIOSFODNN7EXAMPLE"
---
apiVersion: v1
kind: Secret
metadata:
  name: aws-secret-key-secret
type: Opaque
stringData:
  secret-key: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
```

**Required IAM Permissions**: 
- `codeartifact:GetRepositoryEndpoint`
- `codeartifact:ReadFromRepository`

#### 6. Google Cloud Artifact Registry

For packages stored in Google Cloud Artifact Registry.

```yaml
apiVersion: core.humio.com/v1alpha1
kind: HumioPackageRegistry
metadata:
  name: gcloud-registry
spec:
  managedClusterName: my-cluster
  registryType: gcloud
  gcloud:
    url: "https://artifactregistry.googleapis.com"  # Default GCP API URL
    projectId: "my-gcp-project"
    repository: "my-repo"
    location: "us-central1"           # Repository location
    serviceAccountKeyRef:
      name: gcp-service-account-secret
      key: service-account.json
---
apiVersion: v1
kind: Secret
metadata:
  name: gcp-service-account-secret
type: Opaque
stringData:
  service-account.json: |
    {
      "type": "service_account",
      "project_id": "my-gcp-project",
      "private_key_id": "...",
      "private_key": "...",
      "client_email": "...",
      "client_id": "...",
      "auth_uri": "...",
      "token_uri": "...",
      "auth_provider_x509_cert_url": "...",
      "client_x509_cert_url": "..."
    }
```

**Required Service Account Roles**: `Artifact Registry Reader`

## HumioPackage Configuration

### Core Fields

| Field | Description | Required |
|-------|-------------|----------|
| `managedClusterName` | Name of your HumioCluster resource | Yes (or `externalClusterName`) |
| `externalClusterName` | Name of your HumioExternalCluster resource | Yes (or `managedClusterName`) |
| `packageName` | Name of the package (from manifest.yaml) | Yes |
| `packageVersion` | Version to install (from manifest.yaml) | Yes |
| `packageChecksum` | SHA256 checksum for integrity verification | Yes |
| `registryRef` | Reference to HumioPackageRegistry | Yes |
| `packageInstallTargets` | Views where package should be installed | Yes |
| `conflictPolicy` | How to handle conflicts (`error` or `overwrite`) | No (default: `overwrite`) |

### Package Install Targets

You can specify installation targets in two ways:

#### Direct View Names
```yaml
packageInstallTargets:
  - viewNames: ["security", "audit", "compliance"]
```

#### HumioView CRD References
```yaml
packageInstallTargets:
  - viewRef:
      name: production-security-view
      namespace: security-team  # Optional, defaults to package namespace
```

#### Mixed Approach
```yaml
packageInstallTargets:
  - viewNames: ["legacy-view"]
  - viewRef:
      name: modern-security-view
```

### Registry-Specific Configuration

Each registry type requires specific configuration in the HumioPackage:

#### Marketplace Packages
```yaml
marketplace:
  scope: "crowdstrike"      # Publisher/organization
  package: "fdr"            # Package name within scope
```

#### GitLab Packages
```yaml
gitlab:
  package: "logscale-dashboard"    # Package name in GitLab
  assetName: "dashboard-1.0.0.zip" # File name to download
```

#### GitHub Packages
```yaml
github:
  repository: "logscale-packages"          # Repository name (not full path)
  tag: "v1.2.3"                          # Release tag
  assetName: "package-1.2.3.zip"         # Optional: specific asset file
  # If assetName is omitted, downloads the source code archive
```

#### Artifactory Packages
```yaml
artifactory:
  filePath: "packages/security/fdr/1.1.4/fdr-1.1.4.zip"  # Full path in repository
```

#### AWS CodeArtifact Packages
```yaml
aws:
  namespace: "com.company"        # Package namespace
  package: "logscale-fdr"        # Package name
  filename: "fdr-1.1.4.zip"     # File within package version
```

#### Google Cloud Packages
```yaml
gcloud:
  package: "logscale-fdr"        # Package name
  filename: "fdr-1.1.4.zip"     # File within package version
```

## Complete Examples

### Example 1: Marketplace Package

```yaml
# Registry
apiVersion: core.humio.com/v1alpha1
kind: HumioPackageRegistry
metadata:
  name: marketplace
spec:
  managedClusterName: production-humio
  registryType: marketplace
  marketplace:
    url: https://packages.humio.com
---
# Package
apiVersion: core.humio.com/v1alpha1
kind: HumioPackage
metadata:
  name: crowdstrike-fdr
spec:
  managedClusterName: production-humio
  packageName: "crowdstrike/fdr"
  packageVersion: "1.1.4"
  packageChecksum: "sha256:bf6d5929ae79f9b43dc5dd378a20a39e2325e4d4c54bdffef248e8d129886453"
  registryRef:
    name: marketplace
  packageInstallTargets:
    - viewNames: ["security"]
  marketplace:
    scope: "crowdstrike"
    package: "fdr"
```

### Example 2: Private GitHub Repository

```yaml
# Registry with authentication
apiVersion: core.humio.com/v1alpha1
kind: HumioPackageRegistry
metadata:
  name: private-github
spec:
  managedClusterName: staging-humio
  registryType: github
  github:
    url: "https://api.github.com"
    owner: "mycompany"
    tokenRef:
      name: github-token
      key: token
---
# Secret for GitHub authentication
apiVersion: v1
kind: Secret
metadata:
  name: github-token
type: Opaque
stringData:
  token: "ghp_your_personal_access_token_here"
---
# Package
apiVersion: core.humio.com/v1alpha1
kind: HumioPackage
metadata:
  name: custom-dashboard
spec:
  managedClusterName: staging-humio
  packageName: "internal/dashboard-pack"
  packageVersion: "2.1.0"
  packageChecksum: "sha256:1234567890abcdef..."
  registryRef:
    name: private-github
  packageInstallTargets:
    - viewRef:
        name: operations-view
  github:
    repository: "logscale-packages"
    tag: "v2.1.0"
    assetName: "dashboard-pack-2.1.0.zip"
```

## Operations and Monitoring

### Checking Registry Status

```bash
# List all registries
kubectl get humiopackageregistries

# Check specific registry status
kubectl describe humiopackageregistry marketplace

# View registry in different output formats
kubectl get humiopackageregistry marketplace -o yaml
kubectl get humiopackageregistry marketplace -o json
```

### Monitoring Package Installation

```bash
# List all packages
kubectl get humiopackages

# Check package installation status
kubectl describe humiopackage crowdstrike-fdr

# Watch package installation progress
kubectl get humiopackage crowdstrike-fdr -w
```

### Common Status Values

#### HumioPackageRegistry States
- `Active`: Registry is configured and available
- `Disabled`: Registry is disabled (`enabled: false`)
- `ConfigError`: Invalid configuration
- `Unknown`: Status being determined

#### HumioPackage States
- `Installed`: Package successfully installed
- `NotFound`: Package not found in registry
- `ConfigError`: Invalid package configuration
- `FailedInstall`: Installation failed
- `PartialInstall`: Partially installed (some views failed)
- `Unknown`: Installation status being determined

## Troubleshooting

### Common Issues

#### 1. Registry Connection Failures

**Symptom**: Registry shows `ConfigError` state

**Solutions**:
```bash
# Check registry configuration
kubectl describe humiopackageregistry <registry-name>

# Verify secrets exist and have correct keys
kubectl get secret <secret-name> -o yaml

# Test network connectivity (if using private registries)
kubectl run debug --image=curlimages/curl -it --rm -- curl -v <registry-url>
```

#### 2. Authentication Issues

**Symptom**: 401/403 errors in package status messages

**Solutions**:
```bash
# Verify secret contents
kubectl get secret <token-secret> -o yaml | base64 -d

# Check token permissions:
# - GitLab: Ensure token has Package Registry read access
# - GitHub: Ensure token has repo scope for private repos
# - Artifactory: Verify repository read permissions
# - AWS: Check CodeArtifact IAM permissions
# - GCP: Verify service account has Artifact Registry Reader role
```

#### 3. Package Not Found

**Symptom**: Package shows `NotFound` state

**Solutions**:
```bash
# Verify package exists in registry
# For GitHub: Check repository releases
# For GitLab: Check project packages
# For Artifactory: Verify file path exists

# Check package configuration matches registry structure
kubectl get humiopackage <package-name> -o yaml
```

#### 4. Checksum Validation Failures

**Symptom**: Installation fails with checksum mismatch

**Solutions**:
```bash
# Calculate correct checksum manually
curl -L <package-url> | sha256sum

# Update package configuration with correct checksum
kubectl patch humiopackage <name> --type='merge' -p='{"spec":{"packageChecksum":"sha256:correct-checksum"}}'
```

#### 5. View Target Issues

**Symptom**: Package installs but doesn't appear in expected views

**Solutions**:
```bash
# Verify views exist in LogScale
# Check HumioView CRD status if using viewRef
kubectl get humioview <view-name>
kubectl describe humioview <view-name>

# Verify view names match exactly (case-sensitive)
```

### Debugging Commands

```bash
# View operator logs
kubectl logs -n humio-system deployment/humio-operator

# Get detailed resource information
kubectl get humiopackage <name> -o yaml
kubectl get humiopackageregistry <name> -o yaml

# Check events
kubectl get events --field-selector involvedObject.name=<resource-name>
```

## Best Practices

### Security

1. **Use Kubernetes Secrets**: Never embed sensitive data directly in manifests
2. **Namespace Secrets**: Store registry credentials in appropriate namespaces
3. **Principle of Least Privilege**: Use minimal required permissions for tokens/service accounts
4. **Regular Token Rotation**: Rotate authentication tokens periodically
5. **Verify Checksums**: Always include and verify package checksums

### Organization

1. **Naming Conventions**: Use consistent naming for registries and packages
   ```
   humiopackageregistry-<type>-<environment>
   humiopackage-<package-name>-<version>
   ```

2. **Labels and Annotations**: Add metadata for better organization
   ```yaml
   metadata:
     labels:
       environment: production
       team: security
       registry-type: github
     annotations:
       description: "CrowdStrike FDR package for security team"
   ```

3. **Resource Separation**: Consider separate registries per environment/team

### GitOps Integration

1. **Version Control**: Store all configurations in Git
2. **Environment Promotion**: Use separate directories/branches for environments
3. **Automated Deployment**: Use ArgoCD, Flux, or similar tools
4. **Change Reviews**: Require pull request reviews for package changes

### Monitoring

1. **Package Status Alerts**: Monitor package states and alert on failures
2. **Registry Health**: Monitor registry connectivity
3. **Version Tracking**: Track installed package versions across environments
4. **Security Scanning**: Scan packages for vulnerabilities before deployment

## Migration Guide

### From Manual Package Installation

If you're currently installing packages manually through the LogScale UI:

1. **Inventory Current Packages**: Document all installed packages and their versions
2. **Create Registry Configurations**: Set up HumioPackageRegistry resources
3. **Create Package Definitions**: Define HumioPackage resources
4. **Test in Staging**: Validate configurations in non-production environment
5. **Gradual Migration**: Migrate packages incrementally

### Package Version Updates

To update a package version:

```bash
# Update the package version and checksum
kubectl patch humiopackage <name> --type='merge' -p='{
  "spec": {
    "packageVersion": "1.2.0",
    "packageChecksum": "sha256:new-checksum-here"
  }
}'
```

## Reference Links

- [LogScale Package Documentation](https://library.humio.com/integrations/integrations.html)
- [Humio Operator GitHub](https://github.com/humio/humio-operator)
- [Kubernetes Custom Resources](https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/custom-resources/)

## Support

For additional help:

1. **Operator Logs**: Check the Humio Operator logs for detailed error messages
2. **GitHub Issues**: Report bugs and feature requests on the [Humio Operator repository](https://github.com/humio/humio-operator/issues)
3. **Documentation**: Refer to the official LogScale documentation

---

This guide covers the essential aspects of package management with the Humio Operator. For advanced use cases or specific scenarios not covered here, consult the API reference documentation.