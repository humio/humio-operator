/*
Copyright 2020 Humio https://humio.com
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// ---------------------------------------------------------------------------
// Test fixture
// ---------------------------------------------------------------------------

const (
	testHCName      = "test-cluster"
	testHCNamespace = "default"
	testHCUID       = "hc-uid-abc"
)

type reconcileFixture struct {
	t        *testing.T
	r        *HumioClusterReconciler
	fake     client.Client
	recorder *record.FakeRecorder
	scheme   *runtime.Scheme
}

func newReconcileFixture(t *testing.T) *reconcileFixture {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, policyv1.AddToScheme(scheme))
	require.NoError(t, humiov1alpha1.AddToScheme(scheme))

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	recorder := record.NewFakeRecorder(100)

	r := &HumioClusterReconciler{
		Client:     fakeClient,
		BaseLogger: logr.Discard(),
		Log:        logr.Discard(),
		Recorder:   recorder,
	}

	return &reconcileFixture{
		t:        t,
		r:        r,
		fake:     fakeClient,
		recorder: recorder,
		scheme:   scheme,
	}
}

// hc builds a minimal HumioCluster for use as a subject.
func (f *reconcileFixture) hc(state string, featureEnabled bool) *humiov1alpha1.HumioCluster {
	return &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testHCName,
			Namespace: testHCNamespace,
			UID:       testHCUID,
		},
		Spec: humiov1alpha1.HumioClusterSpec{
			OperatorFeatureFlags: humiov1alpha1.HumioOperatorFeatureFlags{
				EnableEvictionProtectionDuringMaintenance: featureEnabled,
			},
		},
		Status: humiov1alpha1.HumioClusterStatus{
			State: state,
		},
	}
}

// pool builds a HumioNodePool named "ingest" with 3 replicas.
func (f *reconcileFixture) pool(state string) *HumioNodePool {
	nc := int32(3)
	return &HumioNodePool{
		clusterName:  testHCName,
		nodePoolName: "ingest",
		namespace:    testHCNamespace,
		state:        state,
		humioNodeSpec: humiov1alpha1.HumioNodeSpec{
			NodeCount: &nc,
		},
	}
}

// pod builds a pod labelled to match MatchingLabelsForHumioNodePool(testHCName, poolName).
func (f *reconcileFixture) pod(poolName string) *corev1.Pod {
	labels := map[string]string{
		"app.kubernetes.io/instance":   testHCName,
		"app.kubernetes.io/name":       "humio",
		"app.kubernetes.io/managed-by": "humio-operator",
		"humio.com/node-pool":          poolName,
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "humio-ingest-0",
			Namespace: testHCNamespace,
			Labels:    labels,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "humio", Image: "humio/humio-core:latest"}},
		},
	}
}

// getPDB fetches a PDB by name. Returns nil if it doesn't exist.
func (f *reconcileFixture) getPDB(name string) *policyv1.PodDisruptionBudget {
	f.t.Helper()
	var pdb policyv1.PodDisruptionBudget
	err := f.fake.Get(context.Background(), types.NamespacedName{Name: name, Namespace: testHCNamespace}, &pdb)
	if k8serrors.IsNotFound(err) {
		return nil
	}
	require.NoError(f.t, err)
	return &pdb
}

// pdbNames lists all PDBs in the test namespace.
func (f *reconcileFixture) pdbNames() []string {
	f.t.Helper()
	var list policyv1.PodDisruptionBudgetList
	require.NoError(f.t, f.fake.List(context.Background(), &list, client.InNamespace(testHCNamespace)))
	names := make([]string, 0, len(list.Items))
	for _, pdb := range list.Items {
		names = append(names, pdb.Name)
	}
	return names
}

// drainEvents reads all buffered events (non-blocking) and returns their raw strings.
// FakeRecorder format is "TYPE REASON MESSAGE".
func (f *reconcileFixture) drainEvents() []string {
	f.t.Helper()
	var out []string
	for {
		select {
		case e := <-f.recorder.Events:
			out = append(out, e)
		default:
			return out
		}
	}
}

// assertEvent matches an event by substring against Type + Reason + Message.
func assertEvent(t *testing.T, events []string, wantType, wantReason, wantMsgSubstr string) {
	t.Helper()
	prefix := wantType + " " + wantReason
	for _, e := range events {
		if strings.HasPrefix(e, prefix) && strings.Contains(e, wantMsgSubstr) {
			return
		}
	}
	t.Errorf("expected event %q containing %q; got events: %v", prefix, wantMsgSubstr, events)
}

// assertNoEvent asserts no event with the given Reason was emitted.
func assertNoEvent(t *testing.T, events []string, wantReason string) {
	t.Helper()
	for _, e := range events {
		if strings.Contains(e, " "+wantReason+" ") {
			t.Errorf("unexpected event with reason %q: %s", wantReason, e)
		}
	}
}

// poolsAsList wraps pools as a HumioNodePoolList for the reconcile methods.
func poolsAsList(pools ...*HumioNodePool) HumioNodePoolList {
	var list HumioNodePoolList
	for _, p := range pools {
		list.Add(p)
	}
	return list
}

// ---------------------------------------------------------------------------
// Group A — Phase 1 (add-only)
// ---------------------------------------------------------------------------

func TestReconcilePhase1_FeatureDisabled_NoOp(t *testing.T) {
	f := newReconcileFixture(t)
	hc := f.hc(humiov1alpha1.HumioClusterStateUpgrading, false)
	pool := f.pool(humiov1alpha1.HumioClusterStateUpgrading)

	// Pod present so the zero-pods short-circuit doesn't mask a broken feature-flag gate.
	pod := f.pod(pool.GetNodePoolName())
	require.NoError(t, f.fake.Create(context.Background(), pod))

	err := f.r.reconcileEvictionProtectionPDBsAddOnly(context.Background(), hc, poolsAsList(pool))
	require.NoError(t, err)

	assert.Empty(t, f.pdbNames(), "no PDB should exist when feature is disabled (even with matching pods)")
	assert.Empty(t, f.drainEvents(), "no events when feature is disabled")
}

func TestReconcilePhase1_ClusterStable_NoOp(t *testing.T) {
	f := newReconcileFixture(t)
	hc := f.hc(humiov1alpha1.HumioClusterStateRunning, true)
	pool := f.pool(humiov1alpha1.HumioClusterStateRunning)

	// Pod present so the zero-pods short-circuit doesn't mask a broken stability check.
	pod := f.pod(pool.GetNodePoolName())
	require.NoError(t, f.fake.Create(context.Background(), pod))

	err := f.r.reconcileEvictionProtectionPDBsAddOnly(context.Background(), hc, poolsAsList(pool))
	require.NoError(t, err)

	assert.Empty(t, f.pdbNames(), "no PDB should exist when cluster is stable (even with matching pods)")
	assert.Empty(t, f.drainEvents(), "no events when cluster is stable")
}

func TestReconcilePhase1_UnstableWithPods_CreatesPDB(t *testing.T) {
	f := newReconcileFixture(t)
	pool := f.pool(humiov1alpha1.HumioClusterStateUpgrading)
	poolFullName := pool.GetNodePoolName()

	pod := f.pod(poolFullName)
	require.NoError(t, f.fake.Create(context.Background(), pod))

	hc := f.hc(humiov1alpha1.HumioClusterStateUpgrading, true)
	require.NoError(t, f.fake.Create(context.Background(), hc))

	err := f.r.reconcileEvictionProtectionPDBsAddOnly(context.Background(), hc, poolsAsList(pool))
	require.NoError(t, err)

	pdbName := evictionProtectionPDBName(pool)
	pdb := f.getPDB(pdbName)
	require.NotNil(t, pdb, "PDB should have been created")

	// maxUnavailable=0 spec
	require.NotNil(t, pdb.Spec.MaxUnavailable)
	assert.Equal(t, int32(0), pdb.Spec.MaxUnavailable.IntVal)
	assert.Nil(t, pdb.Spec.MinAvailable)

	// managed-by label present
	assert.Equal(t, evictionProtectionManagedByLabelValue,
		pdb.Labels[evictionProtectionManagedByLabelKey],
		"managed-by label must be set for orphan sweep to find this PDB")

	// created-at annotation present and RFC3339-parseable
	createdAt := pdb.Annotations[evictionProtectionCreatedAtAnnKey]
	require.NotEmpty(t, createdAt, "created-at annotation must be stamped")
	_, err = time.Parse(time.RFC3339, createdAt)
	assert.NoError(t, err, "created-at must be RFC3339")

	// Owner ref points at HumioCluster with controller=true
	require.Len(t, pdb.OwnerReferences, 1)
	assert.Equal(t, testHCName, pdb.OwnerReferences[0].Name)
	assert.Equal(t, types.UID(testHCUID), pdb.OwnerReferences[0].UID)
	require.NotNil(t, pdb.OwnerReferences[0].Controller)
	assert.True(t, *pdb.OwnerReferences[0].Controller)

	// Event emitted
	events := f.drainEvents()
	assertEvent(t, events, "Normal", evictionProtectionActivatedReason, pdbName)
}

func TestReconcilePhase1_NoPods_ShortCircuit(t *testing.T) {
	f := newReconcileFixture(t)
	hc := f.hc(humiov1alpha1.HumioClusterStateUpgrading, true)
	require.NoError(t, f.fake.Create(context.Background(), hc))
	pool := f.pool(humiov1alpha1.HumioClusterStateUpgrading)

	err := f.r.reconcileEvictionProtectionPDBsAddOnly(context.Background(), hc, poolsAsList(pool))
	require.NoError(t, err)

	assert.Empty(t, f.pdbNames(), "no PDB should be created when zero pods match selector")
	assert.Empty(t, f.drainEvents(), "no Activated event when no PDB was created")
}

func TestReconcilePhase1_ExistingPDB_TTLPreserved(t *testing.T) {
	f := newReconcileFixture(t)
	hc := f.hc(humiov1alpha1.HumioClusterStateUpgrading, true)
	require.NoError(t, f.fake.Create(context.Background(), hc))
	pool := f.pool(humiov1alpha1.HumioClusterStateUpgrading)

	poolFullName := pool.GetNodePoolName()
	pod := f.pod(poolFullName)
	require.NoError(t, f.fake.Create(context.Background(), pod))

	// Pre-seed a PDB with an old created-at timestamp; Phase 1 must NOT overwrite it.
	originalTime := time.Now().Add(-30 * time.Minute).UTC().Format(time.RFC3339)
	existing := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      evictionProtectionPDBName(pool),
			Namespace: testHCNamespace,
			Annotations: map[string]string{
				evictionProtectionCreatedAtAnnKey: originalTime,
			},
			Labels: map[string]string{
				evictionProtectionManagedByLabelKey: evictionProtectionManagedByLabelValue,
			},
		},
	}
	require.NoError(t, f.fake.Create(context.Background(), existing))

	err := f.r.reconcileEvictionProtectionPDBsAddOnly(context.Background(), hc, poolsAsList(pool))
	require.NoError(t, err)

	pdb := f.getPDB(evictionProtectionPDBName(pool))
	require.NotNil(t, pdb)
	assert.Equal(t, originalTime, pdb.Annotations[evictionProtectionCreatedAtAnnKey],
		"existing created-at annotation must be preserved across reconciles")

	// No Activated event on update (only on Created).
	assertNoEvent(t, f.drainEvents(), evictionProtectionActivatedReason)
}

// ---------------------------------------------------------------------------
// Group B — Phase 2 (remove)
// ---------------------------------------------------------------------------

func TestReconcilePhase2_FeatureDisabled_RemovesPDB(t *testing.T) {
	f := newReconcileFixture(t)
	hc := f.hc(humiov1alpha1.HumioClusterStateRunning, false)
	require.NoError(t, f.fake.Create(context.Background(), hc))
	pool := f.pool(humiov1alpha1.HumioClusterStateRunning)

	// Pre-seed a PDB — feature is off, cleanup must remove it.
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      evictionProtectionPDBName(pool),
			Namespace: testHCNamespace,
			Labels: map[string]string{
				evictionProtectionManagedByLabelKey: evictionProtectionManagedByLabelValue,
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "core.humio.com/v1alpha1",
				Kind:       "HumioCluster",
				Name:       testHCName,
				UID:        testHCUID,
				Controller: pointerBoolTrue(),
			}},
		},
	}
	require.NoError(t, f.fake.Create(context.Background(), pdb))

	err := f.r.reconcileEvictionProtectionPDBsRemove(context.Background(), hc, poolsAsList(pool))
	require.NoError(t, err)

	assert.Nil(t, f.getPDB(evictionProtectionPDBName(pool)), "PDB should be removed when feature is disabled")
	assertEvent(t, f.drainEvents(), "Normal", evictionProtectionReleasedReason, "feature disabled")
}

func TestReconcilePhase2_ClusterStable_RemovesPDB(t *testing.T) {
	f := newReconcileFixture(t)
	hc := f.hc(humiov1alpha1.HumioClusterStateRunning, true)
	require.NoError(t, f.fake.Create(context.Background(), hc))
	pool := f.pool(humiov1alpha1.HumioClusterStateRunning)

	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      evictionProtectionPDBName(pool),
			Namespace: testHCNamespace,
			Labels: map[string]string{
				evictionProtectionManagedByLabelKey: evictionProtectionManagedByLabelValue,
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "core.humio.com/v1alpha1",
				Kind:       "HumioCluster",
				Name:       testHCName,
				UID:        testHCUID,
				Controller: pointerBoolTrue(),
			}},
		},
	}
	require.NoError(t, f.fake.Create(context.Background(), pdb))

	err := f.r.reconcileEvictionProtectionPDBsRemove(context.Background(), hc, poolsAsList(pool))
	require.NoError(t, err)

	assert.Nil(t, f.getPDB(evictionProtectionPDBName(pool)), "PDB should be removed when cluster is stable")
	assertEvent(t, f.drainEvents(), "Normal", evictionProtectionReleasedReason, "stable state")
}

func TestReconcilePhase2_UnstableNotExpired_KeepsPDB(t *testing.T) {
	f := newReconcileFixture(t)
	hc := f.hc(humiov1alpha1.HumioClusterStateUpgrading, true)
	require.NoError(t, f.fake.Create(context.Background(), hc))
	pool := f.pool(humiov1alpha1.HumioClusterStateUpgrading)

	recentCreatedAt := time.Now().Add(-5 * time.Minute).UTC().Format(time.RFC3339)
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      evictionProtectionPDBName(pool),
			Namespace: testHCNamespace,
			Annotations: map[string]string{
				evictionProtectionCreatedAtAnnKey: recentCreatedAt,
			},
			Labels: map[string]string{
				evictionProtectionManagedByLabelKey: evictionProtectionManagedByLabelValue,
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "core.humio.com/v1alpha1",
				Kind:       "HumioCluster",
				Name:       testHCName,
				UID:        testHCUID,
				Controller: pointerBoolTrue(),
			}},
		},
	}
	require.NoError(t, f.fake.Create(context.Background(), pdb))

	err := f.r.reconcileEvictionProtectionPDBsRemove(context.Background(), hc, poolsAsList(pool))
	require.NoError(t, err)

	assert.NotNil(t, f.getPDB(evictionProtectionPDBName(pool)),
		"PDB should be retained when unstable and not expired")
	events := f.drainEvents()
	assertNoEvent(t, events, evictionProtectionReleasedReason)
	assertNoEvent(t, events, evictionProtectionTTLExpiredReason)
}

func TestReconcilePhase2_UnstableExpired_NeutersWithWarning(t *testing.T) {
	f := newReconcileFixture(t)
	hc := f.hc(humiov1alpha1.HumioClusterStateUpgrading, true)
	require.NoError(t, f.fake.Create(context.Background(), hc))
	pool := f.pool(humiov1alpha1.HumioClusterStateUpgrading)

	// 3 hours ago — comfortably past the 2h default TTL.
	oldCreatedAt := time.Now().Add(-3 * time.Hour).UTC().Format(time.RFC3339)
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      evictionProtectionPDBName(pool),
			Namespace: testHCNamespace,
			Annotations: map[string]string{
				evictionProtectionCreatedAtAnnKey: oldCreatedAt,
			},
			Labels: map[string]string{
				evictionProtectionManagedByLabelKey: evictionProtectionManagedByLabelValue,
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "core.humio.com/v1alpha1",
				Kind:       "HumioCluster",
				Name:       testHCName,
				UID:        testHCUID,
				Controller: pointerBoolTrue(),
			}},
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MaxUnavailable: intOrStringPtr(intstr.FromInt32(0)),
		},
	}
	require.NoError(t, f.fake.Create(context.Background(), pdb))

	err := f.r.reconcileEvictionProtectionPDBsRemove(context.Background(), hc, poolsAsList(pool))
	require.NoError(t, err)

	// PDB should still exist — neutered, not deleted (deleting would let Phase 1 recreate it).
	got := f.getPDB(evictionProtectionPDBName(pool))
	require.NotNil(t, got, "expired PDB must be neutered in place, NOT deleted (Phase 1 would recreate it otherwise)")

	// spec must be neutered
	require.NotNil(t, got.Spec.MaxUnavailable)
	assert.Equal(t, "100%", got.Spec.MaxUnavailable.StrVal, "neutered PDB must have maxUnavailable=100%%")
	assert.Nil(t, got.Spec.MinAvailable)

	// tombstone annotation must be stamped
	tombstone, ok := got.Annotations[evictionProtectionTTLFiredAtAnnKey]
	require.True(t, ok, "TTL-fired tombstone annotation must be stamped")
	_, parseErr := time.Parse(time.RFC3339, tombstone)
	assert.NoError(t, parseErr, "tombstone annotation must be RFC3339")

	assertEvent(t, f.drainEvents(), "Warning", evictionProtectionTTLExpiredReason, "neutered")
}

func TestReconcilePhase2_MalformedAnnotation_TreatedAsExpired(t *testing.T) {
	f := newReconcileFixture(t)
	hc := f.hc(humiov1alpha1.HumioClusterStateUpgrading, true)
	require.NoError(t, f.fake.Create(context.Background(), hc))
	pool := f.pool(humiov1alpha1.HumioClusterStateUpgrading)

	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      evictionProtectionPDBName(pool),
			Namespace: testHCNamespace,
			Annotations: map[string]string{
				evictionProtectionCreatedAtAnnKey: "not-a-timestamp", // malformed
			},
			Labels: map[string]string{
				evictionProtectionManagedByLabelKey: evictionProtectionManagedByLabelValue,
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "core.humio.com/v1alpha1",
				Kind:       "HumioCluster",
				Name:       testHCName,
				UID:        testHCUID,
				Controller: pointerBoolTrue(),
			}},
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MaxUnavailable: intOrStringPtr(intstr.FromInt32(0)),
		},
	}
	require.NoError(t, f.fake.Create(context.Background(), pdb))

	err := f.r.reconcileEvictionProtectionPDBsRemove(context.Background(), hc, poolsAsList(pool))
	require.NoError(t, err)

	// Same as expired-path: PDB is neutered in place, not deleted.
	got := f.getPDB(evictionProtectionPDBName(pool))
	require.NotNil(t, got, "malformed-annotation PDB (treated as expired) must be neutered, not deleted")
	require.NotNil(t, got.Spec.MaxUnavailable)
	assert.Equal(t, "100%", got.Spec.MaxUnavailable.StrVal)
	assert.Contains(t, got.Annotations, evictionProtectionTTLFiredAtAnnKey, "tombstone must be stamped")

	assertEvent(t, f.drainEvents(), "Warning", evictionProtectionTTLExpiredReason, "neutered")
}

func TestReconcilePhase2_NoPDB_NoOp(t *testing.T) {
	f := newReconcileFixture(t)
	hc := f.hc(humiov1alpha1.HumioClusterStateRunning, true)
	require.NoError(t, f.fake.Create(context.Background(), hc))
	pool := f.pool(humiov1alpha1.HumioClusterStateRunning)

	err := f.r.reconcileEvictionProtectionPDBsRemove(context.Background(), hc, poolsAsList(pool))
	require.NoError(t, err)

	assert.Empty(t, f.pdbNames())
	assert.Empty(t, f.drainEvents())
}

// ---------------------------------------------------------------------------
// Group C — Orphan sweep
// ---------------------------------------------------------------------------

func TestReconcileOrphanSweep_RemovesPDBForRemovedPool(t *testing.T) {
	f := newReconcileFixture(t)
	hc := f.hc(humiov1alpha1.HumioClusterStateRunning, true)
	require.NoError(t, f.fake.Create(context.Background(), hc))

	// Only ingest is in spec; digest was removed but its PDB survives.
	ingestPool := f.pool(humiov1alpha1.HumioClusterStateRunning)
	orphanName := testHCName + "-digest" + evictionProtectionPDBSuffix

	// Digest orphan — owner-ref to THIS HumioCluster
	orphan := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      orphanName,
			Namespace: testHCNamespace,
			Labels: map[string]string{
				evictionProtectionManagedByLabelKey: evictionProtectionManagedByLabelValue,
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "core.humio.com/v1alpha1",
				Kind:       "HumioCluster",
				Name:       testHCName,
				UID:        testHCUID,
				Controller: pointerBoolTrue(),
			}},
		},
	}
	require.NoError(t, f.fake.Create(context.Background(), orphan))

	// Note: reconcileEvictionProtectionPDBsRemove is what invokes the sweep at the end.
	err := f.r.reconcileEvictionProtectionPDBsRemove(context.Background(), hc, poolsAsList(ingestPool))
	require.NoError(t, err)

	assert.Nil(t, f.getPDB(orphanName), "orphaned PDB should be removed by the sweep")
	assertEvent(t, f.drainEvents(), "Normal", evictionProtectionReleasedReason, "no longer present in HumioCluster spec")
}

func TestReconcileOrphanSweep_SkipsPDBOwnedByDifferentHumioCluster(t *testing.T) {
	f := newReconcileFixture(t)
	hc := f.hc(humiov1alpha1.HumioClusterStateRunning, true)
	require.NoError(t, f.fake.Create(context.Background(), hc))

	ingestPool := f.pool(humiov1alpha1.HumioClusterStateRunning)

	// PDB owned by a DIFFERENT HumioCluster (different UID) — must not be touched.
	otherPDBName := "other-cluster-ingest" + evictionProtectionPDBSuffix
	otherHCUID := "hc-uid-other-xyz"
	otherPDB := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      otherPDBName,
			Namespace: testHCNamespace,
			Labels: map[string]string{
				evictionProtectionManagedByLabelKey: evictionProtectionManagedByLabelValue,
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "core.humio.com/v1alpha1",
				Kind:       "HumioCluster",
				Name:       "other-cluster",
				UID:        types.UID(otherHCUID),
				Controller: pointerBoolTrue(),
			}},
		},
	}
	require.NoError(t, f.fake.Create(context.Background(), otherPDB))

	err := f.r.reconcileEvictionProtectionPDBsRemove(context.Background(), hc, poolsAsList(ingestPool))
	require.NoError(t, err)

	assert.NotNil(t, f.getPDB(otherPDBName),
		"PDB owned by a different HumioCluster must be left alone (safety check)")
	assertNoEvent(t, f.drainEvents(), evictionProtectionReleasedReason)
}

func TestReconcileOrphanSweep_IgnoresUserManagedPDBs(t *testing.T) {
	f := newReconcileFixture(t)
	hc := f.hc(humiov1alpha1.HumioClusterStateRunning, true)
	require.NoError(t, f.fake.Create(context.Background(), hc))
	ingestPool := f.pool(humiov1alpha1.HumioClusterStateRunning)

	// User-created PDB that shares the namespace but lacks our managed-by label.
	userPDBName := "my-custom-pdb"
	userPDB := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      userPDBName,
			Namespace: testHCNamespace,
			Labels: map[string]string{
				"app": "totally-unrelated",
			},
		},
	}
	require.NoError(t, f.fake.Create(context.Background(), userPDB))

	err := f.r.reconcileEvictionProtectionPDBsRemove(context.Background(), hc, poolsAsList(ingestPool))
	require.NoError(t, err)

	assert.NotNil(t, f.getPDB(userPDBName),
		"user-created PDB without our managed-by label must be ignored by the sweep")
}

// ---------------------------------------------------------------------------
// Group D — Two-phase idempotency
// ---------------------------------------------------------------------------

func TestReconcilePhase1_Idempotent(t *testing.T) {
	f := newReconcileFixture(t)
	hc := f.hc(humiov1alpha1.HumioClusterStateUpgrading, true)
	require.NoError(t, f.fake.Create(context.Background(), hc))
	pool := f.pool(humiov1alpha1.HumioClusterStateUpgrading)
	pod := f.pod(pool.GetNodePoolName())
	require.NoError(t, f.fake.Create(context.Background(), pod))

	// Advance the clock 30s per call so an accidental re-stamp of the created-at
	// annotation would produce a different timestamp than the first reconcile.
	originalNow := evictionProtectionNow
	tick := 0
	evictionProtectionNow = func() time.Time {
		defer func() { tick++ }()
		return time.Date(2026, 8, 12, 10, 0, tick*30, 0, time.UTC)
	}
	defer func() { evictionProtectionNow = originalNow }()

	// First reconcile — creates PDB with tick=0 timestamp, emits Activated event.
	require.NoError(t, f.r.reconcileEvictionProtectionPDBsAddOnly(context.Background(), hc, poolsAsList(pool)))
	firstPDB := f.getPDB(evictionProtectionPDBName(pool))
	require.NotNil(t, firstPDB)
	firstCreatedAt := firstPDB.Annotations[evictionProtectionCreatedAtAnnKey]

	activated := 0
	for _, e := range f.drainEvents() {
		if strings.Contains(e, evictionProtectionActivatedReason) {
			activated++
		}
	}
	assert.Equal(t, 1, activated, "first reconcile should emit exactly one Activated event")

	// Second reconcile — created-at must be preserved despite the advanced clock.
	require.NoError(t, f.r.reconcileEvictionProtectionPDBsAddOnly(context.Background(), hc, poolsAsList(pool)))
	secondPDB := f.getPDB(evictionProtectionPDBName(pool))
	require.NotNil(t, secondPDB)
	assert.Equal(t, firstCreatedAt, secondPDB.Annotations[evictionProtectionCreatedAtAnnKey],
		"created-at annotation must be preserved across idempotent reconciles (clock advanced between calls)")

	assertNoEvent(t, f.drainEvents(), evictionProtectionActivatedReason)
}

func TestReconcilePhase2_Idempotent(t *testing.T) {
	f := newReconcileFixture(t)
	hc := f.hc(humiov1alpha1.HumioClusterStateRunning, true)
	require.NoError(t, f.fake.Create(context.Background(), hc))
	pool := f.pool(humiov1alpha1.HumioClusterStateRunning)

	// Seed a PDB so first Phase-2 has something to delete.
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      evictionProtectionPDBName(pool),
			Namespace: testHCNamespace,
			Labels: map[string]string{
				evictionProtectionManagedByLabelKey: evictionProtectionManagedByLabelValue,
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "core.humio.com/v1alpha1",
				Kind:       "HumioCluster",
				Name:       testHCName,
				UID:        testHCUID,
				Controller: pointerBoolTrue(),
			}},
		},
	}
	require.NoError(t, f.fake.Create(context.Background(), pdb))

	// First run — deletes PDB, emits Released event.
	require.NoError(t, f.r.reconcileEvictionProtectionPDBsRemove(context.Background(), hc, poolsAsList(pool)))
	assert.Nil(t, f.getPDB(evictionProtectionPDBName(pool)))
	released := 0
	for _, e := range f.drainEvents() {
		if strings.Contains(e, evictionProtectionReleasedReason) {
			released++
		}
	}
	assert.Equal(t, 1, released, "first Phase-2 should emit exactly one Released event")

	// Second run — nothing to delete, no Event, no error.
	require.NoError(t, f.r.reconcileEvictionProtectionPDBsRemove(context.Background(), hc, poolsAsList(pool)))
	assertNoEvent(t, f.drainEvents(), evictionProtectionReleasedReason)
}

// ---------------------------------------------------------------------------
// Group E — Full-cycle safety valve tests
//
// Guards against Phase 2 delete followed by immediate Phase 1 recreate on a
// wedged cluster (which would defeat the TTL safety valve).
// ---------------------------------------------------------------------------

func TestReconcileFullCycle_TTLFires_NoImmediateRecreate(t *testing.T) {
	f := newReconcileFixture(t)
	hc := f.hc(humiov1alpha1.HumioClusterStateUpgrading, true)
	require.NoError(t, f.fake.Create(context.Background(), hc))
	pool := f.pool(humiov1alpha1.HumioClusterStateUpgrading)
	pod := f.pod(pool.GetNodePoolName())
	require.NoError(t, f.fake.Create(context.Background(), pod))

	// Seed an active (non-tombstoned) PDB that is comfortably past TTL.
	oldCreatedAt := time.Now().Add(-3 * time.Hour).UTC().Format(time.RFC3339)
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      evictionProtectionPDBName(pool),
			Namespace: testHCNamespace,
			Annotations: map[string]string{
				evictionProtectionCreatedAtAnnKey: oldCreatedAt,
			},
			Labels: map[string]string{
				evictionProtectionManagedByLabelKey: evictionProtectionManagedByLabelValue,
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "core.humio.com/v1alpha1",
				Kind:       "HumioCluster",
				Name:       testHCName,
				UID:        testHCUID,
				Controller: pointerBoolTrue(),
			}},
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MaxUnavailable: intOrStringPtr(intstr.FromInt32(0)),
		},
	}
	require.NoError(t, f.fake.Create(context.Background(), pdb))

	// First reconcile pass: Phase 1 updates the existing PDB (annotation preserved),
	// Phase 2 sees TTL expired + cluster unstable and neuters it.
	require.NoError(t, f.r.reconcileEvictionProtectionPDBsAddOnly(context.Background(), hc, poolsAsList(pool)))
	require.NoError(t, f.r.reconcileEvictionProtectionPDBsRemove(context.Background(), hc, poolsAsList(pool)))

	got := f.getPDB(evictionProtectionPDBName(pool))
	require.NotNil(t, got, "PDB must still exist (neutered, not deleted)")
	assert.Equal(t, "100%", got.Spec.MaxUnavailable.StrVal, "PDB must be neutered")
	assert.Contains(t, got.Annotations, evictionProtectionTTLFiredAtAnnKey, "tombstone must be stamped")

	// Second reconcile pass on the still-wedged cluster: Phase 1 must honor the
	// tombstone and NOT re-arm the maxUnavailable=0 lock.
	require.NoError(t, f.r.reconcileEvictionProtectionPDBsAddOnly(context.Background(), hc, poolsAsList(pool)))
	require.NoError(t, f.r.reconcileEvictionProtectionPDBsRemove(context.Background(), hc, poolsAsList(pool)))

	afterSecondCycle := f.getPDB(evictionProtectionPDBName(pool))
	require.NotNil(t, afterSecondCycle)
	assert.Equal(t, "100%", afterSecondCycle.Spec.MaxUnavailable.StrVal,
		"PDB must remain neutered after second reconcile; Phase 1 must not re-arm the maxUnavailable=0 lock")
	assert.Contains(t, afterSecondCycle.Annotations, evictionProtectionTTLFiredAtAnnKey,
		"tombstone must persist across reconciles until cluster returns to Running")
}

func TestReconcileFullCycle_RecoveryClearsTombstone(t *testing.T) {
	f := newReconcileFixture(t)
	// Seed a tombstoned (neutered) PDB from a prior TTL fire.
	pool := f.pool(humiov1alpha1.HumioClusterStateRunning)
	oldCreatedAt := time.Now().Add(-4 * time.Hour).UTC().Format(time.RFC3339)
	tombstonedAt := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	neutered := intstr.FromString("100%")
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      evictionProtectionPDBName(pool),
			Namespace: testHCNamespace,
			Annotations: map[string]string{
				evictionProtectionCreatedAtAnnKey:  oldCreatedAt,
				evictionProtectionTTLFiredAtAnnKey: tombstonedAt,
			},
			Labels: map[string]string{
				evictionProtectionManagedByLabelKey: evictionProtectionManagedByLabelValue,
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "core.humio.com/v1alpha1",
				Kind:       "HumioCluster",
				Name:       testHCName,
				UID:        testHCUID,
				Controller: pointerBoolTrue(),
			}},
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MaxUnavailable: &neutered,
		},
	}
	require.NoError(t, f.fake.Create(context.Background(), pdb))

	// Cluster returns to Running. Feature still enabled.
	hc := f.hc(humiov1alpha1.HumioClusterStateRunning, true)
	require.NoError(t, f.fake.Create(context.Background(), hc))

	// Phase 2 on a stable cluster must fully delete the tombstoned PDB.
	require.NoError(t, f.r.reconcileEvictionProtectionPDBsRemove(context.Background(), hc, poolsAsList(pool)))
	assert.Nil(t, f.getPDB(evictionProtectionPDBName(pool)),
		"stable-cluster branch must delete the tombstoned PDB so the next unstable cycle can arm a fresh one")
	assertEvent(t, f.drainEvents(), "Normal", evictionProtectionReleasedReason, "stable state")

	// Now simulate the next unstable cycle: fresh PDB should be armed with maxUnavailable=0.
	unstableHC := f.hc(humiov1alpha1.HumioClusterStateUpgrading, true)
	unstablePool := f.pool(humiov1alpha1.HumioClusterStateUpgrading)
	pod := f.pod(unstablePool.GetNodePoolName())
	require.NoError(t, f.fake.Create(context.Background(), pod))

	require.NoError(t, f.r.reconcileEvictionProtectionPDBsAddOnly(context.Background(), unstableHC, poolsAsList(unstablePool)))
	fresh := f.getPDB(evictionProtectionPDBName(unstablePool))
	require.NotNil(t, fresh, "Phase 1 must arm a fresh PDB after recovery")
	require.NotNil(t, fresh.Spec.MaxUnavailable)
	assert.Equal(t, int32(0), fresh.Spec.MaxUnavailable.IntVal, "fresh PDB must be maxUnavailable=0")
	assert.NotContains(t, fresh.Annotations, evictionProtectionTTLFiredAtAnnKey,
		"fresh PDB must not carry a tombstone; recovery cleared it")
}

// ---------------------------------------------------------------------------
// Small helpers used above
// ---------------------------------------------------------------------------

func pointerBoolTrue() *bool {
	t := true
	return &t
}

func intOrStringPtr(v intstr.IntOrString) *intstr.IntOrString {
	return &v
}
