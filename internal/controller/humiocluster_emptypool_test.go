package controller

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestEnsureEmptyNodePoolHasNoPods_NoOp_WhenCountAboveZero(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = humiov1alpha1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &HumioClusterReconciler{Client: fakeClient, Log: logr.Discard()}

	hnp := &HumioNodePool{
		namespace:   "default",
		clusterName: "test-cluster",
		humioNodeSpec: humiov1alpha1.HumioNodeSpec{
			NodeCount: ptr.To(int32(3)),
		},
	}

	result, err := r.ensureEmptyNodePoolHasNoPods(context.Background(), hnp)
	require.NoError(t, err)
	assert.Equal(t, reconcile.Result{}, result)
}

func TestEnsureEmptyNodePoolHasNoPods_NoOp_WhenNoPods(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = humiov1alpha1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &HumioClusterReconciler{Client: fakeClient, Log: logr.Discard()}

	hnp := &HumioNodePool{
		namespace:   "default",
		clusterName: "test-cluster",
		humioNodeSpec: humiov1alpha1.HumioNodeSpec{
			NodeCount: ptr.To(int32(0)),
		},
	}

	result, err := r.ensureEmptyNodePoolHasNoPods(context.Background(), hnp)
	require.NoError(t, err)
	assert.Equal(t, reconcile.Result{}, result)
}

func TestEnsureEmptyNodePoolHasNoPods_DeletesNewestPod(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = humiov1alpha1.AddToScheme(scheme)

	now := time.Now()
	labels := map[string]string{
		"app.kubernetes.io/instance":   "test-cluster",
		"app.kubernetes.io/managed-by": "humio-operator",
		"app.kubernetes.io/name":       "humio",
		"humio.com/node-pool":          "test-cluster",
	}

	oldPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pod-old",
			Namespace:         "default",
			Labels:            labels,
			CreationTimestamp: metav1.NewTime(now.Add(-10 * time.Minute)),
		},
	}
	newPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pod-new",
			Namespace:         "default",
			Labels:            labels,
			CreationTimestamp: metav1.NewTime(now),
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(oldPod, newPod).Build()
	r := &HumioClusterReconciler{Client: fakeClient, Log: logr.Discard()}

	hnp := &HumioNodePool{
		namespace:   "default",
		clusterName: "test-cluster",
		humioNodeSpec: humiov1alpha1.HumioNodeSpec{
			NodeCount: ptr.To(int32(0)),
		},
	}

	result, err := r.ensureEmptyNodePoolHasNoPods(context.Background(), hnp)
	require.NoError(t, err)
	assert.NotEqual(t, reconcile.Result{}, result, "should requeue after deleting a pod")

	var remaining corev1.PodList
	require.NoError(t, fakeClient.List(context.Background(), &remaining, client.InNamespace("default")))
	require.Len(t, remaining.Items, 1, "one pod should remain")
	assert.Equal(t, "pod-old", remaining.Items[0].Name, "oldest pod should survive")
}

func TestEnsureEmptyNodePoolHasNoPods_SkipsAlreadyDeleting(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = humiov1alpha1.AddToScheme(scheme)

	now := time.Now()
	deletionTime := metav1.NewTime(now)
	labels := map[string]string{
		"app.kubernetes.io/instance":   "test-cluster",
		"app.kubernetes.io/managed-by": "humio-operator",
		"app.kubernetes.io/name":       "humio",
		"humio.com/node-pool":          "test-cluster",
	}

	deletingPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pod-deleting",
			Namespace:         "default",
			Labels:            labels,
			CreationTimestamp: metav1.NewTime(now),
			DeletionTimestamp: &deletionTime,
			Finalizers:        []string{"test-finalizer"},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(deletingPod).Build()
	r := &HumioClusterReconciler{Client: fakeClient, Log: logr.Discard()}

	hnp := &HumioNodePool{
		namespace:   "default",
		clusterName: "test-cluster",
		humioNodeSpec: humiov1alpha1.HumioNodeSpec{
			NodeCount: ptr.To(int32(0)),
		},
	}

	result, err := r.ensureEmptyNodePoolHasNoPods(context.Background(), hnp)
	require.NoError(t, err)
	assert.Equal(t, reconcile.Result{}, result, "should be no-op when all pods are already deleting")
}

func TestEnsureEmptyNodePoolHasNoPods_NoOp_WhenAutoscalingTransientZero(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = humiov1alpha1.AddToScheme(scheme)

	labels := map[string]string{
		"app.kubernetes.io/instance":   "test-cluster",
		"app.kubernetes.io/managed-by": "humio-operator",
		"app.kubernetes.io/name":       "humio",
		"humio.com/node-pool":          "test-cluster",
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-0",
			Namespace: "default",
			Labels:    labels,
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod).Build()
	r := &HumioClusterReconciler{Client: fakeClient, Log: logr.Discard()}

	hnp := &HumioNodePool{
		namespace:   "default",
		clusterName: "test-cluster",
		humioNodeSpec: humiov1alpha1.HumioNodeSpec{
			Autoscaling: &humiov1alpha1.AutoscalingSpec{
				MinReplicas: ptr.To(int32(0)),
				MaxReplicas: 5,
			},
		},
		statusDesiredReplicas: 0,
	}

	result, err := r.ensureEmptyNodePoolHasNoPods(context.Background(), hnp)
	require.NoError(t, err)
	assert.Equal(t, reconcile.Result{}, result, "should not delete pods from autoscaling pool with transient zero")

	var remaining corev1.PodList
	require.NoError(t, fakeClient.List(context.Background(), &remaining, client.InNamespace("default")))
	assert.Len(t, remaining.Items, 1, "pod must survive — autoscaling pool should never trigger fallback deletion")
}

func TestEnsureEmptyNodePoolHasNoPods_NoOp_WhenDownscalingEnabled(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = humiov1alpha1.AddToScheme(scheme)

	labels := map[string]string{
		"app.kubernetes.io/instance":   "test-cluster",
		"app.kubernetes.io/managed-by": "humio-operator",
		"app.kubernetes.io/name":       "humio",
		"humio.com/node-pool":          "test-cluster",
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-0",
			Namespace: "default",
			Labels:    labels,
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod).Build()
	r := &HumioClusterReconciler{Client: fakeClient, Log: logr.Discard()}

	hnp := &HumioNodePool{
		namespace:   "default",
		clusterName: "test-cluster",
		humioNodeSpec: humiov1alpha1.HumioNodeSpec{
			NodeCount: ptr.To(int32(0)),
			Image:     "humio/humio-core:1.175.0",
		},
		enableDownscalingFeature: true,
	}

	result, err := r.ensureEmptyNodePoolHasNoPods(context.Background(), hnp)
	require.NoError(t, err)
	assert.Equal(t, reconcile.Result{}, result, "should defer to processDownscaling when downscaling is enabled")

	var remaining corev1.PodList
	require.NoError(t, fakeClient.List(context.Background(), &remaining, client.InNamespace("default")))
	assert.Len(t, remaining.Items, 1, "pod must survive — processDownscaling handles eviction")
}
