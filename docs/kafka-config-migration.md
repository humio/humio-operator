# Migrating from EXTRA_KAFKA_CONFIGS_FILE to Individual Kafka Environment Variables

## Overview

LogScale 1.173.0 deprecated the `EXTRA_KAFKA_CONFIGS_FILE` environment variable in favor of individual Kafka environment variables. Starting with LogScale 1.225.0, **LogScale will not start** if `EXTRA_KAFKA_CONFIGS_FILE` is present.

The Humio Operator automatically handles this transition to prevent startup failures, but you should migrate your configuration to the new approach.

## Automatic Operator Behavior

The operator automatically manages the deprecation based on your LogScale version:

### LogScale < 1.173.0
- Uses `EXTRA_KAFKA_CONFIGS_FILE` (existing behavior)
- No deprecation warnings

### LogScale 1.173.0 - 1.224.x
- Uses `EXTRA_KAFKA_CONFIGS_FILE` with deprecation warning
- Logs: "EXTRA_KAFKA_CONFIGS_FILE is deprecated, consider migrating..."

### LogScale 1.225.0+
- **Automatically skips** `EXTRA_KAFKA_CONFIGS_FILE` to prevent startup failure
- Logs: "Skipping EXTRA_KAFKA_CONFIGS_FILE for LogScale 1.225.0+ to prevent startup failure"

## Migration Guide

### Step 1: Understand the New Environment Variables

LogScale 1.173.0+ provides individual Kafka environment variables with these prefixes:

- `KAFKA_ADMIN_*` - Admin client configuration
- `KAFKA_CHATTER_CONSUMER_*` - Chatter consumer configuration
- `KAFKA_CHATTER_PRODUCER_*` - Chatter producer configuration
- `KAFKA_GLOBAL_CONSUMER_*` - Global consumer configuration
- `KAFKA_GLOBAL_PRODUCER_*` - Global producer configuration
- `KAFKA_INGEST_QUEUE_CONSUMER_*` - Ingest queue consumer configuration
- `KAFKA_INGEST_QUEUE_PRODUCER_*` - Ingest queue producer configuration
- `KAFKA_COMMON_*` - Configuration applied to all clients (client-specific settings take precedence)

### Step 2: Transform Property Names

Convert Kafka property names using these rules:

1. **Uppercase** the property name
2. **Replace dots (.)** with underscores (_)
3. **Add the appropriate prefix**

**Examples:**
```
bootstrap.servers=kafka:9092         → KAFKA_COMMON_BOOTSTRAP_SERVERS=kafka:9092
request.timeout.ms=30000             → KAFKA_COMMON_REQUEST_TIMEOUT_MS=30000
batch.size=16384                     → KAFKA_GLOBAL_PRODUCER_BATCH_SIZE=16384
auto.offset.reset=earliest           → KAFKA_GLOBAL_CONSUMER_AUTO_OFFSET_RESET=earliest
compression.type=gzip                → KAFKA_GLOBAL_PRODUCER_COMPRESSION_TYPE=gzip
```

### Step 3: Update Your HumioCluster Configuration

#### Before (Deprecated):
```yaml
apiVersion: core.humio.com/v1alpha1
kind: HumioCluster
metadata:
  name: my-humio-cluster
spec:
  # DEPRECATED: Will be ignored in LogScale 1.225.0+
  extraKafkaConfigs: |
    bootstrap.servers=kafka:9092
    request.timeout.ms=30000
    compression.type=gzip
```

#### After (Recommended):
```yaml
apiVersion: core.humio.com/v1alpha1
kind: HumioCluster
metadata:
  name: my-humio-cluster
spec:
  # Use individual environment variables
  commonEnvironmentVariables:
    - name: KAFKA_COMMON_BOOTSTRAP_SERVERS
      value: kafka:9092
    - name: KAFKA_COMMON_REQUEST_TIMEOUT_MS
      value: "30000"
    - name: KAFKA_GLOBAL_PRODUCER_COMPRESSION_TYPE
      value: gzip
```

## Configuration Options

### Using spec.commonEnvironmentVariables (Recommended)
Apply to all node pools in the cluster:

```yaml
spec:
  commonEnvironmentVariables:
    - name: KAFKA_COMMON_BOOTSTRAP_SERVERS
      value: kafka:9092
```

### Using spec.environmentVariables
Apply to the main node pool only:

```yaml
spec:
  environmentVariables:
    - name: KAFKA_COMMON_BOOTSTRAP_SERVERS
      value: kafka:9092
```

### Using spec.nodePools[].environmentVariables
Apply to specific node pools:

```yaml
spec:
  nodePools:
    - name: ingest-nodes
      environmentVariables:
        - name: KAFKA_INGEST_QUEUE_PRODUCER_COMPRESSION_TYPE
          value: gzip
```

## Common Migration Examples

### Basic Kafka Connection
```yaml
# OLD
extraKafkaConfigs: |
  bootstrap.servers=kafka-1:9092,kafka-2:9092,kafka-3:9092
  request.timeout.ms=30000

# NEW
commonEnvironmentVariables:
  - name: KAFKA_COMMON_BOOTSTRAP_SERVERS
    value: kafka-1:9092,kafka-2:9092,kafka-3:9092
  - name: KAFKA_COMMON_REQUEST_TIMEOUT_MS
    value: "30000"
```

### SSL/TLS Configuration
```yaml
# OLD
extraKafkaConfigs: |
  security.protocol=SSL
  ssl.truststore.location=/etc/ssl/certs/truststore.jks
  ssl.truststore.password=changeit
  ssl.keystore.location=/etc/ssl/certs/keystore.jks
  ssl.keystore.password=changeit
  ssl.key.password=changeit

# NEW
commonEnvironmentVariables:
  - name: KAFKA_COMMON_SECURITY_PROTOCOL
    value: SSL
  - name: KAFKA_COMMON_SSL_TRUSTSTORE_LOCATION
    value: /etc/ssl/certs/truststore.jks
  - name: KAFKA_COMMON_SSL_TRUSTSTORE_PASSWORD
    valueFrom:
      secretKeyRef:
        name: kafka-ssl-secrets
        key: truststore-password
  - name: KAFKA_COMMON_SSL_KEYSTORE_LOCATION
    value: /etc/ssl/certs/keystore.jks
  - name: KAFKA_COMMON_SSL_KEYSTORE_PASSWORD
    valueFrom:
      secretKeyRef:
        name: kafka-ssl-secrets
        key: keystore-password
  - name: KAFKA_COMMON_SSL_KEY_PASSWORD
    valueFrom:
      secretKeyRef:
        name: kafka-ssl-secrets
        key: key-password
```

### Performance Tuning with Client-Specific Settings
```yaml
# OLD
extraKafkaConfigs: |
  producer.batch.size=65536
  producer.linger.ms=10
  producer.compression.type=lz4
  consumer.fetch.min.bytes=1024
  consumer.fetch.max.wait.ms=500

# NEW - Using specific client prefixes
commonEnvironmentVariables:
  # Global producer settings
  - name: KAFKA_GLOBAL_PRODUCER_BATCH_SIZE
    value: "65536"
  - name: KAFKA_GLOBAL_PRODUCER_LINGER_MS
    value: "10"
  - name: KAFKA_GLOBAL_PRODUCER_COMPRESSION_TYPE
    value: lz4

  # Ingest queue producer (more specific)
  - name: KAFKA_INGEST_QUEUE_PRODUCER_COMPRESSION_TYPE
    value: gzip  # Overrides global setting for ingest queue

  # Global consumer settings
  - name: KAFKA_GLOBAL_CONSUMER_FETCH_MIN_BYTES
    value: "1024"
  - name: KAFKA_GLOBAL_CONSUMER_FETCH_MAX_WAIT_MS
    value: "500"
```

## Validation

### Check Operator Logs
Monitor the operator logs to see deprecation warnings:

```bash
kubectl logs -l app.kubernetes.io/name=humio-operator -n humio-operator-system
```

Look for messages like:
- `"EXTRA_KAFKA_CONFIGS_FILE is deprecated, consider migrating..."`
- `"Skipping EXTRA_KAFKA_CONFIGS_FILE for LogScale 1.225.0+ to prevent startup failure"`

### Verify Environment Variables
Check that your environment variables are properly set in the pods:

```bash
kubectl exec -it <pod-name> -- env | grep KAFKA_
```

### Test Your Configuration
After migrating, verify that:
1. LogScale starts successfully
2. Kafka connectivity works as expected
3. Performance characteristics are maintained
4. Security settings are properly applied

## Troubleshooting

### LogScale Won't Start After Upgrade to 1.225.0+
**Problem**: LogScale fails to start after upgrading to 1.225.0+
**Solution**: The operator should automatically prevent this by skipping `EXTRA_KAFKA_CONFIGS_FILE`. Check operator logs for any errors.

### Missing Kafka Configuration
**Problem**: Kafka settings aren't being applied
**Solution**: Verify environment variable names follow the transformation rules (uppercase, dots to underscores, proper prefix).

### Configuration Precedence Issues
**Problem**: Settings aren't taking effect as expected
**Solution**: Remember that client-specific settings (e.g., `KAFKA_INGEST_QUEUE_PRODUCER_*`) take precedence over common settings (`KAFKA_COMMON_*`).

### SSL/TLS Connection Issues
**Problem**: SSL connections failing after migration
**Solution**: Ensure certificate paths are correct and passwords are properly referenced from secrets.

## Understanding Client Types

When choosing the right prefix, understand what each client type handles:

- **KAFKA_COMMON_**: Applied to all Kafka clients
- **KAFKA_ADMIN_**: Administrative operations (topic creation, etc.)
- **KAFKA_CHATTER_CONSUMER_/KAFKA_CHATTER_PRODUCER_**: Internal cluster communication
- **KAFKA_GLOBAL_CONSUMER_/KAFKA_GLOBAL_PRODUCER_**: General data processing
- **KAFKA_INGEST_QUEUE_CONSUMER_/KAFKA_INGEST_QUEUE_PRODUCER_**: Data ingestion pipelines

For most configurations, start with `KAFKA_COMMON_` and use more specific prefixes only when you need different settings for different client types.

## Support

For additional help:
1. Check the [LogScale documentation](https://library.humio.com/) for Kafka configuration details
2. Review the [Humio Operator documentation](https://docs.humio.com/docs/humio-operator/)
3. File issues at [github.com/humio/humio-operator](https://github.com/humio/humio-operator)