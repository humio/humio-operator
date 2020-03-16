package humiocluster

import (
	"context"
	"fmt"
	"testing"

	humioapi "github.com/humio/cli/api"
	humioClusterv1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	"github.com/humio/humio-operator/pkg/humio"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	clienttype "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

func TestReconcileHumioCluster_Reconcile(t *testing.T) {
	// Set the logger to development mode for verbose logs.
	logf.SetLogger(logf.ZapLogger(true))

	tests := []struct {
		name         string
		humioCluster *humioClusterv1alpha1.HumioCluster
	}{
		{
			"test simple cluster reconciliation",
			&humioClusterv1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humiocluster",
					Namespace: "logging",
				},
				Spec: humioClusterv1alpha1.HumioClusterSpec{
					Image:                   "humio/humio-core",
					Version:                 "1.9.0",
					TargetReplicationFactor: 3,
					NodeCount:               3,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			// Objects to track in the fake client.
			objs := []runtime.Object{
				tt.humioCluster,
			}

			// Register operator types with the runtime scheme.
			s := scheme.Scheme
			s.AddKnownTypes(humioClusterv1alpha1.SchemeGroupVersion, tt.humioCluster)

			// Create a fake client to mock API calls.
			cl := fake.NewFakeClient(objs...)

			// TODO: create this above when we add more test cases
			humioClient := humio.NewMocklient(
				humioapi.Cluster{
					Nodes: []humioapi.ClusterNode{humioapi.ClusterNode{
						IsAvailable: true,
					}}}, nil, nil, nil)

			// Create a ReconcileHumioCluster object with the scheme and fake client.
			r := &ReconcileHumioCluster{client: cl, humioClient: humioClient, scheme: s}

			// Mock request to simulate Reconcile() being called on an event for a
			// watched resource .
			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      tt.humioCluster.ObjectMeta.Name,
					Namespace: tt.humioCluster.ObjectMeta.Namespace,
				},
			}
			res, err := r.Reconcile(req)
			if err != nil {
				t.Errorf("reconcile: (%v)", err)
			}

			for nodeID := 0; nodeID < tt.humioCluster.Spec.NodeCount; nodeID++ {
				pod := &corev1.Pod{}
				err = cl.Get(context.TODO(), types.NamespacedName{Name: fmt.Sprintf("%s-core-%d", tt.humioCluster.ObjectMeta.Name, nodeID), Namespace: tt.humioCluster.ObjectMeta.Namespace}, pod)
				if err != nil {
					t.Errorf("get pod: (%v). %+v", err, pod)
				}
			}

			service := &corev1.Service{}
			err = cl.Get(context.TODO(), clienttype.ObjectKey{
				Name:      tt.humioCluster.Name,
				Namespace: tt.humioCluster.ObjectMeta.Namespace,
			}, service)
			if err != nil {
				t.Errorf("get service: (%v). %+v", err, service)
			}

			secret := &corev1.Secret{}
			err = cl.Get(context.TODO(), clienttype.ObjectKey{
				Name:      serviceAccountSecretName,
				Namespace: tt.humioCluster.ObjectMeta.Namespace,
			}, secret)
			if err != nil {
				t.Errorf("get secret: (%v). %+v", err, secret)
			}

			// Reconcile again so Reconcile() checks pods and updates the HumioCluster resources' Status.
			res, err = r.Reconcile(req)
			if err != nil {
				t.Errorf("reconcile: (%v)", err)
			}
			if res != (reconcile.Result{}) {
				t.Error("reconcile did not return an empty Result")
			}

			// Get the updated HumioCluster object.
			humioCluster := &humioClusterv1alpha1.HumioCluster{}
			err = r.client.Get(context.TODO(), req.NamespacedName, humioCluster)
			if err != nil {
				t.Errorf("get HumioCluster: (%v)", err)
			}
		})
	}
}
