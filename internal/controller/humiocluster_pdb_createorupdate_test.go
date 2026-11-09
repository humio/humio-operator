package controller

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	controllerutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestCreateOrUpdatePDB_TwoVariablePattern(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, policyv1.AddToScheme(scheme))
	require.NoError(t, humiov1alpha1.AddToScheme(scheme))

	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default", UID: "abc-123"},
	}

	t.Run("create when absent", func(t *testing.T) {
		client := fake.NewClientBuilder().WithScheme(scheme).Build()
		r := &HumioClusterReconciler{
			Client: client,
			Log:    logr.Discard(),
		}

		minAvail := intstr.FromInt32(5)
		desired := &policyv1.PodDisruptionBudget{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pdb",
				Namespace: "default",
				Labels:    map[string]string{"app": "humio"},
			},
			Spec: policyv1.PodDisruptionBudgetSpec{
				MinAvailable:   &minAvail,
				MaxUnavailable: nil,
			},
		}

		op, err := r.createOrUpdatePDB(context.Background(), hc, desired)
		require.NoError(t, err)
		assert.Equal(t, controllerutil.OperationResultCreated, op)

		var live policyv1.PodDisruptionBudget
		require.NoError(t, client.Get(context.Background(), types.NamespacedName{Name: "test-pdb", Namespace: "default"}, &live))
		assert.Equal(t, &minAvail, live.Spec.MinAvailable)
		assert.Nil(t, live.Spec.MaxUnavailable)
		assert.Equal(t, map[string]string{"app": "humio"}, live.Labels)

		require.Len(t, live.OwnerReferences, 1)
		assert.Equal(t, hc.Name, live.OwnerReferences[0].Name)
		assert.Equal(t, hc.UID, live.OwnerReferences[0].UID)
	})

	t.Run("update overwrites stale values", func(t *testing.T) {
		oldMin := intstr.FromInt32(2)
		existingPDB := &policyv1.PodDisruptionBudget{
			ObjectMeta: metav1.ObjectMeta{Name: "test-pdb", Namespace: "default"},
			Spec: policyv1.PodDisruptionBudgetSpec{
				MinAvailable: &oldMin,
			},
		}
		client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existingPDB).Build()
		r := &HumioClusterReconciler{
			Client: client,
			Log:    logr.Discard(),
		}

		newMax := intstr.FromInt32(1)
		desired := &policyv1.PodDisruptionBudget{
			ObjectMeta: metav1.ObjectMeta{Name: "test-pdb", Namespace: "default"},
			Spec: policyv1.PodDisruptionBudgetSpec{
				MinAvailable:   nil,
				MaxUnavailable: &newMax,
			},
		}

		op, err := r.createOrUpdatePDB(context.Background(), hc, desired)
		require.NoError(t, err)
		assert.Equal(t, controllerutil.OperationResultUpdated, op)

		var live policyv1.PodDisruptionBudget
		require.NoError(t, client.Get(context.Background(), types.NamespacedName{Name: "test-pdb", Namespace: "default"}, &live))
		assert.Nil(t, live.Spec.MinAvailable)
		assert.Equal(t, &newMax, live.Spec.MaxUnavailable)
	})

}

func TestCreateOrUpdatePDB_ErrorWrapping(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, policyv1.AddToScheme(scheme))
	require.NoError(t, humiov1alpha1.AddToScheme(scheme))

	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default", UID: "abc-123"},
	}

	t.Run("wraps client error with PDB context", func(t *testing.T) {
		injectedErr := assert.AnError
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					return injectedErr
				},
			}).
			Build()

		r := &HumioClusterReconciler{
			Client: fakeClient,
			Log:    logr.Discard(),
		}

		minAvail := intstr.FromInt32(5)
		desired := &policyv1.PodDisruptionBudget{
			ObjectMeta: metav1.ObjectMeta{Name: "test-pdb", Namespace: "default"},
			Spec:       policyv1.PodDisruptionBudgetSpec{MinAvailable: &minAvail},
		}

		op, err := r.createOrUpdatePDB(context.Background(), hc, desired)
		assert.Error(t, err)
		assert.Equal(t, controllerutil.OperationResultNone, op)
		assert.Contains(t, err.Error(), "failed to create or update PDB")
		assert.Contains(t, err.Error(), "default/test-pdb")
	})
}
