package humiocluster

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
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
					TargetReplicationFactor: 2,
					StoragePartitionsCount:  3,
					DigestPartitionsCount:   3,
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

			// Start up http server that can send the mock jwt token
			server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
				rw.Write([]byte(`{"token": "sometempjwttoken"}`))
			}))
			defer server.Close()

			// TODO: create this above when we add more test cases
			storagePartitions := []humioapi.StoragePartition{
				humioapi.StoragePartition{
					Id:      1,
					NodeIds: []int{0},
				},
				humioapi.StoragePartition{
					Id:      2,
					NodeIds: []int{0},
				},
				humioapi.StoragePartition{
					Id:      3,
					NodeIds: []int{0},
				},
			}
			ingestPartitions := []humioapi.IngestPartition{
				humioapi.IngestPartition{
					Id:      1,
					NodeIds: []int{0},
				},
				humioapi.IngestPartition{
					Id:      2,
					NodeIds: []int{0},
				},
				humioapi.IngestPartition{
					Id:      3,
					NodeIds: []int{0},
				},
			}
			humioClient := humio.NewMocklient(
				humioapi.Cluster{
					Nodes: []humioapi.ClusterNode{
						humioapi.ClusterNode{
							Id:          0,
							IsAvailable: true,
						},
						humioapi.ClusterNode{
							Id:          1,
							IsAvailable: true,
						},
						humioapi.ClusterNode{
							Id:          2,
							IsAvailable: true,
						},
					},
					StoragePartitions: storagePartitions,
					IngestPartitions:  ingestPartitions,
				}, nil, nil, nil, fmt.Sprintf("%s/", server.URL))

			// Create a ReconcileHumioCluster object with the scheme and fake client.
			r := &ReconcileHumioCluster{
				client:      cl,
				humioClient: humioClient,
				scheme:      s,
			}

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

			// Check that the developer password exists as a k8s secret
			secret := &corev1.Secret{}
			err = cl.Get(context.TODO(), clienttype.ObjectKey{
				Name:      serviceAccountSecretName,
				Namespace: tt.humioCluster.ObjectMeta.Namespace,
			}, secret)
			if err != nil {
				t.Errorf("get secret with password: (%v). %+v", err, secret)
			}
			if string(secret.Data["password"]) == "" {
				t.Errorf("password secret %s expected content to not be empty, but it was", serviceAccountSecretName)
			}

			for nodeID := 0; nodeID < tt.humioCluster.Spec.NodeCount; nodeID++ {
				pod := &corev1.Pod{}
				err = cl.Get(context.TODO(), types.NamespacedName{Name: fmt.Sprintf("%s-core-%d", tt.humioCluster.ObjectMeta.Name, nodeID), Namespace: tt.humioCluster.ObjectMeta.Namespace}, pod)
				if err != nil {
					t.Errorf("get pod: (%v). %+v", err, pod)
				}
			}

			// Check that the service exists
			service := &corev1.Service{}
			err = cl.Get(context.TODO(), clienttype.ObjectKey{
				Name:      tt.humioCluster.Name,
				Namespace: tt.humioCluster.ObjectMeta.Namespace,
			}, service)
			if err != nil {
				t.Errorf("get service: (%v). %+v", err, service)
			}

			// Check that the persistent token exists as a k8s secret
			token := &corev1.Secret{}
			err = cl.Get(context.TODO(), clienttype.ObjectKey{
				Name:      serviceTokenSecretName,
				Namespace: tt.humioCluster.ObjectMeta.Namespace,
			}, token)
			if err != nil {
				t.Errorf("get secret with api token: (%v). %+v", err, token)
			}
			if string(token.Data["token"]) != "mocktoken" {
				t.Errorf("api token secret %s expected content \"%+v\", but got \"%+v\"", serviceTokenSecretName, "mocktoken", string(token.Data["token"]))
			}

			// Reconcile again so Reconcile() checks pods and updates the HumioCluster resources' Status.
			res, err = r.Reconcile(req)
			if err != nil {
				t.Errorf("reconcile: (%v)", err)
			}
			if res != (reconcile.Result{}) {
				t.Error("reconcile did not return an empty Result")
			}

			// Check that the persistent token
			tokenInUse, err := r.humioClient.ApiToken()
			if tokenInUse != "mocktoken" {
				t.Errorf("expected api token in use to be \"%+v\", but got \"%+v\"", "mocktoken", tokenInUse)
			}

			// Get the updated HumioCluster object.
			humioCluster := &humioClusterv1alpha1.HumioCluster{}
			err = r.client.Get(context.TODO(), req.NamespacedName, humioCluster)
			if err != nil {
				t.Errorf("get HumioCluster: (%v)", err)
			}

			// Check that the partitions are balanced
			// TODO: fix this test
			clusterController := humio.NewClusterController(humioClient)
			if b, err := clusterController.AreStoragePartitionsBalanced(humioCluster); !b || err != nil {
				t.Errorf("expected storage partitions to be balanced. got %v, err %s", b, err)
			}
			if b, err := clusterController.AreIngestPartitionsBalanced(humioCluster); !b || err != nil {
				t.Errorf("expected ingest partitions to be balanced. got %v, err %s", b, err)
			}
		})
	}
}
