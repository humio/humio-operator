package controller

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/kubernetes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestEnsureWorkloadLabels_PatchesMissingLabels(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = humiov1alpha1.AddToScheme(scheme)

	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: humiov1alpha1.HumioClusterSpec{
			WorkloadServices: []humiov1alpha1.WorkloadServiceSpec{
				{Name: "ingest-svc", WorkloadType: "ingest"},
			},
		},
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-0",
			Namespace: "default",
			Labels: map[string]string{
				"app.kubernetes.io/instance":   "test-cluster",
				"app.kubernetes.io/managed-by": "humio-operator",
				"app.kubernetes.io/name":       "humio",
				"humio.com/node-pool":          "test-cluster",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hc, pod).Build()
	r := &HumioClusterReconciler{Client: fakeClient, Log: logr.Discard()}

	err := r.ensureWorkloadLabels(context.Background(), hc)
	require.NoError(t, err)

	var updated corev1.Pod
	require.NoError(t, fakeClient.Get(context.Background(), client.ObjectKeyFromObject(pod), &updated))
	assert.Equal(t, "true", updated.Labels[kubernetes.WorkloadTypeLabelPrefix+"ingest"])
}

func TestEnsureWorkloadLabels_SkipsAlreadyLabeled(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = humiov1alpha1.AddToScheme(scheme)

	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: humiov1alpha1.HumioClusterSpec{
			WorkloadServices: []humiov1alpha1.WorkloadServiceSpec{
				{Name: "ingest-svc", WorkloadType: "ingest"},
			},
		},
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-0",
			Namespace: "default",
			Labels: map[string]string{
				"app.kubernetes.io/instance":                  "test-cluster",
				"app.kubernetes.io/managed-by":                "humio-operator",
				"app.kubernetes.io/name":                      "humio",
				"humio.com/node-pool":                         "test-cluster",
				kubernetes.WorkloadTypeLabelPrefix + "ingest": "true",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hc, pod).Build()
	r := &HumioClusterReconciler{Client: fakeClient, Log: logr.Discard()}

	err := r.ensureWorkloadLabels(context.Background(), hc)
	require.NoError(t, err)

	var updated corev1.Pod
	require.NoError(t, fakeClient.Get(context.Background(), client.ObjectKeyFromObject(pod), &updated))
	assert.Equal(t, "true", updated.Labels[kubernetes.WorkloadTypeLabelPrefix+"ingest"])
}

func TestEnsureWorkloadLabels_RemovesStaleLabels(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = humiov1alpha1.AddToScheme(scheme)

	ingestOnly := []string{"ingest"}
	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: humiov1alpha1.HumioClusterSpec{
			WorkloadServices: []humiov1alpha1.WorkloadServiceSpec{
				{Name: "ingest-svc", WorkloadType: "ingest"},
			},
			NodePools: []humiov1alpha1.HumioNodePoolSpec{
				{
					Name: "ingest-pool",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						WorkloadTypes: &ingestOnly,
					},
				},
			},
		},
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-0",
			Namespace: "default",
			Labels: map[string]string{
				"app.kubernetes.io/instance":                  "test-cluster",
				"app.kubernetes.io/managed-by":                "humio-operator",
				"app.kubernetes.io/name":                      "humio",
				"humio.com/node-pool":                         "test-cluster-ingest-pool",
				kubernetes.WorkloadTypeLabelPrefix + "ingest": "true",
				kubernetes.WorkloadTypeLabelPrefix + "digest": "true",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hc, pod).Build()
	r := &HumioClusterReconciler{Client: fakeClient, Log: logr.Discard()}

	err := r.ensureWorkloadLabels(context.Background(), hc)
	require.NoError(t, err)

	var updated corev1.Pod
	require.NoError(t, fakeClient.Get(context.Background(), client.ObjectKeyFromObject(pod), &updated))
	assert.Equal(t, "true", updated.Labels[kubernetes.WorkloadTypeLabelPrefix+"ingest"])
	_, hasDigest := updated.Labels[kubernetes.WorkloadTypeLabelPrefix+"digest"]
	assert.False(t, hasDigest, "stale digest label should be removed")
}

func TestEnsureWorkloadLabels_DisabledNoOp(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = humiov1alpha1.AddToScheme(scheme)

	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec:       humiov1alpha1.HumioClusterSpec{},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hc).Build()
	r := &HumioClusterReconciler{Client: fakeClient, Log: logr.Discard()}

	err := r.ensureWorkloadLabels(context.Background(), hc)
	require.NoError(t, err)
}

func TestEnsureWorkloadLabels_OverlappingPools(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = humiov1alpha1.AddToScheme(scheme)

	workloadTypes := []string{"ingest", "digest"}
	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: humiov1alpha1.HumioClusterSpec{
			WorkloadServices: []humiov1alpha1.WorkloadServiceSpec{
				{Name: "ingest-svc", WorkloadType: "ingest"},
				{Name: "digest-svc", WorkloadType: "digest"},
			},
			NodePools: []humiov1alpha1.HumioNodePoolSpec{
				{
					Name: "combo",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						WorkloadTypes: &workloadTypes,
					},
				},
			},
		},
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "combo-pod-0",
			Namespace: "default",
			Labels: map[string]string{
				"app.kubernetes.io/instance":   "test-cluster",
				"app.kubernetes.io/managed-by": "humio-operator",
				"app.kubernetes.io/name":       "humio",
				"humio.com/node-pool":          "test-cluster-combo",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hc, pod).Build()
	r := &HumioClusterReconciler{Client: fakeClient, Log: logr.Discard()}

	err := r.ensureWorkloadLabels(context.Background(), hc)
	require.NoError(t, err)

	var updated corev1.Pod
	require.NoError(t, fakeClient.Get(context.Background(), client.ObjectKeyFromObject(pod), &updated))
	assert.Equal(t, "true", updated.Labels[kubernetes.WorkloadTypeLabelPrefix+"ingest"])
	assert.Equal(t, "true", updated.Labels[kubernetes.WorkloadTypeLabelPrefix+"digest"])
}
