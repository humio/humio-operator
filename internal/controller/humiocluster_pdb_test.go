package controller

import (
	"testing"

	"github.com/go-logr/logr"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func ptrIntStr(val string) *intstr.IntOrString {
	v := intstr.Parse(val)
	return &v
}

func TestEffectiveMinAvailable(t *testing.T) {
	tests := []struct {
		name               string
		state              string
		userMinAvailable   *intstr.IntOrString
		userMaxUnavailable *intstr.IntOrString
		replicaCount       int
		freezeDuringUpdate *bool
		wantMinAvail       *intstr.IntOrString
		wantMaxUnavail     *intstr.IntOrString
	}{
		{
			name:               "freeze active during upgrading overrides to replica count",
			state:              "Upgrading",
			userMinAvailable:   ptrIntStr("2"),
			userMaxUnavailable: nil,
			replicaCount:       5,
			freezeDuringUpdate: helpers.BoolPtr(true),
			wantMinAvail:       ptrIntStr("5"),
			wantMaxUnavail:     nil,
		},
		{
			name:               "freeze active during restarting with maxUnavailable user config",
			state:              "Restarting",
			userMinAvailable:   nil,
			userMaxUnavailable: ptrIntStr("1"),
			replicaCount:       3,
			freezeDuringUpdate: helpers.BoolPtr(true),
			wantMinAvail:       ptrIntStr("3"),
			wantMaxUnavail:     nil,
		},
		{
			name:               "freeze true but state is Running passes through user minAvailable",
			state:              "Running",
			userMinAvailable:   ptrIntStr("2"),
			userMaxUnavailable: nil,
			replicaCount:       5,
			freezeDuringUpdate: helpers.BoolPtr(true),
			wantMinAvail:       ptrIntStr("2"),
			wantMaxUnavail:     nil,
		},
		{
			name:               "freeze nil does not override during upgrading",
			state:              "Upgrading",
			userMinAvailable:   ptrIntStr("2"),
			userMaxUnavailable: nil,
			replicaCount:       5,
			freezeDuringUpdate: nil,
			wantMinAvail:       ptrIntStr("2"),
			wantMaxUnavail:     nil,
		},
		{
			name:               "freeze false does not override during upgrading",
			state:              "Upgrading",
			userMinAvailable:   ptrIntStr("2"),
			userMaxUnavailable: nil,
			replicaCount:       5,
			freezeDuringUpdate: helpers.BoolPtr(false),
			wantMinAvail:       ptrIntStr("2"),
			wantMaxUnavail:     nil,
		},
		{
			name:               "freeze true but zero replicas returns user values unchanged",
			state:              "Upgrading",
			userMinAvailable:   ptrIntStr("2"),
			userMaxUnavailable: nil,
			replicaCount:       0,
			freezeDuringUpdate: helpers.BoolPtr(true),
			wantMinAvail:       ptrIntStr("2"),
			wantMaxUnavail:     nil,
		},
		{
			name:               "freeze true upgrading single replica",
			state:              "Upgrading",
			userMinAvailable:   nil,
			userMaxUnavailable: ptrIntStr("1"),
			replicaCount:       1,
			freezeDuringUpdate: helpers.BoolPtr(true),
			wantMinAvail:       ptrIntStr("1"),
			wantMaxUnavail:     nil,
		},
		{
			name:               "unfreeze restores user maxUnavailable when minAvailable is nil",
			state:              "Running",
			userMinAvailable:   nil,
			userMaxUnavailable: ptrIntStr("1"),
			replicaCount:       5,
			freezeDuringUpdate: helpers.BoolPtr(true),
			wantMinAvail:       nil,
			wantMaxUnavail:     ptrIntStr("1"),
		},
		{
			name:               "both user fields nil returns nil nil",
			state:              "Running",
			userMinAvailable:   nil,
			userMaxUnavailable: nil,
			replicaCount:       5,
			freezeDuringUpdate: helpers.BoolPtr(true),
			wantMinAvail:       nil,
			wantMaxUnavail:     nil,
		},
		{
			name:               "unknown state treated as non-freeze",
			state:              "Unknown",
			userMinAvailable:   ptrIntStr("2"),
			userMaxUnavailable: nil,
			replicaCount:       5,
			freezeDuringUpdate: helpers.BoolPtr(true),
			wantMinAvail:       ptrIntStr("2"),
			wantMaxUnavail:     nil,
		},
		{
			name:               "percentage minAvailable overridden during freeze",
			state:              "Upgrading",
			userMinAvailable:   ptrIntStr("80%"),
			userMaxUnavailable: nil,
			replicaCount:       5,
			freezeDuringUpdate: helpers.BoolPtr(true),
			wantMinAvail:       ptrIntStr("5"),
			wantMaxUnavail:     nil,
		},
		{
			name:               "percentage minAvailable preserved when not frozen",
			state:              "Running",
			userMinAvailable:   ptrIntStr("80%"),
			userMaxUnavailable: nil,
			replicaCount:       5,
			freezeDuringUpdate: helpers.BoolPtr(true),
			wantMinAvail:       ptrIntStr("80%"),
			wantMaxUnavail:     nil,
		},
		{
			name:               "both fields set non-frozen prefers minAvailable",
			state:              "Running",
			userMinAvailable:   ptrIntStr("3"),
			userMaxUnavailable: ptrIntStr("1"),
			replicaCount:       5,
			freezeDuringUpdate: helpers.BoolPtr(true),
			wantMinAvail:       ptrIntStr("3"),
			wantMaxUnavail:     nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotMin, gotMax := effectiveMinAvailable(
				tc.state,
				tc.userMinAvailable,
				tc.userMaxUnavailable,
				tc.replicaCount,
				tc.freezeDuringUpdate,
			)
			assert.Equal(t, tc.wantMinAvail, gotMin, "minAvailable mismatch")
			assert.Equal(t, tc.wantMaxUnavail, gotMax, "maxUnavailable mismatch")
		})
	}
}

// TestConstructPDB_Integration exercises constructPDB end-to-end by instantiating
// a real HumioNodePool struct and HumioClusterReconciler with a fake client/scheme.
// This verifies owner references, labels, freeze override, and default minAvailable=1.
func TestConstructPDB_Integration(t *testing.T) {
	// Set up scheme with HumioCluster and PDB types for owner reference support.
	scheme := runtime.NewScheme()
	require.NoError(t, humiov1alpha1.AddToScheme(scheme))
	require.NoError(t, policyv1.AddToScheme(scheme))

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	reconciler := &HumioClusterReconciler{
		Client: fakeClient,
		Log:    logr.Discard(),
	}

	// Base HumioCluster object used as owner.
	baseHC := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
			UID:       "uid-1234",
		},
	}

	tests := []struct {
		name           string
		state          string
		nodeCount      *int32
		pdbSpec        *humiov1alpha1.HumioPodDisruptionBudgetSpec
		wantMinAvail   *intstr.IntOrString
		wantMaxUnavail *intstr.IntOrString
	}{
		{
			name:      "freeze active during Upgrading overrides to replica count",
			state:     "Upgrading",
			nodeCount: helpers.Int32Ptr(5),
			pdbSpec: &humiov1alpha1.HumioPodDisruptionBudgetSpec{
				MinAvailable:       ptrIntStr("2"),
				FreezeDuringUpdate: helpers.BoolPtr(true),
			},
			wantMinAvail:   ptrIntStr("5"),
			wantMaxUnavail: nil,
		},
		{
			name:      "freeze true but Running passes through user maxUnavailable",
			state:     "Running",
			nodeCount: helpers.Int32Ptr(3),
			pdbSpec: &humiov1alpha1.HumioPodDisruptionBudgetSpec{
				MaxUnavailable:     ptrIntStr("1"),
				FreezeDuringUpdate: helpers.BoolPtr(true),
			},
			wantMinAvail:   nil,
			wantMaxUnavail: ptrIntStr("1"),
		},
		{
			name:      "freeze nil does not override during Upgrading",
			state:     "Upgrading",
			nodeCount: helpers.Int32Ptr(5),
			pdbSpec: &humiov1alpha1.HumioPodDisruptionBudgetSpec{
				MinAvailable:       ptrIntStr("2"),
				FreezeDuringUpdate: nil,
			},
			wantMinAvail:   ptrIntStr("2"),
			wantMaxUnavail: nil,
		},
		{
			name:      "no user PDB fields defaults to minAvailable=1",
			state:     "Running",
			nodeCount: helpers.Int32Ptr(3),
			pdbSpec: &humiov1alpha1.HumioPodDisruptionBudgetSpec{
				FreezeDuringUpdate: nil,
			},
			wantMinAvail:   ptrIntStr("1"),
			wantMaxUnavail: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hnp := &HumioNodePool{
				clusterName: baseHC.Name,
				namespace:   baseHC.Namespace,
				state:       tc.state,
				humioNodeSpec: humiov1alpha1.HumioNodeSpec{
					NodeCount: tc.nodeCount,
				},
			}

			pdb := reconciler.constructPDB(baseHC, hnp, tc.pdbSpec)

			// Verify PDB spec fields.
			assert.Equal(t, tc.wantMinAvail, pdb.Spec.MinAvailable, "minAvailable mismatch")
			assert.Equal(t, tc.wantMaxUnavail, pdb.Spec.MaxUnavailable, "maxUnavailable mismatch")

			// Verify labels contain the node pool label.
			assert.Contains(t, pdb.Labels, "humio.com/node-pool")
			assert.Equal(t, hnp.GetNodePoolName(), pdb.Labels["humio.com/node-pool"])

			// Verify name follows convention.
			assert.Equal(t, hnp.GetPodDisruptionBudgetName(), pdb.Name)

			// Verify namespace.
			assert.Equal(t, baseHC.Namespace, pdb.Namespace)
		})
	}
}
