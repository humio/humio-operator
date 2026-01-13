#!/bin/bash

# KIND Cluster Setup Script for Testing Humio Telemetry
# This script creates a KIND cluster with all necessary components for testing telemetry functionality
#
# Creates:
# - KIND cluster with Zookeeper/Kafka
# - Humio operator built from current code
# - HumioCluster with telemetry configuration enabled
# - Leaves cluster in running state for local testing

set -e

# Configuration
CLUSTER_NAME="${CLUSTER_NAME:-kind}"
HUMIO_NAMESPACE="logging"  # Dedicated namespace for telemetry testing

# Source the existing functions from hack/functions.sh
PROJECT_ROOT="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )/.."
cd "$PROJECT_ROOT"

source ./hack/functions.sh

# Declare required variables that functions.sh expects
declare -r docker=$(which docker)
declare -r docker_username=${DOCKER_USERNAME:-none}
declare -r docker_password=${DOCKER_PASSWORD:-none}
declare -r dummy_logscale_image=${DUMMY_LOGSCALE_IMAGE:-false}
declare -r use_certmanager=${USE_CERTMANAGER:-true}
declare -r preserve_kind_cluster=${PRESERVE_KIND_CLUSTER:-false}

# Override cluster name for telemetry testing
export KIND_CLUSTER_NAME="${CLUSTER_NAME}"

# Colors for output (disabled for simplicity)
RED=''
GREEN=''
BLUE=''
YELLOW=''
NC=''

print_section() {
    echo -e "${BLUE}==== $1 ====${NC}"
}

print_success() {
    echo -e "${GREEN}✓ $1${NC}"
}

print_warning() {
    echo -e "${YELLOW}⚠ $1${NC}"
}

print_info() {
    echo -e "${BLUE}→ $1${NC}"
}

print_error() {
    echo -e "${RED}✗ $1${NC}"
}

# Check prerequisites
check_telemetry_prerequisites() {
    print_section "Checking Telemetry Test Prerequisites"

    # Check for required environment variables
    if [ -z "${HUMIO_E2E_LICENSE:-}" ]; then
        print_error "HUMIO_E2E_LICENSE environment variable is required but not set"
        echo "Please set the environment variable with a valid LogScale license:"
        echo "export HUMIO_E2E_LICENSE=\"your-license-jwt-here\""
        exit 1
    fi
    print_success "HUMIO_E2E_LICENSE environment variable is set"

    # Use existing functions for tool installation
    install_kind
    install_kubectl
    install_helm

    print_success "All prerequisites ready"
}

# Setup KIND cluster using existing functions
setup_cluster() {
    print_section "Setting up KIND Cluster"

    # Use existing functions from hack/functions.sh
    start_kind_cluster
    preload_container_images
    kubectl_create_dockerhub_secret

    print_success "KIND cluster setup completed"
}

# Install Kafka using existing function
install_kafka_for_telemetry() {
    print_section "Installing Kafka and Zookeeper for Telemetry Testing"

    # Use existing function from hack/functions.sh
    helm_install_zookeeper_and_kafka

    # Wait for pods to be ready
    wait_for_pod humio-cp-zookeeper-0
    wait_for_pod humio-cp-kafka-0

    print_success "Kafka and Zookeeper ready for telemetry testing"
}

# Build and load operator images
build_and_load_operators() {
    print_section "Building and Loading Humio Operators"

    # Build main operator image
    print_info "Building main operator image..."
    IMG=humio/humio-operator:dev make docker-build-operator

    # Verify main operator image was built
    if ! docker images humio/humio-operator:dev --format "table" | grep -q "dev"; then
        print_error "Failed to build main operator image"
        exit 1
    fi

    # Build webhook operator image with the correct name that Helm expects
    print_info "Building webhook operator image..."
    IMG=humio/humio-operator-webhook:dev make docker-build-operator-webhook

    # Verify webhook operator image was built
    if ! docker images humio/humio-operator-webhook:dev --format "table" | grep -q "dev"; then
        print_error "Failed to build webhook operator image"
        exit 1
    fi

    # Load images into KIND cluster
    print_info "Loading operator images into KIND cluster..."
    $kind load docker-image humio/humio-operator:dev --name kind
    $kind load docker-image humio/humio-operator-webhook:dev --name kind

    # Verify images are loaded in KIND
    print_info "Verifying images are available in KIND cluster..."
    if ! $docker exec -i kind-control-plane crictl images | grep -q "humio/humio-operator.*dev"; then
        print_error "Main operator image not found in KIND cluster"
        exit 1
    fi

    if ! $docker exec -i kind-control-plane crictl images | grep -q "humio/humio-operator-webhook.*dev"; then
        print_error "Webhook operator image not found in KIND cluster"
        exit 1
    fi

    print_success "Operator images built and loaded successfully"
}

# Install Humio Operator via Helm Chart
install_humio_operator() {
    print_section "Installing Humio Operator via Helm Chart"

    # Detect cluster architecture
    print_info "Detecting cluster architecture..."
    CLUSTER_ARCH=$(kubectl get nodes -o jsonpath='{.items[0].metadata.labels.kubernetes\.io/arch}')
    print_info "Detected architecture: ${CLUSTER_ARCH}"

    # Create temporary values override file for architecture-specific affinity
    print_info "Creating temporary values override file..."
    TEMP_VALUES_FILE=$(mktemp)
    cat > "${TEMP_VALUES_FILE}" << EOF
operator:
  image:
    repository: humio/humio-operator
    tag: dev
    pullPolicy: IfNotPresent
    pullSecrets: []
  metrics:
    enabled: true
    listen:
      port: 8080
    secure: false
  prometheus:
    serviceMonitor:
      enabled: false  # Disable since we don't have prometheus for telemetry tests
  certmanager: true
  rbac:
    create: true
  resources:
    limits:
      cpu: 250m
      memory: 200Mi
    requests:
      cpu: 250m
      memory: 200Mi
  podAnnotations: {}
  nodeSelector: {}
  tolerations: []
  affinity:
    nodeAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
        nodeSelectorTerms:
        - matchExpressions:
          - key: kubernetes.io/arch
            operator: In
            values:
            - amd64
            - arm64
            - "${CLUSTER_ARCH}"
          - key: kubernetes.io/os
            operator: In
            values:
            - linux

webhook:
  enabled: true
  image:
    repository: humio/humio-webhook-operator
    tag: dev
    pullPolicy: IfNotPresent
  resources:
    limits:
      cpu: 100m
      memory: 128Mi
    requests:
      cpu: 50m
      memory: 64Mi
  podAnnotations: {}
  nodeSelector: {}
  tolerations: []
  affinity:
    nodeAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
        nodeSelectorTerms:
        - matchExpressions:
          - key: kubernetes.io/arch
            operator: In
            values:
            - amd64
            - arm64
            - "${CLUSTER_ARCH}"
          - key: kubernetes.io/os
            operator: In
            values:
            - linux
EOF

    # Install operator using local Helm chart with values override
    print_info "Installing Humio operator via Helm chart..."

    $helm install humio-operator ./charts/humio-operator \
        --namespace ${HUMIO_NAMESPACE} \
        --create-namespace \
        --values "${TEMP_VALUES_FILE}" \
        --wait --timeout=300s

    # Clean up temporary file
    rm -f "${TEMP_VALUES_FILE}"

    print_success "Humio Operator installed via Helm chart"
}

# Create telemetry test cluster
create_telemetry_test_cluster() {
    print_section "Creating Telemetry Test LogScale Cluster"

    # Create telemetry-test namespace if it doesn't exist
    kubectl create namespace "${HUMIO_NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -

    # Create license secret from environment variable in telemetry-test namespace
    kubectl create secret generic logscale-test-license \
        --from-literal=data="${HUMIO_E2E_LICENSE}" \
        --namespace="${HUMIO_NAMESPACE}" \
        --dry-run=client -o yaml | kubectl apply -f -

    # Detect cluster architecture
    print_info "Detecting cluster architecture..."
    CLUSTER_ARCH=$(kubectl get nodes -o jsonpath='{.items[0].metadata.labels.kubernetes\.io/arch}')
    print_info "Detected architecture: ${CLUSTER_ARCH}"

    kubectl apply -f - << EOF
apiVersion: core.humio.com/v1alpha1
kind: HumioCluster
metadata:
  name: logscale-test
  namespace: ${HUMIO_NAMESPACE}
spec:
  image: humio/humio-core:1.210.0
  targetReplicationFactor: 1
  storagePartitionsCount: 12
  digestPartitionsCount: 12

  # Multi-node pool configuration for testing node role-aware telemetry
  nodePools:
    - name: "query-digest"
      spec:
        nodeCount: 1
        environmentVariables:
          - name: NODE_ROLES
            value: "all"
          - name: "ORGANIZATION_MODE"
            value: "single"
          - name: "AUTHENTICATION_METHOD"
            value: "static"
          - name: "STATIC_USERS"
            value: "admin:admin"
          - name: "KAFKA_SERVERS"
            value: "humio-cp-kafka-0.humio-cp-kafka-headless.default:9092"
          - name: "ZOOKEEPER_URL"
            value: "humio-cp-zookeeper-0.humio-cp-zookeeper-headless.default:2181"
          - name: "HUMIO_KAFKA_TOPIC_PREFIX"
            value: "logscale-test"
          - name: "INGEST_QUEUE_INITIAL_REPLICATION_FACTOR"
            value: "1"
          - name: "CHATTER_INITIAL_REPLICATION_FACTOR"
            value: "1"
          - name: "GLOBAL_INITIAL_REPLICATION_FACTOR"
            value: "1"
          # Enable telemetry debugging
          - name: "TELEMETRY_DEBUG"
            value: "true"
        resources:
          requests:
            cpu: "200m"
            memory: 1Gi
          limits:
            cpu: "1000m"
            memory: 2Gi
        # Architecture-specific affinity for query-digest node
        affinity:
          nodeAffinity:
            requiredDuringSchedulingIgnoredDuringExecution:
              nodeSelectorTerms:
              - matchExpressions:
                - key: kubernetes.io/arch
                  operator: In
                  values:
                  - amd64
                  - arm64
                  - "${CLUSTER_ARCH}"
                - key: kubernetes.io/os
                  operator: In
                  values:
                  - linux
        # Use persistent volume claim template for query-capable node
        dataVolumePersistentVolumeClaimSpecTemplate:
          accessModes: ["ReadWriteOnce"]
          resources:
            requests:
              storage: "10Gi"

    - name: "ingest-only"
      spec:
        nodeCount: 1
        environmentVariables:
          - name: NODE_ROLES
            value: "ingestonly"
          - name: "ORGANIZATION_MODE"
            value: "single"
          - name: "AUTHENTICATION_METHOD"
            value: "static"
          - name: "STATIC_USERS"
            value: "admin:admin"
          - name: "KAFKA_SERVERS"
            value: "humio-cp-kafka-0.humio-cp-kafka-headless.default:9092"
          - name: "ZOOKEEPER_URL"
            value: "humio-cp-zookeeper-0.humio-cp-zookeeper-headless.default:2181"
          - name: "HUMIO_KAFKA_TOPIC_PREFIX"
            value: "logscale-test"
          - name: "INGEST_QUEUE_INITIAL_REPLICATION_FACTOR"
            value: "1"
          - name: "CHATTER_INITIAL_REPLICATION_FACTOR"
            value: "1"
          - name: "GLOBAL_INITIAL_REPLICATION_FACTOR"
            value: "1"
          # Enable telemetry debugging
          - name: "TELEMETRY_DEBUG"
            value: "true"
        resources:
          requests:
            cpu: "200m"
            memory: 1Gi
          limits:
            cpu: "1000m"
            memory: 2Gi
        # Architecture-specific affinity for ingest-only node
        affinity:
          nodeAffinity:
            requiredDuringSchedulingIgnoredDuringExecution:
              nodeSelectorTerms:
              - matchExpressions:
                - key: kubernetes.io/arch
                  operator: In
                  values:
                  - amd64
                  - arm64
                  - "${CLUSTER_ARCH}"
                - key: kubernetes.io/os
                  operator: In
                  values:
                  - linux
        # Use persistent volume claim template for ingest-only node
        dataVolumePersistentVolumeClaimSpecTemplate:
          accessModes: ["ReadWriteOnce"]
          resources:
            requests:
              storage: "10Gi"

  # Telemetry configuration enabled
  telemetryConfig:
    clusterIdentifier: "telemetry-test-cluster"
    remoteReport:
      url: "http://logscale-test-ingest-only.${HUMIO_NAMESPACE}.svc.cluster.local:8080/api/v1/ingest/hec"  # Local LogScale HEC endpoint
      token:
        secretKeyRef:
          name: "telemetry-token-secret"
          key: "token"
    collections:
      # GraphQL API collections (frequent for testing)
      - interval: "1m"
        include:
          - "license"
          - "cluster_info"
      # LogScale search query collections (for comprehensive testing)
      - interval: "2m"
        include:
          - "ingestion_metrics"
          - "repository_usage"
      - interval: "3m"
        include:
          - "user_activity"
      - interval: "4m"
        include:
          - "detailed_analytics"
      # Mixed collections for testing hybrid functionality
      - interval: "5m"
        include:
          - "license"
          - "ingestion_metrics"

  # Common environment variables moved to commonEnvironmentVariables
  commonEnvironmentVariables:
    - name: "TELEMETRY_DEBUG"
      value: "true"

  # Disable TLS for simplicity in local testing
  tls:
    enabled: false

  license:
    secretKeyRef:
      name: logscale-test-license
      key: data
EOF

    print_success "Telemetry test LogScale cluster created"
}

# Create telemetry repository and ingest token
create_telemetry_repository() {
    print_section "Creating Telemetry Repository and Ingest Token"

    # Wait for HumioCluster to be ready first
    print_info "Waiting for LogScale cluster to be fully ready..."
    for i in {1..30}; do
        ready_pods=$(kubectl get pods -n ${HUMIO_NAMESPACE} -l app.kubernetes.io/name=humio --field-selector=status.phase=Running --no-headers 2>/dev/null | wc -l || echo "0")
        if [[ "$ready_pods" -gt 0 ]]; then
            print_info "LogScale cluster pods are running"
            break
        fi
        print_info "Waiting for LogScale pods to be ready (attempt $i/30)..."
        sleep 10
    done

    # Create telemetry repository
    print_info "Creating telemetry repository..."
    kubectl apply -f - << EOF
apiVersion: core.humio.com/v1alpha1
kind: HumioRepository
metadata:
  name: telemetry-repo
  namespace: ${HUMIO_NAMESPACE}
spec:
  managedClusterName: "logscale-test"
  name: "telemetry"
  description: "Repository for collecting telemetry data from Humio clusters"
  retention:
    timeInDays: 30
    ingestSizeInGB: 5
    storageSizeInGB: 1
  automaticSearch: true
EOF

    # Wait for repository to be ready
    print_info "Waiting for repository to be ready..."
    for i in {1..30}; do
        repo_state=$(kubectl get humiorepository/telemetry-repo -n ${HUMIO_NAMESPACE} -o jsonpath='{.status.state}' 2>/dev/null || echo "")
        if [[ "$repo_state" == "Exists" ]]; then
            print_info "Telemetry repository is ready"
            break
        fi
        print_info "Waiting for repository (attempt $i/30, current state: $repo_state)..."
        sleep 10
    done

    # Create ingest token for telemetry
    print_info "Creating telemetry ingest token..."
    kubectl apply -f - << EOF
apiVersion: core.humio.com/v1alpha1
kind: HumioIngestToken
metadata:
  name: telemetry-ingest-token
  namespace: ${HUMIO_NAMESPACE}
spec:
  managedClusterName: "logscale-test"
  repositoryName: "telemetry"
  name: "telemetry-token"
  tokenSecretName: "telemetry-token-secret"
EOF

    # Wait for ingest token to be ready
    print_info "Waiting for ingest token to be ready..."
    for i in {1..30}; do
        token_state=$(kubectl get humioingesttoken/telemetry-ingest-token -n ${HUMIO_NAMESPACE} -o jsonpath='{.status.state}' 2>/dev/null || echo "")
        if [[ "$token_state" == "Exists" ]]; then
            print_info "Telemetry ingest token is ready"
            break
        fi
        print_info "Waiting for ingest token (attempt $i/30, current state: $token_state)..."
        sleep 10
    done

    print_success "Telemetry repository and ingest token created"
}

# Wait for resources and show status
show_cluster_status() {
    print_section "Checking Cluster Status"

    echo "Waiting for LogScale cluster to be ready..."
    # Wait for HumioCluster to reach Running state
    for i in {1..60}; do
        state=$(kubectl get humiocluster/logscale-test -n ${HUMIO_NAMESPACE} -o jsonpath='{.status.state}' 2>/dev/null || echo "")
        if [[ "$state" == "Running" ]]; then
            echo "HumioCluster is now Running"
            break
        fi
        echo "Waiting for HumioCluster (attempt $i/60, current state: $state)..."
        sleep 10
    done

    # Wait for LogScale pods to be ready
    echo "Waiting for LogScale pods to be ready..."
    kubectl wait --for=condition=Ready pod -l app.kubernetes.io/name=humio -n ${HUMIO_NAMESPACE} --timeout=300s || true

    echo ""
    echo "=== Cluster Resources ==="
    kubectl get nodes
    echo ""

    echo "=== Humio Operator ==="
    kubectl get pods -n ${HUMIO_NAMESPACE}
    echo ""

    echo "=== LogScale Cluster ==="
    kubectl get humiocluster,humiotelemetrycollection,humiotelemetryexport,pods -n ${HUMIO_NAMESPACE} -l app.kubernetes.io/name=humio
    echo ""

    echo "=== Telemetry Resources ==="
    kubectl get humiotelemetrycollection,humiotelemetryexport -n ${HUMIO_NAMESPACE} || echo "No Telemetry resources found yet"
}

# Cleanup cluster using existing function
cleanup_telemetry_cluster() {
    print_section "Cleaning Up Telemetry Test Cluster"

    read -p "Do you want to delete the KIND cluster? (y/N): " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        # Use existing cleanup function
        cleanup_kind_cluster
        print_success "Telemetry test cluster deleted"
    else
        print_warning "Telemetry test cluster preserved"
    fi
}

# Show usage instructions
show_usage() {
    cat << EOF

${GREEN}🎉 Telemetry Test Cluster Setup Complete!${NC}

${BLUE}Next Steps:${NC}

1. ${YELLOW}Check the telemetry setup:${NC}
   kubectl get humiocluster,humiorepository,humioingesttoken,humiotelemetrycollection,humiotelemetryexport,pods -n ${HUMIO_NAMESPACE}

2. ${YELLOW}Access LogScale UI:${NC}
   kubectl port-forward -n ${HUMIO_NAMESPACE} svc/logscale-test 8080:8080
   Open: http://localhost:8080 (admin/admin)

3. ${YELLOW}Check telemetry data in LogScale:${NC}
   - Login to LogScale UI (admin/admin)
   - Navigate to the "telemetry" repository
   - Search for: sourcetype="humio:telemetry:*"

4. ${YELLOW}Check telemetry logs:${NC}
   kubectl logs -n ${HUMIO_NAMESPACE} -l app.kubernetes.io/name=humio -f

5. ${YELLOW}View Telemetry Collection resource:${NC}
   kubectl get humiotelemetrycollection -n ${HUMIO_NAMESPACE} -o yaml

6. ${YELLOW}View Telemetry Export resource:${NC}
   kubectl get humiotelemetryexport -n ${HUMIO_NAMESPACE} -o yaml

7. ${YELLOW}Monitor telemetry collection:${NC}
   kubectl describe humiotelemetrycollection -n ${HUMIO_NAMESPACE}

8. ${YELLOW}Monitor telemetry export:${NC}
   kubectl describe humiotelemetryexport -n ${HUMIO_NAMESPACE}

9. ${YELLOW}Check operator logs:${NC}
   kubectl logs -n ${HUMIO_NAMESPACE} deployment/humio-operator-controller-manager -f

${BLUE}Telemetry Configuration:${NC}
- ${YELLOW}Cluster Identifier:${NC} telemetry-test-cluster
- ${YELLOW}Collection Intervals:${NC} 1m for GraphQL API (license/cluster_info), 2-5m for search-based
- ${YELLOW}GraphQL API Collections:${NC} license, cluster_info
- ${YELLOW}Search-based Collections:${NC} ingestion_metrics, repository_usage, user_activity, detailed_analytics (LogScale search)
- ${YELLOW}Target Repository:${NC} telemetry (in local LogScale cluster)
- ${YELLOW}HEC Endpoint:${NC} http://logscale-test.${HUMIO_NAMESPACE}.svc.cluster.local:8080/api/v1/ingest/hec
- ${YELLOW}Debug Mode:${NC} Enabled via TELEMETRY_DEBUG=true

${BLUE}Resources Created:${NC}
- ${YELLOW}HumioCluster:${NC} logscale-test (target cluster for telemetry data)
- ${YELLOW}HumioRepository:${NC} telemetry-repo (repository for telemetry data)
- ${YELLOW}HumioIngestToken:${NC} telemetry-ingest-token (ingest token for HEC)
- ${YELLOW}HumioTelemetryCollection:${NC} (created automatically by telemetryConfig)
- ${YELLOW}HumioTelemetryExport:${NC} (created automatically by telemetryConfig)

${BLUE}Testing Commands:${NC}
- kubectl get events -n ${HUMIO_NAMESPACE} --sort-by='.lastTimestamp'
- kubectl logs -n ${HUMIO_NAMESPACE} deployment/humio-operator | grep -i telemetry
- kubectl describe humiocluster logscale-test -n ${HUMIO_NAMESPACE}

${BLUE}Cleanup:${NC}
- Run: ./hack/run-telemetry-test-cluster.sh cleanup

EOF
}

# Main execution
main() {
    case "${1:-}" in
        "cleanup")
            cleanup_telemetry_cluster
            ;;
        "")
            check_telemetry_prerequisites
            setup_cluster
            install_kafka_for_telemetry
            helm_install_cert_manager
            build_and_load_operators
            install_humio_operator
            create_telemetry_test_cluster
            create_telemetry_repository
            show_cluster_status
            show_usage
            ;;
        *)
            echo "Usage: $0 [cleanup]"
            echo ""
            echo "  (no args)  - Set up complete telemetry test environment"
            echo "  cleanup    - Delete the test cluster"
            exit 1
            ;;
    esac
}

# Run main function
main "$@"
