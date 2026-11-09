# HumioBootstrapToken Management Guide

## Overview

The Humio Operator provides native Kubernetes support for managing LogScale bootstrap tokens through the **HumioBootstrapToken** custom resource.

### Default Behavior (Fully Automatic)

**By default, you don't need to do anything.** When you create a HumioCluster, the Humio Operator automatically:

1. **Creates a HumioBootstrapToken resource** with the same name as your HumioCluster
2. **Generates a secure random bootstrap token** 
3. **Creates the necessary Kubernetes secret** containing both plain and hashed tokens
4. **Links everything together** without requiring any manual configuration

This means **most users can simply create a HumioCluster and the operator handles all bootstrap token management automatically.**

### Advanced Configuration Options

For users who need custom bootstrap token management, the operator also supports:

- **Custom plain tokens**: Provide your own bootstrap token secret and let the operator generate the hashed version
- **Pre-hashed tokens**: Provide both plain and hashed tokens if you've already generated them
- **Separate secrets**: Store plain and hashed tokens in different secrets for enhanced security
- **Manual lifecycle management**: Full control over bootstrap token creation and rotation

The operator automatically handles the complex token hashing process, ensuring compatibility with LogScale's internal bootstrap token requirements.

## How Bootstrap Token Hashing Works

LogScale clusters require bootstrap tokens to be stored in two forms:
1. **Plain Token**: The original token value for API access
2. **Hashed Token**: A cryptographically hashed version for internal cluster authentication

The Humio Operator simplifies this by:
1. Accepting secrets with only the plain token
2. Automatically generating the hashed token using LogScale's `TokenHashing` utility
3. Adding the hashed token to the same secret
4. Managing the complete token lifecycle

This ensures your tokens are properly formatted and compatible with LogScale's security requirements without requiring manual token hashing.

## Getting Started

### Default Behavior: Zero Configuration Required

**For most use cases, simply create your HumioCluster and the operator handles everything:**

```yaml
# That's it! No bootstrap token configuration needed.
apiVersion: core.humio.com/v1alpha1
kind: HumioCluster
metadata:
  name: my-humio-cluster
  namespace: humio-operator
spec:
  image: humio/humio-core:1.210.0
  targetReplicationFactor: 2
  storagePartitionsCount: 12
  digestPartitionsCount: 12
  
  # The operator automatically:
  # 1. Creates a HumioBootstrapToken named "my-humio-cluster" 
  # 2. Generates a secure bootstrap token
  # 3. Creates the secret with both plain and hashed tokens
  # 4. Configures cluster pods to use the tokens
```

When you apply this HumioCluster, the operator automatically creates:
- A `HumioBootstrapToken` resource named `my-humio-cluster`
- A Kubernetes secret containing both plain and hashed bootstrap tokens
- All necessary pod configurations to use the bootstrap token

**You can verify the automatic creation:**

```bash
# Check that bootstrap token was created automatically
kubectl get humiobootstraptoken my-humio-cluster

# Check that secret was created automatically  
kubectl get secret my-humio-cluster-bootstrap-token

# Both should show as "Ready" without any manual intervention
```

### Custom Configuration: Advanced Use Cases

If you need custom bootstrap token behavior, you can create your own HumioBootstrapToken resource **before** creating the HumioCluster. When a custom HumioBootstrapToken exists, the operator uses it instead of creating a default one.

#### Option 1: Provide Your Own Plain Token (Recommended)

Create a secret with only your plain bootstrap token, and let the operator generate the hashed version:

```bash
# Generate a secure random token
BOOTSTRAP_TOKEN=$(openssl rand -base64 32)

# Create secret with only the plain token
kubectl create secret generic my-custom-bootstrap-secret \
  --from-literal=secret="$BOOTSTRAP_TOKEN" \
  --namespace=humio-operator
```

```yaml
# Create custom HumioBootstrapToken before HumioCluster
apiVersion: core.humio.com/v1alpha1
kind: HumioBootstrapToken
metadata:
  name: my-humio-cluster  # Must match HumioCluster name
  namespace: humio-operator
spec:
  managedClusterName: my-humio-cluster
  tokenSecret:
    secretKeyRef:
      name: my-custom-bootstrap-secret
      key: secret
  hashedTokenSecret:
    secretKeyRef:
      name: my-custom-bootstrap-secret
      key: hashedToken  # Operator will add this key
```

**Important**: Only include the plain token (`secret` field). The operator will automatically add the `hashedToken` field to the same secret.

#### Option 2: Provide Both Plain and Hashed Tokens

If you've already generated both tokens (e.g., using LogScale's TokenHashing utility), you can provide both:

```yaml
# Secret with both tokens pre-populated
apiVersion: v1
kind: Secret
metadata:
  name: pre-hashed-bootstrap-secret
  namespace: humio-operator
type: Opaque
data:
  secret: <base64-encoded-plain-token>
  hashedToken: <base64-encoded-hashed-token>

---
# HumioBootstrapToken pointing to existing secret
apiVersion: core.humio.com/v1alpha1
kind: HumioBootstrapToken
metadata:
  name: my-humio-cluster
  namespace: humio-operator
spec:
  managedClusterName: my-humio-cluster
  tokenSecret:
    secretKeyRef:
      name: pre-hashed-bootstrap-secret
      key: secret
  hashedTokenSecret:
    secretKeyRef:
      name: pre-hashed-bootstrap-secret
      key: hashedToken
```

#### Option 3: Separate Secrets for Enhanced Security

For enhanced security, you can store plain and hashed tokens in separate secrets:

```yaml
# Plain token secret (restricted access)
apiVersion: v1
kind: Secret
metadata:
  name: bootstrap-plain-token
type: Opaque
data:
  token: <base64-plain-token>

---
# Hashed token secret (broader access)  
apiVersion: v1
kind: Secret
metadata:
  name: bootstrap-hashed-token
type: Opaque
# hashedToken will be added by operator

---
# HumioBootstrapToken configuration
apiVersion: core.humio.com/v1alpha1
kind: HumioBootstrapToken
metadata:
  name: my-humio-cluster
spec:
  managedClusterName: my-humio-cluster
  tokenSecret:
    secretKeyRef:
      name: bootstrap-plain-token
      key: token
  hashedTokenSecret:
    secretKeyRef:
      name: bootstrap-hashed-token
      key: hashedToken
```

### Configuration Summary

| Approach | When to Use | What You Provide | What Operator Does |
|----------|-------------|------------------|--------------------|
| **Default (Recommended)** | Standard deployments | Just the HumioCluster | Creates HumioBootstrapToken, generates tokens, creates secret |
| **Custom Plain Token** | You have existing tokens or specific token requirements | Plain token in a secret | Creates HumioBootstrapToken, generates hashed token |  
| **Pre-hashed Tokens** | You've already generated hashed tokens externally | Both plain and hashed tokens | Uses existing tokens, skips generation |
| **Separate Secrets** | Enhanced security isolation | Plain token in one secret | Generates hashed token in separate secret |

## Complete Configuration Examples

### Example 1: Default Behavior (Recommended)

**Most users should use this approach - zero configuration required:**

```yaml
# Simply create your HumioCluster - no bootstrap token configuration needed
apiVersion: core.humio.com/v1alpha1
kind: HumioCluster
metadata:
  name: production-cluster
  namespace: humio-operator
spec:
  image: humio/humio-core:1.210.0
  targetReplicationFactor: 2
  storagePartitionsCount: 12
  digestPartitionsCount: 12

  # License configuration
  license:
    secretKeyRef:
      name: production-license
      key: data

  # Node pool configuration - operator automatically injects bootstrap token
  nodePools:
    - name: "all-nodes"
      spec:
        nodeCount: 3
        environmentVariables:
          - name: NODE_ROLES
            value: "all"
          - name: ORGANIZATION_MODE
            value: "single"
          - name: AUTHENTICATION_METHOD
            value: "static"
          - name: STATIC_USERS
            value: "admin:admin"
        resources:
          requests:
            cpu: "500m"
            memory: 2Gi
          limits:
            cpu: "2000m"
            memory: 4Gi

# The operator automatically creates:
# 1. HumioBootstrapToken named "production-cluster"
# 2. Secret named "production-cluster-bootstrap-token" 
# 3. All necessary pod configurations
```

### Example 2: Custom Plain Token

**For when you need to provide your own bootstrap token:**

```yaml
# 1. Bootstrap Token Secret (user-provided plain token)
apiVersion: v1
kind: Secret
metadata:
  name: production-bootstrap-token
  namespace: humio-operator
  labels:
    app.kubernetes.io/name: humio
    app.kubernetes.io/instance: production-cluster
    app.kubernetes.io/managed-by: humio-operator
type: Opaque
data:
  secret: <base64-encoded-plain-token>
  # hashedToken will be automatically added by the operator

---
# 2. HumioBootstrapToken Resource (created before HumioCluster)
apiVersion: core.humio.com/v1alpha1
kind: HumioBootstrapToken
metadata:
  name: production-cluster  # Must match HumioCluster name
  namespace: humio-operator
  labels:
    app.kubernetes.io/name: humio
    app.kubernetes.io/instance: production-cluster
    app.kubernetes.io/managed-by: humio-operator
    managed-cluster-name: production-cluster
spec:
  managedClusterName: production-cluster
  tokenSecret:
    secretKeyRef:
      name: production-bootstrap-token
      key: secret
  hashedTokenSecret:
    secretKeyRef:
      name: production-bootstrap-token
      key: hashedToken

---
# 3. HumioCluster using the custom bootstrap token
apiVersion: core.humio.com/v1alpha1
kind: HumioCluster
metadata:
  name: production-cluster  # This name links to the bootstrap token above
  namespace: humio-operator
spec:
  image: humio/humio-core:1.210.0
  targetReplicationFactor: 2
  storagePartitionsCount: 12
  digestPartitionsCount: 12
  # ... rest of configuration
  # Operator automatically discovers and uses the custom HumioBootstrapToken
```

### Example 3: Pre-hashed Tokens

**For when you've already generated both tokens externally:**

```yaml
# Secret with both tokens pre-populated
apiVersion: v1
kind: Secret
metadata:
  name: pre-hashed-bootstrap-secret
  namespace: humio-operator
type: Opaque
data:
  secret: <base64-encoded-plain-token>
  hashedToken: <base64-encoded-hashed-token>

---
# HumioBootstrapToken pointing to existing secret
apiVersion: core.humio.com/v1alpha1
kind: HumioBootstrapToken
metadata:
  name: production-cluster  # Must match HumioCluster name
  namespace: humio-operator
spec:
  managedClusterName: production-cluster
  tokenSecret:
    secretKeyRef:
      name: pre-hashed-bootstrap-secret
      key: secret
  hashedTokenSecret:
    secretKeyRef:
      name: pre-hashed-bootstrap-secret
      key: hashedToken

---
# HumioCluster - operator detects existing bootstrap token and uses it
apiVersion: core.humio.com/v1alpha1
kind: HumioCluster
metadata:
  name: production-cluster
  namespace: humio-operator
spec:
  # ... cluster configuration
  # Operator skips token generation since both tokens already exist
```

## How Bootstrap Token Discovery Works

### Default Behavior: Automatic Creation and Discovery

When you create a HumioCluster **without** a matching HumioBootstrapToken:

1. **Automatic Creation**: The operator creates a HumioBootstrapToken with the same name as the HumioCluster
2. **Token Generation**: Generates secure random bootstrap tokens
3. **Secret Creation**: Creates a secret containing both plain and hashed tokens  
4. **Pod Configuration**: Automatically configures cluster pods to use the bootstrap token

### Custom Behavior: Pre-existing Bootstrap Token Discovery  

When you create a HumioBootstrapToken **before** creating the HumioCluster:

1. **Name-Based Linking**: The operator matches `HumioBootstrapToken.spec.managedClusterName` with `HumioCluster.metadata.name`
2. **Namespace Scope**: Both resources must be in the same Kubernetes namespace
3. **Label-Based Discovery**: The operator uses labels to efficiently find matching bootstrap tokens
4. **Status Population**: Once linked, the bootstrap token status shows the secret references for the cluster to use

The Humio Operator automatically connects HumioBootstrapToken resources to HumioCluster resources without explicit references in the cluster spec.

### Bootstrap Token Injection

When the HumioCluster pods are created, the operator:
1. **Discovers** the matching bootstrap token via `managedClusterName`
2. **Retrieves** the hashed token from the secret
3. **Injects** the `BOOTSTRAP_ROOT_TOKEN_HASHED` environment variable into cluster pods
4. **Manages** the token lifecycle automatically

This design eliminates the need for manual secret references in cluster specifications while maintaining security and flexibility.

## HumioBootstrapToken Configuration

### Core Fields

| Field | Description | Required |
|-------|-------------|----------|
| `managedClusterName` | Name of the HumioCluster resource this token belongs to | Yes |
| `tokenSecret.secretKeyRef` | Reference to the plain bootstrap token in a Kubernetes secret | Yes |
| `hashedTokenSecret.secretKeyRef` | Reference to where the hashed token will be stored | Yes |

**Note**: Both `tokenSecret` and `hashedTokenSecret` typically reference the same secret but different keys.

### Secret Structure

#### Before Operator Processing
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: my-bootstrap-token
data:
  secret: <base64-encoded-plain-token>  # User-provided
```

#### After Operator Processing
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: my-bootstrap-token
data:
  secret: <base64-encoded-plain-token>       # User-provided
  hashedToken: <base64-encoded-hashed-token> # Operator-generated
```

## Token Hashing Process

The operator uses LogScale's official `TokenHashing` utility to ensure compatibility:

1. **Detection**: Operator detects missing `hashedToken` in the secret
2. **Hashing**: Runs LogScale's `TokenHashing` utility in a temporary container:
   ```bash
   java -Dlog4j2.configurationFile=bin/tools-log4j2.xml \
     com.humio.main.TokenHashing --json
   ```
3. **Storage**: Adds the generated `hashedToken` to the original secret
4. **Validation**: Verifies the token format and updates resource status

### Token Hashing Security

- Hashing runs in isolated, temporary containers
- Uses official LogScale Docker images
- Plain tokens are passed via secure environment variables
- Temporary containers are immediately deleted after hashing
- All hashing operations are logged for audit purposes

## Operations and Monitoring

### Checking Bootstrap Token Status

```bash
# List all bootstrap tokens
kubectl get humiobootstraptoken

# Check specific token status
kubectl describe humiobootstraptoken my-cluster-bootstrap
```

#### Healthy Status Example
```yaml
Status:
  State: Ready
  Message: "Bootstrap token is ready"
  Last Updated: "2024-01-15T10:30:00Z"
  Token Status:
    Plain Token: Found
    Hashed Token: Generated
    Last Hash Time: "2024-01-15T10:29:45Z"
```

### Monitoring Secret Changes

```bash
# Check secret contents
kubectl get secret my-bootstrap-token-secret -o jsonpath='{.data}' | jq

# Verify both tokens are present
kubectl get secret my-bootstrap-token-secret -o jsonpath='{.data}' | jq 'keys[]'
```

### Bootstrap Token States

- **Ready**: Both plain and hashed tokens are available and cluster is linked
- **NotReady**: Bootstrap token processing in progress or temporary error
- **ConfigError**: Invalid user configuration (missing secrets, incorrect references, missing cluster)
- **Unknown**: Status being determined

## Advanced Configuration

### Using Separate Secrets

You can store plain and hashed tokens in separate secrets for enhanced security:

```yaml
# Plain token secret (restricted access)
apiVersion: v1
kind: Secret
metadata:
  name: bootstrap-plain-token
type: Opaque
data:
  token: <base64-plain-token>

---
# Hashed token secret (broader access)
apiVersion: v1
kind: Secret
metadata:
  name: bootstrap-hashed-token
type: Opaque
# hashedToken will be added by operator

---
# Bootstrap token configuration
apiVersion: core.humio.com/v1alpha1
kind: HumioBootstrapToken
metadata:
  name: my-cluster
spec:
  managedClusterName: my-cluster
  tokenSecret:
    secretKeyRef:
      name: bootstrap-plain-token
      key: token
  hashedTokenSecret:
    secretKeyRef:
      name: bootstrap-hashed-token
      key: hashedToken
```

### Multi-Cluster Bootstrap Tokens

For managing multiple clusters with unique bootstrap tokens:

```yaml
# Development cluster
apiVersion: core.humio.com/v1alpha1
kind: HumioBootstrapToken
metadata:
  name: dev-cluster-bootstrap
  namespace: development
spec:
  managedClusterName: dev-cluster
  tokenSecret:
    secretKeyRef:
      name: dev-bootstrap-secret
      key: secret
  hashedTokenSecret:
    secretKeyRef:
      name: dev-bootstrap-secret
      key: hashedToken

---
# Production cluster
apiVersion: core.humio.com/v1alpha1
kind: HumioBootstrapToken
metadata:
  name: prod-cluster-bootstrap
  namespace: production
spec:
  managedClusterName: prod-cluster
  tokenSecret:
    secretKeyRef:
      name: prod-bootstrap-secret
      key: secret
  hashedTokenSecret:
    secretKeyRef:
      name: prod-bootstrap-secret
      key: hashedToken
```

## Troubleshooting

### Common Issues

#### 1. Token Hashing Failures

**Symptoms**:
```yaml
Status:
  State: HashingFailed
  Message: "Failed to hash bootstrap token: container execution failed"
```

**Solutions**:
```bash
# Check operator logs for detailed error information
kubectl logs -n humio-system deployment/humio-operator | grep -i bootstrap

# Verify the LogScale image is available
kubectl run test --image=humio/humio-core:1.210.0 --rm -it -- echo "Image test"

# Check if the plain token is valid base64
kubectl get secret my-bootstrap-token -o jsonpath='{.data.secret}' | base64 -d
```

#### 2. Secret Reference Errors

**Symptoms**:
```yaml
Status:
  State: ConfigError
  Message: "user-provided TokenSecret 'my-bootstrap-token' not found. Please create the secret or remove the tokenSecret.secretKeyRef from the HumioBootstrapToken spec"
```

**Solutions**:
```bash
# Verify secret exists in correct namespace
kubectl get secret my-bootstrap-token -n <namespace>

# Check secret has required keys
kubectl get secret my-bootstrap-token -o jsonpath='{.data}' | jq 'keys[]'

# Verify secret references match exactly
kubectl get humiobootstraptoken <name> -o yaml | grep -A 5 secretKeyRef

# If secret was deleted, recreate it:
kubectl create secret generic my-bootstrap-token \
  --from-literal=secret="<your-plain-token>" \
  --namespace=<namespace>

# Or remove the reference to let operator manage the secret:
kubectl patch humiobootstraptoken <name> --type='json' \
  -p='[{"op": "remove", "path": "/spec/tokenSecret"}]'
```

**Note**: ConfigError state persists until the user fixes the configuration. The operator won't retry automatically to avoid endless loops.

#### 3. Cluster Association Issues

**Symptoms**:
```yaml
Status:
  State: ConfigError
  Message: "managed cluster 'my-cluster' not found"
```

**Solutions**:
```bash
# Verify HumioCluster exists
kubectl get humiocluster my-cluster

# Ensure bootstrap token and cluster are in same namespace
kubectl get humiocluster,humiobootstraptoken -n <namespace>

# Check cluster name matches exactly (case-sensitive)
```

#### 4. Token Re-hashing

**Symptoms**: Need to regenerate hashed token (token rotation, corruption, etc.)

**Solutions**:
```bash
# Remove the hashed token to trigger re-hashing
kubectl patch secret my-bootstrap-token --type=json \
  -p='[{"op": "remove", "path": "/data/hashedToken"}]'

# Watch the bootstrap token reconciler regenerate it
kubectl get humiobootstraptoken my-token -w

# Verify new hashed token was generated
kubectl get secret my-bootstrap-token -o jsonpath='{.data.hashedToken}' | base64 -d
```

## Security Best Practices

### Token Generation

1. **Use Cryptographically Secure Tokens**:
   ```bash
   # Generate strong tokens
   openssl rand -base64 32  # 32 bytes = 256 bits of entropy

   # Or use uuidgen for shorter but still secure tokens
   uuidgen | tr -d '-'
   ```

2. **Avoid Predictable Tokens**: Never use simple passwords, dictionary words, or sequential values

### Secret Management

1. **Kubernetes RBAC**: Restrict access to bootstrap token secrets:
   ```yaml
   apiVersion: rbac.authorization.k8s.io/v1
   kind: Role
   metadata:
     name: bootstrap-token-reader
   rules:
   - apiGroups: [""]
     resources: ["secrets"]
     resourceNames: ["bootstrap-token-*"]
     verbs: ["get", "list"]
   ```

2. **Namespace Isolation**: Store bootstrap tokens in dedicated namespaces
3. **Secret Encryption**: Enable Kubernetes secret encryption at rest
4. **Regular Rotation**: Rotate bootstrap tokens periodically

### Monitoring and Auditing

1. **Audit Secret Access**:
   ```bash
   # Monitor secret access in audit logs
   kubectl get events --field-selector reason=SecretAccess
   ```

2. **Token Usage Tracking**: Monitor which clusters are using which bootstrap tokens
3. **Failed Access Attempts**: Alert on repeated bootstrap token authentication failures

## Testing Bootstrap Token Configuration

Use the provided test script to validate your bootstrap token setup:

```bash
# Set required environment variable
export HUMIO_E2E_LICENSE="your-logscale-license-jwt"

# Run the bootstrap token test environment
./hack/run-bootstrap-token-test.sh

# The script will:
# 1. Create a KIND cluster with Kafka/Zookeeper
# 2. Build and install the Humio Operator
# 3. Create a test bootstrap token secret (plain token only)
# 4. Create a HumioBootstrapToken resource
# 5. Create a test HumioCluster using the bootstrap token
# 6. Verify the operator generates the hashed token automatically

# Monitor the bootstrap token processing
kubectl get humiobootstraptoken logscale-test -n logging -w

# Check that hashed token was generated
kubectl get secret logscale-test-bootstrap-token-only-secret -n logging \
  -o jsonpath='{.data}' | jq 'keys[]'

# Clean up test environment
./hack/run-bootstrap-token-test.sh cleanup
```

## Integration with GitOps

### Kustomize Example

```yaml
# base/bootstrap-token.yaml
apiVersion: v1
kind: Secret
metadata:
  name: bootstrap-token
type: Opaque
stringData:
  secret: "will-be-replaced-by-overlay"

---
apiVersion: core.humio.com/v1alpha1
kind: HumioBootstrapToken
metadata:
  name: cluster-bootstrap
spec:
  managedClusterName: humio-cluster
  tokenSecret:
    secretKeyRef:
      name: bootstrap-token
      key: secret
  hashedTokenSecret:
    secretKeyRef:
      name: bootstrap-token
      key: hashedToken
```

```yaml
# overlays/production/kustomization.yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
- ../../base

patchesStrategicMerge:
- bootstrap-token-patch.yaml

secretGenerator:
- name: bootstrap-token
  literals:
  - secret=production-secure-token-here
  behavior: replace
```

### ArgoCD Integration

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: humio-bootstrap-tokens
spec:
  project: default
  source:
    repoURL: https://github.com/company/humio-configs
    targetRevision: HEAD
    path: bootstrap-tokens
  destination:
    server: https://kubernetes.default.svc
    namespace: humio-operator
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    syncOptions:
    - RespectIgnoreDifferences=true
    - ApplyOutOfSyncOnly=true
```

## Migration Guide

### From Manual Token Management

If you're currently managing bootstrap tokens manually:

1. **Inventory Existing Tokens**: Document current bootstrap tokens and their usage
2. **Create Kubernetes Secrets**: Store existing plain tokens in Kubernetes secrets
3. **Create Bootstrap Token Resources**: Define HumioBootstrapToken resources
4. **Update Cluster References**: Point HumioCluster resources to new secret structure
5. **Validate Operation**: Ensure hashed tokens are generated correctly
6. **Remove Manual Processes**: Eliminate manual token hashing procedures

### Token Rotation Process

```bash
# 1. Generate new token
NEW_TOKEN=$(openssl rand -base64 32)

# 2. Update secret with new plain token
kubectl patch secret bootstrap-token --type='merge' \
  -p="{\"data\":{\"secret\":\"$(echo -n $NEW_TOKEN | base64)\"}}"

# 3. Remove old hashed token to trigger re-hashing
kubectl patch secret bootstrap-token --type=json \
  -p='[{"op": "remove", "path": "/data/hashedToken"}]'

# 4. Wait for operator to generate new hashed token
kubectl wait --for=condition=Ready humiobootstraptoken/my-token --timeout=300s

# 5. Restart affected HumioCluster pods to pick up new token
kubectl rollout restart deployment -l app.kubernetes.io/name=humio
```

## FAQ

### Q: Do I need to create a HumioBootstrapToken for my HumioCluster?

**A**: **No, in most cases you don't need to do anything.** The operator automatically creates a HumioBootstrapToken and handles all bootstrap token management when you create a HumioCluster. Only create a custom HumioBootstrapToken if you need specific token requirements or enhanced security controls.

### Q: How does the operator generate bootstrap tokens automatically?

**A**: When you create a HumioCluster without a matching HumioBootstrapToken, the operator:
1. Creates a HumioBootstrapToken resource with the same name as your cluster
2. Generates a cryptographically secure random bootstrap token
3. Creates a Kubernetes secret with both plain and hashed versions
4. Configures all cluster pods to use the bootstrap token automatically

### Q: When should I create a custom HumioBootstrapToken?

**A**: Create a custom HumioBootstrapToken when you need:
- **Specific token values**: You have existing tokens or compliance requirements for token format
- **External token generation**: You generate tokens using external systems
- **Enhanced security**: Separate secrets, custom key names, or specific access controls
- **Token reuse**: Share tokens across environments (though not generally recommended)

### Q: Can I use the same bootstrap token for multiple clusters?

**A**: While technically possible, it's not recommended for security. Each cluster should have a unique bootstrap token to limit blast radius in case of compromise and enable granular token rotation.

### Q: What happens if I delete the automatically-created bootstrap token secret?

**A**: The operator will detect the missing secret and recreate it with a new random bootstrap token. However, this will require restarting your HumioCluster pods to pick up the new token, so avoid deleting operator-managed secrets.

### Q: What happens if I delete the hashed token from a custom secret?

**A**: The operator will automatically detect the missing hashed token and regenerate it using the plain token. This is useful for token recovery or forced re-hashing when using custom secrets.

### Q: How often should I rotate bootstrap tokens?

**A**: Bootstrap tokens should be rotated:
- Every 90-180 days as part of regular security maintenance
- Immediately after suspected compromise
- When team members with access leave the organization
- After major security incidents

### Q: Can I backup and restore bootstrap tokens?

**A**: Yes, but with important considerations:
- **Backup**: Store plain tokens securely (encrypted backups, secure credential managers)
- **Restore**: Create new secrets with plain tokens; let operator regenerate hashed tokens
- **Never backup hashed tokens**: They're cluster-specific and automatically regenerated

### Q: What's the difference between bootstrap tokens and other LogScale tokens?

**A**: Bootstrap tokens are specifically for initial cluster setup and inter-node authentication. They differ from:
- **API tokens**: For application/user access to LogScale APIs
- **Ingest tokens**: For data ingestion via HEC or other protocols
- **User tokens**: For individual user authentication

### Q: How do I troubleshoot token hashing container failures?

**A**: Check several areas:
```bash
# 1. Operator logs for detailed error messages
kubectl logs deployment/humio-operator | grep -i bootstrap

# 2. Node resources and image pull capability
kubectl describe node | grep -A 5 "System Info"

# 3. Network policies that might block container execution
kubectl get networkpolicy -A

# 4. Security contexts and pod security policies
kubectl get psp,scc -A
```

## Related Documentation

- [HumioBootstrapToken CRD Reference](../api/v1alpha1/humiobootstraptoken_types.go)
- [HumioCluster Configuration](../README.md#humio-cluster-configuration)
- [Operator Security Guide](../SECURITY.md)
- [Testing with KIND Clusters](../hack/run-bootstrap-token-test.sh)

## Support

For issues with bootstrap token management:

1. Check the troubleshooting section above
2. Review operator logs: `kubectl logs deployment/humio-operator`
3. Verify cluster connectivity and health
4. Test with the provided bootstrap token test script
5. Create an issue in the [humio-operator repository](https://github.com/humio/humio-operator/issues)