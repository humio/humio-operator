# Telemetry Testing Guide

This guide provides comprehensive instructions for testing the HumioTelemetry functionality using the provided test scripts.

## Overview

The Humio Operator includes telemetry collection capabilities that automatically gather usage and cluster information from HumioCluster instances. This data helps improve the product and provides insights into cluster usage patterns.

Two main testing scripts are available:
- **Interactive testing**: `hack/run-telemetry-test-cluster.sh` - Creates a persistent test cluster for manual testing
- **Automated testing**: `hack/run-telemetry-integration-using-kind.sh` - Runs automated integration tests

## Prerequisites

Both testing approaches require the following:

### Environment Variables
```bash
# Required: Valid LogScale license for testing
export HUMIO_E2E_LICENSE="your-license-here"

# Optional: Custom cluster name (defaults to "logscale-test")
export CLUSTER_NAME="my-test-cluster"
```

### System Requirements
- Docker installed and running
- At least 4GB available RAM (KIND + Kafka/Zookeeper is resource-intensive)
- Available ports 8080 (LogScale UI) and other Kubernetes service ports

### Tools
The scripts will automatically install required tools if missing:
- `kind` (Kubernetes in Docker)
- `kubectl` (Kubernetes CLI)
- `helm` (Kubernetes package manager)

## Interactive Testing Setup

Use this approach for manual testing, development, and debugging telemetry functionality.

### 1. Start Test Cluster

```bash
# Navigate to the project root
cd /path/to/humio-operator

# Start the telemetry test cluster
./hack/run-telemetry-test-cluster.sh
```

This script performs the following operations:
1. Validates prerequisites (`HUMIO_E2E_LICENSE` environment variable)
2. Installs required tools (kind, kubectl, helm)
3. Creates a KIND cluster named `logscale-test`
4. Installs Kafka and Zookeeper for LogScale messaging
5. Builds and loads operator Docker images
6. Installs the Humio Operator via Helm
7. Creates a test HumioCluster with telemetry enabled
8. Sets up telemetry data collection infrastructure

### 2. Access the Cluster

After successful setup, the script provides access instructions:

```bash
# Access LogScale UI (runs in foreground)
kubectl port-forward -n logging svc/logscale-test 8080:8080

# Then open: http://localhost:8080
# Default credentials: admin/admin
```

### 3. Monitor Telemetry Data

#### View Collected Telemetry
1. Access LogScale UI at http://localhost:8080
2. Navigate to the "telemetry" repository
3. Search for telemetry data: `sourcetype="humio:telemetry:*"`

#### Monitor Telemetry Resources
```bash
# View HumioTelemetry resource status
kubectl get humiotelemetry -n logging -o yaml

# Check telemetry collection logs
kubectl logs -n logging deployment/humio-operator-controller-manager -f

# View all telemetry-related resources
kubectl get humiotelemetry,humiorepository,humioingesttoken -n logging
```

### 4. Test Configuration

The test cluster is configured with:
- **Telemetry Collection Intervals**:
  - License and cluster info: Every 1 minute
  - All license data: Every 5 minutes
- **Data Types Collected**:
  - `license`: License usage and status
  - `cluster_info`: Cluster configuration and health
  - `user_info`: User activity metrics
  - `repository_info`: Repository usage statistics
- **Debug Mode**: Enabled via `TELEMETRY_DEBUG=true`

### 5. Cleanup

When finished testing:

```bash
# Clean up the test cluster
./hack/run-telemetry-test-cluster.sh cleanup

# Confirm deletion when prompted
```

## Automated Integration Testing

Use this approach for CI/CD pipelines and automated validation of telemetry functionality.

### Main E2E Test Suite (Includes Telemetry)

The telemetry tests are included in the main E2E test suite and run automatically:

```bash
# Run all E2E tests including telemetry (recommended)
make run-e2e-tests-local-kind
```

This approach:
- Runs the complete test suite with all controller types
- Includes telemetry tests as part of the broader validation
- Uses the same infrastructure as other E2E tests
- Is the standard way to validate the operator

### Telemetry-Specific Integration Tests

For focused telemetry testing without the full E2E infrastructure:

```bash
# Navigate to the project root
cd /path/to/humio-operator

# Run automated telemetry integration tests
./hack/run-telemetry-integration-using-kind.sh
```

This script:
1. Creates a minimal KIND cluster (no Kafka/Zookeeper needed)
2. Installs fresh CRDs using server-side apply
3. Runs Ginkgo integration tests with strict error handling
4. Automatically cleans up resources on completion/failure

### Unit Tests (Telemetry Controller Only)

For fast unit testing of telemetry controller logic:

```bash
# Run telemetry controller unit tests using envtest
make run-telemetry-tests
```

This approach:
- Uses envtest (lightweight Kubernetes API server)
- Focuses on controller logic and CRD validation
- Fastest testing option (no cluster creation needed)
- Runs tests in `./internal/controller/suite/telemetry/...`

### Test Configuration

The integration tests:
- Run with 1 parallel process (`GINKGO_NODES=1`)
- Use label filter `real` to select appropriate tests
- Include 10-minute timeout for comprehensive testing
- Focus on controller logic and CRD validation
- Located in: `./internal/controller/suite/telemetry/...`

### CI/CD Integration

For GitHub Actions or other CI systems:

```bash
# Set environment variables for CI
export GITHUB_WORKFLOW_ID="your-workflow-id"
export GITHUB_RUN_ID="your-run-id"
export GITHUB_RUN_ATTEMPT="1"

# Optional: Docker registry credentials
export DOCKER_USERNAME="your-username"
export DOCKER_PASSWORD="your-password"

# Run tests
./hack/run-telemetry-integration-using-kind.sh
```

## Telemetry Data Types

The HumioTelemetry controller collects the following data types:

### License Information (`license`)
- License usage statistics
- Feature availability
- Expiration information
- Compliance status

### Cluster Information (`cluster_info`)
- Node count and configuration
- Resource utilization
- Health status
- Version information

### User Information (`user_info`)
- Active user counts
- Authentication methods
- User activity patterns
- Role distributions

### Repository Information (`repository_info`)
- Repository count and sizes
- Retention policies
- Ingestion rates
- Storage utilization

## Troubleshooting

### Common Issues

#### License Not Set
```
Error: HUMIO_E2E_LICENSE environment variable is required
```
**Solution**: Export a valid LogScale license:
```bash
export HUMIO_E2E_LICENSE="your-license-here"
```

#### Insufficient Resources
```
Error: KIND cluster creation failed - insufficient memory
```
**Solution**:
- Close other applications to free memory
- Increase Docker Desktop memory allocation
- Use integration tests instead of full cluster setup

#### Port Conflicts
```
Error: Port 8080 already in use
```
**Solution**:
- Stop other services using port 8080
- Use a different port: `kubectl port-forward -n logging svc/logscale-test 8081:8080`

#### CRD Conflicts
```
Error: CustomResourceDefinition already exists
```
**Solution**: Clean up existing CRDs:
```bash
kubectl delete crd humiotelemetries.core.humio.com
./hack/run-telemetry-test-cluster.sh
```

### Debug Information

#### View Telemetry Controller Logs
```bash
# Follow controller manager logs
kubectl logs -n logging deployment/humio-operator-controller-manager -f

# View specific pod logs
kubectl get pods -n logging
kubectl logs -n logging <controller-pod-name> -c manager
```

#### Check Resource Status
```bash
# Detailed telemetry resource status
kubectl describe humiotelemetry -n logging

# Check telemetry collection errors
kubectl get humiotelemetry -n logging -o jsonpath='{.items[0].status.exportErrors}'

# View collection timestamps
kubectl get humiotelemetry -n logging -o jsonpath='{.items[0].status.lastCollectionTime}'
```

#### Verify Configuration
```bash
# Check HumioCluster telemetry configuration
kubectl get humiocluster -n logging -o yaml | grep -A 10 telemetry

# Validate telemetry repository setup
kubectl get humiorepository telemetry -n logging -o yaml

# Check ingest token
kubectl get humioingesttoken telemetry-token -n logging -o yaml
```

## Development and Customization

### Modifying Test Configuration

To customize the test setup, edit the configuration in `hack/run-telemetry-test-cluster.sh`:

```bash
# Change collection intervals
COLLECTION_INTERVAL="2m"  # Default: 1m for license/cluster_info, 5m for license

# Modify data types collected
DATA_TYPES=("license" "cluster_info")  # Remove user_info, repository_info

# Adjust cluster resources
CLUSTER_CPU="500m"      # Default: 1000m
CLUSTER_MEMORY="1Gi"    # Default: 2Gi
```

### Adding Custom Tests

Create additional integration tests in `internal/controller/suite/telemetry/`:

```go
// Example: custom_telemetry_test.go
var _ = Describe("Custom Telemetry Collection", func() {
    It("Should collect custom metrics", func() {
        // Test implementation
    })
})
```

### Extending Telemetry Data Types

To add new telemetry data types:

1. Update the valid data types in `humiotelemetry_controller.go`:
```go
validTypes := map[string]bool{
    "license":         true,
    "cluster_info":    true,
    "user_info":       true,
    "repository_info": true,
    "custom_metrics":  true,  // Add new type
}
```

2. Implement collection logic in the Humio client
3. Add tests for the new data type
4. Update documentation

## Phase 2 Development (Upcoming)

The telemetry system is planned for expansion in Phase 2, which will include:

- **Search Query Execution**: REST API-based query execution for advanced metrics
- **Ingestion Metrics**: Daily, weekly, and monthly ingestion volume tracking
- **Repository-Specific Analytics**: Per-repository usage and growth statistics
- **Enhanced User Activity**: Detailed user interaction and query patterns

For implementation details, see `telemetry-tech-arch-questions.txt`.

## References

- [Humio Operator Documentation](https://library.humio.com/humio-operator/)
- [KIND Documentation](https://kind.sigs.k8s.io/)
- [Kubernetes Custom Resources](https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/custom-resources/)
- [Controller Runtime](https://github.com/kubernetes-sigs/controller-runtime)