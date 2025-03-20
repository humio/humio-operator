# PDF Render Service Test Improvements

This document outlines suggested improvements and additions to the test suites for `HumioPdfRenderService` and its interaction with `HumioCluster`.

## 1. `internal/controller/suite/resources/humioresources_controller_test.go`

The existing `Context("HumioPdfRenderService", ...)` block provides a good starting point, but needs expansion to cover the controller's logic more thoroughly.

**Existing Tests:**

*   `It("should create Deployment and Service", ...)`: Good baseline. Ensure it checks owner references.
*   `It("should update Deployment and Service when CR is updated", ...)`: Good baseline. Expand to cover more spec fields (see below).
*   `It("should correctly handle TLS configuration", ...)`: Good baseline. Ensure it specifically tests:
    *   Enabling TLS creates the correct volume mounts, volumes (using derived secret name `<name>-certificate`), and environment variables.
    *   Disabling TLS removes these elements.
    *   The correct derived secret name (`<name>-certificate`) is used, not a non-existent `spec.tls.secretName`.
*   `It("should correctly set resource requirements and probes", ...)`: Good baseline. Ensure it checks both creation and updates.
*   `It("should correctly set environment variables", ...)`: Good baseline. Ensure it checks both creation and updates, including TLS-related env vars.
*   `It("should correctly set pod annotations", ...)`: Good baseline. Ensure it checks both Deployment *and* Pod template annotations.
*   `It("should update status based on Deployment state", ...)`: Good baseline. Needs expansion (see below).

**Improvements & Missing Tests:**

1.  **Status Update Tests (`It("should update status based on Deployment state", ...)`):**
    *   **Expand State Coverage:** Explicitly test transitions to `Pending`, `Upgrading`, `Running`, `ScaledDown`, and `ConfigError` by manipulating the underlying Deployment's status (e.g., setting `ReadyReplicas`, `UpdatedReplicas`, `ObservedGeneration` vs `Generation`).
    *   **ConfigError Preservation:** Test that if the CR enters `ConfigError` (e.g., due to TLS validation failure), it doesn't revert to `Pending`/`Upgrading` even if the Deployment status changes, unless it becomes fully `Running`.
    *   **Nodes Field:** While harder in `envtest`, consider if a basic check for `Status.Nodes` being populated (even if empty when no pods exist) is feasible.

2.  **TLS Validation Failure Test:**
    *   Add a new test case: `It("should set ConfigError state if TLS secret is missing", ...)`
    *   Create an HPRS with `spec.tls.enabled: true`.
    *   *Do not* create the corresponding `<name>-certificate` secret.
    *   Verify that the HPRS status becomes `ConfigError`.
    *   Verify that reconciliation returns an error initially.

3.  **Finalizer and Deletion Test:**
    *   Add a new test case: `It("should add a finalizer and clean up resources on deletion", ...)`
    *   Create an HPRS and wait for Deployment/Service.
    *   Verify the finalizer (`core.humio.com/finalizer`) is present on the HPRS.
    *   Delete the HPRS CR.
    *   Verify the Deployment and Service are deleted by Kubernetes garbage collection (or explicitly check for deletion triggered by the finalizer).
    *   Verify the HPRS object itself is eventually removed.

4.  **Detailed Update Scenarios (`It("should update Deployment and Service when CR is updated", ...)`):**
    *   Explicitly test updates for:
        *   `spec.image`
        *   `spec.replicas` (including scaling down to 0)
        *   `spec.port`
        *   `spec.resources`
        *   `spec.affinity`
        *   `spec.serviceAccountName`
        *   `spec.imagePullSecrets`
        *   `spec.volumes` / `spec.volumeMounts`
        *   `spec.securityContext` / `spec.podSecurityContext`
        *   `spec.serviceType`

5.  **Service Reconciliation:**
    *   Ensure tests verify that the `Service` spec (ports, type, selector, annotations) is correctly reconciled using `CreateOrUpdate`.

## 2. `internal/controller/suite/clusters/humiocluster_controller_test.go`

The `Context("Humio Cluster PDF Render Service", ...)` block is currently empty and needs tests to cover the interaction logic implemented in `HumioClusterReconciler`.

**Missing Tests:**

1.  **Valid Reference:**
    *   Add test case: `It("should reconcile successfully when pdfRenderServiceRef points to a valid service", ...)`
    *   Create a valid `HumioPdfRenderService` (hprs-valid).
    *   Create a `HumioCluster` (hc) with `spec.pdfRenderServiceRef` pointing to `hprs-valid`.
    *   Verify `hc` reaches `Running` state.
    *   Verify no cluster-specific PDF service (`<hc-name>-pdf-render-service`) is created.

2.  **Invalid Reference (Not Found):**
    *   Add test case: `It("should set ConfigError state when pdfRenderServiceRef points to a non-existent service", ...)`
    *   Create a `HumioCluster` (hc) with `spec.pdfRenderServiceRef` pointing to a non-existent service name.
    *   Verify `hc` status becomes `ConfigError`.
    *   Verify reconciliation returns an error.

3.  **Reference Removal:**
    *   Add test case: `It("should reconcile successfully when pdfRenderServiceRef is removed", ...)`
    *   Create `hprs-valid`.
    *   Create `hc` referencing `hprs-valid` and wait for `Running`.
    *   Update `hc` to remove `spec.pdfRenderServiceRef`.
    *   Verify `hc` remains `Running` (or reconciles successfully).

4.  **Cleanup of Cluster-Specific Service:**
    *   Add test case: `It("should remove cluster-specific service when pdfRenderServiceRef is added", ...)`
    *   Create `hc` *without* `pdfRenderServiceRef`.
    *   *Manually* create a `HumioPdfRenderService` named `<hc-name>-pdf-render-service` (this simulates a leftover resource).
    *   Create `hprs-valid`.
    *   Update `hc` to add `spec.pdfRenderServiceRef` pointing to `hprs-valid`.
    *   Verify the cluster-specific service (`<hc-name>-pdf-render-service`) gets deleted.
    *   Verify `hc` reaches `Running` state.

5.  **No Reference:**
    *   Add test case: `It("should reconcile successfully when pdfRenderServiceRef is not set", ...)`
    *   Create `hc` *without* `spec.pdfRenderServiceRef`.
    *   Verify `hc` reaches `Running` state.
    *   Verify no cluster-specific PDF service (`<hc-name>-pdf-render-service`) is created by the `HumioCluster` controller (it shouldn't be).

