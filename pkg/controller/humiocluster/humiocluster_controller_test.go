package humiocluster

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	humioapi "github.com/humio/cli/api"
	humioClusterv1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	"github.com/humio/humio-operator/pkg/humio"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
					Image:                   "humio/humio-core:1.9.1",
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
							Uri:         fmt.Sprintf("http://192.168.0.%d:8080", 0),
							Id:          0,
							IsAvailable: true,
						},
						humioapi.ClusterNode{
							Uri:         fmt.Sprintf("http://192.168.0.%d:8080", 1),
							Id:          1,
							IsAvailable: true,
						},
						humioapi.ClusterNode{
							Uri:         fmt.Sprintf("http://192.168.0.%d:8080", 2),
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
					Name:      tt.humioCluster.Name,
					Namespace: tt.humioCluster.Namespace,
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
				Namespace: tt.humioCluster.Namespace,
			}, secret)
			if err != nil {
				t.Errorf("get secret with password: (%v). %+v", err, secret)
			}
			if string(secret.Data["password"]) == "" {
				t.Errorf("password secret %s expected content to not be empty, but it was", serviceAccountSecretName)
			}

			for nodeCount := 0; nodeCount < tt.humioCluster.Spec.NodeCount; nodeCount++ {
				var foundPodList corev1.PodList

				cl.List(context.TODO(), &foundPodList, client.InNamespace(tt.humioCluster.Namespace), matchingLabelsForHumio(tt.humioCluster.Name))

				if len(foundPodList.Items) != nodeCount+1 {
					t.Errorf("expected list pods to return equal to %d, got %d", nodeCount+1, len(foundPodList.Items))
				}

				// We must update the IP address because when we attempt to add labels to the pod we validate that they have IP addresses first
				// We also must update the ready condition as the reconciler will wait until all pods are ready before continuing
				for nodeID, pod := range foundPodList.Items {
					//pod.Name = fmt.Sprintf("%s-core-somesuffix%d", tt.humioCluster.Name, nodeID)
					pod.Status.PodIP = fmt.Sprintf("192.168.0.%d", nodeID)
					pod.Status.Conditions = []corev1.PodCondition{
						corev1.PodCondition{
							Type:   corev1.PodConditionType("Ready"),
							Status: corev1.ConditionTrue,
						},
					}
					err := cl.Status().Update(context.TODO(), &pod)
					if err != nil {
						t.Errorf("failed to update pods to prepare for testing the labels: %s", err)
					}
				}

				// Reconcile again so Reconcile() checks pods and updates the HumioCluster resources' Status.
				res, err = r.Reconcile(req)
				if err != nil {
					t.Errorf("reconcile: (%v)", err)
				}

			}

			// Check that the service exists
			service := &corev1.Service{}
			err = cl.Get(context.TODO(), clienttype.ObjectKey{
				Name:      tt.humioCluster.Name,
				Namespace: tt.humioCluster.Namespace,
			}, service)
			if err != nil {
				t.Errorf("get service: (%v). %+v", err, service)
			}

			// Check that the persistent token exists as a k8s secret
			token := &corev1.Secret{}
			err = cl.Get(context.TODO(), clienttype.ObjectKey{
				Name:      serviceTokenSecretName,
				Namespace: tt.humioCluster.Namespace,
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
			if res != (reconcile.Result{Requeue: true, RequeueAfter: time.Second * 30}) {
				t.Error("reconcile finished, requeueing the resource after 30 seconds")
			}

			// Check that the persistent token
			tokenInUse, err := r.humioClient.ApiToken()
			if tokenInUse != "mocktoken" {
				t.Errorf("expected api token in use to be \"%+v\", but got \"%+v\"", "mocktoken", tokenInUse)
			}

			// Get the updated HumioCluster object.
			updatedHumioCluster := &humioClusterv1alpha1.HumioCluster{}
			err = r.client.Get(context.TODO(), req.NamespacedName, updatedHumioCluster)
			if err != nil {
				t.Errorf("get HumioCluster: (%v)", err)
			}

			// Check that the partitions are balanced
			clusterController := humio.NewClusterController(humioClient)
			if b, err := clusterController.AreStoragePartitionsBalanced(updatedHumioCluster); !b || err != nil {
				t.Errorf("expected storage partitions to be balanced. got %v, err %s", b, err)
			}
			if b, err := clusterController.AreIngestPartitionsBalanced(updatedHumioCluster); !b || err != nil {
				t.Errorf("expected ingest partitions to be balanced. got %v, err %s", b, err)
			}

			foundPodList, err := ListPods(cl, tt.humioCluster)
			if err != nil {
				t.Errorf("could not list pods to validate their content: %v", err)
			}

			if len(foundPodList) != tt.humioCluster.Spec.NodeCount {
				t.Errorf("expected list pods to return equal to %d, got %d", tt.humioCluster.Spec.NodeCount, len(foundPodList))
			}

			// Ensure that we add node_id label to all pods
			for _, pod := range foundPodList {
				if !labelListContainsLabel(pod.GetLabels(), "node_id") {
					t.Errorf("expected pod %s to have label node_id", pod.Name)
				}
			}
		})
	}
}

func TestReconcileHumioCluster_Reconcile_update_humio_image(t *testing.T) {
	// Set the logger to development mode for verbose logs.
	logf.SetLogger(logf.ZapLogger(true))

	tests := []struct {
		name          string
		humioCluster  *humioClusterv1alpha1.HumioCluster
		imageToUpdate string
	}{
		{
			"test simple cluster humio image update",
			&humioClusterv1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humiocluster",
					Namespace: "logging",
				},
				Spec: humioClusterv1alpha1.HumioClusterSpec{
					Image:                   "humio/humio-core:1.9.1",
					TargetReplicationFactor: 2,
					StoragePartitionsCount:  3,
					DigestPartitionsCount:   3,
					NodeCount:               3,
				},
			},
			"humio/humio-core:1.9.2",
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
							Uri:         fmt.Sprintf("http://192.168.0.%d:8080", 0),
							Id:          0,
							IsAvailable: true,
						},
						humioapi.ClusterNode{
							Uri:         fmt.Sprintf("http://192.168.0.%d:8080", 1),
							Id:          1,
							IsAvailable: true,
						},
						humioapi.ClusterNode{
							Uri:         fmt.Sprintf("http://192.168.0.%d:8080", 2),
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
					Name:      tt.humioCluster.Name,
					Namespace: tt.humioCluster.Namespace,
				},
			}
			_, err := r.Reconcile(req)
			if err != nil {
				t.Errorf("reconcile: (%v)", err)
			}

			for nodeCount := 0; nodeCount < tt.humioCluster.Spec.NodeCount; nodeCount++ {
				var foundPodList corev1.PodList
				var matchingLabels client.MatchingLabels

				matchingLabels = labelsForHumio(tt.humioCluster.Name)
				cl.List(context.TODO(), &foundPodList, client.InNamespace(tt.humioCluster.Namespace), matchingLabels)

				if len(foundPodList.Items) != nodeCount+1 {
					t.Errorf("expected list pods to return equal to %d, got %d", nodeCount+1, len(foundPodList.Items))
				}

				// We must update the IP address because when we attempt to add labels to the pod we validate that they have IP addresses first
				// We also must update the ready condition as the reconciler will wait until all pods are ready before continuing
				for nodeID, pod := range foundPodList.Items {
					//pod.Name = fmt.Sprintf("%s-core-somesuffix%d", tt.humioCluster.Name, nodeID)
					pod.Status.PodIP = fmt.Sprintf("192.168.0.%d", nodeID)
					pod.Status.Conditions = []corev1.PodCondition{
						corev1.PodCondition{
							Type:   corev1.PodConditionType("Ready"),
							Status: corev1.ConditionTrue,
						},
					}
					err := cl.Status().Update(context.TODO(), &pod)
					if err != nil {
						t.Errorf("failed to update pods to prepare for testing the labels: %s", err)
					}
				}

				// Reconcile again so Reconcile() checks pods and updates the HumioCluster resources' Status.
				_, err = r.Reconcile(req)
				if err != nil {
					t.Errorf("reconcile: (%v)", err)
				}
			}

			// Update humio image
			tt.humioCluster.Spec.Image = tt.imageToUpdate
			cl.Update(context.TODO(), tt.humioCluster)

			for nodeCount := 0; nodeCount < tt.humioCluster.Spec.NodeCount; nodeCount++ {
				res, err := r.Reconcile(req)
				if err != nil {
					t.Errorf("reconcile: (%v)", err)
				}
				if res != (reconcile.Result{Requeue: true}) {
					t.Errorf("reconcile did not match expected %v", res)
				}
			}

			var foundPodList corev1.PodList

			cl.List(context.TODO(), &foundPodList, client.InNamespace(tt.humioCluster.Namespace), matchingLabelsForHumio(tt.humioCluster.Name))

			if len(foundPodList.Items) != 0 {
				t.Errorf("expected list pods to return equal to %d, got %d", 0, len(foundPodList.Items))
			}

			for nodeCount := 0; nodeCount < tt.humioCluster.Spec.NodeCount; nodeCount++ {
				res, err := r.Reconcile(req)
				if err != nil {
					t.Errorf("reconcile: (%v)", err)
				}
				if res != (reconcile.Result{Requeue: true}) {
					t.Errorf("reconcile did not match expected %v", res)
				}
			}

			cl.List(context.TODO(), &foundPodList, client.InNamespace(tt.humioCluster.Namespace), matchingLabelsForHumio(tt.humioCluster.Name))

			if len(foundPodList.Items) != tt.humioCluster.Spec.NodeCount {
				t.Errorf("expected list pods to return equal to %d, got %d", tt.humioCluster.Spec.NodeCount, len(foundPodList.Items))
			}
		})
	}
}
