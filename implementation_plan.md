# Implementation Plan – HumioPdfRenderService (Revised)

This document lists the **concrete engineering tasks** required to eliminate the
flaky env-test failures around `HumioPdfRenderService` (HPRS) and the
`PdfRenderServiceRef` feature in `HumioCluster` (HC).

> The primary root cause of flakiness appears to be race conditions where tests
> expect child resources (like Deployments) to exist or be ready before the
> controller has reconciled the parent Custom Resource (CR) and updated its status.
> Ensuring that tests wait for `Status.ObservedGeneration == metadata.Generation` on
> the parent CR *after* an action, and *before* checking for controller-driven
> side-effects (like child resource creation or status updates), is crucial.
> Secondary issues can include inconsistent status reporting, finalizer handling,
> and non-deterministic test setup.

---

## 1. Controller-side fixes (HumioPdfRenderService - HPRS)

*(This section largely aligns with the original plan and appears mostly implemented. Verification and minor enhancements are noted.)*

### 1.1 Status handling & observedGeneration
*   **Verify**: A single `setStatus` helper is used consistently within the HPRS controller.
    *   It **must** always set `Status.ObservedGeneration = metadata.Generation` from the HPRS object being reconciled.
    *   It **must** be called exactly once per `Reconcile` loop, ideally via `defer`, to ensure status is updated even on errors.
*   **Action**: Perform a final check in `internal/controller/humiopdfrenderservice_controller.go` to ensure no direct status modifications or multiple update points remain.

### 1.2 State machine correctness
*   **Verify**: Enum values for `HPRSStateRunning`, `HPRSStateConfiguring`, `HPRSStateScaledDown`, `HPRSStateError` (defined in `api/v1alpha1/humiopdfrenderservice_types.go`) are consistently used in `internal/controller/humiopdfrenderservice_controller.go`.
*   **Verify**: Transitional states (e.g., `Configuring`) are correctly detected based on Deployment status (e.g., `Deployment.Status.ObservedGeneration < Deployment.Generation`, `Deployment.Status.ReadyReplicas < Deployment.Spec.Replicas`).
*   **Verify**: `ScaledDown` state is correctly detected (e.g., `HPRS.Spec.Replicas == 0` and child Deployment reflects this or is absent).
*   **Action**: Solidify logic ensuring `HPRSStateRunning` is only set when the underlying Deployment is fully ready (i.e., `Deployment.Status.ReadyReplicas == HPRS.Spec.Replicas` and `Deployment.Status.ObservedGeneration == Deployment.Generation`).

### 1.3 Requeue semantics
*   **Verify**: `ctrl.Result{RequeueAfter: ...}` is only returned *after* the status update (guaranteed if status update is deferred).
*   **Verify**: Requeue occurs if HPRS state is `Configuring` *and* the Deployment reconciliation is still pending.
*   **Verify**: Steady states (`Running`, `ScaledDown`) return `ctrl.Result{}` (no requeue unless triggered by other watches or errors).

### 1.4 Finalizer robustness
*   **Verify**: A single constant (e.g., `hprsFinalizer` in `internal/controller/humiopdfrenderservice_controller.go`) is used for the finalizer.
*   **Verify**: On deletion, `cleanupOwnedResources` (or equivalent logic) is called.
*   **Verify**: Finalizer removal uses exponential backoff (e.g., via `wait.ExponentialBackoff`) to handle potential conflicts during status updates if the CR is being updated concurrently.

### 1.5 Child resource reconciliation (Deployment, Service)
*   **Verify**: Mutate functions for Deployment and Service (in `internal/controller/humiopdfrenderservice_controller.go`) use `reflect.DeepEqual` or semantic equality checks for relevant sub-fields (e.g., `Deployment.Spec.Template`) to minimize unnecessary updates.
*   **Verify**: Only explicitly supported fields are copied/managed by the HPRS controller.
*   **Verify**: `controllerutil.SetControllerReference` is always used to set `OwnerReferences` on child resources.

### 1.6 TLS validation
*   **Verify**: If TLS is configured in `HPRS.Spec` but the required Kubernetes Secret is not found, the HPRS status is set to `ConfigError`, and an error is returned to trigger a requeue. The status update should occur via `defer`.

---

## 2. Controller-side fixes (HumioCluster - HC)

### 2.1 Handling `PdfRenderServiceRef`
*   **Verify**: When a `HumioPdfRenderService` is referenced by `HumioCluster.Spec.PdfRenderServiceRef` (logic in `internal/controller/humiocluster_controller.go`):
    *   If the referenced HPRS is not found (e.g., `ensurePdfRenderService` around line 383), HC status is set to `humiov1alpha1.HumioClusterStateConfigError`, and an error is returned to requeue.
    *   If the referenced HPRS is found but its status is not `humiov1alpha1.HumioPdfRenderServiceStateRunning` (or an equivalent "ready" state like `HumioPdfRenderServiceStateExists` with `Status.ReadyReplicas > 0`), HC status is set to `humiov1alpha1.HumioClusterStateConfigError`, and an error is returned to requeue (e.g., `ensurePdfRenderService` around line 395, `ensureReferencedPdfRenderServiceReady` around line 601).
*   **Action**: Ensure that after setting `ConfigError` on HC, the reconcile loop returns an error or a `ctrl.Result` that causes a requeue, allowing HC to re-check the HPRS status later.
*   **Verify**: If the HC's own TLS configuration changes and it references an HPRS, `syncPdfRenderServiceConfig` (or equivalent logic, e.g., `ensurePdfRenderService` around line 443) is triggered. If this results in changes requiring HPRS to update, the HC reconcile should requeue appropriately.

---

## 3. Test suite hardening (Critical for eliminating flakiness)

### 3.1 Generic test helpers

*   **`CreatePdfRenderServiceCR` (e.g., `internal/controller/suite/common.go` line 881)**:
    *   **Verified**: Correctly checks for `k8serrors.IsNotFound` before creating.
    *   **Action**: Ensure it *always* waits for the created CR to be retrievable via `k8sClient.Get` before returning. The existing `Eventually` block for this (line 905) serves this purpose.

*   **`EnsurePdfRenderDeploymentReady` (e.g., `internal/controller/suite/common.go` line 921)**:
    *   **Problem**: This helper is a likely source of flakiness if the Deployment doesn't appear, as seen in `internal_controller_suite_resources_test-results-junit.xml`. The timeout for Deployment existence is 60s.
    *   **Current Behavior**:
        1.  Waits for Deployment to exist.
        2.  If `helpers.UseEnvtest()` is true, it *manually patches* the Deployment's status (`Replicas`, `ReadyReplicas`, `AvailableReplicas`, `ObservedGeneration`).
        3.  Waits for `ReadyReplicas > 0`.
    *   **Actions**:
        1.  **Crucial Fix**: Before calling this helper, tests *must* ensure the parent `HumioPdfRenderService` CR has reconciled its `Status.ObservedGeneration` to match its `metadata.Generation`. This confirms the HPRS controller has processed the CR change that should trigger Deployment creation.
            ```go
            // In test, after creating/updating hprsCR:
            // 1. suite.CreatePdfRenderServiceCR(ctx, k8sClient, hprsKey, false) // or update hprsCR
            // 2. suite.WaitForObservedGeneration(ctx, k8sClient, hprsCR, testTimeout, testInterval) // CRITICAL STEP
            // 3. suite.EnsurePdfRenderDeploymentReady(ctx, k8sClient, hprsKey)
            ```
            This pattern needs to be applied in all tests that create an HPRS and then expect its Deployment, such as in `internal/controller/suite/resources/humioresources_controller_test.go` (e.g., around lines 3949, 3993, 4136, 4630).
        2.  The manual patching of Deployment status in `envtest` (line 949) is acceptable for simulating readiness but relies on the Deployment object actually being created by the controller first.
        3.  The timeout for Deployment existence (`DefaultTestTimeout*2`) should be sufficient if the controller acts promptly after observing the CR. If Deployments are consistently not created even after waiting for HPRS `ObservedGeneration`, the issue is likely in the HPRS controller's Deployment creation logic.

*   **`WaitForObservedGeneration` (e.g., `internal/controller/suite/common.go` line 824)**:
    *   **Verify**: This helper correctly fetches the object and compares `Status.ObservedGeneration` with `metadata.Generation`.
    *   **Action**: **This helper must be used in tests *every time* after a CR modification (Create/Update/Patch/Delete with finalizers) *before* asserting any status fields or the existence/state of child resources.** This ensures the test is observing the state *after* the controller has processed the change.

### 3.2 Remove `time.Sleep`
*   **Verify**: All `time.Sleep` calls used for synchronization have been replaced with `Eventually` blocks.
*   **Action**: `Eventually` blocks should wait on specific, observable conditions:
    *   `HPRS.Status.ObservedGeneration == HPRS.Generation` (after HPRS CR update).
    *   `Deployment.Status.ObservedGeneration == Deployment.Generation` (if checking Deployment directly).
    *   `HPRS.Status.State == humiov1alpha1.HPRSStateRunning` (or other expected states, *after* `ObservedGeneration` matches).
    *   `len(HPRS.ObjectMeta.Finalizers) == 0` (when testing finalizer removal).
    *   Existence of child resources (e.g., Deployment, Service) using `k8sClient.Get` within `Eventually`.

### 3.3 Avoid hard-coded Deployment/Service names
*   **Verify**: Child resource names are derived using the parent CR's name and a consistent suffix. The `childSuffix` constant (`-pdf-render-service`) from `internal/controller/humiopdfrenderservice_controller.go` should be the source of truth.
    *   Example in tests: `deploymentKey := types.NamespacedName{Name: hprsKey.Name + controller.ChildSuffix, ...}` (assuming `controller.ChildSuffix` is accessible or replicated). Many tests already use `hprsKey.Name + "-pdf-render-service"`.

### 3.4 Finalizer tests
*   **Verify**: Tests for finalizers (e.g., `internal/controller/suite/resources/humioresources_controller_test.go` line 4630):
    1.  Create the HPRS CR.
    2.  Wait for `ObservedGeneration` to match `Generation`.
    3.  Delete the HPRS CR (`k8sClient.Delete`).
    4.  `Eventually` check that `k8sClient.Get` for the HPRS CR returns `k8serrors.IsNotFound(err)`. This implicitly tests that the controller's finalizer was processed and removed, allowing deletion.

### 3.5 Stop duplicate CR creation
*   **Verified**: `CreatePdfRenderServiceCR` in `internal/controller/suite/common.go` (line 881) checks `IsNotFound` before `Create`. This is correctly implemented.

---

## 4. Utility/Refactor tasks

*   **Test utility package (`pkg/testutil/envtest.go` or similar)**:
    *   The current helpers in `internal/controller/suite/common.go` are generally well-placed. No immediate need for a separate package unless complexity grows significantly.
*   **Constants package (`pkg/constants/pdf.go` or similar)**:
    *   HPRS state enums are in `api/v1alpha1/humiopdfrenderservice_types.go`. The `childSuffix` is in `internal/controller/humiopdfrenderservice_controller.go`. This separation is acceptable.
*   **`helpers/volume.go`**:
    *   Review if environment variable, volume, and volume mount deduplication logic within the HPRS controller is complex and generic enough to warrant moving to a shared helper in `internal/helpers/`.
*   **Structured Logging**:
    *   **Action**: Ensure all key reconciliation steps in both HPRS and HC controllers use structured logging (e.g., `r.Log.WithValues("HumioPdfRenderService", req.NamespacedName, "DeploymentName", deploymentName).Info("Creating Deployment")`). This greatly aids debugging test failures.

---

## 5. Roll-out sequence (Revised Focus)

1.  **Strictly enforce `WaitForObservedGeneration`**: Audit all tests in `humioresources_controller_test.go` and `humiocluster_controller_test.go` (and any others interacting with HPRS). After *any* action that modifies an HPRS CR (`Create`, `Update`, `Patch`), insert a `suite.WaitForObservedGeneration(ctx, k8sClient, hprsCR, ...)` call *before* checking its status or the state/existence of its children (like Deployments via `EnsurePdfRenderDeploymentReady`).
2.  **Controller Logic Review for Deployment Creation**: Double-check HPRS controller logic in `reconcileDeployment`. Ensure it reliably attempts to create the Deployment once the HPRS CR is processed and prerequisites are met. Add detailed structured logs around this creation attempt.
3.  **Run tests with increased verbosity/logging**: If flakiness persists after the above, enable more detailed logging from the controller-runtime and Ginkgo to trace the exact sequence of events during failing tests.
4.  **Iteratively fix**: Address specific failures by analyzing logs and controller behavior, focusing on the synchronization between test actions, controller reconciliation, and status updates.

---

## 6. Expected outcome

*   **Deterministic test execution**: Tests reliably wait for the controller to process changes before making assertions, eliminating races related to stale reads or premature checks for child resources.
*   **Clearer failure analysis**: Failures (if any) are more likely to point to genuine controller bugs or incorrect test logic rather than simple timing issues.
*   **Significant reduction in test flakiness**: The "Deployment not found" errors should be largely eliminated by ensuring tests wait for the HPRS controller to act.
*   Overall env-test execution time might not drastically reduce if timeouts are still hit due to actual underlying issues, but successful runs will be far more consistent.
