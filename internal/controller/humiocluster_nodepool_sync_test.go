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
	"fmt"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
)

func TestShadowNodePoolForeignCollision(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = humiov1alpha1.AddToScheme(scheme)

	ctx := context.Background()

	// Create test HumioCluster
	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
			UID:       types.UID("test-cluster-uid"),
		},
		Spec: humiov1alpha1.HumioClusterSpec{
			HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
				NodeCount: ptr.To(int32(3)),
				Image:     "humio/humio:latest",
			},
			OperatorFeatureFlags: humiov1alpha1.HumioOperatorFeatureFlags{
				EnableIndependentHumioNodePools: true,
			},
		},
	}

	// Create foreign HumioNodePool with different owner UID
	foreignNodePool := &humiov1alpha1.HumioNodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "core.humio.com/v1alpha1",
				Kind:       "HumioCluster",
				Name:       "other-cluster",
				UID:        types.UID("foreign-uid"),
			}},
		},
		Spec: humiov1alpha1.HumioNodePoolSpec{
			Name:        "main",
			ClusterName: "other-cluster",
		},
	}

	// Create fake client with the foreign resource
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(hc, foreignNodePool).
		WithStatusSubresource(&humiov1alpha1.HumioNodePool{}).
		Build()

	// Create reconciler with a buffer to capture logs
	logBuffer := &logBuffer{entries: []string{}}
	logger := zap.New(zap.WriteTo(logBuffer), zap.UseDevMode(true))

	reconciler := &HumioClusterReconciler{
		Client: fakeClient,
		Log:    logger,
	}

	// Run ensureNodePoolResources
	err := reconciler.ensureNodePoolResources(ctx, hc)

	// Assert no error returned
	assert.NoError(t, err, "ensureNodePoolResources should not return error for foreign resource")

	// Assert foreign resource is untouched (still has foreign OwnerRef)
	updatedNodePool := &humiov1alpha1.HumioNodePool{}
	err = fakeClient.Get(ctx, types.NamespacedName{Name: "test-cluster", Namespace: "default"}, updatedNodePool)
	assert.NoError(t, err)
	assert.Len(t, updatedNodePool.OwnerReferences, 1)
	assert.Equal(t, types.UID("foreign-uid"), updatedNodePool.OwnerReferences[0].UID, "foreign resource should be untouched")

	// Assert warning log contains expected substring
	foundLog := false
	expectedSubstring := "foreign HumioNodePool resource exists with same name, skipping shadow creation"
	for _, entry := range logBuffer.entries {
		if strings.Contains(entry, expectedSubstring) &&
			strings.Contains(entry, "test-cluster") &&
			strings.Contains(entry, "shadow-node-pool-sync") {
			foundLog = true
			break
		}
	}
	assert.True(t, foundLog, "expected warning log with substring '%s' and component='shadow-node-pool-sync'", expectedSubstring)
}

// logBuffer captures log entries for testing
type logBuffer struct {
	entries []string
}

func (lb *logBuffer) Write(p []byte) (n int, err error) {
	lb.entries = append(lb.entries, string(p))
	return len(p), nil
}

func TestShadowNodePoolDeletionTimestamp(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = humiov1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	t.Run("HumioCluster with DeletionTimestamp set should no-op and create no shadow node pools", func(t *testing.T) {
		ctx := context.Background()
		now := metav1.Now()

		hc := &humiov1alpha1.HumioCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "test-cluster",
				Namespace:         "default",
				DeletionTimestamp: &now,
				Finalizers:        []string{"humio.com/finalizer"},
			},
			Spec: humiov1alpha1.HumioClusterSpec{
				HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
					NodeCount: ptr.To(int32(3)),
					Image:     "humio/humio:1.0.0",
				},
				OperatorFeatureFlags: humiov1alpha1.HumioOperatorFeatureFlags{
					EnableIndependentHumioNodePools: true,
				},
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(hc).
			WithStatusSubresource(&humiov1alpha1.HumioNodePool{}).
			Build()

		logger := logr.Discard()

		r := &HumioClusterReconciler{
			Client: fakeClient,
			Log:    logger,
		}

		err := r.ensureNodePoolResources(ctx, hc)
		assert.NoError(t, err, "ensureNodePoolResources should return nil without error")

		nodePoolList := &humiov1alpha1.HumioNodePoolList{}
		err = fakeClient.List(ctx, nodePoolList)
		assert.NoError(t, err, "failed to list HumioNodePool resources")

		assert.Equal(t, 0, len(nodePoolList.Items), "no shadow node pools should be created when DeletionTimestamp is set")
	})

	t.Run("HumioCluster without DeletionTimestamp should create shadow node pools normally", func(t *testing.T) {
		ctx := context.Background()

		hc := &humiov1alpha1.HumioCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-2",
				Namespace: "default",
			},
			Spec: humiov1alpha1.HumioClusterSpec{
				HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
					NodeCount: ptr.To(int32(3)),
					Image:     "humio/humio:1.0.0",
				},
				OperatorFeatureFlags: humiov1alpha1.HumioOperatorFeatureFlags{
					EnableIndependentHumioNodePools: true,
				},
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(hc).
			WithStatusSubresource(&humiov1alpha1.HumioNodePool{}).
			Build()

		logger := logr.Discard()

		r := &HumioClusterReconciler{
			Client: fakeClient,
			Log:    logger,
		}

		err := r.ensureNodePoolResources(ctx, hc)
		assert.NoError(t, err, "ensureNodePoolResources should succeed")

		nodePoolList := &humiov1alpha1.HumioNodePoolList{}
		err = fakeClient.List(ctx, nodePoolList)
		assert.NoError(t, err, "failed to list HumioNodePool resources")

		assert.Equal(t, 1, len(nodePoolList.Items), "should create main shadow node pool")

		mainPool := nodePoolList.Items[0]
		expectedName := shadowNodePoolResourceName("test-cluster-2", "main")
		assert.Equal(t, expectedName, mainPool.Name, "shadow node pool should have correct resource name")
		assert.Equal(t, shadowNodePoolManagedBy, mainPool.Annotations[annotationManagedBy], "shadow node pool should have managed-by annotation")
	})
}

func TestShadowNodePoolFinalizer(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = humiov1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	ctx := context.Background()

	t.Run("Shadow CR with finalizers should log warning", func(t *testing.T) {
		// Create test HumioCluster
		hc := &humiov1alpha1.HumioCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
				UID:       types.UID("test-cluster-uid"),
			},
			Spec: humiov1alpha1.HumioClusterSpec{
				HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
					NodeCount: ptr.To(int32(3)),
					Image:     "humio/humio:latest",
				},
				OperatorFeatureFlags: humiov1alpha1.HumioOperatorFeatureFlags{
					EnableIndependentHumioNodePools: true,
				},
			},
		}

		// Create shadow HumioNodePool with a finalizer (unexpected)
		shadowNodePool := &humiov1alpha1.HumioNodePool{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-cluster",
				Namespace:  "default",
				Finalizers: []string{"humio.com/unexpected-finalizer", "another.io/finalizer"},
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: "core.humio.com/v1alpha1",
					Kind:       "HumioCluster",
					Name:       "test-cluster",
					UID:        types.UID("test-cluster-uid"),
				}},
				Annotations: map[string]string{
					annotationManagedBy: shadowNodePoolManagedBy,
					annotationCluster:   "test-cluster",
				},
			},
			Spec: humiov1alpha1.HumioNodePoolSpec{
				Name:        "main",
				ClusterName: "test-cluster",
			},
		}

		// Create fake client
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(hc, shadowNodePool).
			WithStatusSubresource(&humiov1alpha1.HumioNodePool{}).
			Build()

		// Create reconciler with a buffer to capture logs
		logBuffer := &logBuffer{entries: []string{}}
		logger := zap.New(zap.WriteTo(logBuffer), zap.UseDevMode(true))

		reconciler := &HumioClusterReconciler{
			Client: fakeClient,
			Log:    logger,
		}

		// Run ensureNodePoolResources
		err := reconciler.ensureNodePoolResources(ctx, hc)

		// Assert no error returned
		assert.NoError(t, err, "ensureNodePoolResources should not return error when finalizer detected")

		// Assert warning log contains expected substring and finalizer list
		foundLog := false
		expectedSubstring := "shadow HumioNodePool has unexpected finalizers"
		for _, entry := range logBuffer.entries {
			if strings.Contains(entry, expectedSubstring) &&
				strings.Contains(entry, "test-cluster") &&
				strings.Contains(entry, "shadow-node-pool-sync") &&
				strings.Contains(entry, "humio.com/unexpected-finalizer") {
				foundLog = true
				break
			}
		}
		assert.True(t, foundLog, "expected warning log with substring '%s' and finalizer list", expectedSubstring)

		// Verify the finalizers are still present (not removed)
		updatedNodePool := &humiov1alpha1.HumioNodePool{}
		err = fakeClient.Get(ctx, types.NamespacedName{Name: "test-cluster", Namespace: "default"}, updatedNodePool)
		assert.NoError(t, err)
		assert.Len(t, updatedNodePool.Finalizers, 2, "finalizers should not be removed")
	})

	t.Run("Shadow CR without finalizers should not log warning", func(t *testing.T) {
		// Create test HumioCluster
		hc := &humiov1alpha1.HumioCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-2",
				Namespace: "default",
				UID:       types.UID("test-cluster-2-uid"),
			},
			Spec: humiov1alpha1.HumioClusterSpec{
				HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
					NodeCount: ptr.To(int32(3)),
					Image:     "humio/humio:latest",
				},
				OperatorFeatureFlags: humiov1alpha1.HumioOperatorFeatureFlags{
					EnableIndependentHumioNodePools: true,
				},
			},
		}

		// Create shadow HumioNodePool without finalizers (normal case)
		shadowNodePool := &humiov1alpha1.HumioNodePool{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-2-main",
				Namespace: "default",
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: "core.humio.com/v1alpha1",
					Kind:       "HumioCluster",
					Name:       "test-cluster-2",
					UID:        types.UID("test-cluster-2-uid"),
				}},
				Annotations: map[string]string{
					annotationManagedBy: shadowNodePoolManagedBy,
					annotationCluster:   "test-cluster-2",
				},
			},
			Spec: humiov1alpha1.HumioNodePoolSpec{
				Name:        "main",
				ClusterName: "test-cluster-2",
			},
		}

		// Create fake client
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(hc, shadowNodePool).
			WithStatusSubresource(&humiov1alpha1.HumioNodePool{}).
			Build()

		// Create reconciler with a buffer to capture logs
		logBuffer := &logBuffer{entries: []string{}}
		logger := zap.New(zap.WriteTo(logBuffer), zap.UseDevMode(true))

		reconciler := &HumioClusterReconciler{
			Client: fakeClient,
			Log:    logger,
		}

		// Run ensureNodePoolResources
		err := reconciler.ensureNodePoolResources(ctx, hc)

		// Assert no error returned
		assert.NoError(t, err, "ensureNodePoolResources should succeed normally")

		// Assert no warning log about finalizers
		foundLog := false
		unexpectedSubstring := "shadow HumioNodePool has unexpected finalizers"
		for _, entry := range logBuffer.entries {
			if strings.Contains(entry, unexpectedSubstring) {
				foundLog = true
				break
			}
		}
		assert.False(t, foundLog, "should not log warning when no finalizers present")
	})
}

func TestShadowNodePool_FlattenedNodeCount(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = humiov1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	ctx := context.Background()

	t.Run("Shadow CR should expose nodeCount at top-level .spec.nodeCount", func(t *testing.T) {
		hc := &humiov1alpha1.HumioCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
				UID:       types.UID("test-cluster-uid"),
			},
			Spec: humiov1alpha1.HumioClusterSpec{
				HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
					NodeCount: ptr.To(int32(5)),
					Image:     "humio/humio:1.0.0",
				},
				OperatorFeatureFlags: humiov1alpha1.HumioOperatorFeatureFlags{
					EnableIndependentHumioNodePools: true,
				},
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(hc).
			WithStatusSubresource(&humiov1alpha1.HumioNodePool{}).
			Build()

		logger := logr.Discard()

		r := &HumioClusterReconciler{
			Client: fakeClient,
			Log:    logger,
		}

		err := r.ensureNodePoolResources(ctx, hc)
		assert.NoError(t, err, "ensureNodePoolResources should succeed")

		shadowNodePool := &humiov1alpha1.HumioNodePool{}
		expectedName := shadowNodePoolResourceName("test-cluster", "main")
		err = fakeClient.Get(ctx, types.NamespacedName{Name: expectedName, Namespace: "default"}, shadowNodePool)
		assert.NoError(t, err, "shadow node pool should exist")

		// Assert top-level .Spec.NodeCount is set
		assert.Equal(t, int32(5), shadowNodePool.Spec.NodeCount, ".Spec.NodeCount (top-level) should be set to 5")

		// Assert embedded .Spec.HumioNodeSpec.NodeCount remains at its original value
		assert.Equal(t, ptr.To(int32(5)), shadowNodePool.Spec.HumioNodeSpec.NodeCount, ".Spec.HumioNodeSpec.NodeCount (embedded) should be set to 5")
	})

	t.Run("Both fields should coexist independently", func(t *testing.T) {
		hc := &humiov1alpha1.HumioCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-2",
				Namespace: "default",
				UID:       types.UID("test-cluster-2-uid"),
			},
			Spec: humiov1alpha1.HumioClusterSpec{
				HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
					NodeCount: ptr.To(int32(3)),
					Image:     "humio/humio:1.0.0",
				},
				OperatorFeatureFlags: humiov1alpha1.HumioOperatorFeatureFlags{
					EnableIndependentHumioNodePools: true,
				},
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(hc).
			WithStatusSubresource(&humiov1alpha1.HumioNodePool{}).
			Build()

		logger := logr.Discard()

		r := &HumioClusterReconciler{
			Client: fakeClient,
			Log:    logger,
		}

		err := r.ensureNodePoolResources(ctx, hc)
		assert.NoError(t, err, "ensureNodePoolResources should succeed")

		shadowNodePool := &humiov1alpha1.HumioNodePool{}
		expectedName := shadowNodePoolResourceName("test-cluster-2", "main")
		err = fakeClient.Get(ctx, types.NamespacedName{Name: expectedName, Namespace: "default"}, shadowNodePool)
		assert.NoError(t, err, "shadow node pool should exist")

		// Both fields should have the same value from the source
		assert.Equal(t, int32(3), shadowNodePool.Spec.NodeCount, ".Spec.NodeCount should be 3")
		assert.Equal(t, ptr.To(int32(3)), shadowNodePool.Spec.HumioNodeSpec.NodeCount, ".Spec.HumioNodeSpec.NodeCount should be 3")

		// Verify they are independent by checking their types and storage
		// Top-level is int32, embedded is *int32
		assert.IsType(t, int32(0), shadowNodePool.Spec.NodeCount, "top-level NodeCount should be int32")
		assert.IsType(t, (*int32)(nil), shadowNodePool.Spec.HumioNodeSpec.NodeCount, "embedded NodeCount should be *int32")
	})

	t.Run("nodeCount=0 should return replicas=0 boundary condition", func(t *testing.T) {
		hc := &humiov1alpha1.HumioCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-zero",
				Namespace: "default",
				UID:       types.UID("test-cluster-zero-uid"),
			},
			Spec: humiov1alpha1.HumioClusterSpec{
				HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
					NodeCount: ptr.To(int32(0)),
					Image:     "humio/humio:1.0.0",
				},
				OperatorFeatureFlags: humiov1alpha1.HumioOperatorFeatureFlags{
					EnableIndependentHumioNodePools: true,
				},
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(hc).
			WithStatusSubresource(&humiov1alpha1.HumioNodePool{}).
			Build()

		logger := logr.Discard()

		r := &HumioClusterReconciler{
			Client: fakeClient,
			Log:    logger,
		}

		// When nodeCount=0, main pool should not be created
		err := r.ensureNodePoolResources(ctx, hc)
		assert.NoError(t, err, "ensureNodePoolResources should succeed")

		nodePoolList := &humiov1alpha1.HumioNodePoolList{}
		err = fakeClient.List(ctx, nodePoolList)
		assert.NoError(t, err, "list should succeed")

		// No main pool should be created when NodeCount=0
		assert.Equal(t, 0, len(nodePoolList.Items), "no shadow node pool should be created when NodeCount=0")
	})
}

func TestShadowNodePool_NilNodeCountNoAutoscaling_SkipsMainPool(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = humiov1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	ctx := context.Background()

	// This test reproduces the reported bug: when enableIndependentHumioNodePools=true
	// and root spec has nil nodeCount, nil autoscaling, and NO storage configuration,
	// the operator must NOT create a main pool shadow. Previously the nil nodeCount
	// defaulted to 2 via effectiveMinReplicas, creating a shadow that failed validation:
	// "no storage configuration provided: exactly one of dataVolumeSource and
	// dataVolumePersistentVolumeClaimSpecTemplate must be set"
	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster-nopool",
			Namespace: "default",
			UID:       types.UID("test-cluster-nopool-uid"),
		},
		Spec: humiov1alpha1.HumioClusterSpec{
			HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
				NodeCount:   nil, // omitted — user manages all pods via named pools
				Autoscaling: nil, // no HPA on root
				Image:       "humio/humio:1.0.0",
				// Deliberately NO dataVolumeSource or dataVolumePersistentVolumeClaimSpecTemplate
			},
			OperatorFeatureFlags: humiov1alpha1.HumioOperatorFeatureFlags{
				EnableIndependentHumioNodePools: true,
			},
			NodePools: []humiov1alpha1.HumioNodePoolSpec{
				{Name: "ingest", HumioNodeSpec: humiov1alpha1.HumioNodeSpec{NodeCount: ptr.To(int32(2))}},
				{Name: "digest", HumioNodeSpec: humiov1alpha1.HumioNodeSpec{NodeCount: ptr.To(int32(3))}},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(hc).
		WithStatusSubresource(&humiov1alpha1.HumioNodePool{}).
		Build()

	r := &HumioClusterReconciler{
		Client: fakeClient,
		Log:    logr.Discard(),
	}

	err := r.ensureNodePoolResources(ctx, hc)
	assert.NoError(t, err, "ensureNodePoolResources should succeed without storage validation error")

	nodePoolList := &humiov1alpha1.HumioNodePoolList{}
	err = fakeClient.List(ctx, nodePoolList)
	assert.NoError(t, err)

	// Only the named pools should be created, not a main pool
	assert.Equal(t, 2, len(nodePoolList.Items), "should only create named pool shadows, not a main pool")

	names := map[string]bool{}
	for _, np := range nodePoolList.Items {
		names[np.Name] = true
	}
	assert.True(t, names["test-cluster-nopool-ingest"], "ingest pool should exist")
	assert.True(t, names["test-cluster-nopool-digest"], "digest pool should exist")
	assert.False(t, names["test-cluster-nopool"], "main pool shadow should NOT be created")
}

func TestShadowNodePool_NilNodeCountWithAutoscaling_CreatesMainPool(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = humiov1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	ctx := context.Background()

	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster-hpa2",
			Namespace: "default",
			UID:       types.UID("test-cluster-hpa2-uid"),
		},
		Spec: humiov1alpha1.HumioClusterSpec{
			HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
				NodeCount: nil, // HPA manages replicas
				Image:     "humio/humio:1.0.0",
				Autoscaling: &humiov1alpha1.AutoscalingSpec{
					MinReplicas: ptr.To(int32(2)),
					MaxReplicas: 10,
				},
			},
			OperatorFeatureFlags: humiov1alpha1.HumioOperatorFeatureFlags{
				EnableIndependentHumioNodePools: true,
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(hc).
		WithStatusSubresource(&humiov1alpha1.HumioNodePool{}).
		Build()

	r := &HumioClusterReconciler{
		Client: fakeClient,
		Log:    logr.Discard(),
	}

	err := r.ensureNodePoolResources(ctx, hc)
	assert.NoError(t, err, "ensureNodePoolResources should succeed")

	// Main pool shadow should be created because autoscaling is configured
	shadowNodePool := &humiov1alpha1.HumioNodePool{}
	expectedName := shadowNodePoolResourceName("test-cluster-hpa2", "main")
	err = fakeClient.Get(ctx, types.NamespacedName{Name: expectedName, Namespace: "default"}, shadowNodePool)
	assert.NoError(t, err, "main pool shadow should be created when nodeCount is nil but autoscaling is set")
	assert.NotNil(t, shadowNodePool.Spec.Autoscaling)
}

func TestShadowNodePool_ExplicitNodeCountPositive_CreatesMainPool(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = humiov1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	ctx := context.Background()

	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster-explicit",
			Namespace: "default",
			UID:       types.UID("test-cluster-explicit-uid"),
		},
		Spec: humiov1alpha1.HumioClusterSpec{
			HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
				NodeCount:   ptr.To(int32(3)),
				Autoscaling: nil, // no HPA, fixed size
				Image:       "humio/humio:1.0.0",
			},
			OperatorFeatureFlags: humiov1alpha1.HumioOperatorFeatureFlags{
				EnableIndependentHumioNodePools: true,
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(hc).
		WithStatusSubresource(&humiov1alpha1.HumioNodePool{}).
		Build()

	r := &HumioClusterReconciler{
		Client: fakeClient,
		Log:    logr.Discard(),
	}

	err := r.ensureNodePoolResources(ctx, hc)
	assert.NoError(t, err)

	shadowNodePool := &humiov1alpha1.HumioNodePool{}
	expectedName := shadowNodePoolResourceName("test-cluster-explicit", "main")
	err = fakeClient.Get(ctx, types.NamespacedName{Name: expectedName, Namespace: "default"}, shadowNodePool)
	assert.NoError(t, err, "main pool shadow should be created when nodeCount > 0")
	assert.Equal(t, int32(3), shadowNodePool.Spec.NodeCount)
}

func TestNodeCountOrDefault_NilWithNilAutoscaling_Returns2(t *testing.T) {
	// Documents the root cause of the storage validation bug:
	// nodeCountOrDefault(nil, nil) returns 2 (via effectiveMinReplicas fallback).
	// Without the condition fix, the main pool shadow would be created with nodeCount=2,
	// then ensureValidStorageConfiguration would fire because GetNodeCount()=2 > 0,
	// producing: "no storage configuration provided: exactly one of dataVolumeSource
	// and dataVolumePersistentVolumeClaimSpecTemplate must be set"
	//
	// The fix prevents this by not creating the shadow at all when nodeCount is nil
	// and autoscaling is nil.
	result := nodeCountOrDefault(nil, nil)
	assert.Equal(t, int32(2), result, "nodeCountOrDefault(nil, nil) returns 2 — this is why the shadow must not be created in this case")
}

func TestShadowNodePoolStatusPatch(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = humiov1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	ctx := context.Background()

	tests := []struct {
		name             string
		readyPodCount    int
		notReadyPodCount int
		expectedReplicas int32
		expectedSelector string
	}{
		{
			name:             "0 Ready pods",
			readyPodCount:    0,
			notReadyPodCount: 0,
			expectedReplicas: 0,
			expectedSelector: "app.kubernetes.io/instance=test-cluster,app.kubernetes.io/managed-by=humio-operator,app.kubernetes.io/name=humio,humio.com/node-pool=test-cluster",
		},
		{
			name:             "1 Ready pod",
			readyPodCount:    1,
			notReadyPodCount: 0,
			expectedReplicas: 1,
			expectedSelector: "app.kubernetes.io/instance=test-cluster,app.kubernetes.io/managed-by=humio-operator,app.kubernetes.io/name=humio,humio.com/node-pool=test-cluster",
		},
		{
			name:             "3 Ready pods, 1 NotReady",
			readyPodCount:    3,
			notReadyPodCount: 1,
			expectedReplicas: 3,
			expectedSelector: "app.kubernetes.io/instance=test-cluster,app.kubernetes.io/managed-by=humio-operator,app.kubernetes.io/name=humio,humio.com/node-pool=test-cluster",
		},
		{
			name:             "5 Ready pods",
			readyPodCount:    5,
			notReadyPodCount: 0,
			expectedReplicas: 5,
			expectedSelector: "app.kubernetes.io/instance=test-cluster,app.kubernetes.io/managed-by=humio-operator,app.kubernetes.io/name=humio,humio.com/node-pool=test-cluster",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hc := &humiov1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
					UID:       types.UID("test-cluster-uid"),
				},
				Spec: humiov1alpha1.HumioClusterSpec{
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount: ptr.To(int32(3)),
						Image:     "humio/humio:1.0.0",
					},
					OperatorFeatureFlags: humiov1alpha1.HumioOperatorFeatureFlags{
						EnableIndependentHumioNodePools: true,
					},
				},
			}

			// Create mock pods
			pods := []client.Object{}
			resourceName := shadowNodePoolResourceName("test-cluster", "main")
			podLabels := map[string]string{
				"app.kubernetes.io/instance":   "test-cluster",
				"app.kubernetes.io/managed-by": "humio-operator",
				"app.kubernetes.io/name":       "humio",
				"humio.com/node-pool":          resourceName,
			}

			// Create Ready pods
			for i := 0; i < tt.readyPodCount; i++ {
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pod-ready-" + string(rune(i)),
						Namespace: "default",
						Labels:    podLabels,
					},
					Status: corev1.PodStatus{
						Conditions: []corev1.PodCondition{
							{
								Type:   corev1.PodReady,
								Status: corev1.ConditionTrue,
							},
						},
					},
				}
				pods = append(pods, pod)
			}

			// Create NotReady pods
			for i := 0; i < tt.notReadyPodCount; i++ {
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pod-notready-" + string(rune(i)),
						Namespace: "default",
						Labels:    podLabels,
					},
					Status: corev1.PodStatus{
						Conditions: []corev1.PodCondition{
							{
								Type:   corev1.PodReady,
								Status: corev1.ConditionFalse,
							},
						},
					},
				}
				pods = append(pods, pod)
			}

			objects := []client.Object{hc}
			objects = append(objects, pods...)

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				WithStatusSubresource(&humiov1alpha1.HumioNodePool{}).
				Build()

			logger := logr.Discard()

			r := &HumioClusterReconciler{
				Client: fakeClient,
				Log:    logger,
			}

			err := r.ensureNodePoolResources(ctx, hc)
			assert.NoError(t, err, "ensureNodePoolResources should succeed")

			// Retrieve the shadow CR
			shadowNodePool := &humiov1alpha1.HumioNodePool{}
			err = fakeClient.Get(ctx, types.NamespacedName{Name: resourceName, Namespace: "default"}, shadowNodePool)
			assert.NoError(t, err, "shadow node pool should exist")

			// Assert .Status.CurrentReplicas
			assert.Equal(t, tt.expectedReplicas, shadowNodePool.Status.CurrentReplicas, "CurrentReplicas mismatch")

			// Assert .Status.Selector
			assert.Equal(t, tt.expectedSelector, shadowNodePool.Status.Selector, "Selector mismatch")
		})
	}
}

func TestShadowNodePoolStatusPatch_PodListingFailure(t *testing.T) {
	// TU-001: Logging test for pod listing failure requires either:
	// 1. A fake client that can inject errors on List operations (not available in fake.ClientBuilder)
	// 2. A custom client implementation
	// For now, we document this as an uncertainty and skip this specific error-path test.
	// The behavioral contract states: "Pod listing fails -> Existing .status.currentReplicas unchanged; error logged"
	// This test is deferred pending mock client support.
	t.Skip("Skipping pod listing failure test - requires injectable error mock client")
}

func TestShadowNodePoolIntegration(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = humiov1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	t.Run("Feature flag enabled with default + 2 pools creates 3 shadow CRs", func(t *testing.T) {
		hc := &humiov1alpha1.HumioCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-cluster",
				Namespace: "default",
				UID:       types.UID("uid-1"),
			},
			Spec: humiov1alpha1.HumioClusterSpec{
				HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
					NodeCount: ptr.To(int32(3)),
				},
				OperatorFeatureFlags: humiov1alpha1.HumioOperatorFeatureFlags{
					EnableIndependentHumioNodePools: true,
				},
				NodePools: []humiov1alpha1.HumioNodePoolSpec{
					{Name: "ingest", HumioNodeSpec: humiov1alpha1.HumioNodeSpec{NodeCount: ptr.To(int32(2))}},
					{Name: "digest", HumioNodeSpec: humiov1alpha1.HumioNodeSpec{NodeCount: ptr.To(int32(4))}},
				},
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(hc).
			WithStatusSubresource(&humiov1alpha1.HumioNodePool{}).
			Build()

		r := &HumioClusterReconciler{Client: fakeClient, Log: zap.New(zap.UseDevMode(true))}
		ctx := context.Background()

		err := r.ensureNodePoolResources(ctx, hc)
		assert.NoError(t, err)

		var shadowList humiov1alpha1.HumioNodePoolList
		err = fakeClient.List(ctx, &shadowList, client.InNamespace("default"))
		assert.NoError(t, err)
		assert.Equal(t, 3, len(shadowList.Items))

		names := map[string]bool{}
		for _, s := range shadowList.Items {
			names[s.Name] = true
		}
		assert.True(t, names["my-cluster"])
		assert.True(t, names["my-cluster-ingest"])
		assert.True(t, names["my-cluster-digest"])
	})

	t.Run("Feature flag disabled creates no shadow CRs", func(t *testing.T) {
		hc := &humiov1alpha1.HumioCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-cluster",
				Namespace: "default",
				UID:       types.UID("uid-2"),
			},
			Spec: humiov1alpha1.HumioClusterSpec{
				HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
					NodeCount: ptr.To(int32(3)),
				},
				OperatorFeatureFlags: humiov1alpha1.HumioOperatorFeatureFlags{
					EnableIndependentHumioNodePools: false,
				},
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(hc).
			WithStatusSubresource(&humiov1alpha1.HumioNodePool{}).
			Build()

		r := &HumioClusterReconciler{Client: fakeClient, Log: zap.New(zap.UseDevMode(true))}
		ctx := context.Background()

		err := r.ensureNodePoolResources(ctx, hc)
		assert.NoError(t, err)

		var shadowList humiov1alpha1.HumioNodePoolList
		err = fakeClient.List(ctx, &shadowList, client.InNamespace("default"))
		assert.NoError(t, err)
		assert.Equal(t, 0, len(shadowList.Items))
	})

	t.Run("Flag toggled true then false deletes all shadows", func(t *testing.T) {
		hc := &humiov1alpha1.HumioCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-cluster",
				Namespace: "default",
				UID:       types.UID("uid-3"),
			},
			Spec: humiov1alpha1.HumioClusterSpec{
				HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
					NodeCount: ptr.To(int32(3)),
				},
				OperatorFeatureFlags: humiov1alpha1.HumioOperatorFeatureFlags{
					EnableIndependentHumioNodePools: true,
				},
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(hc).
			WithStatusSubresource(&humiov1alpha1.HumioNodePool{}).
			Build()

		r := &HumioClusterReconciler{Client: fakeClient, Log: zap.New(zap.UseDevMode(true))}
		ctx := context.Background()

		err := r.ensureNodePoolResources(ctx, hc)
		assert.NoError(t, err)

		// Disable feature flag
		hc.Spec.OperatorFeatureFlags.EnableIndependentHumioNodePools = false
		err = r.ensureNodePoolResources(ctx, hc)
		assert.NoError(t, err)

		var shadowList humiov1alpha1.HumioNodePoolList
		err = fakeClient.List(ctx, &shadowList, client.InNamespace("default"))
		assert.NoError(t, err)
		assert.Equal(t, 0, len(shadowList.Items))
	})

	t.Run("Pool removed from spec triggers orphan cleanup", func(t *testing.T) {
		hc := &humiov1alpha1.HumioCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-cluster",
				Namespace: "default",
				UID:       types.UID("uid-4"),
			},
			Spec: humiov1alpha1.HumioClusterSpec{
				HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
					NodeCount: ptr.To(int32(3)),
				},
				OperatorFeatureFlags: humiov1alpha1.HumioOperatorFeatureFlags{
					EnableIndependentHumioNodePools: true,
				},
				NodePools: []humiov1alpha1.HumioNodePoolSpec{
					{Name: "ingest", HumioNodeSpec: humiov1alpha1.HumioNodeSpec{NodeCount: ptr.To(int32(2))}},
				},
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(hc).
			WithStatusSubresource(&humiov1alpha1.HumioNodePool{}).
			Build()

		r := &HumioClusterReconciler{Client: fakeClient, Log: zap.New(zap.UseDevMode(true))}
		ctx := context.Background()

		err := r.ensureNodePoolResources(ctx, hc)
		assert.NoError(t, err)

		// Remove ingest pool
		hc.Spec.NodePools = nil
		err = r.ensureNodePoolResources(ctx, hc)
		assert.NoError(t, err)

		var shadowList humiov1alpha1.HumioNodePoolList
		err = fakeClient.List(ctx, &shadowList, client.InNamespace("default"))
		assert.NoError(t, err)
		assert.Equal(t, 1, len(shadowList.Items))
		assert.Equal(t, "my-cluster", shadowList.Items[0].Name)
	})

	t.Run("nodeCount changed updates shadow CR", func(t *testing.T) {
		hc := &humiov1alpha1.HumioCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-cluster",
				Namespace: "default",
				UID:       types.UID("uid-5"),
			},
			Spec: humiov1alpha1.HumioClusterSpec{
				HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
					NodeCount: ptr.To(int32(3)),
				},
				OperatorFeatureFlags: humiov1alpha1.HumioOperatorFeatureFlags{
					EnableIndependentHumioNodePools: true,
				},
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(hc).
			WithStatusSubresource(&humiov1alpha1.HumioNodePool{}).
			Build()

		r := &HumioClusterReconciler{Client: fakeClient, Log: zap.New(zap.UseDevMode(true))}
		ctx := context.Background()

		err := r.ensureNodePoolResources(ctx, hc)
		assert.NoError(t, err)

		// Change nodeCount
		hc.Spec.NodeCount = ptr.To(int32(7))
		err = r.ensureNodePoolResources(ctx, hc)
		assert.NoError(t, err)

		var shadow humiov1alpha1.HumioNodePool
		err = fakeClient.Get(ctx, types.NamespacedName{Name: "my-cluster", Namespace: "default"}, &shadow)
		assert.NoError(t, err)
		assert.Equal(t, int32(7), shadow.Spec.NodeCount)
	})

	t.Run("OwnerReference points to parent HumioCluster", func(t *testing.T) {
		hc := &humiov1alpha1.HumioCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-cluster",
				Namespace: "default",
				UID:       types.UID("uid-6"),
			},
			Spec: humiov1alpha1.HumioClusterSpec{
				HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
					NodeCount: ptr.To(int32(3)),
				},
				OperatorFeatureFlags: humiov1alpha1.HumioOperatorFeatureFlags{
					EnableIndependentHumioNodePools: true,
				},
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(hc).
			WithStatusSubresource(&humiov1alpha1.HumioNodePool{}).
			Build()

		r := &HumioClusterReconciler{Client: fakeClient, Log: zap.New(zap.UseDevMode(true))}
		ctx := context.Background()

		err := r.ensureNodePoolResources(ctx, hc)
		assert.NoError(t, err)

		var shadow humiov1alpha1.HumioNodePool
		err = fakeClient.Get(ctx, types.NamespacedName{Name: "my-cluster", Namespace: "default"}, &shadow)
		assert.NoError(t, err)
		assert.Len(t, shadow.OwnerReferences, 1)
		assert.Equal(t, types.UID("uid-6"), shadow.OwnerReferences[0].UID)
		assert.Equal(t, "my-cluster", shadow.OwnerReferences[0].Name)
	})

	t.Run("Default pool nodeCount from HumioCluster.Spec.NodeCount", func(t *testing.T) {
		hc := &humiov1alpha1.HumioCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-cluster",
				Namespace: "default",
				UID:       types.UID("uid-7"),
			},
			Spec: humiov1alpha1.HumioClusterSpec{
				HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
					NodeCount: ptr.To(int32(7)),
				},
				OperatorFeatureFlags: humiov1alpha1.HumioOperatorFeatureFlags{
					EnableIndependentHumioNodePools: true,
				},
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(hc).
			WithStatusSubresource(&humiov1alpha1.HumioNodePool{}).
			Build()

		r := &HumioClusterReconciler{Client: fakeClient, Log: zap.New(zap.UseDevMode(true))}
		ctx := context.Background()

		err := r.ensureNodePoolResources(ctx, hc)
		assert.NoError(t, err)

		var shadow humiov1alpha1.HumioNodePool
		err = fakeClient.Get(ctx, types.NamespacedName{Name: "my-cluster", Namespace: "default"}, &shadow)
		assert.NoError(t, err)
		assert.Equal(t, int32(7), shadow.Spec.NodeCount)
	})
}

func TestShadowNodePoolImmutability(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = humiov1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: "default",
			UID:       types.UID("uid-immut"),
		},
		Spec: humiov1alpha1.HumioClusterSpec{
			HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
				NodeCount: ptr.To(int32(3)),
			},
			OperatorFeatureFlags: humiov1alpha1.HumioOperatorFeatureFlags{
				EnableIndependentHumioNodePools: true,
			},
			NodePools: []humiov1alpha1.HumioNodePoolSpec{
				{Name: "ingest", HumioNodeSpec: humiov1alpha1.HumioNodeSpec{NodeCount: ptr.To(int32(2))}},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(hc).
		WithStatusSubresource(&humiov1alpha1.HumioNodePool{}).
		Build()

	r := &HumioClusterReconciler{Client: fakeClient, Log: zap.New(zap.UseDevMode(true))}
	ctx := context.Background()

	err := r.ensureNodePoolResources(ctx, hc)
	assert.NoError(t, err)

	// Verify initial values
	var shadow humiov1alpha1.HumioNodePool
	err = fakeClient.Get(ctx, types.NamespacedName{Name: "my-cluster-ingest", Namespace: "default"}, &shadow)
	assert.NoError(t, err)
	assert.Equal(t, "my-cluster", shadow.Spec.ClusterName)
	assert.Equal(t, "ingest", shadow.Spec.Name)
	assert.Equal(t, int32(2), shadow.Spec.NodeCount)

	// Re-sync with different nodeCount (simulating nodeCount update)
	hc.Spec.NodePools[0].HumioNodeSpec.NodeCount = ptr.To(int32(5))
	err = r.ensureNodePoolResources(ctx, hc)
	assert.NoError(t, err)

	// Verify clusterName and poolName are unchanged, only nodeCount updated
	err = fakeClient.Get(ctx, types.NamespacedName{Name: "my-cluster-ingest", Namespace: "default"}, &shadow)
	assert.NoError(t, err)
	assert.Equal(t, "my-cluster", shadow.Spec.ClusterName, "clusterName must be immutable after creation")
	assert.Equal(t, "ingest", shadow.Spec.Name, "poolName must be immutable after creation")
	assert.Equal(t, int32(5), shadow.Spec.NodeCount, "nodeCount should be updated")
}

func TestHumioNodePoolStatusNoDesiredReplicas(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = humiov1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
			UID:       types.UID("test-uid"),
		},
		Spec: humiov1alpha1.HumioClusterSpec{
			HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
				NodeCount: ptr.To(int32(3)),
			},
			OperatorFeatureFlags: humiov1alpha1.HumioOperatorFeatureFlags{
				EnableIndependentHumioNodePools: true,
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(hc).
		WithStatusSubresource(&humiov1alpha1.HumioNodePool{}).
		Build()

	r := &HumioClusterReconciler{
		Client: fakeClient,
		Log:    zap.New(zap.UseDevMode(true)),
	}

	ctx := context.Background()
	err := r.ensureNodePoolResources(ctx, hc)
	assert.NoError(t, err)

	var shadowList humiov1alpha1.HumioNodePoolList
	err = fakeClient.List(ctx, &shadowList, client.InNamespace("default"))
	assert.NoError(t, err)
	assert.NotEmpty(t, shadowList.Items)

	for _, shadow := range shadowList.Items {
		assert.Equal(t, int32(0), shadow.Status.DesiredReplicas,
			"DesiredReplicas must not be set by shadow sync in Phase 1")
	}
}

func TestShadowNodePoolDefaultNaming(t *testing.T) {
	t.Run("main pool returns clusterName without hyphen", func(t *testing.T) {
		result := shadowNodePoolResourceName("prod", "main")
		assert.Equal(t, "prod", result, "main pool should return just clusterName without hyphen")
	})

	t.Run("non-default pool returns clusterName-poolName", func(t *testing.T) {
		result := shadowNodePoolResourceName("prod", "workers")
		assert.Equal(t, "prod-workers", result, "non-default pool should return clusterName-poolName")
	})

	t.Run("another non-default pool", func(t *testing.T) {
		result := shadowNodePoolResourceName("staging", "ingest")
		assert.Equal(t, "staging-ingest", result, "non-default pool should return clusterName-poolName")
	})
}

func TestShadowNodePoolPodListFailure(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = humiov1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	ctx := context.Background()

	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
			UID:       types.UID("test-cluster-uid"),
		},
		Spec: humiov1alpha1.HumioClusterSpec{
			HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
				NodeCount: ptr.To(int32(3)),
				Image:     "humio/humio:1.0.0",
			},
			OperatorFeatureFlags: humiov1alpha1.HumioOperatorFeatureFlags{
				EnableIndependentHumioNodePools: true,
			},
		},
	}

	// Create the shadow node pool with pre-existing status
	resourceName := shadowNodePoolResourceName("test-cluster", "main")
	shadowNodePool := &humiov1alpha1.HumioNodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName,
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "core.humio.com/v1alpha1",
				Kind:       "HumioCluster",
				Name:       "test-cluster",
				UID:        types.UID("test-cluster-uid"),
			}},
			Annotations: map[string]string{
				annotationManagedBy: shadowNodePoolManagedBy,
				annotationCluster:   "test-cluster",
			},
		},
		Spec: humiov1alpha1.HumioNodePoolSpec{
			Name:        "main",
			ClusterName: "test-cluster",
			NodeCount:   3,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app.kubernetes.io/instance":   "test-cluster",
					"app.kubernetes.io/managed-by": "humio-operator",
					"app.kubernetes.io/name":       "humio",
					"humio.com/node-pool":          resourceName,
				},
			},
		},
		Status: humiov1alpha1.HumioNodePoolStatus{
			CurrentReplicas: 2, // Pre-existing value
		},
	}

	// Create a fake client that will fail on List operations for PodList
	fakeClient := &errorInjectingClient{
		Client: fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(hc, shadowNodePool).
			WithStatusSubresource(&humiov1alpha1.HumioNodePool{}).
			Build(),
		failOnPodList: true,
	}

	logBuffer := &logBuffer{entries: []string{}}
	logger := zap.New(zap.WriteTo(logBuffer), zap.UseDevMode(true))

	r := &HumioClusterReconciler{
		Client: fakeClient,
		Log:    logger,
	}

	// Call ensureNodePoolResources which internally calls patchShadowNodePoolStatus
	err := r.ensureNodePoolResources(ctx, hc)

	// CRITICAL: Must return nil, not an error
	assert.NoError(t, err, "ensureNodePoolResources must return nil when pod listing fails")

	// Verify error was logged
	foundLog := false
	for _, entry := range logBuffer.entries {
		if strings.Contains(entry, "failed to list pods for node pool") &&
			strings.Contains(entry, resourceName) &&
			strings.Contains(entry, "shadow-node-pool-sync") {
			foundLog = true
			break
		}
	}
	assert.True(t, foundLog, "expected error log for pod listing failure")

	// Verify status was not modified (should still be 2, not 0 or any other value)
	updatedNodePool := &humiov1alpha1.HumioNodePool{}
	err = fakeClient.Get(ctx, types.NamespacedName{Name: resourceName, Namespace: "default"}, updatedNodePool)
	assert.NoError(t, err)
	assert.Equal(t, int32(2), updatedNodePool.Status.CurrentReplicas, "status should remain unchanged when pod listing fails")
}

// errorInjectingClient wraps a client and injects errors for specific operations
type errorInjectingClient struct {
	client.Client
	failOnPodList  bool
	failOnUpdate   bool
	updateErrorMsg string
}

func (c *errorInjectingClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if c.failOnPodList {
		if _, ok := list.(*corev1.PodList); ok {
			return fmt.Errorf("injected error: pod list operation failed")
		}
	}
	return c.Client.List(ctx, list, opts...)
}

func (c *errorInjectingClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	return c.Client.Get(ctx, key, obj, opts...)
}

func (c *errorInjectingClient) Status() client.StatusWriter {
	return c.Client.Status()
}

func (c *errorInjectingClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if c.failOnUpdate {
		return fmt.Errorf("%s", c.updateErrorMsg)
	}
	return c.Client.Update(ctx, obj, opts...)
}

func TestForwardSyncAutoscaling(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = humiov1alpha1.AddToScheme(scheme)

	tests := []struct {
		name                string
		parentAutoscaling   *humiov1alpha1.AutoscalingSpec
		shadowAutoscaling   *humiov1alpha1.AutoscalingSpec
		expectedAutoscaling *humiov1alpha1.AutoscalingSpec
	}{
		{
			name:                "mirror parent autoscaling",
			parentAutoscaling:   &humiov1alpha1.AutoscalingSpec{MinReplicas: ptr.To(int32(2)), MaxReplicas: 10},
			shadowAutoscaling:   nil,
			expectedAutoscaling: &humiov1alpha1.AutoscalingSpec{MinReplicas: ptr.To(int32(2)), MaxReplicas: 10},
		},
		{
			name:                "clear stale autoscaling",
			parentAutoscaling:   nil,
			shadowAutoscaling:   &humiov1alpha1.AutoscalingSpec{MinReplicas: ptr.To(int32(2)), MaxReplicas: 10},
			expectedAutoscaling: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shadow := &humiov1alpha1.HumioNodePool{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pool", Namespace: "default"},
				Spec: humiov1alpha1.HumioNodePoolSpec{
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						Autoscaling: tt.shadowAutoscaling,
					},
				},
			}
			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(shadow).Build()

			err := forwardSyncAutoscaling(context.Background(), cl, shadow, tt.parentAutoscaling)
			assert.NoError(t, err)

			updated := &humiov1alpha1.HumioNodePool{}
			err = cl.Get(context.Background(), types.NamespacedName{Name: "test-pool", Namespace: "default"}, updated)
			assert.NoError(t, err)

			if tt.expectedAutoscaling == nil {
				assert.Nil(t, updated.Spec.Autoscaling)
			} else {
				assert.NotNil(t, updated.Spec.Autoscaling)
				assert.Equal(t, tt.expectedAutoscaling.MaxReplicas, updated.Spec.Autoscaling.MaxReplicas)
				if tt.expectedAutoscaling.MinReplicas != nil {
					assert.Equal(t, *tt.expectedAutoscaling.MinReplicas, *updated.Spec.Autoscaling.MinReplicas)
				}
			}
		})
	}
}

func TestForwardSyncAutoscaling_UpdateError(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = humiov1alpha1.AddToScheme(scheme)

	shadow := &humiov1alpha1.HumioNodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pool", Namespace: "default"},
		Spec:       humiov1alpha1.HumioNodePoolSpec{},
	}

	cl := &errorInjectingClient{
		Client:         fake.NewClientBuilder().WithScheme(scheme).WithObjects(shadow).Build(),
		failOnPodList:  false,
		failOnUpdate:   true,
		updateErrorMsg: "API error",
	}

	err := forwardSyncAutoscaling(context.Background(), cl, shadow, &humiov1alpha1.AutoscalingSpec{MaxReplicas: 10})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "forward-sync autoscaling failed")
}

func TestForwardSyncNodeCount_Conditional(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = humiov1alpha1.AddToScheme(scheme)

	tests := []struct {
		name              string
		parentNodeCount   *int32
		shadowNodeCount   int32
		expectedNodeCount int32
		shouldOverwrite   bool
	}{
		{
			name:              "explicit value overwrites",
			parentNodeCount:   ptr.To(int32(5)),
			shadowNodeCount:   7,
			expectedNodeCount: 5,
			shouldOverwrite:   true,
		},
		{
			name:              "nil preserves shadow",
			parentNodeCount:   nil,
			shadowNodeCount:   7,
			expectedNodeCount: 7,
			shouldOverwrite:   false,
		},
		{
			name:              "explicit zero overwrites",
			parentNodeCount:   ptr.To(int32(0)),
			shadowNodeCount:   3,
			expectedNodeCount: 0,
			shouldOverwrite:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shadow := &humiov1alpha1.HumioNodePool{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pool", Namespace: "default"},
				Spec:       humiov1alpha1.HumioNodePoolSpec{NodeCount: tt.shadowNodeCount},
			}
			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(shadow).Build()

			err := forwardSyncNodeCount(context.Background(), cl, shadow, tt.parentNodeCount)
			assert.NoError(t, err)

			updated := &humiov1alpha1.HumioNodePool{}
			err = cl.Get(context.Background(), types.NamespacedName{Name: "test-pool", Namespace: "default"}, updated)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedNodeCount, updated.Spec.NodeCount)
		})
	}
}

func TestReverseSyncWithClamping(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = humiov1alpha1.AddToScheme(scheme)

	tests := []struct {
		name                  string
		shadowNodeCount       int32
		autoscaling           *humiov1alpha1.AutoscalingSpec
		expectedDesired       int32
		expectedShadowCorrect int32
		shouldClamp           bool
	}{
		{
			name:                  "in bounds",
			shadowNodeCount:       7,
			autoscaling:           &humiov1alpha1.AutoscalingSpec{MinReplicas: ptr.To(int32(3)), MaxReplicas: 10},
			expectedDesired:       7,
			expectedShadowCorrect: 7,
			shouldClamp:           false,
		},
		{
			name:                  "above max",
			shadowNodeCount:       12,
			autoscaling:           &humiov1alpha1.AutoscalingSpec{MinReplicas: ptr.To(int32(3)), MaxReplicas: 10},
			expectedDesired:       10,
			expectedShadowCorrect: 10,
			shouldClamp:           true,
		},
		{
			name:                  "below min",
			shadowNodeCount:       1,
			autoscaling:           &humiov1alpha1.AutoscalingSpec{MinReplicas: ptr.To(int32(3)), MaxReplicas: 10},
			expectedDesired:       3,
			expectedShadowCorrect: 3,
			shouldClamp:           true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shadow := &humiov1alpha1.HumioNodePool{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pool", Namespace: "default"},
				Spec: humiov1alpha1.HumioNodePoolSpec{
					NodeCount: tt.shadowNodeCount,
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						Autoscaling: tt.autoscaling,
					},
				},
			}
			cluster := &humiov1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
				Status: humiov1alpha1.HumioClusterStatus{
					NodePoolStatus: humiov1alpha1.HumioNodePoolStatusList{
						{Name: "test-pool"},
					},
				},
			}
			cl := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(shadow, cluster).
				WithStatusSubresource(&humiov1alpha1.HumioCluster{}, &humiov1alpha1.HumioNodePool{}).
				Build()

			err := reverseSyncNodeCount(context.Background(), cl, cluster, shadow, "test-pool")
			assert.NoError(t, err)

			// Check cluster status
			updated := &humiov1alpha1.HumioCluster{}
			err = cl.Get(context.Background(), types.NamespacedName{Name: "test-cluster", Namespace: "default"}, updated)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedDesired, updated.Status.NodePoolStatus[0].DesiredReplicas)

			// Check shadow correction
			updatedShadow := &humiov1alpha1.HumioNodePool{}
			err = cl.Get(context.Background(), types.NamespacedName{Name: "test-pool", Namespace: "default"}, updatedShadow)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedShadowCorrect, updatedShadow.Spec.NodeCount)
		})
	}
}

func TestReverseSyncNodeCount_PoolNotFound(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = humiov1alpha1.AddToScheme(scheme)

	shadow := &humiov1alpha1.HumioNodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pool", Namespace: "default"},
		Spec: humiov1alpha1.HumioNodePoolSpec{
			NodeCount: 7,
			HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
				Autoscaling: &humiov1alpha1.AutoscalingSpec{MinReplicas: ptr.To(int32(3)), MaxReplicas: 10},
			},
		},
	}
	cluster := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Status: humiov1alpha1.HumioClusterStatus{
			NodePoolStatus: humiov1alpha1.HumioNodePoolStatusList{
				{Name: "other-pool"},
			},
		},
	}
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(shadow, cluster).
		WithStatusSubresource(&humiov1alpha1.HumioCluster{}).
		Build()

	err := reverseSyncNodeCount(context.Background(), cl, cluster, shadow, "test-pool")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reverse-sync")
	assert.Contains(t, err.Error(), "test-pool")
}

func TestStalenessTracking(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = humiov1alpha1.AddToScheme(scheme)

	cluster := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Status: humiov1alpha1.HumioClusterStatus{
			NodePoolStatus: humiov1alpha1.HumioNodePoolStatusList{
				{Name: "test-pool", DesiredReplicas: 7},
			},
		},
	}

	// No shadow CR — simulates not found
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		WithStatusSubresource(&humiov1alpha1.HumioCluster{}).
		Build()

	counter := &stalenessCounter{counts: make(map[string]int)}

	// First failure
	err := reverseSyncWithStalenessTracking(context.Background(), cl, cluster, "test-pool", counter, nil)
	assert.Error(t, err)
	counter.mu.RLock()
	assert.Equal(t, 1, counter.counts["test-pool"])
	counter.mu.RUnlock()

	// Failures 2-5 (up to threshold)
	for i := 0; i < 4; i++ {
		_ = reverseSyncWithStalenessTracking(context.Background(), cl, cluster, "test-pool", counter, nil)
	}
	counter.mu.RLock()
	assert.Equal(t, 5, counter.counts["test-pool"])
	counter.mu.RUnlock()

	// Status.desiredReplicas should remain 7 (last-known)
	updated := &humiov1alpha1.HumioCluster{}
	_ = cl.Get(context.Background(), types.NamespacedName{Name: "test-cluster", Namespace: "default"}, updated)
	assert.Equal(t, int32(7), updated.Status.NodePoolStatus[0].DesiredReplicas)
}

func TestStalenessTracking_ResetOnSuccess(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = humiov1alpha1.AddToScheme(scheme)

	// Shadow CR name must match shadowNodePoolResourceName(cluster.Name, poolName)
	shadow := &humiov1alpha1.HumioNodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster-test-pool", Namespace: "default"},
		Spec: humiov1alpha1.HumioNodePoolSpec{
			NodeCount: 5,
			HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
				Autoscaling: &humiov1alpha1.AutoscalingSpec{MinReplicas: ptr.To(int32(2)), MaxReplicas: 10},
			},
		},
	}
	cluster := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Status: humiov1alpha1.HumioClusterStatus{
			NodePoolStatus: humiov1alpha1.HumioNodePoolStatusList{
				{Name: "test-pool", DesiredReplicas: 3},
			},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(shadow, cluster).
		WithStatusSubresource(&humiov1alpha1.HumioCluster{}, &humiov1alpha1.HumioNodePool{}).
		Build()

	counter := &stalenessCounter{counts: make(map[string]int)}
	counter.counts["test-pool"] = 3 // simulate prior failures

	// Successful read should reset counter
	err := reverseSyncWithStalenessTracking(context.Background(), cl, cluster, "test-pool", counter, nil)
	assert.NoError(t, err)

	counter.mu.RLock()
	assert.Equal(t, 0, counter.counts["test-pool"])
	counter.mu.RUnlock()
}

func TestStalenessTracking_UsesCorrectShadowName(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = humiov1alpha1.AddToScheme(scheme)

	// Shadow CR uses the actual resource name (clusterName-poolName)
	shadow := &humiov1alpha1.HumioNodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster-workers", Namespace: "default"},
		Spec: humiov1alpha1.HumioNodePoolSpec{
			NodeCount: 5,
			HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
				Autoscaling: &humiov1alpha1.AutoscalingSpec{MinReplicas: ptr.To(int32(2)), MaxReplicas: 10},
			},
		},
	}
	cluster := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Status: humiov1alpha1.HumioClusterStatus{
			NodePoolStatus: humiov1alpha1.HumioNodePoolStatusList{
				{Name: "workers", DesiredReplicas: 3},
			},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(shadow, cluster).
		WithStatusSubresource(&humiov1alpha1.HumioCluster{}, &humiov1alpha1.HumioNodePool{}).
		Build()

	counter := &stalenessCounter{counts: make(map[string]int)}

	// Reverse-sync must use shadowNodePoolResourceName to construct lookup key
	err := reverseSyncWithStalenessTracking(context.Background(), cl, cluster, "workers", counter, nil)
	assert.NoError(t, err)

	counter.mu.RLock()
	assert.Equal(t, 0, counter.counts["workers"], "counter should be 0 after successful read")
	counter.mu.RUnlock()
}

func TestEnsureShadowNodePool_PreservesHPANodeCount(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = humiov1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	ctx := context.Background()

	// Parent cluster with nodeCount=nil (HPA-managed mode)
	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
			UID:       types.UID("test-cluster-uid"),
		},
		Spec: humiov1alpha1.HumioClusterSpec{
			HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
				NodeCount: nil, // HPA-managed
				Image:     "humio/humio:1.0.0",
			},
			OperatorFeatureFlags: humiov1alpha1.HumioOperatorFeatureFlags{
				EnableIndependentHumioNodePools: true,
			},
		},
	}

	// Existing shadow with nodeCount=7 (set by HPA)
	resourceName := shadowNodePoolResourceName("test-cluster", "main")
	shadowNodePool := &humiov1alpha1.HumioNodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName,
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "core.humio.com/v1alpha1",
				Kind:       "HumioCluster",
				Name:       "test-cluster",
				UID:        types.UID("test-cluster-uid"),
			}},
			Annotations: map[string]string{
				annotationManagedBy: shadowNodePoolManagedBy,
				annotationCluster:   "test-cluster",
			},
		},
		Spec: humiov1alpha1.HumioNodePoolSpec{
			Name:        "main",
			ClusterName: "test-cluster",
			NodeCount:   7, // HPA-set value
			HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
				NodeCount: nil,
				Image:     "humio/humio:1.0.0",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(hc, shadowNodePool).
		WithStatusSubresource(&humiov1alpha1.HumioNodePool{}).
		Build()

	r := &HumioClusterReconciler{
		Client: fakeClient,
		Log:    logr.Discard(),
	}

	// Run ensureShadowNodePool
	// This should NOT overwrite nodeCount=7 since parent has nil
	err := r.ensureShadowNodePool(ctx, hc, mainNodePoolName, hc.Spec.HumioNodeSpec)
	assert.NoError(t, err)

	// Verify shadow still has nodeCount=7
	updatedShadow := &humiov1alpha1.HumioNodePool{}
	err = fakeClient.Get(ctx, types.NamespacedName{Name: resourceName, Namespace: "default"}, updatedShadow)
	assert.NoError(t, err)
	assert.Equal(t, int32(7), updatedShadow.Spec.NodeCount, "shadow nodeCount should be preserved when parent is nil")
}

func TestForwardSyncNodeCount_NilNodeCountPreservesShadowForNodePool(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = humiov1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	ctx := context.Background()

	// Create HumioCluster with a node pool that has nil NodeCount in HumioNodeSpec (HPA-managed mode)
	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
			UID:       types.UID("test-cluster-uid"),
		},
		Spec: humiov1alpha1.HumioClusterSpec{
			HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
				NodeCount: ptr.To(int32(3)),
			},
			OperatorFeatureFlags: humiov1alpha1.HumioOperatorFeatureFlags{
				EnableIndependentHumioNodePools: true,
			},
			NodePools: []humiov1alpha1.HumioNodePoolSpec{
				{
					Name: "hpa-managed",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount: nil, // HPA-managed mode
						Autoscaling: &humiov1alpha1.AutoscalingSpec{
							MinReplicas: ptr.To(int32(2)),
							MaxReplicas: 10,
						},
					},
					NodeCount: 5, // Scale subresource field (should not affect forward sync)
				},
			},
		},
		Status: humiov1alpha1.HumioClusterStatus{
			NodePoolStatus: humiov1alpha1.HumioNodePoolStatusList{
				{Name: "main", DesiredReplicas: 3},
				{Name: "hpa-managed", DesiredReplicas: 8},
			},
		},
	}

	// Create shadow with existing nodeCount set by HPA
	mainShadow := &humiov1alpha1.HumioNodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "core.humio.com/v1alpha1",
				Kind:       "HumioCluster",
				Name:       "test-cluster",
				UID:        types.UID("test-cluster-uid"),
			}},
			Annotations: map[string]string{
				annotationManagedBy: shadowNodePoolManagedBy,
				annotationCluster:   "test-cluster",
			},
		},
		Spec: humiov1alpha1.HumioNodePoolSpec{
			Name:        "main",
			ClusterName: "test-cluster",
			NodeCount:   3,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app.kubernetes.io/instance":   "test-cluster",
					"app.kubernetes.io/managed-by": "humio-operator",
					"app.kubernetes.io/name":       "humio",
					"humio.com/node-pool":          "test-cluster",
				},
			},
			HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
				NodeCount: ptr.To(int32(3)),
			},
		},
	}

	shadowNodePool := &humiov1alpha1.HumioNodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster-hpa-managed",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "core.humio.com/v1alpha1",
				Kind:       "HumioCluster",
				Name:       "test-cluster",
				UID:        types.UID("test-cluster-uid"),
			}},
			Annotations: map[string]string{
				annotationManagedBy: shadowNodePoolManagedBy,
				annotationCluster:   "test-cluster",
			},
		},
		Spec: humiov1alpha1.HumioNodePoolSpec{
			Name:        "hpa-managed",
			ClusterName: "test-cluster",
			NodeCount:   8, // HPA set this to 8
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app.kubernetes.io/instance":   "test-cluster",
					"app.kubernetes.io/managed-by": "humio-operator",
					"app.kubernetes.io/name":       "humio",
					"humio.com/node-pool":          "test-cluster-hpa-managed",
				},
			},
			HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
				NodeCount: nil,
				Autoscaling: &humiov1alpha1.AutoscalingSpec{
					MinReplicas: ptr.To(int32(2)),
					MaxReplicas: 10,
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(hc, mainShadow, shadowNodePool).
		WithStatusSubresource(&humiov1alpha1.HumioNodePool{}, &humiov1alpha1.HumioCluster{}).
		Build()

	logger := logr.Discard()

	r := &HumioClusterReconciler{
		Client:            fakeClient,
		Log:               logger,
		stalenessCounters: &stalenessCounter{counts: make(map[string]int)},
	}

	// Run ensureNodePoolResources which should call forwardSyncNodeCount
	err := r.ensureNodePoolResources(ctx, hc)
	assert.NoError(t, err, "ensureNodePoolResources should succeed")

	// Retrieve shadow and verify nodeCount was NOT overwritten
	updatedShadow := &humiov1alpha1.HumioNodePool{}
	err = fakeClient.Get(ctx, types.NamespacedName{Name: "test-cluster-hpa-managed", Namespace: "default"}, updatedShadow)
	assert.NoError(t, err, "shadow node pool should exist")

	// CRITICAL: When parent NodeCount is nil, shadow should preserve HPA-set value (8)
	assert.Equal(t, int32(8), updatedShadow.Spec.NodeCount,
		"shadow nodeCount must NOT be overwritten when parent HumioNodeSpec.NodeCount is nil (HPA-managed mode)")
}

func TestShadowNodePool_MainPoolNilNodeCountCreatesHPAManagedShadow(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = humiov1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	ctx := context.Background()

	// HumioCluster with nodeCount=nil (HPA-managed mode)
	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster-hpa",
			Namespace: "default",
			UID:       types.UID("test-cluster-hpa-uid"),
		},
		Spec: humiov1alpha1.HumioClusterSpec{
			HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
				NodeCount: nil, // HPA-managed
				Image:     "humio/humio:1.0.0",
				Autoscaling: &humiov1alpha1.AutoscalingSpec{
					MinReplicas: ptr.To(int32(2)),
					MaxReplicas: 10,
				},
			},
			OperatorFeatureFlags: humiov1alpha1.HumioOperatorFeatureFlags{
				EnableIndependentHumioNodePools: true,
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(hc).
		WithStatusSubresource(&humiov1alpha1.HumioNodePool{}).
		Build()

	logger := logr.Discard()

	r := &HumioClusterReconciler{
		Client: fakeClient,
		Log:    logger,
	}

	err := r.ensureNodePoolResources(ctx, hc)
	assert.NoError(t, err, "ensureNodePoolResources should succeed")

	// Verify shadow was created
	shadowNodePool := &humiov1alpha1.HumioNodePool{}
	expectedName := shadowNodePoolResourceName("test-cluster-hpa", "main")
	err = fakeClient.Get(ctx, types.NamespacedName{Name: expectedName, Namespace: "default"}, shadowNodePool)
	assert.NoError(t, err, "main pool shadow should be created when NodeCount is nil (HPA mode)")

	// Verify shadow has autoscaling spec
	assert.NotNil(t, shadowNodePool.Spec.Autoscaling, "shadow should have autoscaling spec")
	assert.Equal(t, int32(10), shadowNodePool.Spec.Autoscaling.MaxReplicas)
}

func TestReverseSyncNodeCount_SkipsClampingWhenAutoscalingNil(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = humiov1alpha1.AddToScheme(scheme)

	// Shadow has autoscaling=nil and nodeCount=5 (set by user)
	// Without fix: effectiveMin=2, effectiveMax=2, result clamped to 2
	// With fix: skip clamping entirely, result=5
	shadow := &humiov1alpha1.HumioNodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pool", Namespace: "default"},
		Spec: humiov1alpha1.HumioNodePoolSpec{
			NodeCount: 5,
			HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
				Autoscaling: nil, // No autoscaling configured
			},
		},
	}
	cluster := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Status: humiov1alpha1.HumioClusterStatus{
			NodePoolStatus: humiov1alpha1.HumioNodePoolStatusList{
				{Name: "test-pool", DesiredReplicas: 0},
			},
		},
	}
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(shadow, cluster).
		WithStatusSubresource(&humiov1alpha1.HumioCluster{}, &humiov1alpha1.HumioNodePool{}).
		Build()

	err := reverseSyncNodeCount(context.Background(), cl, cluster, shadow, "test-pool")
	assert.NoError(t, err)

	// Verify cluster status shows desiredReplicas=5 (not clamped to 2)
	updated := &humiov1alpha1.HumioCluster{}
	err = cl.Get(context.Background(), types.NamespacedName{Name: "test-cluster", Namespace: "default"}, updated)
	assert.NoError(t, err)
	assert.Equal(t, int32(5), updated.Status.NodePoolStatus[0].DesiredReplicas, "desiredReplicas should not be clamped when autoscaling is nil")

	// Verify shadow nodeCount remains unchanged
	updatedShadow := &humiov1alpha1.HumioNodePool{}
	err = cl.Get(context.Background(), types.NamespacedName{Name: "test-pool", Namespace: "default"}, updatedShadow)
	assert.NoError(t, err)
	assert.Equal(t, int32(5), updatedShadow.Spec.NodeCount, "shadow nodeCount should not be corrected when autoscaling is nil")
}
