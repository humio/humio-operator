# Humio Operator Telemetry Collection Guide

This guide explains how to enable telemetry collection in the Humio Operator to monitor your LogScale clusters and export usage metrics to remote telemetry systems.

## Overview

The Humio Operator telemetry system consists of two main components:

- **[HumioTelemetryCollection](api/v1alpha1/humiotelemetrycollection_types.go)**: Defines what data to collect and how often
- **[HumioTelemetryExport](api/v1alpha1/humiotelemetryexport_types.go)**: Defines where to send collected data

This decoupled architecture allows you to:
- Collect different types of data on different schedules
- Send telemetry data to multiple destinations
- Enable collection without export for local monitoring
- Manage telemetry configuration independently from cluster configuration

## Quick Telemetry Setup (HumioCluster)

For a simple setup, you can enable telemetry directly in your HumioCluster specification. This approach automatically creates the required HumioTelemetryCollection and HumioTelemetryExport resources for you.

Add the following to your HumioCluster spec:

```yaml
apiVersion: core.humio.com/v1alpha1
kind: HumioCluster
metadata:
  name: example-humiocluster
spec:
  # ... your existing cluster configuration ...

  # Enable telemetry with default collection
  telemetryConfig:
    clusterIdentifier: "telemetry-test-cluster"
    remoteReport:
      url: "<your-logscale-url>/api/v1/ingest/hec"  # Your LogScale HEC endpoint
      token:
        secretKeyRef:
          name: "telemetry-token-secret"
          key: "token"
    collections:
      - interval: "1d"
        include:
          - "license"
          - "cluster_info"
          - "ingestion_metrics"
      - interval: "1h"
        include:
          - "repository_usage"
```

Before applying this configuration, create the required token secret:

```bash
kubectl create secret generic telemetry-token-secret \
  --from-literal=token="your-hec-token-here" \
  --namespace=your-humio-namespace
```

This simple approach is perfect for getting started quickly. For more advanced setups with multiple exporters or fine-grained control, use the separate resource approach described below.

## Quick Start

### 1. Create Telemetry Token Secret

First, create a Kubernetes Secret containing your telemetry ingest token:

```bash
kubectl create secret generic telemetry-token-secret \
  --from-literal=token="your-hec-token-here" \
  --namespace=humio-operator
```

### 2. Create HumioTelemetryCollection

Create a collection resource to define what data to collect:

```yaml
apiVersion: core.humio.com/v1alpha1
kind: HumioTelemetryCollection
metadata:
  name: my-cluster-telemetry
  namespace: humio-operator
spec:
  managedClusterName: "my-humio-cluster"  # Must exist in same namespace
  clusterIdentifier: "production-cluster-us-west"  # Custom identifier for telemetry
  collections:
    # Daily collection for license and cluster overview
    - interval: "1d"
      include:
        - "license"
        - "cluster_info"
        - "ingestion_metrics"

    # Hourly collection for repository usage trends
    - interval: "1h"
      include:
        - "repository_usage"
```

### 3. Create HumioTelemetryExport

Create an export resource to define where to send the data:

```yaml
apiVersion: core.humio.com/v1alpha1
kind: HumioTelemetryExport
metadata:
  name: my-telemetry-exporter
  namespace: humio-operator
spec:
  remoteReport:
    # LogScale HEC endpoint (replace with your telemetry cluster URL)
    url: "http://logscale-test.humio-operator.svc.cluster.local:8080/api/v1/ingest/hec"
    token:
      secretKeyRef:
        name: "telemetry-token-secret"
        key: "token"
    tls:
      insecureSkipVerify: false  # Set to true for development only

  # Register which collections this exporter should receive data from
  registeredCollections:
    - name: "my-cluster-telemetry"
      namespace: "humio-operator"  # Optional if same namespace

  sendCollectionErrors: true  # Export collection errors for debugging
```

### 4. Apply and Verify

```bash
# Apply the configurations
kubectl apply -f telemetry-collection.yaml
kubectl apply -f telemetry-export.yaml

# Check collection status
kubectl describe humiotelemetrycollection my-cluster-telemetry

# Check export status
kubectl describe humiotelemetryexport my-telemetry-exporter
```

## Configuration Reference

### Complete Configuration Example

Here's a comprehensive example showing all available options:

```yaml
# Telemetry Collection Configuration
apiVersion: core.humio.com/v1alpha1
kind: HumioTelemetryCollection
metadata:
  name: production-telemetry
  namespace: humio-operator
spec:
  managedClusterName: "my-humio-cluster"
  clusterIdentifier: "telemetry-test-cluster"
  collections:
    # Daily business metrics
    - interval: "1d"
      include:
        - "license"           # License details, expiration, limits
        - "cluster_info"      # Version, node count
        - "ingestion_metrics" # Daily/weekly/monthly ingest volumes

    # Hourly operational metrics
    - interval: "1h"
      include:
        - "repository_usage"  # Per-repository storage and activity

---
# Telemetry Export Configuration
apiVersion: core.humio.com/v1alpha1
kind: HumioTelemetryExport
metadata:
  name: production-exporter
  namespace: humio-operator
spec:
  remoteReport:
    url: "https://telemetry.example.com/api/v1/ingest/hec"
    token:
      secretKeyRef:
        name: "telemetry-token-secret"
        key: "token"
    tls:
      insecureSkipVerify: false
  registeredCollections:
    - name: "production-telemetry"
  sendCollectionErrors: true
```

### Environment Variable Substitution

You can use environment variables in the URL configuration:

```yaml
spec:
  remoteReport:
    url: "http://logscale-test.${HUMIO_NAMESPACE}.svc.cluster.local:8080/api/v1/ingest/hec"
```

The operator will substitute `${HUMIO_NAMESPACE}` with the actual namespace value.

## Collection Types Explained

Each collection type gathers specific telemetry data. Here's what each collects and why you might want them:

### `license` (Recommended: Daily)
**What it collects:**
- License type (onprem, trial, SaaS)
- Expiration and issued dates
- Maximum users allowed
- Daily ingestion limits (from JWT if available)
- Core limits and license subject

**Why you want it:**
- Track license utilization and approaching limits
- Plan for license renewals
- Monitor compliance with license terms
- Identify when clusters approach capacity limits

**Collection method:** GraphQL + JWT extraction from cluster secrets

### `cluster_info` (Recommended: Daily)
**What it collects:**
- LogScale version running
- Number of nodes in cluster
- User count (TODO: not yet implemented)
- Repository count (TODO: not yet implemented)

**Why you want it:**
- Track cluster growth and scaling
- Monitor version deployments across environments
- Capacity planning for infrastructure
- Verify cluster health and node availability

**Collection method:** GraphQL queries

### `ingestion_metrics` (Recommended: Daily)
**What it collects:**
- Organization details and subscription info
- Contracted vs actual daily ingestion volumes
- Storage utilization (current and Falcon storage)
- 30-day average ingestion rates
- Daily, weekly, and monthly ingestion trends
- Event counts and average event sizes

**Why you want it:**
- Monitor ingestion against contracted limits
- Identify ingestion growth trends
- Plan for capacity increases
- Track storage costs and optimization opportunities
- Detect unusual ingestion spikes or drops

**Collection method:** LogScale search queries on `humio-usage` repository

### `repository_usage` (Recommended: Hourly)
**What it collects:**
- Per-repository ingestion volumes (24h)
- Event counts per repository
- Storage usage by repository
- Retention settings
- Last activity timestamps
- Top repositories by volume

**Why you want it:**
- Identify high-volume repositories for optimization
- Track repository growth patterns
- Monitor data retention compliance
- Detect inactive repositories for cleanup
- Chargeback and cost allocation by team/application

**Collection method:** GraphQL + LogScale search queries

## Interval Guidelines

Choose collection intervals based on your monitoring needs and data volume:

| Interval | Use Case | Collection Types | Impact |
|----------|----------|------------------|---------|
| `1d` | Business metrics | `license`, `cluster_info`, `ingestion_metrics` | Low impact, good for trends |
| `1h` | Operational monitoring | `repository_usage` | Medium impact, good for capacity planning |

**Note:** More frequent collection increases load on your LogScale cluster and telemetry storage. Balance monitoring needs with performance impact.

## Multi-Cluster Telemetry

### Basic vs Advanced Telemetry Split

For data sensitivity and compliance reasons, you may want to split telemetry into basic and advanced collections, sending basic data to multiple destinations (including remote systems) and advanced data only to local/secure systems:

```yaml
# Basic telemetry - safe for remote export
apiVersion: core.humio.com/v1alpha1
kind: HumioTelemetryCollection
metadata:
  name: basic-telemetry
  namespace: humio-operator
spec:
  managedClusterName: "production-cluster"
  clusterIdentifier: "prod-us-west"
  collections:
    # Safe, non-sensitive operational metrics
    - interval: "1d"
      include:
        - "license"           # License status (no personal data)
        - "cluster_info"      # Version and node count only
        - "ingestion_metrics" # Volume metrics (no content)

---
# Advanced telemetry - sensitive data for local export only
apiVersion: core.humio.com/v1alpha1
kind: HumioTelemetryCollection
metadata:
  name: advanced-telemetry
  namespace: humio-operator
spec:
  managedClusterName: "production-cluster"
  clusterIdentifier: "prod-us-west-detailed"
  collections:
    # Potentially sensitive detailed metrics
    - interval: "1h"
      include:
        - "repository_usage"  # May contain repository names/patterns

---
# Export basic data to both local and remote systems
apiVersion: core.humio.com/v1alpha1
kind: HumioTelemetryExport
metadata:
  name: basic-multi-export
spec:
  remoteReport:
    url: "https://vendor-telemetry.example.com/api/v1/ingest/hec"
    token:
      secretKeyRef:
        name: "vendor-telemetry-token"
        key: "token"
  registeredCollections:
    - name: "basic-telemetry"
  sendCollectionErrors: false  # Don't send errors to external systems

---
# Export basic data to local cluster too
apiVersion: core.humio.com/v1alpha1
kind: HumioTelemetryExport
metadata:
  name: basic-local-export
spec:
  remoteReport:
    url: "http://local-logscale.humio.svc.cluster.local:8080/api/v1/ingest/hec"
    token:
      secretKeyRef:
        name: "local-telemetry-token"
        key: "token"
  registeredCollections:
    - name: "basic-telemetry"
  sendCollectionErrors: true  # Local systems can handle error details

---
# Export advanced data ONLY to local cluster
apiVersion: core.humio.com/v1alpha1
kind: HumioTelemetryExport
metadata:
  name: advanced-local-only
spec:
  remoteReport:
    url: "http://local-logscale.humio.svc.cluster.local:8080/api/v1/ingest/hec"
    token:
      secretKeyRef:
        name: "local-telemetry-token"
        key: "token"
  registeredCollections:
    - name: "advanced-telemetry"
  sendCollectionErrors: true
```

**Benefits of this approach:**

1. **Compliance**: Keep sensitive data (user activity, repository names) within your infrastructure
2. **Cost Optimization**: Send less data to external/paid telemetry systems
3. **Data Sovereignty**: Meet regulatory requirements for data location
4. **Dual Visibility**: Vendors get operational metrics, you get full visibility locally
5. **Error Handling**: Send detailed error information only to trusted systems

**Data Sensitivity Guidelines:**

| Collection Type | Sensitivity | Recommended Export |
|----------------|-------------|-------------------|
| `license` | Low | ✅ Remote + Local |
| `cluster_info` | Low | ✅ Remote + Local |
| `ingestion_metrics` | Low-Medium | ✅ Remote + Local (aggregated data only) |
| `repository_usage` | Medium | ⚠️ Local Only (contains repository names) |

### Multiple Collections → Single Exporter

You can collect telemetry from multiple clusters and send to one destination:

```yaml
# Collection for Cluster 1
apiVersion: core.humio.com/v1alpha1
kind: HumioTelemetryCollection
metadata:
  name: prod-cluster-telemetry
spec:
  managedClusterName: "prod-cluster"
  clusterIdentifier: "production-us-west"
  collections:
    - interval: "1h"
      include: ["license", "ingestion_metrics"]

---
# Collection for Cluster 2
apiVersion: core.humio.com/v1alpha1
kind: HumioTelemetryCollection
metadata:
  name: staging-cluster-telemetry
spec:
  managedClusterName: "staging-cluster"
  clusterIdentifier: "staging-us-west"
  collections:
    - interval: "1h"
      include: ["license", "ingestion_metrics"]

---
# Single exporter for both clusters
apiVersion: core.humio.com/v1alpha1
kind: HumioTelemetryExport
metadata:
  name: multi-cluster-exporter
spec:
  remoteReport:
    url: "https://central-telemetry.example.com/api/v1/ingest/hec"
    token:
      secretKeyRef:
        name: "central-telemetry-token"
        key: "token"
  registeredCollections:
    - name: "prod-cluster-telemetry"
    - name: "staging-cluster-telemetry"
```

### Single Collection → Multiple Exporters

You can send the same telemetry data to multiple destinations:

```yaml
# One collection
apiVersion: core.humio.com/v1alpha1
kind: HumioTelemetryCollection
metadata:
  name: cluster-telemetry
spec:
  managedClusterName: "my-cluster"
  collections:
    - interval: "1h"
      include: ["license", "ingestion_metrics"]

---
# Export to corporate telemetry system
apiVersion: core.humio.com/v1alpha1
kind: HumioTelemetryExport
metadata:
  name: corporate-exporter
spec:
  remoteReport:
    url: "https://corp-telemetry.example.com/api/v1/ingest/hec"
    token:
      secretKeyRef:
        name: "corp-telemetry-token"
        key: "token"
  registeredCollections:
    - name: "cluster-telemetry"

---
# Export to vendor telemetry system
apiVersion: core.humio.com/v1alpha1
kind: HumioTelemetryExport
metadata:
  name: vendor-exporter
spec:
  remoteReport:
    url: "https://vendor-telemetry.example.com/api/v1/ingest/hec"
    token:
      secretKeyRef:
        name: "vendor-telemetry-token"
        key: "token"
  registeredCollections:
    - name: "cluster-telemetry"
```

## Troubleshooting

### Checking Collection Status

Use `kubectl describe` to check the status of your telemetry collection:

```bash
kubectl describe humiotelemetrycollection my-cluster-telemetry
```

**Example healthy output:**
```yaml
Status:
  Collection Status:
    ingestion_metrics:
      Collection Count:       24
      Error Count:           0
      Last Collection Time:  2024-01-15T10:00:00Z
    license:
      Collection Count:       1
      Error Count:           0
      Last Collection Time:  2024-01-15T00:00:00Z
  Export Push Results:
    my-telemetry-exporter:
      Last Push Time:    2024-01-15T10:30:15Z
      Status:           success
      Total Pushes:     169
  Last Collection Time:    2024-01-15T10:30:00Z
  Next Scheduled Collection:
    ingestion_metrics:   2024-01-15T11:00:00Z
    license:            2024-01-16T00:00:00Z
  State:                 Enabled
```

### Checking Export Status

Use `kubectl describe` to check the status of your telemetry export:

```bash
kubectl describe humiotelemetryexport my-telemetry-exporter
```

**Example healthy output:**
```yaml
Status:
  Last Export Time:  2024-01-15T10:30:15Z
  Registered Collection Status:
    my-cluster-telemetry:
      Found:                 true
      Last Data Received:    2024-01-15T10:30:00Z
      Successful Exports:    169
      Total Exports:         169
  State:                    Enabled
```

### Common Issues and Solutions

#### Collection Not Starting

**Symptoms:**
```yaml
Status:
  State: ConfigError
  Message: "managed cluster 'my-cluster' not found"
```

**Solution:**
1. Verify the HumioCluster exists: `kubectl get humiocluster my-cluster`
2. Ensure the collection and cluster are in the same namespace
3. Check cluster name spelling in the collection spec

#### Authentication Failures

**Symptoms:**
```yaml
Status:
  Export Push Results:
    my-exporter:
      Status: failed
      Error: "401 Unauthorized"
```

**Solutions:**
1. Verify the token secret exists: `kubectl get secret telemetry-token-secret`
2. Check the token value: `kubectl get secret telemetry-token-secret -o jsonpath='{.data.token}' | base64 -d`
3. Ensure the token has HEC ingest permissions in the target LogScale cluster
4. Verify the HEC endpoint URL is correct

#### Collection Errors

**Symptoms:**
```yaml
Status:
  Collection Status:
    ingestion_metrics:
      Error Count: 5
      Last Error: "search query failed: repository 'humio-usage' not found"
```

**Solutions:**

**For search-based collections (`ingestion_metrics`, `repository_usage`):**
1. Verify the LogScale cluster has the required system repositories:
   - `humio-usage` (for ingestion metrics)
2. Check if the cluster has data in these repositories
3. Verify cluster API connectivity from the operator

**For GraphQL-based collections (`license`, `cluster_info`):**
1. Check cluster API connectivity
2. Verify cluster authentication credentials
3. Check if the cluster is in a healthy state

#### Network Connectivity Issues

**Symptoms:**
```yaml
Status:
  Export Push Results:
    my-exporter:
      Status: failed
      Error: "dial tcp: i/o timeout"
```

**Solutions:**
1. Test connectivity from operator pod:
   ```bash
   kubectl exec -it deployment/humio-operator -- curl -v https://your-telemetry-endpoint
   ```
2. Check firewall rules and network policies
3. Verify DNS resolution of the telemetry endpoint
4. For internal services, ensure service discovery works

#### TLS/Certificate Issues

**Symptoms:**
```yaml
Status:
  Export Push Results:
    my-exporter:
      Status: failed
      Error: "x509: certificate signed by unknown authority"
```

**Solutions:**
1. For development, temporarily set `insecureSkipVerify: true`
2. For production, configure proper TLS:
   ```yaml
   spec:
     remoteReport:
       tls:
         insecureSkipVerify: false
         # Add CA bundle or certificate configuration as needed
   ```

#### High Collection Frequency Issues

**Symptoms:**
- High CPU usage on LogScale cluster
- Collection timeouts or failures
- "too many requests" errors

**Solutions:**
1. Reduce collection frequency for high-impact types:
   ```yaml
   collections:
     - interval: "1h"  # Instead of "10m"
       include:
         - "repository_usage"
   ```
2. Stagger collection times across multiple collections
3. Monitor LogScale cluster performance during collection

## Telemetry Data Structure

### Payload Format

All telemetry data is sent as JSON payloads via HTTP Event Collector (HEC). Each payload includes:

```json
{
  "@timestamp": "2024-01-15T10:30:00Z",
  "cluster_id": "production-us-west",
  "cluster_guid": "humio-repo-id-12345",
  "collection_type": "license",
  "source_type": "json",
  "data": {
    "license_uid": "lic-12345",
    "license_type": "onprem",
    "expiration_date": "2024-12-31T23:59:59Z",
    "max_users": 100,
    "max_ingest_gb_per_day": 50.0
  },
  "collection_errors": []
}
```

### Data Fields

- `@timestamp`: When the data was collected (ISO 8601)
- `cluster_id`: Your custom cluster identifier from `clusterIdentifier` field
- `cluster_guid`: Internal LogScale repository ID (for correlation)
- `collection_type`: Type of data (`license`, `cluster_info`, etc.)
- `source_type`: Always "json" for structured data
- `data`: The actual telemetry data (structure varies by collection type)
- `collection_errors`: Any errors encountered during collection

## Security Considerations

### Token Management

1. **Use Kubernetes Secrets** for storing telemetry tokens:
   ```bash
   kubectl create secret generic telemetry-token \
     --from-literal=token="your-secure-token"
   ```

2. **Rotate tokens regularly** and update secrets accordingly

3. **Limit token permissions** to only HEC ingest on the target repository

### Network Security

1. **Use TLS** for telemetry endpoints (avoid `insecureSkipVerify: true` in production)

2. **Network Policies** to restrict operator pod network access

3. **Firewall rules** to allow only necessary outbound connections

### Data Privacy

1. **Review data types** collected to ensure compliance with privacy policies

2. **Consider data residency** requirements when choosing telemetry destinations

3. **Implement data retention** policies on the telemetry cluster

## Advanced Configuration

### Custom Query Settings

The operator uses these default settings for LogScale queries:

- **Max Execution Time**: 30 seconds
- **Max Result Size**: 1MB
- **Timeout Retries**: 2
- **Time Range Mode**: relative

These settings are optimized for telemetry collection and generally don't need modification.

### Troubleshooting Logs

Check operator logs for detailed telemetry information:

```bash
kubectl logs deployment/humio-operator -f | grep -i telemetry
```

Look for log entries containing:
- Collection execution details
- Export success/failure messages
- Authentication and connectivity issues
- Performance metrics and timing information

## FAQ

### Q: How often should I collect telemetry data?

**A:** It depends on your use case:
- **Business/compliance**: Daily for license and ingestion metrics
- **Operational monitoring**: Hourly for repository usage
- **Development/debugging**: 5-10 minutes temporarily, then reduce

### Q: What's the impact on my LogScale cluster?

**A:** Telemetry collection is designed to be lightweight:
- GraphQL queries (license, cluster_info) have minimal impact
- Search queries are limited to 30 seconds execution time and 1MB result size
- Collections are scheduled to avoid overlapping execution

### Q: Can I collect telemetry without sending it anywhere?

**A:** Yes! You can create a `HumioTelemetryCollection` without any `HumioTelemetryExport` resources. The data will be collected and status will be tracked, but not exported. This is useful for:
- Testing telemetry configuration
- Local monitoring via `kubectl describe`
- Future integration planning

### Q: Why are my collections failing with "repository not found"?

**A:** Some collection types require specific system repositories:
- `ingestion_metrics`: Needs `humio-usage` repository with organizational usage data

These repositories are automatically created in most LogScale installations, but may not exist in:
- Very new clusters (< 24 hours old)
- Minimal or development installations
- Clusters with disabled audit logging

### Q: How do I send telemetry to multiple destinations?

**A:** Create multiple `HumioTelemetryExport` resources that reference the same collection:

```yaml
# Export to corporate system
apiVersion: core.humio.com/v1alpha1
kind: HumioTelemetryExport
metadata:
  name: corporate-export
spec:
  registeredCollections:
    - name: "my-collection"
  remoteReport:
    url: "https://corp-telemetry.com/hec"
    token: { secretKeyRef: { name: "corp-token", key: "token" }}

---
# Export to vendor system
apiVersion: core.humio.com/v1alpha1
kind: HumioTelemetryExport
metadata:
  name: vendor-export
spec:
  registeredCollections:
    - name: "my-collection"
  remoteReport:
    url: "https://vendor-telemetry.com/hec"
    token: { secretKeyRef: { name: "vendor-token", key: "token" }}
```

### Q: Can I disable specific collection types temporarily?

**A:** Yes, edit the `HumioTelemetryCollection` and remove the collection types from the `include` lists:

```bash
# Edit the collection
kubectl edit humiotelemetrycollection my-collection

# Remove unwanted types from collections[].include arrays
# The collection will automatically adjust on the next reconcile cycle
```

### Q: What happens if my telemetry endpoint is down?

**A:** The operator handles export failures gracefully:
- Failed exports are logged and tracked in status
- Collections continue running (data isn't lost)
- Export retries happen on the next collection cycle
- Status shows the last successful export time

### Q: How do I change collection frequency without losing data?

**A:** Edit the collection resource:

```bash
kubectl edit humiotelemetrycollection my-collection

# Change interval values in collections array
# New schedule takes effect immediately
```

The operator will:
- Reschedule collections based on new intervals
- Preserve existing status and metrics
- Continue from the current state seamlessly

### Q: How do I split telemetry between local and remote export for security?

**A:** Use separate collection resources for basic and advanced data with different export destinations:

**Basic Collection (Remote + Local Export):**
- `license`: Safe operational data
- `cluster_info`: Non-sensitive version/node info
- `ingestion_metrics`: Aggregated volume data (no content)

**Advanced Collection (Local Export Only):**
- `repository_usage`: Contains repository names/patterns

**Benefits:**
- Meet compliance requirements for sensitive data
- Reduce costs by sending less data to external systems
- Maintain full visibility locally while sharing operational metrics
- Control error information sharing (send detailed errors only locally)

**Example configuration:**
```bash
# Create two collections with different sensitivity levels
kubectl apply -f basic-telemetry-collection.yaml      # Goes to multiple exporters
kubectl apply -f advanced-telemetry-collection.yaml   # Goes to local exporter only

# Create exporters with appropriate data routing
kubectl apply -f basic-multi-export.yaml     # Sends basic data to vendor
kubectl apply -f basic-local-export.yaml     # Sends basic data locally
kubectl apply -f advanced-local-export.yaml  # Sends advanced data locally only
```

This pattern is especially useful for:
- Regulatory compliance (GDPR, CCPA, data residency)
- Vendor telemetry agreements (limit what data you share)
- Cost optimization (pay for less data to external systems)
- Security policies (keep detailed analytics in-house)

### Q: Can I use different telemetry tokens for different collection types?

**A:** Not directly. Each `HumioTelemetryExport` uses one token for all data it receives. However, you can:

1. **Create multiple export resources** with different tokens:
   ```yaml
   # License data to corporate system
   apiVersion: core.humio.com/v1alpha1
   kind: HumioTelemetryExport
   metadata:
     name: corporate-export
   spec:
     registeredCollections:
       - name: "license-collection"  # Only collects license data

   ---
   # Performance data to operations team
   apiVersion: core.humio.com/v1alpha1
   kind: HumioTelemetryExport
   metadata:
     name: operations-export
   spec:
     registeredCollections:
       - name: "performance-collection"  # Only collects repository_usage
   ```

2. **Split collection resources** by data sensitivity and route to different exports

### Q: How do I verify my telemetry data is arriving correctly?

**A:** Check several places:

1. **Collection status** shows successful collection:
   ```bash
   kubectl describe humiotelemetrycollection my-collection
   # Look for "Collection Status" with recent timestamps and zero error counts
   ```

2. **Export status** shows successful transmission:
   ```bash
   kubectl describe humiotelemetryexport my-export
   # Look for "Last Export Time" and "Successful Exports" counters
   ```

3. **Target telemetry system** should show incoming events:
   - Search for events with `cluster_id` field matching your configuration
   - Check event timestamps are recent
   - Verify data structure matches expected telemetry payload format

4. **Operator logs** provide detailed execution information:
   ```bash
   kubectl logs deployment/humio-operator | grep -i "telemetry\|collection\|export"
   ```

## Related Documentation

- [HumioTelemetryCollection CRD Reference](api/v1alpha1/humiotelemetrycollection_types.go)
- [HumioTelemetryExport CRD Reference](api/v1alpha1/humiotelemetryexport_types.go)
- [Telemetry Integration Tests](internal/controller/suite/telemetry/telemetry_integration_test.go)
- [Humio Operator Configuration](README.md)

## Support

For issues with telemetry collection:

1. Check the troubleshooting section above
2. Review operator logs: `kubectl logs deployment/humio-operator`
3. Verify cluster connectivity and health
4. Create an issue in the [humio-operator repository](https://github.com/humio/humio-operator/issues)