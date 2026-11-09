#!/bin/bash

# KIND Cluster Setup Script for Testing Humio Bootstrap Token Hashing
# This script creates a KIND cluster with all necessary components for testing bootstrap token functionality
#
# Creates:
# - KIND cluster with Zookeeper/Kafka
# - Humio operator built from current code
# - Custom bootstrap token secret (without hashedToken)
# - HumioBootstrapToken resource (referencing only the plain token)
# - HumioCluster that uses the custom bootstrap token
# - Leaves cluster in running state for local testing

set -e

# Configuration
CLUSTER_NAME="${CLUSTER_NAME:-kind}"
HUMIO_NAMESPACE="logging"  # Dedicated namespace for bootstrap token testing

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

# Override cluster name for bootstrap token testing
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
check_bootstrap_prerequisites() {
    print_section "Checking Bootstrap Token Test Prerequisites"

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
install_kafka_for_bootstrap_test() {
    print_section "Installing Kafka and Zookeeper for Bootstrap Token Testing"

    # Use existing function from hack/functions.sh
    helm_install_zookeeper_and_kafka

    # Wait for pods to be ready
    wait_for_pod humio-cp-zookeeper-0
    wait_for_pod humio-cp-kafka-0

    print_success "Kafka and Zookeeper ready for bootstrap token testing"
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
      enabled: false  # Disable since we don't have prometheus for bootstrap token tests
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

# Create custom bootstrap token secret (only containing plain token, no hashedToken)
create_custom_bootstrap_token_secret() {
    print_section "Creating Custom Bootstrap Token Secret"

    # Create namespace if it doesn't exist
    kubectl create namespace "${HUMIO_NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -

    # Generate a random bootstrap token (base64 encoded 32 bytes)
    print_info "Generating random bootstrap token..."
    BOOTSTRAP_TOKEN=$(openssl rand -base64 32)
    print_info "Generated bootstrap token: ${BOOTSTRAP_TOKEN}"

    # Create secret with only the plain token (no hashedToken)
    kubectl apply -f - << EOF
apiVersion: v1
kind: Secret
metadata:
  name: logscale-test-bootstrap-token-only-secret
  namespace: ${HUMIO_NAMESPACE}
  labels:
    app.kubernetes.io/instance: logscale-test
    app.kubernetes.io/managed-by: humio-operator
    app.kubernetes.io/name: humio
    humio.com/secret-identifier: logscale-test-bootstrap-token
type: Opaque
data:
  secret: $(echo -n "${BOOTSTRAP_TOKEN}" | base64)
EOF

    print_success "Custom bootstrap token secret created (plain token only)"
    print_info "Secret name: logscale-test-bootstrap-token-only-secret"
    print_info "Token field: secret (base64 encoded)"
}

# Create custom HumioBootstrapToken resource
create_custom_humio_bootstrap_token() {
    print_section "Creating Custom HumioBootstrapToken Resource"

    kubectl apply -f - << EOF
apiVersion: core.humio.com/v1alpha1
kind: HumioBootstrapToken
metadata:
  name: logscale-test
  namespace: ${HUMIO_NAMESPACE}
  labels:
    app.kubernetes.io/instance: logscale-test
    app.kubernetes.io/managed-by: humio-operator
    app.kubernetes.io/name: humio
    managed-cluster-name: logscale-test
spec:
  managedClusterName: logscale-test
  tokenSecret:
    secretKeyRef:
      key: secret
      name: logscale-test-bootstrap-token-only-secret
  hashedTokenSecret:
    secretKeyRef:
      key: hashedToken
      name: logscale-test-bootstrap-token-only-secret
EOF

    print_success "Custom HumioBootstrapToken resource created"
    print_info "Resource name: logscale-test"
    print_info "References: logscale-test-bootstrap-token-only-secret (both secret and hashedToken)"
}

# Create bootstrap token test cluster
create_bootstrap_test_cluster() {
    print_section "Creating Bootstrap Token Test LogScale Cluster"

    # Create license secret from environment variable
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

  # Multi-node pool configuration for testing bootstrap token functionality
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

  # Disable TLS for simplicity in local testing
  tls:
    enabled: false

  license:
    secretKeyRef:
      name: logscale-test-license
      key: data
EOF

    print_success "Bootstrap token test LogScale cluster created"
}

# Wait for resources and show status
show_cluster_status() {
    print_section "Checking Cluster Status"

    echo "Waiting for HumioBootstrapToken to be processed..."
    # Wait for HumioBootstrapToken to reach Ready state
    for i in {1..60}; do
        bootstrap_state=$(kubectl get humiobootstraptoken/logscale-test -n ${HUMIO_NAMESPACE} -o jsonpath='{.status.state}' 2>/dev/null || echo "")
        if [[ "$bootstrap_state" == "Ready" ]]; then
            echo "HumioBootstrapToken is now Ready"
            break
        fi
        echo "Waiting for HumioBootstrapToken (attempt $i/60, current state: $bootstrap_state)..."
        sleep 5
    done

    echo "Waiting for LogScale cluster to be ready..."
    # Wait for HumioCluster to reach Running state
    for i in {1..60}; do
        cluster_state=$(kubectl get humiocluster/logscale-test -n ${HUMIO_NAMESPACE} -o jsonpath='{.status.state}' 2>/dev/null || echo "")
        if [[ "$cluster_state" == "Running" ]]; then
            echo "HumioCluster is now Running"
            break
        fi
        echo "Waiting for HumioCluster (attempt $i/60, current state: $cluster_state)..."
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

    echo "=== Bootstrap Token Resources ==="
    kubectl get humiobootstraptoken,secret -n ${HUMIO_NAMESPACE} -l app.kubernetes.io/name=humio
    echo ""

    echo "=== LogScale Cluster ==="
    kubectl get humiocluster,pods -n ${HUMIO_NAMESPACE} -l app.kubernetes.io/name=humio
    echo ""

    echo "=== Bootstrap Token Secret Contents ==="
    echo "Secret keys:"
    kubectl get secret logscale-test-bootstrap-token-only-secret -n ${HUMIO_NAMESPACE} -o jsonpath='{.data}' | jq -r 'keys[]' 2>/dev/null || echo "Error reading secret keys"
    echo ""
    if kubectl get secret logscale-test-bootstrap-token-only-secret -n ${HUMIO_NAMESPACE} -o jsonpath='{.data.hashedToken}' >/dev/null 2>&1; then
        echo "✓ hashedToken field was successfully added by operator"
    else
        echo "✗ hashedToken field not found - operator hashing may have failed"
    fi
}

# Cleanup cluster using existing function
cleanup_bootstrap_cluster() {
    print_section "Cleaning Up Bootstrap Token Test Cluster"

    read -p "Do you want to delete the KIND cluster? (y/N): " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        # Use existing cleanup function
        cleanup_kind_cluster
        print_success "Bootstrap token test cluster deleted"
    else
        print_warning "Bootstrap token test cluster preserved"
    fi
}

# Show usage instructions
show_usage() {
    cat << EOF

${GREEN}🎉 Bootstrap Token Test Cluster Setup Complete!${NC}

${BLUE}Next Steps:${NC}

1. ${YELLOW}Check the bootstrap token setup:${NC}
   kubectl get humiobootstraptoken,humiocluster,secret -n ${HUMIO_NAMESPACE}

2. ${YELLOW}Verify operator hashing worked:${NC}
   kubectl get secret logscale-test-bootstrap-token-only-secret -n ${HUMIO_NAMESPACE} -o jsonpath='{.data}' | jq

3. ${YELLOW}Check bootstrap token status:${NC}
   kubectl describe humiobootstraptoken logscale-test -n ${HUMIO_NAMESPACE}

4. ${YELLOW}Access LogScale UI:${NC}
   kubectl port-forward -n ${HUMIO_NAMESPACE} svc/logscale-test 8080:8080
   Open: http://localhost:8080 (admin/admin)

5. ${YELLOW}Check bootstrap token hashing logs:${NC}
   kubectl logs -n ${HUMIO_NAMESPACE} deployment/humio-operator-controller-manager -f | grep -i bootstrap

6. ${YELLOW}Monitor operator events:${NC}
   kubectl get events -n ${HUMIO_NAMESPACE} --sort-by='.lastTimestamp' | grep -i bootstrap

7. ${YELLOW}Test the token hashing fix:${NC}
   # Remove hashedToken to re-trigger hashing:
   kubectl patch secret logscale-test-bootstrap-token-only-secret -n ${HUMIO_NAMESPACE} --type=json -p='[{"op": "remove", "path": "/data/hashedToken"}]'
   
   # Watch the bootstrap token reconciler work:
   kubectl get humiobootstraptoken logscale-test -n ${HUMIO_NAMESPACE} -w

${BLUE}Bootstrap Token Configuration:${NC}
- ${YELLOW}Bootstrap Token Resource:${NC} logscale-test
- ${YELLOW}Secret Name:${NC} logscale-test-bootstrap-token-only-secret
- ${YELLOW}Plain Token Field:${NC} secret (user-provided)
- ${YELLOW}Hashed Token Field:${NC} hashedToken (operator-generated)
- ${YELLOW}Cluster Reference:${NC} logscale-test

${BLUE}Resources Created:${NC}
- ${YELLOW}Secret:${NC} logscale-test-bootstrap-token-only-secret (custom with plain token)
- ${YELLOW}HumioBootstrapToken:${NC} logscale-test (references custom secret)
- ${YELLOW}HumioCluster:${NC} logscale-test (uses custom bootstrap token)

${BLUE}Testing Commands:${NC}
- kubectl get events -n ${HUMIO_NAMESPACE} --sort-by='.lastTimestamp' | grep bootstrap
- kubectl logs -n ${HUMIO_NAMESPACE} deployment/humio-operator-controller-manager | grep -i bootstrap
- kubectl describe humiobootstraptoken logscale-test -n ${HUMIO_NAMESPACE}
- kubectl describe humiocluster logscale-test -n ${HUMIO_NAMESPACE}

${BLUE}Cleanup:${NC}
- Run: ./hack/run-bootstrap-token-test.sh cleanup

EOF
}

# Main execution
main() {
    case "${1:-}" in
        "cleanup")
            cleanup_bootstrap_cluster
            ;;
        "")
            check_bootstrap_prerequisites
            setup_cluster
            install_kafka_for_bootstrap_test
            helm_install_cert_manager
            build_and_load_operators
            install_humio_operator
            create_custom_bootstrap_token_secret
            create_custom_humio_bootstrap_token
            create_bootstrap_test_cluster
            show_cluster_status
            show_usage
            ;;
        *)
            echo "Usage: $0 [cleanup]"
            echo ""
            echo "  (no args)  - Set up complete bootstrap token test environment"
            echo "  cleanup    - Delete the test cluster"
            exit 1
            ;;
    esac
}

# Run main function
main "$@"