# Sharing the HumioCluster CA with the PDF Render Service

This guide explains how the Humio operator wires TLS between a `HumioCluster` and a
`HumioPdfRenderService`, and shows how to make both workloads trust the same
Certificate Authority (CA) secret. The sample manifests under
`config/samples/core_v1alpha1_humiocluster_with_pdf_render_service.yaml` and
`config/samples/core_v1alpha1_humiopdfrenderservice.yaml` implement the approach
described here.

## How the operator handles TLS

- **HumioCluster** – When TLS is enabled (`spec.tls.enabled: true` or
  cert-manager auto enablement), the reconciler ensures a CA secret exists using
  `ensureValidCASecret()` and stores it as `tls.crt`/`tls.key` in a secret named
  either the value of `spec.tls.caSecretName` or `<cluster-name>-ca-keypair`
  (`internal/controller/humiocluster_tls.go:53`). Certificates for the Humio pods
  are issued from this CA (`ensureHumioNodeCertificates()`).
- **PDF render service** – When TLS is enabled, the reconciler mounts the secret
  returned by `helpers.GetCASecretNameForHPRS()` (default `<pdf-name>-ca-keypair`
  or any value specified in `spec.tls.caSecretName`) and exposes it to the
  container via `TLS_CA_PATH` (`internal/controller/humiopdfrenderservice_controller.go:1693`).
  The same secret is used by the optional cert-manager Issuer for the service
  (`EnsureValidCAIssuerForHPRS`).

To share the HumioCluster CA, configure both CRs to reference the same
Kubernetes TLS secret. The secret must live in the namespace where both
resources reside and contain `tls.crt` and `tls.key` entries.

## Step-by-step configuration

1. **Create or reuse a CA secret** – Either let the operator create it for the
   HumioCluster (default `<cluster-name>-ca-keypair`) or provide your own
   `kubernetes.io/tls` secret.
2. **Reference the secret from the HumioCluster** – Set
   `spec.tls.caSecretName` if you supply your own secret. Otherwise note the
   auto-generated name.
3. **Reference the same secret from the PDF render service** – Set
   `spec.tls.enabled: true` and `spec.tls.caSecretName` to the HumioCluster CA
   secret. The operator will mount the CA at `/etc/ca/ca.crt` and set
   `TLS_ENABLED=true`, `TLS_CERT_PATH`, `TLS_KEY_PATH`, and `TLS_CA_PATH`
   automatically; remove any manually maintained TLS environment variables.
4. **(Optional) Enable auto-sync** – If the PDF render service has no explicit
   TLS section, the controller can copy the cluster’s TLS settings when the
   cluster enables scheduled reports (`ENABLE_SCHEDULED_REPORT=true` or
   `DEFAULT_PDF_RENDER_SERVICE_URL`), but defining the `tls` block explicitly
   makes intent clear when sharing a CA.

## Full example

The following manifests place both workloads in the `logging` namespace and
make them share a CA secret named `humio-shared-ca`.

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: logging
---
apiVersion: v1
kind: Secret
metadata:
  name: humio-shared-ca
  namespace: logging
type: kubernetes.io/tls
data:
  tls.crt: <base64-encoded-ca-certificate>
  tls.key: <base64-encoded-ca-private-key>
---
apiVersion: core.humio.com/v1alpha1
kind: HumioCluster
metadata:
  name: example-humio
  namespace: logging
spec:
  tls:
    enabled: true
    caSecretName: humio-shared-ca
  environmentVariables:
    - name: ENABLE_SCHEDULED_REPORT
      value: "true"
    - name: DEFAULT_PDF_RENDER_SERVICE_URL
      value: "http://pdf-render-service.logging.svc.cluster.local:5123"
  # ... rest of the cluster spec ...
---
apiVersion: core.humio.com/v1alpha1
kind: HumioPdfRenderService
metadata:
  name: pdf-render-service
  namespace: logging
spec:
  tls:
    enabled: true
    caSecretName: humio-shared-ca
  image: humio/pdf-render-service:0.1.2--build-104--sha-9a7598de95bb9775b6f59d874c37a206713bae01
  replicas: 2
  port: 5123
  # environmentVariables, resources, probes, etc.
```

## What to expect at runtime

- The Humio operator uses `humio-shared-ca` to issue certificates for both the
  Humio nodes and the PDF render service pods. Each deployment mounts its server
  certificates from its own `*-tls` secret, signed by the shared CA.
- The PDF render service pods mount `/etc/ca/ca.crt` from `humio-shared-ca` and
  receive `TLS_ENABLED=true`, `TLS_CERT_PATH=/etc/tls/tls.crt`,
  `TLS_KEY_PATH=/etc/tls/tls.key`, and `TLS_CA_PATH=/etc/ca/ca.crt` via
  environment variables, ensuring that outbound calls to Humio validate its TLS
  chain against the same CA the cluster uses.

## Verifying the setup

After deployment, you can confirm that both workloads use the shared CA:

```bash
kubectl -n logging get secret humio-shared-ca
kubectl -n logging get pods -l humio-pdf-render-service=pdf-render-service -o yaml | rg "/etc/ca/ca.crt"
kubectl -n logging describe certificate example-humio
```

These commands show the CA secret, the mounted CA path inside the PDF render
service pods, and the cert-manager `Certificate` status proving that certificates
are issued from the shared CA.
