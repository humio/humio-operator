# Sharing the HumioCluster CA with the PDF Render Service

This guide explains how the Humio operator wires TLS between a `HumioCluster` and a
`HumioPdfRenderService`, and shows how to make both workloads trust the same
Certificate Authority (CA) secret. The sample manifests under
`config/samples/core_v1alpha1_humiocluster_with_pdf_render_service.yaml` and
`config/samples/core_v1alpha1_humiopdfrenderservice.yaml` implement the approach
described here when cert-manager provisions the HumioCluster CA (the default setup).

## How the operator handles TLS

- **HumioCluster** – When TLS is enabled (`spec.tls.enabled: true` or
  cert-manager auto enablement), the reconciler ensures a CA secret exists using
  `ensureValidCASecret()` and stores it as `tls.crt`/`tls.key` in a secret named
  either the value of `spec.tls.caSecretName` or `<cluster-name>-ca-keypair`
  (`internal/controller/humiocluster_tls.go:53`). When cert-manager is installed,
  it creates and maintains the `<cluster-name>-ca-keypair` secret automatically.
  Certificates for the Humio pods are issued from this CA
  (`ensureHumioNodeCertificates()`).
- **PDF render service** – When TLS is enabled, the reconciler mounts the secret
  returned by `helpers.GetCASecretNameForHPRS()` (default `<pdf-name>-ca-keypair`
  or any value specified in `spec.tls.caSecretName`) and exposes it to the
  container via `TLS_CA_PATH` (`internal/controller/humiopdfrenderservice_controller.go:1693`).
  The same secret is used by the optional cert-manager Issuer for the service
  (`EnsureValidCAIssuerForHPRS`).

To share the HumioCluster CA, configure both CRs to reference the same
Kubernetes TLS secret. The secret must live in the namespace where both
resources reside and contain `tls.crt` and `tls.key` entries. In most
installations this is the cert-manager managed `<cluster-name>-ca-keypair`
secret, so no manual CA creation is required.

## Step-by-step configuration

1. **Deploy the HumioCluster** – Enable TLS and let cert-manager handle the CA.
   With `metadata.name: example-humio`, the operator requests or reuses the
   `example-humio-ca-keypair` secret. Leave `spec.tls.caSecretName` unset unless
   you must supply a custom secret.
2. **Reference the secret from the PDF render service** – Set
   `spec.tls.enabled: true` and `spec.tls.caSecretName` to the HumioCluster CA
   secret (for example `example-humio-ca-keypair`). The operator will mount the
   CA at `/etc/ca/ca.crt` and set `TLS_ENABLED=true`, `TLS_CERT_PATH`,
   `TLS_KEY_PATH`, and `TLS_CA_PATH` automatically; remove any manually
   maintained TLS environment variables.
3. **(Optional) Override the CA secret** – If you need a different CA, create a
   `kubernetes.io/tls` secret and set `spec.tls.caSecretName` on both CRs to the
   shared secret. The rest of this guide still applies.
4. **(Optional) Enable auto-sync** – If the PDF render service has no explicit
   TLS section, the controller can copy the cluster’s TLS settings when the
   cluster enables scheduled reports (`ENABLE_SCHEDULED_REPORT=true` or
   `DEFAULT_PDF_RENDER_SERVICE_URL`), but defining the `tls` block explicitly
   makes intent clear when sharing a CA.

## Full example

The following manifests place both workloads in the `logging` namespace. The
HumioCluster uses the default cert-manager managed CA secret
`example-humio-ca-keypair`, which the HumioPdfRenderService references.

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: logging
---
apiVersion: core.humio.com/v1alpha1
kind: HumioCluster
metadata:
  name: example-humio
  namespace: logging
spec:
  tls:
    enabled: true
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
    caSecretName: example-humio-ca-keypair
  image: humio/pdf-render-service:0.1.2--build-104--sha-9a7598de95bb9775b6f59d874c37a206713bae01
  replicas: 2
  port: 5123
  # environmentVariables, resources, probes, etc.
```

## What to expect at runtime

- The Humio operator uses `example-humio-ca-keypair` to issue certificates for both the
  Humio nodes and the PDF render service pods. Each deployment mounts its server
  certificates from its own `*-tls` secret, signed by the shared CA.
- The PDF render service pods mount `/etc/ca/ca.crt` from `example-humio-ca-keypair` and
  receive `TLS_ENABLED=true`, `TLS_CERT_PATH=/etc/tls/tls.crt`,
  `TLS_KEY_PATH=/etc/tls/tls.key`, and `TLS_CA_PATH=/etc/ca/ca.crt` via
  environment variables, ensuring that outbound calls to Humio validate its TLS
  chain against the same CA the cluster uses.

## Verifying the setup

After deployment, you can confirm that both workloads use the shared CA:

```bash
kubectl -n logging get secret example-humio-ca-keypair
kubectl -n logging get pods -l humio-pdf-render-service=pdf-render-service -o yaml | rg "/etc/ca/ca.crt"
kubectl -n logging describe certificate example-humio
```

These commands show the CA secret, the mounted CA path inside the PDF render
service pods, and the cert-manager `Certificate` status proving that certificates
are issued from the shared CA.
