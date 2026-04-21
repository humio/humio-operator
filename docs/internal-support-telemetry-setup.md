# Internal Support: Customer Telemetry Setup Guide

This document provides step-by-step instructions for CrowdStrike/LogScale support personnel to set up telemetry collection for customers using the Humio Operator.

## Prerequisites

- Access to the OEM Telemetry LogScale instance: https://ops.oem-telemetry.logscale.us-2.crowdstrike.com/
- Admin access to the "Customer Telemetry" organization (ID: `rKARAVW04YXlZsaIZstpiajNygl2ko0p`)
- Customer's cluster name and contact information

## Support Team Setup Steps

### 1. Access the OEM Telemetry System

1. Navigate to https://ops.oem-telemetry.logscale.us-2.crowdstrike.com/
2. Sign in with your CrowdStrike/LogScale support credentials
3. Ensure you are in the "Customer Telemetry" organization (ID: `rKARAVW04YXlZsaIZstpiajNygl2ko0p`)

### 2. Create Customer Repository

1. In the LogScale UI, go to **Repositories** in the left sidebar
2. Click **+ Create Repository**
3. Configure the repository:
   - **Name**: `<customer>-telemetry` (replace `<customer>` with the actual customer name, use lowercase and hyphens)
   - **Description**: `Telemetry data for <Customer Name> clusters`
   - **Retention**: Set based on customer agreement (default: 30 days for telemetry data)
   - **Storage**: Use appropriate storage settings based on expected data volume
4. Click **Create Repository**

**Example repository names:**
- `acme-corp-telemetry`
- `global-bank-telemetry`
- `manufacturing-co-telemetry`

### 3. Update the Telemetry View

1. Navigate to **Views** in the left sidebar
2. Find and click on the **"telemetry"** view
3. Click **Edit View** or **Settings**
4. In the **Connected Repositories** section:
   - Click **Add Repository Connection**
   - Select the newly created `<customer>-telemetry` repository
   - Set appropriate permissions (typically read-only for the view)
5. Save the view configuration

### 4. Create Ingest Token

1. Navigate to the customer's repository: `<customer>-telemetry`
2. Go to **Settings** → **Ingest Tokens**
3. Click **Add Token**
4. Configure the token:
   - **Name**: `telemetry`
   - **Parser**: `json`
   - **Description**: `Telemetry collection for <Customer Name> Humio Operator`
   - **Permissions**: Standard ingest permissions
5. Click **Create Token**
6. **IMPORTANT**: Copy the generated token immediately - it cannot be retrieved later
7. Store the token securely for delivery to the customer

### 5. Verify Setup

1. Confirm the repository appears in the "telemetry" view
2. Test the ingest token by sending a sample event (optional):
   ```bash
   curl -X POST "https://ops.oem-telemetry.logscale.us-2.crowdstrike.com/api/v1/ingest/hec" \
     -H "Authorization: Bearer <INGEST_TOKEN>" \
     -H "Content-Type: application/json" \
     -d '{"message": "test telemetry setup", "source": "support-test"}'
   ```

## Customer Configuration Instructions

Once the support team setup is complete, provide the customer with:

### Required Information
- **Ingest Token**: The generated token from step 4 above
- **HEC Endpoint**: `https://ops.oem-telemetry.logscale.us-2.crowdstrike.com/api/v1/ingest/hec`
- **Repository Name**: The `<customer>-telemetry` repository name created

### Customer Setup Steps

Send the customer the following instructions:

---

**Customer Telemetry Configuration**

Your telemetry collection has been set up in our system. Please follow these steps to configure your Humio Operator:

#### 1. Create the Ingest Token Secret

Create a Kubernetes secret containing your telemetry token:

```bash
kubectl create secret generic telemetry-token-secret \
  --from-literal=token="<PROVIDED_INGEST_TOKEN>" \
  --namespace=<your-humio-namespace>
```

#### 2. Configure Telemetry in Your HumioCluster

Add the following telemetry configuration to your HumioCluster spec:

```yaml
apiVersion: core.humio.com/v1alpha1
kind: HumioCluster
metadata:
  name: your-cluster-name
  namespace: your-humio-namespace
spec:
  # ... your existing cluster configuration ...

  # Enable telemetry collection and export
  telemetryConfig:
    # Choose a meaningful identifier for your cluster
    clusterIdentifier: "<your-meaningful-cluster-id>"

    remoteReport:
      url: "https://ops.oem-telemetry.logscale.us-2.crowdstrike.com/api/v1/ingest/hec"
      token:
        secretKeyRef:
          name: "telemetry-token-secret"
          key: "token"

    # Default collection configuration - adjust intervals as needed
    collections:
      # Daily business metrics (low impact)
      - interval: "1d"
        include:
          - "license"           # License status and limits
          - "cluster_info"      # Version and node information
          - "ingestion_metrics" # Daily/weekly/monthly ingest volumes

      # Hourly operational metrics (medium impact)
      - interval: "1h"
        include:
          - "repository_usage"  # Per-repository usage statistics

      # High-frequency monitoring (higher impact - adjust as needed)
      - interval: "10m"
        include:
          - "user_activity"     # User activity and query patterns
          - "detailed_analytics" # Performance and detailed metrics
```

#### 3. Apply the Configuration

```bash
kubectl apply -f your-humiocluster-config.yaml
```

#### 4. Verify Telemetry Collection

```bash
# Check that telemetry collection is running
kubectl get humiotelemetrycollection -n <your-namespace>

# Check collection status
kubectl describe humiotelemetrycollection <cluster-name>-telemetry -n <your-namespace>

# Check export status
kubectl describe humiotelemetryexport <cluster-name>-telemetry-export -n <your-namespace>
```

---

### Important Notes for Customers

**Cluster Identifiers:**
- Choose meaningful cluster identifiers that help you identify different environments
- Examples: `production-us-east`, `staging-eu-west`, `dev-cluster-1`
- You can reuse the same ingest token across multiple clusters, but use different cluster identifiers
- Cluster identifiers appear in all telemetry data for filtering and analysis

**Multiple Clusters:**
- You can use the same ingest token for multiple clusters
- Ensure each cluster has a unique `clusterIdentifier` value
- This allows you to differentiate telemetry data from different clusters

**Collection Frequency:**
- The default configuration provides a good balance of data collection vs. performance impact
- You can adjust intervals based on your monitoring needs:
  - More frequent = better monitoring, higher impact on cluster
  - Less frequent = lower impact, less detailed monitoring

## Troubleshooting Guide

### Common Issues and Solutions

#### 1. Customer Reports Authentication Failures

**Symptoms:** Customer sees "401 Unauthorized" in telemetry export status

**Solutions:**
1. Verify the ingest token was copied correctly
2. Check the token hasn't expired (LogScale tokens don't expire by default, but organization policies may apply)
3. Ensure the token has proper permissions in the customer repository
4. Verify the customer is using the correct HEC endpoint URL

**Verification:**
```bash
# Customer can test the token
curl -X POST "https://ops.oem-telemetry.logscale.us-2.crowdstrike.com/api/v1/ingest/hec" \
  -H "Authorization: Bearer <TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{"test": "connection"}'
```

#### 2. Customer Reports Collection Errors

**Symptoms:** Customer sees collection errors in status like "repository not found"

**Root Causes:**
- Very new LogScale clusters (< 24 hours) may not have system repositories yet
- Minimal installations might not have audit logging enabled
- Custom LogScale configurations might disable certain system repositories

**Solutions:**
1. Wait 24-48 hours for new clusters to populate system repositories
2. Check if the customer's LogScale cluster has:
   - `humio-usage` repository (for ingestion metrics)
   - `humio` system repository (for user activity)
3. Temporarily disable problematic collection types:
   ```yaml
   collections:
     - interval: "1d"
       include:
         - "license"      # Usually works
         - "cluster_info" # Usually works
         # Remove ingestion_metrics and user_activity temporarily
   ```

#### 3. Customer Reports High Performance Impact

**Symptoms:** Customer reports increased CPU/memory usage or slower LogScale performance

**Solutions:**
1. Reduce collection frequency:
   ```yaml
   collections:
     - interval: "1d"    # Instead of hourly
       include:
         - "repository_usage"
     - interval: "1h"    # Instead of 10 minutes
       include:
         - "user_activity"
         - "detailed_analytics"
   ```
2. Remove high-impact collection types temporarily:
   - `detailed_analytics` is the most resource-intensive
   - `user_activity` can be high-impact on very active clusters

#### 4. Network Connectivity Issues

**Symptoms:** Customer sees "timeout" or "connection refused" errors

**Solutions:**
1. Check if customer's Kubernetes cluster has outbound internet access
2. Verify firewall rules allow HTTPS traffic to `ops.oem-telemetry.logscale.us-2.crowdstrike.com`
3. Test connectivity from inside the cluster:
   ```bash
   kubectl run -i --tty debug --image=curlimages/curl --rm -- sh
   # From inside the pod:
   curl -v https://ops.oem-telemetry.logscale.us-2.crowdstrike.com/api/v1/ingest/hec
   ```

#### 5. Customer Can't Find Telemetry Resources

**Symptoms:** Customer reports no telemetry collections or exports created

**Solutions:**
1. Verify the customer added the `telemetryConfig` to the HumioCluster spec (not a separate resource)
2. Check that the HumioCluster was successfully updated:
   ```bash
   kubectl get humiocluster -o yaml | grep -A 20 telemetryConfig
   ```
3. Look for controller errors:
   ```bash
   kubectl logs deployment/humio-operator | grep -i telemetry
   ```

#### 6. Customer Reports Sensitive Data Concerns

**Symptoms:** Customer is concerned about sending detailed data externally

**Solutions:**
1. Recommend using the basic telemetry split approach from [telemetry-collection.md](telemetry-collection.md):
   - Send only `license`, `cluster_info`, `ingestion_metrics` externally
   - Keep `repository_usage`, `user_activity`, `detailed_analytics` local
2. Set `sendCollectionErrors: false` to avoid sending error details externally
3. Point to the "Security Considerations" and "Data Sensitivity Guidelines" sections in the main documentation

### Support Escalation

If issues cannot be resolved using this guide:

1. **Gather Information:**
   - Customer's cluster configuration (sanitized)
   - Telemetry collection and export status outputs
   - Humio Operator logs (grep for telemetry-related entries)
   - Customer's LogScale cluster version and configuration

2. **Check Internal Systems:**
   - Verify the customer repository exists and is accessible
   - Check the telemetry view configuration
   - Confirm the ingest token is valid and has proper permissions

3. **Escalate to Engineering:**
   - Create a support ticket with all gathered information
   - Include specific error messages and status outputs
   - Mention this document was followed for initial setup

## Documentation References

- **Customer-Facing Documentation**: [docs/telemetry-collection.md](telemetry-collection.md)
- **API References**:
  - [HumioTelemetryCollection](api/v1alpha1/humiotelemetrycollection_types.go)
  - [HumioTelemetryExport](api/v1alpha1/humiotelemetryexport_types.go)
- **Integration Tests**: [internal/controller/suite/telemetry/](internal/controller/suite/telemetry/)

## Security Notes

- **Token Security**: Ingest tokens should be treated as sensitive credentials
- **Data Classification**: Telemetry data may contain organizational information - handle appropriately
- **Access Control**: Only authorized support personnel should access the OEM telemetry system
- **Retention**: Follow data retention policies for customer telemetry data

## FAQ

### Q: Can customers use their own LogScale instance for telemetry?
**A:** Yes, customers can configure telemetry to send to their own LogScale clusters instead of the CrowdStrike OEM system. They would follow the same configuration steps but use their own HEC endpoint and tokens.

### Q: How much data does telemetry collection generate?
**A:** Typical volumes:
- **Daily collections** (`license`, `cluster_info`, `ingestion_metrics`): < 10KB per day
- **Hourly collections** (`repository_usage`): 1-100KB per hour (depends on repository count)
- **High-frequency collections**: 10KB-1MB per collection interval (depends on cluster activity)

### Q: Can we customize what data is collected?
**A:** Yes, customers can:
- Choose which collection types to include in each interval
- Adjust collection frequencies
- Split collections between local and remote export
- Disable specific collections entirely

### Q: What happens if the customer's token expires or is revoked?
**A:**
- Collections continue but exports fail
- Error messages appear in the export status
- Customer needs to update the secret with a new token
- Historical collection data is preserved

### Q: How do we handle customers with multiple clusters?
**A:**
- Same ingest token can be used across clusters
- Each cluster must have a unique `clusterIdentifier`
- Consider creating separate repositories for very large customers
- Customer can configure different collection schedules per cluster

---

**Document Version**: 1.0
**Last Updated**: 2026-01-07
**Maintainer**: CrowdStrike/LogScale Support Team