# HumioScheduledSearch Migration Guide: v1alpha1 to v1beta1

## Overview

This guide helps you migrate from HumioScheduledSearch v1alpha1 to v1beta1. The v1beta1 API provides improved validation, better field naming, and support for Humio's V2 scheduled search APIs.

## Key Changes

### API Version
- **Before (v1alpha1)**: `apiVersion: core.humio.com/v1alpha1`
- **After (v1beta1)**: `apiVersion: core.humio.com/v1beta1`

### Field Changes

| v1alpha1 Field | v1beta1 Field | Description |
|---|---|---|
| `queryStart` (string) | `searchIntervalSeconds` (int64) | Time interval converted to seconds |
| `queryEnd` (string) | `searchIntervalOffsetSeconds` (*int64) | Offset converted to seconds, optional |
| `backfillLimit` (int) | `backfillLimit` (*int) | Now optional pointer |
| N/A | `queryTimestampType` (enum) | **Required**: `EventTimestamp` or `IngestTimestamp` |
| N/A | `maxWaitTimeSeconds` (int64) | Optional, for `IngestTimestamp` type only |

### New Validation Rules

v1beta1 includes comprehensive validation that prevents common configuration errors:

1. **Mutual exclusion**: Must specify exactly one of `managedClusterName` or `externalClusterName`
2. **Conditional requirements**:
   - `queryTimestampType: EventTimestamp` requires `backfillLimit ≥ 0` and `searchIntervalOffsetSeconds ≥ 0`
   - `queryTimestampType: IngestTimestamp` requires `maxWaitTimeSeconds ≥ 0`
3. **Format validation**: Cron expressions and timezone formats are validated
4. **Immutable fields**: The `name` field cannot be changed after creation

## Migration Strategies

### Strategy 1: Automatic Conversion (Recommended)

The operator automatically converts v1alpha1 resources to v1beta1 when you upgrade. No manual intervention required.

**Steps:**
1. Upgrade the humio-operator to the version supporting v1beta1
2. Your existing v1alpha1 resources continue to work
3. The operator stores them internally as v1beta1
4. You can read them using either API version

**Example:**
```bash
# Your existing v1alpha1 resource continues to work
kubectl get humioscheduledsearches.v1alpha1.core.humio.com my-search -o yaml

# But it's also available as v1beta1
kubectl get humioscheduledsearches.v1beta1.core.humio.com my-search -o yaml
```

### Strategy 2: Manual Migration

For better control and to adopt new v1beta1 features, manually migrate your resources.

#### Step 1: Export Existing Resource
```bash
kubectl get humioscheduledsearches.v1alpha1.core.humio.com my-search -o yaml > my-search-v1alpha1.yaml
```

#### Step 2: Convert to v1beta1 Format

**Before (v1alpha1):**
```yaml
apiVersion: core.humio.com/v1alpha1
kind: HumioScheduledSearch
metadata:
  name: my-search
spec:
  managedClusterName: my-cluster
  name: my-search
  viewName: my-view
  queryString: "#repo = humio | error = true"
  queryStart: "1h"           # String-based time
  queryEnd: "now"            # String-based time
  schedule: "0 * * * *"
  timeZone: "UTC"
  backfillLimit: 3           # Required int
  enabled: true
  actions: ["my-action"]
```

**After (v1beta1):**
```yaml
apiVersion: core.humio.com/v1beta1
kind: HumioScheduledSearch
metadata:
  name: my-search
spec:
  managedClusterName: my-cluster
  name: my-search
  viewName: my-view
  queryString: "#repo = humio | error = true"
  searchIntervalSeconds: 3600        # 1h = 3600 seconds
  searchIntervalOffsetSeconds: 0     # "now" = 0 seconds offset
  queryTimestampType: EventTimestamp # Required new field
  schedule: "0 * * * *"
  timeZone: "UTC"
  backfillLimit: 3                   # Optional (but recommended for EventTimestamp)
  enabled: true
  actions: ["my-action"]
```

#### Step 3: Apply New Resource
```bash
kubectl apply -f my-search-v1beta1.yaml
```

## Time Format Conversion Reference

### String to Seconds Conversion

| v1alpha1 String | v1beta1 Seconds | Description |
|---|---|---|
| `"now"` | `0` | Current time |
| `"30s"` | `30` | 30 seconds |
| `"5m"` | `300` | 5 minutes |
| `"1h"` | `3600` | 1 hour |
| `"2h"` | `7200` | 2 hours |
| `"1d"` | `86400` | 1 day |
| `"1w"` | `604800` | 1 week |
| `"1y"` | `31536000` | 1 year |

### Supported Time Units
- **Seconds**: `s`, `sec`, `second`, `seconds`
- **Minutes**: `m`, `min`, `minute`, `minutes`  
- **Hours**: `h`, `hour`, `hours`
- **Days**: `d`, `day`, `days`
- **Weeks**: `w`, `week`, `weeks`
- **Years**: `y`, `year`, `years`

## Configuration Examples

### Basic Event-based Search
```yaml
apiVersion: core.humio.com/v1beta1
kind: HumioScheduledSearch
metadata:
  name: error-monitor
spec:
  managedClusterName: production-cluster
  name: error-monitor
  viewName: application-logs
  queryString: "level = ERROR"
  searchIntervalSeconds: 3600        # Search last 1 hour
  searchIntervalOffsetSeconds: 0     # Up to now
  queryTimestampType: EventTimestamp # Use @timestamp
  schedule: "0 * * * *"              # Every hour
  timeZone: "UTC"
  backfillLimit: 24                  # Backfill up to 24 missed searches
  enabled: true
  actions: ["alert-email"]
```

### Ingest-based Search with Wait Time
```yaml
apiVersion: core.humio.com/v1beta1
kind: HumioScheduledSearch
metadata:
  name: realtime-monitor  
spec:
  managedClusterName: production-cluster
  name: realtime-monitor
  viewName: live-data
  queryString: "status = CRITICAL"
  searchIntervalSeconds: 300         # Search last 5 minutes
  queryTimestampType: IngestTimestamp # Use @ingesttimestamp
  maxWaitTimeSeconds: 60             # Wait up to 60s for data
  schedule: "*/5 * * * *"            # Every 5 minutes
  timeZone: "UTC"
  enabled: true
  actions: ["immediate-alert"]
```

### Complex Time Offset Example
```yaml
apiVersion: core.humio.com/v1beta1
kind: HumioScheduledSearch
metadata:
  name: daily-report
spec:
  managedClusterName: production-cluster
  name: daily-report
  viewName: business-metrics
  queryString: "metric = daily_revenue"
  searchIntervalSeconds: 86400       # Last 24 hours (1d)
  searchIntervalOffsetSeconds: 3600  # Excluding last 1 hour (1h offset)
  queryTimestampType: EventTimestamp
  schedule: "0 9 * * *"              # 9 AM daily
  timeZone: "UTC-08"                 # Pacific Time
  backfillLimit: 5                   # Backfill up to 5 days
  enabled: true
  actions: ["daily-report-email"]
```

## Validation and Troubleshooting

### Common Validation Errors

#### 1. Missing QueryTimestampType
```
error validating data: ValidationError(HumioScheduledSearch.spec): 
missing required field "queryTimestampType"
```
**Solution:** Add `queryTimestampType: EventTimestamp` or `queryTimestampType: IngestTimestamp`

#### 2. Conflicting Cluster References
```
error: Must specify exactly one of managedClusterName or externalClusterName
```
**Solution:** Specify only one cluster reference field

#### 3. Missing Required Fields for TimestampType
```
error: backfillLimit is required when QueryTimestampType is EventTimestamp
```
**Solution:** Add `backfillLimit: 0` (or desired value) for EventTimestamp searches

#### 4. Invalid Time Format
```
error: searchIntervalSeconds must be greater than 0
```
**Solution:** Ensure time values are positive integers in seconds

### Testing Your Migration

#### 1. Validate Conversion
```bash
# Create a test resource
kubectl apply -f test-search-v1beta1.yaml

# Verify it can be read as both versions
kubectl get humioscheduledsearches.v1alpha1.core.humio.com test-search -o yaml
kubectl get humioscheduledsearches.v1beta1.core.humio.com test-search -o yaml
```

#### 2. Check Resource Status
```bash
kubectl describe humioscheduledsearches.v1beta1.core.humio.com my-search
```

Look for:
- `Status.State: Exists` (successful creation in Humio)
- No validation errors in events
- Correct field mapping in status

#### 3. Verify in Humio UI
1. Log into your Humio instance
2. Navigate to your repository/view
3. Check "Scheduled Searches" section
4. Verify the search appears with correct configuration

## Rollback Strategy

If you need to rollback:

### Option 1: Use v1alpha1 API
Your resources remain accessible via v1alpha1 API even after migration:
```bash
kubectl get humioscheduledsearches.v1alpha1.core.humio.com
```

### Option 2: Recreate as v1alpha1
If you manually migrated and need to rollback:
```bash
# Delete v1beta1 resource
kubectl delete humioscheduledsearches.v1beta1.core.humio.com my-search

# Restore from backup
kubectl apply -f my-search-v1alpha1-backup.yaml
```

## Best Practices

### 1. Gradual Migration
- Start with non-critical searches
- Test thoroughly in staging environment
- Migrate production resources during maintenance windows

### 2. Backup Strategy
```bash
# Backup all v1alpha1 resources before migration
kubectl get humioscheduledsearches.v1alpha1.core.humio.com -o yaml > hss-backup.yaml
```

### 3. Monitoring
- Watch for deprecation warnings in operator logs
- Monitor scheduled search execution after migration  
- Set up alerts for validation errors

### 4. Documentation Updates
- Update your infrastructure-as-code templates
- Update documentation to use v1beta1 examples
- Train team members on new field names

## FAQ

**Q: When will v1alpha1 be removed?**
A: v1alpha1 is deprecated in LogScale 1.180.0 and will be removed in 1.231.0. Plan your migration accordingly.

**Q: Can I mix v1alpha1 and v1beta1 resources?**
A: Yes, during the transition period you can have both versions. The operator handles conversion automatically.

**Q: Will my existing searches stop working?**
A: No, existing searches continue to work unchanged. The operator automatically converts them internally.

**Q: Do I need to update my monitoring/alerting?**
A: You may want to update resource selectors to use v1beta1, but it's not required immediately.

**Q: What happens to custom fields I added?**
A: Custom fields in annotations and labels are preserved during conversion.

## Support

For additional help:
- Check operator logs: `kubectl logs -n humio-system deployment/humio-operator`
- Review validation errors: `kubectl describe humioscheduledsearches.v1beta1.core.humio.com <name>`
- Consult the [Humio Operator documentation](https://github.com/humio/humio-operator)
- Open issues on [GitHub](https://github.com/humio/humio-operator/issues)