package humiocluster

import (
	"context"
	"fmt"
	"github.com/humio/humio-operator/pkg/helpers"
	"reflect"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"

	humioapi "github.com/humio/cli/api"
	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	"github.com/humio/humio-operator/pkg/humio"
	"github.com/humio/humio-operator/pkg/kubernetes"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/api/networking/v1beta1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestReconcileHumioCluster_Reconcile(t *testing.T) {
	tests := []struct {
		name         string
		humioCluster *corev1alpha1.HumioCluster
		humioClient  *humio.MockClientConfig
		version      string
	}{
		{
			"test simple cluster reconciliation without partition rebalancing",
			&corev1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humiocluster",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioClusterSpec{
					Image:                   image,
					AutoRebalancePartitions: false,
					TargetReplicationFactor: 2,
					StoragePartitionsCount:  3,
					DigestPartitionsCount:   3,
					NodeCount:               3,
				},
			},
			humio.NewMocklient(
				humioapi.Cluster{
					Nodes:             buildClusterNodesList(3),
					StoragePartitions: buildStoragePartitionsList(3, 1),
					IngestPartitions:  buildIngestPartitionsList(3, 1),
				}, nil, nil, nil, "1.9.2--build-12365--sha-bf4188482a"),
			"1.9.2--build-12365--sha-bf4188482a",
		},
		{
			"test simple cluster reconciliation with partition rebalancing",
			&corev1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humiocluster",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioClusterSpec{
					Image:                   image,
					AutoRebalancePartitions: true,
					TargetReplicationFactor: 2,
					StoragePartitionsCount:  3,
					DigestPartitionsCount:   3,
					NodeCount:               3,
				},
			},
			humio.NewMocklient(
				humioapi.Cluster{
					Nodes:             buildClusterNodesList(3),
					StoragePartitions: buildStoragePartitionsList(3, 1),
					IngestPartitions:  buildIngestPartitionsList(3, 1),
				}, nil, nil, nil, "1.9.2--build-12365--sha-bf4188482a"),
			"1.9.2--build-12365--sha-bf4188482a",
		},
		{
			"test large cluster reconciliation without partition rebalancing",
			&corev1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humiocluster",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioClusterSpec{
					Image:                   image,
					AutoRebalancePartitions: false,
					TargetReplicationFactor: 3,
					StoragePartitionsCount:  72,
					DigestPartitionsCount:   72,
					NodeCount:               18,
				},
			},
			humio.NewMocklient(
				humioapi.Cluster{
					Nodes:             buildClusterNodesList(18),
					StoragePartitions: buildStoragePartitionsList(72, 2),
					IngestPartitions:  buildIngestPartitionsList(72, 2),
				}, nil, nil, nil, "1.9.2--build-12365--sha-bf4188482a"),
			"1.9.2--build-12365--sha-bf4188482a",
		},
		{
			"test large cluster reconciliation with partition rebalancing",
			&corev1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humiocluster",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioClusterSpec{
					Image:                   image,
					AutoRebalancePartitions: true,
					TargetReplicationFactor: 3,
					StoragePartitionsCount:  72,
					DigestPartitionsCount:   72,
					NodeCount:               18,
				},
			},
			humio.NewMocklient(
				humioapi.Cluster{
					Nodes:             buildClusterNodesList(18),
					StoragePartitions: buildStoragePartitionsList(72, 2),
					IngestPartitions:  buildIngestPartitionsList(72, 2),
				}, nil, nil, nil, "1.9.2--build-12365--sha-bf4188482a"),
			"1.9.2--build-12365--sha-bf4188482a",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, req := reconcileWithHumioClient(tt.humioCluster, tt.humioClient)
			defer r.logger.Sync()

			_, err := r.Reconcile(req)
			if err != nil {
				t.Errorf("reconcile: (%v)", err)
			}

			updatedHumioCluster := &corev1alpha1.HumioCluster{}
			err = r.client.Get(context.TODO(), req.NamespacedName, updatedHumioCluster)
			if err != nil {
				t.Errorf("get HumioCluster: (%v)", err)
			}
			if updatedHumioCluster.Status.State != corev1alpha1.HumioClusterStateBootstrapping {
				t.Errorf("expected cluster state to be %s but got %s", corev1alpha1.HumioClusterStateBootstrapping, updatedHumioCluster.Status.State)
			}

			// Check that the init service account, secret, cluster role and cluster role binding are created
			foundSecretsList, err := kubernetes.ListSecrets(context.TODO(), r.client, updatedHumioCluster.Namespace, kubernetes.MatchingLabelsForSecret(updatedHumioCluster.Name, initServiceAccountSecretName(updatedHumioCluster)))
			if err != nil {
				t.Errorf("get init service account secrets list: (%v). %+v", err, foundSecretsList)
			}
			if len(foundSecretsList) != 1 {
				t.Errorf("get init service account secrets list: (%v). %+v", err, foundSecretsList)
			}

			_, err = kubernetes.GetServiceAccount(context.TODO(), r.client, initServiceAccountNameOrDefault(updatedHumioCluster), updatedHumioCluster.Namespace)
			if err != nil {
				t.Errorf("failed to get init service account: %s", err)
			}
			_, err = kubernetes.GetClusterRole(context.TODO(), r.client, initClusterRoleName(updatedHumioCluster))
			if err != nil {
				t.Errorf("failed to get init cluster role: %s", err)
			}
			_, err = kubernetes.GetClusterRoleBinding(context.TODO(), r.client, initClusterRoleBindingName(updatedHumioCluster))
			if err != nil {
				t.Errorf("failed to get init cluster role binding: %s", err)
			}

			// Check that the auth service account, secret, role and role binding are created
			foundSecretsList, err = kubernetes.ListSecrets(context.TODO(), r.client, updatedHumioCluster.Namespace, kubernetes.MatchingLabelsForSecret(updatedHumioCluster.Name, authServiceAccountSecretName(updatedHumioCluster)))
			if err != nil {
				t.Errorf("get auth service account secrets list: (%v). %+v", err, foundSecretsList)
			}
			if len(foundSecretsList) != 1 {
				t.Errorf("get auth service account secrets list: (%v). %+v", err, foundSecretsList)
			}

			_, err = kubernetes.GetServiceAccount(context.TODO(), r.client, authServiceAccountNameOrDefault(updatedHumioCluster), updatedHumioCluster.Namespace)
			if err != nil {
				t.Errorf("failed to get auth service account: %s", err)
			}
			_, err = kubernetes.GetRole(context.TODO(), r.client, authRoleName(updatedHumioCluster), updatedHumioCluster.Namespace)
			if err != nil {
				t.Errorf("failed to get auth cluster role: %s", err)
			}
			_, err = kubernetes.GetRoleBinding(context.TODO(), r.client, authRoleBindingName(updatedHumioCluster), updatedHumioCluster.Namespace)
			if err != nil {
				t.Errorf("failed to get auth cluster role binding: %s", err)
			}

			for nodeCount := 1; nodeCount <= tt.humioCluster.Spec.NodeCount; nodeCount++ {
				foundPodList, err := kubernetes.ListPods(r.client, updatedHumioCluster.Namespace, kubernetes.MatchingLabelsForHumio(updatedHumioCluster.Name))
				if err != nil {
					t.Errorf("failed to list pods: %s", err)
				}
				if len(foundPodList) != nodeCount {
					t.Errorf("expected list pods to return equal to %d, got %d", nodeCount, len(foundPodList))
				}

				// We must update the IP address because when we attempt to add labels to the pod we validate that they have IP addresses first
				// We also must update the ready condition as the reconciler will wait until all pods are ready before continuing
				err = markPodsAsRunning(r.client, foundPodList)
				if err != nil {
					t.Errorf("failed to update pods to prepare for testing the labels: %s", err)
				}

				// Reconcile again so Reconcile() checks pods and updates the HumioCluster resources' Status.
				_, err = r.Reconcile(req)
				if err != nil {
					t.Errorf("reconcile: (%v)", err)
				}
			}

			// Simulate sidecar creating the secret which contains the admin token use to authenticate with humio
			secretData := map[string][]byte{"token": []byte("")}
			adminTokenSecretName := fmt.Sprintf("%s-%s", updatedHumioCluster.Name, kubernetes.ServiceTokenSecretNameSuffix)
			desiredSecret := kubernetes.ConstructSecret(updatedHumioCluster.Name, updatedHumioCluster.Namespace, adminTokenSecretName, secretData)
			err = r.client.Create(context.TODO(), desiredSecret)
			if err != nil {
				t.Errorf("unable to create service token secret: %s", err)
			}
			_, err = r.Reconcile(req)
			if err != nil {
				t.Errorf("reconcile: (%v)", err)
			}

			// Check that we do not create more than expected number of humio pods
			_, err = r.Reconcile(req)
			if err != nil {
				t.Errorf("reconcile: (%v)", err)
			}
			foundPodList, err := kubernetes.ListPods(r.client, updatedHumioCluster.Namespace, kubernetes.MatchingLabelsForHumio(updatedHumioCluster.Name))
			if err != nil {
				t.Errorf("failed to list pods: %s", err)
			}
			if len(foundPodList) != tt.humioCluster.Spec.NodeCount {
				t.Errorf("expected list pods to return equal to %d, got %d", tt.humioCluster.Spec.NodeCount, len(foundPodList))
			}

			// Test that we have the proper status
			updatedHumioCluster = &corev1alpha1.HumioCluster{}
			err = r.client.Get(context.TODO(), req.NamespacedName, updatedHumioCluster)
			if err != nil {
				t.Errorf("get HumioCluster: (%v)", err)
			}
			if updatedHumioCluster.Status.State != corev1alpha1.HumioClusterStateRunning {
				t.Errorf("expected cluster state to be %s but got %s", corev1alpha1.HumioClusterStateRunning, updatedHumioCluster.Status.State)
			}
			if updatedHumioCluster.Status.Version != tt.version {
				t.Errorf("expected cluster version to be %s but got %s", tt.version, updatedHumioCluster.Status.Version)
			}
			if updatedHumioCluster.Status.NodeCount != tt.humioCluster.Spec.NodeCount {
				t.Errorf("expected node count to be %d but got %d", tt.humioCluster.Spec.NodeCount, updatedHumioCluster.Status.NodeCount)
			}

			// Check that the service exists
			service, err := kubernetes.GetService(context.TODO(), r.client, updatedHumioCluster.Name, updatedHumioCluster.Namespace)
			if err != nil {
				t.Errorf("get service: (%v). %+v", err, service)
			}

			// Reconcile again so Reconcile() checks pods and updates the HumioCluster resources' Status.
			res, err := r.Reconcile(req)
			if err != nil {
				t.Errorf("reconcile: (%v)", err)
			}
			if res != (reconcile.Result{Requeue: true, RequeueAfter: time.Second * 30}) {
				t.Error("reconcile finished, requeuing the resource after 30 seconds")
			}

			// Get the updated HumioCluster to update it with the partitions
			err = r.client.Get(context.TODO(), req.NamespacedName, updatedHumioCluster)
			if err != nil {
				t.Errorf("get HumioCluster: (%v)", err)
			}

			// Check that the partitions are balanced if configured
			clusterController := humio.NewClusterController(r.logger, r.humioClient)
			if b, err := clusterController.AreStoragePartitionsBalanced(updatedHumioCluster); !(b == updatedHumioCluster.Spec.AutoRebalancePartitions) || err != nil {
				t.Errorf("expected storage partitions to be balanced. got %v, err %s", b, err)
			}
			if b, err := clusterController.AreIngestPartitionsBalanced(updatedHumioCluster); !(b == updatedHumioCluster.Spec.AutoRebalancePartitions) || err != nil {
				t.Errorf("expected ingest partitions to be balanced. got %v, err %s", b, err)
			}

			foundPodList, err = kubernetes.ListPods(r.client, updatedHumioCluster.Namespace, kubernetes.MatchingLabelsForHumio(updatedHumioCluster.Name))
			if err != nil {
				t.Errorf("could not list pods to validate their content: %s", err)
			}

			if len(foundPodList) != tt.humioCluster.Spec.NodeCount {
				t.Errorf("expected list pods to return equal to %d, got %d", tt.humioCluster.Spec.NodeCount, len(foundPodList))
			}

			// Ensure that we add kubernetes.NodeIdLabelName label to all pods
			for _, pod := range foundPodList {
				if !kubernetes.LabelListContainsLabel(pod.GetLabels(), kubernetes.NodeIdLabelName) {
					t.Errorf("expected pod %s to have label %s", pod.Name, kubernetes.NodeIdLabelName)
				}
			}
		})
	}
}

func TestReconcileHumioCluster_Reconcile_update_humio_image(t *testing.T) {
	tests := []struct {
		name          string
		humioCluster  *corev1alpha1.HumioCluster
		humioClient   *humio.MockClientConfig
		imageToUpdate string
		version       string
	}{
		{
			"test simple cluster humio image update",
			&corev1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humiocluster",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioClusterSpec{
					Image:                   "humio/humio-core:1.13.0",
					TargetReplicationFactor: 2,
					StoragePartitionsCount:  3,
					DigestPartitionsCount:   3,
					NodeCount:               3,
				},
			},
			humio.NewMocklient(
				humioapi.Cluster{
					Nodes:             buildClusterNodesList(3),
					StoragePartitions: buildStoragePartitionsList(3, 1),
					IngestPartitions:  buildIngestPartitionsList(3, 1),
				}, nil, nil, nil, "1.9.2--build-12365--sha-bf4188482a"),
			"humio/humio-core:1.13.1",
			"1.9.2--build-12365--sha-bf4188482a",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, req := reconcileWithHumioClient(tt.humioCluster, tt.humioClient)
			defer r.logger.Sync()

			_, err := r.Reconcile(req)
			if err != nil {
				t.Errorf("reconcile: (%v)", err)
			}

			updatedHumioCluster := &corev1alpha1.HumioCluster{}
			err = r.client.Get(context.TODO(), req.NamespacedName, updatedHumioCluster)
			if err != nil {
				t.Errorf("get HumioCluster: (%v)", err)
			}
			if updatedHumioCluster.Status.State != corev1alpha1.HumioClusterStateBootstrapping {
				t.Errorf("expected cluster state to be %s but got %s", corev1alpha1.HumioClusterStateBootstrapping, updatedHumioCluster.Status.State)
			}
			tt.humioCluster = updatedHumioCluster

			for nodeCount := 0; nodeCount < tt.humioCluster.Spec.NodeCount; nodeCount++ {
				foundPodList, err := kubernetes.ListPods(r.client, updatedHumioCluster.Namespace, kubernetes.MatchingLabelsForHumio(updatedHumioCluster.Name))
				if err != nil {
					t.Errorf("failed to list pods: %s", err)
				}
				if len(foundPodList) != nodeCount+1 {
					t.Errorf("expected list pods to return equal to %d, got %d", nodeCount+1, len(foundPodList))
				}

				// We must update the IP address because when we attempt to add labels to the pod we validate that they have IP addresses first
				// We also must update the ready condition as the reconciler will wait until all pods are ready before continuing
				err = markPodsAsRunning(r.client, foundPodList)
				if err != nil {
					t.Errorf("failed to update pods to prepare for testing the labels: %s", err)
				}

				// Reconcile again so Reconcile() checks pods and updates the HumioCluster resources' Status.
				_, err = r.Reconcile(req)
				if err != nil {
					t.Errorf("reconcile: (%v)", err)
				}
			}

			// Simulate sidecar creating the secret which contains the admin token use to authenticate with humio
			secretData := map[string][]byte{"token": []byte("")}
			adminTokenSecretName := fmt.Sprintf("%s-%s", updatedHumioCluster.Name, kubernetes.ServiceTokenSecretNameSuffix)
			desiredSecret := kubernetes.ConstructSecret(updatedHumioCluster.Name, updatedHumioCluster.Namespace, adminTokenSecretName, secretData)
			err = r.client.Create(context.TODO(), desiredSecret)
			if err != nil {
				t.Errorf("unable to create service token secret: %s", err)
			}
			_, err = r.Reconcile(req)
			if err != nil {
				t.Errorf("reconcile: (%v)", err)
			}

			// Test that we have the proper status
			updatedHumioCluster = &corev1alpha1.HumioCluster{}
			err = r.client.Get(context.TODO(), req.NamespacedName, updatedHumioCluster)
			if err != nil {
				t.Errorf("get HumioCluster: (%v)", err)
			}
			if updatedHumioCluster.Status.State != corev1alpha1.HumioClusterStateRunning {
				t.Errorf("expected cluster state to be %s but got %s", corev1alpha1.HumioClusterStateRunning, updatedHumioCluster.Status.State)
			}
			if updatedHumioCluster.Status.Version != tt.version {
				t.Errorf("expected cluster version to be %s but got %s", tt.version, updatedHumioCluster.Status.Version)
			}
			if updatedHumioCluster.Status.NodeCount != tt.humioCluster.Spec.NodeCount {
				t.Errorf("expected node count to be %d but got %d", tt.humioCluster.Spec.NodeCount, updatedHumioCluster.Status.NodeCount)
			}

			// Update humio image
			updatedHumioCluster.Spec.Image = tt.imageToUpdate
			r.client.Update(context.TODO(), updatedHumioCluster)

			for nodeCount := 0; nodeCount < tt.humioCluster.Spec.NodeCount; nodeCount++ {
				res, err := r.Reconcile(req)
				if err != nil {
					t.Errorf("reconcile: (%v)", err)
				}
				if res != (reconcile.Result{Requeue: true}) {
					t.Errorf("reconcile did not match expected: %v", res)
				}
			}

			// Ensure all the pods are shut down to prep for the image update (the first check where foundPodList == 0)
			// Simulate the reconcile being run again for each node so they all are started (the following checks)
			for nodeCount := 0; nodeCount <= tt.humioCluster.Spec.NodeCount; nodeCount++ {
				foundPodList, err := kubernetes.ListPods(r.client, updatedHumioCluster.Namespace, kubernetes.MatchingLabelsForHumio(updatedHumioCluster.Name))
				if err != nil {
					t.Errorf("failed to list pods: %s", err)
				}
				if len(foundPodList) != nodeCount {
					t.Errorf("expected list pods to return equal to %d, got %d", nodeCount, len(foundPodList))
				}

				// We must update the IP address because when we attempt to add labels to the pod we validate that they have IP addresses first
				// We also must update the ready condition as the reconciler will wait until all pods are ready before continuing
				err = markPodsAsRunning(r.client, foundPodList)
				if err != nil {
					t.Errorf("failed to update pods to prepare for testing the labels: %s", err)
				}

				// check that the cluster is in state Upgrading
				updatedHumioCluster := &corev1alpha1.HumioCluster{}
				err = r.client.Get(context.TODO(), req.NamespacedName, updatedHumioCluster)
				if err != nil {
					t.Errorf("get HumioCluster: (%v)", err)
				}
				if updatedHumioCluster.Status.State != corev1alpha1.HumioClusterStateUpgrading {
					t.Errorf("expected cluster state to be %s but got %s", corev1alpha1.HumioClusterStateUpgrading, updatedHumioCluster.Status.State)
				}

				// Reconcile again so Reconcile() checks pods and updates the HumioCluster resources' Status.
				_, err = r.Reconcile(req)
				if err != nil {
					t.Errorf("reconcile: (%v)", err)
				}
			}

			// Test that we have the proper status
			updatedHumioCluster = &corev1alpha1.HumioCluster{}
			err = r.client.Get(context.TODO(), req.NamespacedName, updatedHumioCluster)
			if err != nil {
				t.Errorf("get HumioCluster: (%v)", err)
			}
			if updatedHumioCluster.Status.State != corev1alpha1.HumioClusterStateRunning {
				t.Errorf("expected cluster state to be %s but got %s", corev1alpha1.HumioClusterStateRunning, updatedHumioCluster.Status.State)
			}
			if updatedHumioCluster.Status.Version != tt.version {
				t.Errorf("expected cluster version to be %s but got %s", tt.version, updatedHumioCluster.Status.Version)
			}
			if updatedHumioCluster.Status.NodeCount != tt.humioCluster.Spec.NodeCount {
				t.Errorf("expected node count to be %d but got %d", tt.humioCluster.Spec.NodeCount, updatedHumioCluster.Status.NodeCount)
			}
		})
	}
}

func TestReconcileHumioCluster_Reconcile_update_environment_variable(t *testing.T) {
	tests := []struct {
		name           string
		humioCluster   *corev1alpha1.HumioCluster
		humioClient    *humio.MockClientConfig
		envVarToUpdate corev1.EnvVar
		desiredEnvVar  corev1.EnvVar
	}{
		{
			"test simple cluster environment variable update",
			&corev1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humiocluster",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioClusterSpec{
					EnvironmentVariables: []corev1.EnvVar{
						{
							Name:  "test",
							Value: "",
						},
					},
					TargetReplicationFactor: 2,
					StoragePartitionsCount:  3,
					DigestPartitionsCount:   3,
					NodeCount:               3,
				},
			},
			humio.NewMocklient(
				humioapi.Cluster{
					Nodes:             buildClusterNodesList(3),
					StoragePartitions: buildStoragePartitionsList(3, 1),
					IngestPartitions:  buildIngestPartitionsList(3, 1),
				}, nil, nil, nil, "1.9.2--build-12365--sha-bf4188482a"),
			corev1.EnvVar{
				Name:  "test",
				Value: "update",
			},
			corev1.EnvVar{
				Name:  "test",
				Value: "update",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, req := reconcileWithHumioClient(tt.humioCluster, tt.humioClient)
			defer r.logger.Sync()

			_, err := r.Reconcile(req)
			if err != nil {
				t.Errorf("reconcile: (%v)", err)
			}

			updatedHumioCluster := &corev1alpha1.HumioCluster{}
			err = r.client.Get(context.TODO(), req.NamespacedName, updatedHumioCluster)
			if err != nil {
				t.Errorf("get HumioCluster: (%v)", err)
			}
			if updatedHumioCluster.Status.State != corev1alpha1.HumioClusterStateBootstrapping {
				t.Errorf("expected cluster state to be %s but got %s", corev1alpha1.HumioClusterStateBootstrapping, updatedHumioCluster.Status.State)
			}
			tt.humioCluster = updatedHumioCluster

			for nodeCount := 0; nodeCount < tt.humioCluster.Spec.NodeCount; nodeCount++ {
				foundPodList, err := kubernetes.ListPods(r.client, updatedHumioCluster.Namespace, kubernetes.MatchingLabelsForHumio(updatedHumioCluster.Name))
				if err != nil {
					t.Errorf("failed to list pods: %s", err)
				}
				if len(foundPodList) != nodeCount+1 {
					t.Errorf("expected list pods to return equal to %d, got %d", nodeCount+1, len(foundPodList))
				}

				// We must update the IP address because when we attempt to add labels to the pod we validate that they have IP addresses first
				// We also must update the ready condition as the reconciler will wait until all pods are ready before continuing
				err = markPodsAsRunning(r.client, foundPodList)
				if err != nil {
					t.Errorf("failed to update pods to prepare for testing the labels: %s", err)
				}

				// Reconcile again so Reconcile() checks pods and updates the HumioCluster resources' Status.
				_, err = r.Reconcile(req)
				if err != nil {
					t.Errorf("reconcile: (%v)", err)
				}
			}

			// Simulate sidecar creating the secret which contains the admin token use to authenticate with humio
			secretData := map[string][]byte{"token": []byte("")}
			desiredSecret := kubernetes.ConstructSecret(updatedHumioCluster.Name, updatedHumioCluster.Namespace, fmt.Sprintf("%s-%s", updatedHumioCluster.Name, kubernetes.ServiceTokenSecretNameSuffix), secretData)
			err = r.client.Create(context.TODO(), desiredSecret)
			if err != nil {
				t.Errorf("unable to create service token secret: %s", err)
			}
			_, err = r.Reconcile(req)
			if err != nil {
				t.Errorf("reconcile: (%v)", err)
			}

			// Test that we have the proper status
			updatedHumioCluster = &corev1alpha1.HumioCluster{}
			err = r.client.Get(context.TODO(), req.NamespacedName, updatedHumioCluster)
			if err != nil {
				t.Errorf("get HumioCluster: (%v)", err)
			}
			if updatedHumioCluster.Status.State != corev1alpha1.HumioClusterStateRunning {
				t.Errorf("expected cluster state to be %s but got %s", corev1alpha1.HumioClusterStateRunning, updatedHumioCluster.Status.State)
			}

			if updatedHumioCluster.Status.NodeCount != tt.humioCluster.Spec.NodeCount {
				t.Errorf("expected node count to be %d but got %d", tt.humioCluster.Spec.NodeCount, updatedHumioCluster.Status.NodeCount)
			}

			// Update humio env var
			for idx, envVar := range updatedHumioCluster.Spec.EnvironmentVariables {
				if envVar.Name == "test" {
					updatedHumioCluster.Spec.EnvironmentVariables[idx] = tt.envVarToUpdate
				}
			}
			r.client.Update(context.TODO(), updatedHumioCluster)

			// Reconcile again so Reconcile() checks pods and updates the HumioCluster resources' Status.
			_, err = r.Reconcile(req)
			if err != nil {
				t.Errorf("reconcile: (%v)", err)
			}

			// Simulate the reconcile being run again for each node so they all are restarted
			for nodeCount := 0; nodeCount < tt.humioCluster.Spec.NodeCount; nodeCount++ {

				foundPodList, err := kubernetes.ListPods(r.client, updatedHumioCluster.Namespace, kubernetes.MatchingLabelsForHumio(updatedHumioCluster.Name))
				if err != nil {
					t.Errorf("failed to list pods: %s", err)
				}
				if len(foundPodList) != tt.humioCluster.Spec.NodeCount {
					t.Errorf("expected list pods to return equal to %d, got %d", nodeCount, tt.humioCluster.Spec.NodeCount)
				}

				// check that the cluster is in state Upgrading
				updatedHumioCluster := &corev1alpha1.HumioCluster{}
				err = r.client.Get(context.TODO(), req.NamespacedName, updatedHumioCluster)
				if err != nil {
					t.Errorf("get HumioCluster: (%v)", err)
				}
				if updatedHumioCluster.Status.State != corev1alpha1.HumioClusterStateRestarting {
					t.Errorf("expected cluster state to be %s but got %s", corev1alpha1.HumioClusterStateRestarting, updatedHumioCluster.Status.State)
				}

				// We must update the IP address because when we attempt to add labels to the pod we validate that they have IP addresses first
				// We also must update the ready condition as the reconciler will wait until all pods are ready before continuing
				err = markPodsAsRunning(r.client, foundPodList)
				if err != nil {
					t.Errorf("failed to update pods to prepare for testing the labels: %s", err)
				}

				// Reconcile again so Reconcile() checks pods and updates the HumioCluster resources' Status.
				_, err = r.Reconcile(req)
				if err != nil {
					t.Errorf("reconcile: (%v)", err)
				}

			}

			// Test that we have the proper status
			updatedHumioCluster = &corev1alpha1.HumioCluster{}
			err = r.client.Get(context.TODO(), req.NamespacedName, updatedHumioCluster)
			if err != nil {
				t.Errorf("get HumioCluster: (%v)", err)
			}
			if updatedHumioCluster.Status.State != corev1alpha1.HumioClusterStateRunning {
				t.Errorf("expected cluster state to be %s but got %s", corev1alpha1.HumioClusterStateRunning, updatedHumioCluster.Status.State)
			}

			if updatedHumioCluster.Status.NodeCount != tt.humioCluster.Spec.NodeCount {
				t.Errorf("expected node count to be %d but got %d", tt.humioCluster.Spec.NodeCount, updatedHumioCluster.Status.NodeCount)
			}
			for _, envVar := range updatedHumioCluster.Spec.EnvironmentVariables {
				if envVar.Name == "test" {
					if envVar.Value != tt.desiredEnvVar.Value {
						t.Errorf("expected test cluster env var to be %s but got %s", tt.desiredEnvVar.Value, envVar.Value)
					}
				}
			}
		})
	}
}

func TestReconcileHumioCluster_Reconcile_init_service_account(t *testing.T) {
	tests := []struct {
		name                       string
		humioCluster               *corev1alpha1.HumioCluster
		humioClient                *humio.MockClientConfig
		version                    string
		wantInitServiceAccount     bool
		wantInitClusterRole        bool
		wantInitClusterRoleBinding bool
	}{
		{
			"test cluster reconciliation with no init service account specified creates the service account, cluster role and cluster role binding",
			&corev1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humiocluster",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioClusterSpec{},
			},
			humio.NewMocklient(
				humioapi.Cluster{}, nil, nil, nil, ""), "",
			true,
			true,
			true,
		},
		{
			"test cluster reconciliation with an init service account specified does not create the service account, cluster role and cluster role binding",
			&corev1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humiocluster",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioClusterSpec{
					InitServiceAccountName: "some-custom-service-account",
				},
			},
			humio.NewMocklient(
				humioapi.Cluster{}, nil, nil, nil, ""), "",
			false,
			false,
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, req := reconcileWithHumioClient(tt.humioCluster, tt.humioClient)
			defer r.logger.Sync()

			_, err := r.Reconcile(req)
			if err != nil {
				t.Errorf("reconcile: (%v)", err)
			}

			// Check that the init service account, cluster role and cluster role binding are created only if they should be
			serviceAccount, err := kubernetes.GetServiceAccount(context.TODO(), r.client, initServiceAccountNameOrDefault(tt.humioCluster), tt.humioCluster.Namespace)
			if (err != nil) == tt.wantInitServiceAccount {
				t.Errorf("failed to check init service account: %s", err)
			}
			if reflect.DeepEqual(serviceAccount, &corev1.ServiceAccount{}) == tt.wantInitServiceAccount {
				t.Errorf("failed to compare init service account: %s, wantInitServiceAccount: %v", serviceAccount, tt.wantInitServiceAccount)
			}

			clusterRole, err := kubernetes.GetClusterRole(context.TODO(), r.client, initClusterRoleName(tt.humioCluster))
			if (err != nil) == tt.wantInitClusterRole {
				t.Errorf("failed to get init cluster role: %s", err)
			}
			if reflect.DeepEqual(clusterRole, &rbacv1.ClusterRole{}) == tt.wantInitClusterRole {
				t.Errorf("failed to compare init cluster role: %s, wantInitClusterRole %v", clusterRole, tt.wantInitClusterRole)
			}

			clusterRoleBinding, err := kubernetes.GetClusterRoleBinding(context.TODO(), r.client, initClusterRoleBindingName(tt.humioCluster))
			if (err != nil) == tt.wantInitClusterRoleBinding {
				t.Errorf("failed to get init cluster role binding: %s", err)
			}
			if reflect.DeepEqual(clusterRoleBinding, &rbacv1.ClusterRoleBinding{}) == tt.wantInitClusterRoleBinding {
				t.Errorf("failed to compare init cluster role binding: %s, wantInitClusterRoleBinding: %v", clusterRoleBinding, tt.wantInitClusterRoleBinding)
			}
		})
	}
}

func TestReconcileHumioCluster_Reconcile_extra_kafka_configs_configmap(t *testing.T) {
	tests := []struct {
		name                           string
		humioCluster                   *corev1alpha1.HumioCluster
		humioClient                    *humio.MockClientConfig
		version                        string
		wantExtraKafkaConfigsConfigMap bool
	}{
		{
			"test cluster reconciliation with no extra kafka configs",
			&corev1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humiocluster",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioClusterSpec{},
			},
			humio.NewMocklient(
				humioapi.Cluster{}, nil, nil, nil, ""), "",
			false,
		},
		{
			"test cluster reconciliation with extra kafka configs",
			&corev1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humiocluster",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioClusterSpec{
					ExtraKafkaConfigs: "security.protocol=SSL",
				},
			},
			humio.NewMocklient(
				humioapi.Cluster{}, nil, nil, nil, ""), "",
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, req := reconcileWithHumioClient(tt.humioCluster, tt.humioClient)
			defer r.logger.Sync()

			_, err := r.Reconcile(req)
			if err != nil {
				t.Errorf("reconcile: (%v)", err)
			}
			configMap, err := kubernetes.GetConfigMap(context.TODO(), r.client, extraKafkaConfigsConfigMapName(tt.humioCluster), tt.humioCluster.Namespace)
			if (err != nil) == tt.wantExtraKafkaConfigsConfigMap {
				t.Errorf("failed to check extra kafka configs configMap: %s", err)
			}
			if reflect.DeepEqual(configMap, &corev1.ConfigMap{}) == tt.wantExtraKafkaConfigsConfigMap {
				t.Errorf("failed to compare extra kafka configs configMap: %s, wantExtraKafkaConfigsConfigMap: %v", configMap, tt.wantExtraKafkaConfigsConfigMap)
			}
			foundEnvVar := false
			foundVolumeMount := false
			if tt.wantExtraKafkaConfigsConfigMap {
				foundPodList, err := kubernetes.ListPods(r.client, tt.humioCluster.Namespace, kubernetes.MatchingLabelsForHumio(tt.humioCluster.Name))
				if err != nil {
					t.Errorf("failed to list pods %s", err)
				}
				if len(foundPodList) > 0 {
					for _, container := range foundPodList[0].Spec.Containers {
						if container.Name != "humio" {
							continue
						}
						for _, env := range container.Env {
							if env.Name == "EXTRA_KAFKA_CONFIGS_FILE" {
								foundEnvVar = true
							}
						}
						for _, volumeMount := range container.VolumeMounts {
							if volumeMount.Name == "extra-kafka-configs" {
								foundVolumeMount = true
							}
						}
					}

				}
			}
			if tt.wantExtraKafkaConfigsConfigMap && !foundEnvVar {
				t.Errorf("failed to validate extra kafka configs env var, want: %v, got %v", tt.wantExtraKafkaConfigsConfigMap, foundEnvVar)
			}
			if tt.wantExtraKafkaConfigsConfigMap && !foundVolumeMount {
				t.Errorf("failed to validate extra kafka configs volume mount, want: %v, got %v", tt.wantExtraKafkaConfigsConfigMap, foundVolumeMount)
			}
		})
	}
}

func TestReconcileHumioCluster_Reconcile_extra_volumes(t *testing.T) {
	tests := []struct {
		name                       string
		humioCluster               *corev1alpha1.HumioCluster
		humioClient                *humio.MockClientConfig
		version                    string
		wantExtraHumioVolumeMounts []corev1.VolumeMount
		wantExtraVolumes           []corev1.Volume
		wantError                  bool
	}{
		{
			"test cluster reconciliation with no extra volumes",
			&corev1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humiocluster",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioClusterSpec{},
			},
			humio.NewMocklient(
				humioapi.Cluster{}, nil, nil, nil, ""), "",
			[]corev1.VolumeMount{},
			[]corev1.Volume{},
			false,
		},
		{
			"test cluster reconciliation with extra volumes",
			&corev1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humiocluster",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioClusterSpec{
					ExtraHumioVolumeMounts: []corev1.VolumeMount{
						{
							Name:      "gcp-storage-account-json-file",
							MountPath: "/var/lib/humio/gcp-storage-account-json-file",
							ReadOnly:  true,
						},
					},
					ExtraVolumes: []corev1.Volume{
						{
							Name: "gcp-storage-account-json-file",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: "gcp-storage-account-json-file",
								},
							},
						},
					},
				},
			},
			humio.NewMocklient(
				humioapi.Cluster{}, nil, nil, nil, ""), "",
			[]corev1.VolumeMount{
				{
					Name:      "gcp-storage-account-json-file",
					MountPath: "/var/lib/humio/gcp-storage-account-json-file",
					ReadOnly:  true,
				},
			},
			[]corev1.Volume{
				{
					Name: "gcp-storage-account-json-file",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: "gcp-storage-account-json-file",
						},
					},
				},
			},
			false,
		},
		{
			"test cluster reconciliation with conflicting volume name",
			&corev1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humiocluster",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioClusterSpec{
					ExtraHumioVolumeMounts: []corev1.VolumeMount{
						{
							Name: "humio-data",
						},
					},
				},
			},
			humio.NewMocklient(
				humioapi.Cluster{}, nil, nil, nil, ""), "",
			[]corev1.VolumeMount{},
			[]corev1.Volume{},
			true,
		},
		{
			"test cluster reconciliation with conflicting volume mount path",
			&corev1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humiocluster",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioClusterSpec{
					ExtraHumioVolumeMounts: []corev1.VolumeMount{
						{
							Name:      "something-unique",
							MountPath: humioAppPath,
						},
					},
				},
			},
			humio.NewMocklient(
				humioapi.Cluster{}, nil, nil, nil, ""), "",
			[]corev1.VolumeMount{},
			[]corev1.Volume{},
			true,
		},
		{
			"test cluster reconciliation with conflicting volume name",
			&corev1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humiocluster",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioClusterSpec{
					ExtraVolumes: []corev1.Volume{
						{
							Name: "humio-data",
						},
					},
				},
			},
			humio.NewMocklient(
				humioapi.Cluster{}, nil, nil, nil, ""), "",
			[]corev1.VolumeMount{},
			[]corev1.Volume{},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, req := reconcileWithHumioClient(tt.humioCluster, tt.humioClient)
			defer r.logger.Sync()

			_, err := r.Reconcile(req)
			if !tt.wantError && err != nil {
				t.Errorf("reconcile: (%v)", err)
			}
			if tt.wantError {
				if err == nil {
					t.Errorf("did not receive error when ensuring volumes, expected: %v, got %v", tt.wantError, err)
				}
				return
			}

			var humioVolumeMounts []corev1.VolumeMount
			var volumes []corev1.Volume

			foundVolumeMountsCount := 0
			foundVolumesCount := 0

			foundPodList, err := kubernetes.ListPods(r.client, tt.humioCluster.Namespace, kubernetes.MatchingLabelsForHumio(tt.humioCluster.Name))
			if err != nil {
				t.Errorf("failed to list pods %s", err)
			}
			if len(foundPodList) > 0 {
				for _, podVolume := range foundPodList[0].Spec.Volumes {
					volumes = append(volumes, podVolume)
				}
				for _, container := range foundPodList[0].Spec.Containers {
					if container.Name != "humio" {
						continue
					}
					for _, containerVolumeMount := range container.VolumeMounts {
						humioVolumeMounts = append(humioVolumeMounts, containerVolumeMount)
					}
				}
			}

			for _, humioVolumeMount := range humioVolumeMounts {
				for _, wantHumioVolumeMount := range tt.wantExtraHumioVolumeMounts {
					if reflect.DeepEqual(humioVolumeMount, wantHumioVolumeMount) {
						foundVolumeMountsCount++
					}
				}
			}
			for _, volume := range volumes {
				for _, wantVolume := range tt.wantExtraVolumes {
					if reflect.DeepEqual(volume, wantVolume) {
						foundVolumesCount++
					}
				}
			}

			if len(tt.wantExtraHumioVolumeMounts) != foundVolumeMountsCount {
				t.Errorf("failed to validate extra volume mounts, want: %v, got %d matching volume mounts", tt.wantExtraHumioVolumeMounts, foundVolumeMountsCount)
			}
			if len(tt.wantExtraVolumes) != foundVolumesCount {
				t.Errorf("failed to validate extra volumes, want: %v, got %d matching volumes", tt.wantExtraVolumes, foundVolumesCount)
			}

		})
	}
}

func TestReconcileHumioCluster_Reconcile_persistent_volumes(t *testing.T) {
	tests := []struct {
		name         string
		humioCluster *corev1alpha1.HumioCluster
		humioClient  *humio.MockClientConfig
		version      string
	}{
		{
			"test cluster reconciliation with persistent volumes",
			&corev1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humiocluster",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioClusterSpec{
					NodeCount: 3,
					DataVolumePersistentVolumeClaimSpecTemplate: corev1.PersistentVolumeClaimSpec{
						AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: resource.MustParse("10Gi"),
							},
						},
					},
				},
			},
			humio.NewMocklient(
				humioapi.Cluster{}, nil, nil, nil, ""), "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, req := reconcileWithHumioClient(tt.humioCluster, tt.humioClient)
			defer r.logger.Sync()

			// Simulate creating pvcs
			for nodeCount := 0; nodeCount <= tt.humioCluster.Spec.NodeCount; nodeCount++ {
				_, err := r.Reconcile(req)
				if err != nil {
					t.Errorf("reconcile: (%v)", err)
				}
			}

			pvcList, err := kubernetes.ListPersistentVolumeClaims(r.client, tt.humioCluster.Namespace, kubernetes.MatchingLabelsForHumio(tt.humioCluster.Name))
			if err != nil {
				t.Errorf("failed to list pvcs %s", err)
			}
			if len(pvcList) != tt.humioCluster.Spec.NodeCount {
				t.Errorf("failed to validate pvcs, want: %v, got %v", tt.humioCluster.Spec.NodeCount, len(pvcList))
			}

			// Simulate creating pods
			for nodeCount := 1; nodeCount < tt.humioCluster.Spec.NodeCount; nodeCount++ {
				foundPodList, err := kubernetes.ListPods(r.client, tt.humioCluster.Namespace, kubernetes.MatchingLabelsForHumio(tt.humioCluster.Name))
				if err != nil {
					t.Errorf("failed to list pods: %s", err)
				}
				if len(foundPodList) != nodeCount {
					t.Errorf("expected list pods to return equal to %d, got %d", nodeCount, len(foundPodList))
				}

				// We must update the IP address because when we attempt to add labels to the pod we validate that they have IP addresses first
				// We also must update the ready condition as the reconciler will wait until all pods are ready before continuing
				err = markPodsAsRunning(r.client, foundPodList)
				if err != nil {
					t.Errorf("failed to update pods to prepare for testing pvcs: %s", err)
				}

				// Reconcile again so Reconcile() checks pods and updates the HumioCluster resources' Status.
				res, err := r.Reconcile(req)
				if err != nil {
					t.Errorf("reconcile: (%v)", err)
				}
				if res != (reconcile.Result{Requeue: true}) {
					t.Errorf("reconcile: (%v)", res)
				}
			}

			// Check that each pod is using a pvc that we created
			foundPodList, err := kubernetes.ListPods(r.client, tt.humioCluster.Namespace, kubernetes.MatchingLabelsForHumio(tt.humioCluster.Name))
			if err != nil {
				t.Errorf("failed to list pods: %s", err)
			}
			for _, pod := range foundPodList {
				if _, err := findPvcForPod(pvcList, pod); err != nil {
					t.Errorf("failed to get pvc for pod: expected pvc but got error %s", err)
				}
			}

			// Check that we have used all the pvcs that we have available
			if pvcName, err := findNextAvailablePvc(pvcList, foundPodList); err == nil {
				t.Errorf("expected pvc %s to be used but it is available", pvcName)
			}
		})
	}
}

func TestReconcileHumioCluster_Reconcile_container_security_context(t *testing.T) {
	tests := []struct {
		name                       string
		humioCluster               *corev1alpha1.HumioCluster
		wantDefaultSecurityContext bool
	}{
		{
			"test cluster reconciliation with no container security context",
			&corev1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humiocluster",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioClusterSpec{},
			},
			true,
		},
		{
			"test cluster reconciliation with empty container security context",
			&corev1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humiocluster",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioClusterSpec{
					ContainerSecurityContext: &corev1.SecurityContext{},
				},
			},
			false,
		},
		{
			"test cluster reconciliation with non-empty container security context",
			&corev1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humiocluster",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioClusterSpec{
					ContainerSecurityContext: &corev1.SecurityContext{
						Capabilities: &corev1.Capabilities{
							Add: []corev1.Capability{
								"NET_ADMIN",
							},
						},
					},
				},
			},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, req := reconcileInit(tt.humioCluster)
			defer r.logger.Sync()

			_, err := r.Reconcile(req)
			if err != nil {
				t.Errorf("reconcile: (%v)", err)
			}

			foundPodList, err := kubernetes.ListPods(r.client, tt.humioCluster.Namespace, kubernetes.MatchingLabelsForHumio(tt.humioCluster.Name))
			if err != nil {
				t.Errorf("failed to list pods %s", err)
			}

			foundExpectedSecurityContext := false
			if tt.wantDefaultSecurityContext {
				if reflect.DeepEqual(*foundPodList[0].Spec.Containers[0].SecurityContext, *containerSecurityContextOrDefault(tt.humioCluster)) {
					foundExpectedSecurityContext = true
				}
			} else {
				if reflect.DeepEqual(*foundPodList[0].Spec.Containers[0].SecurityContext, *tt.humioCluster.Spec.ContainerSecurityContext) {
					foundExpectedSecurityContext = true
				}
			}

			if !foundExpectedSecurityContext {
				t.Errorf("failed to validate container security context, expected: %v, got %v", *tt.humioCluster.Spec.ContainerSecurityContext, *foundPodList[0].Spec.Containers[0].SecurityContext)
			}
		})
	}
}

func TestReconcileHumioCluster_Reconcile_pod_security_context(t *testing.T) {
	tests := []struct {
		name                       string
		humioCluster               *corev1alpha1.HumioCluster
		wantDefaultSecurityContext bool
	}{
		{
			"test cluster reconciliation with no pod security context",
			&corev1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humiocluster",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioClusterSpec{},
			},
			true,
		},
		{
			"test cluster reconciliation with empty pod security context",
			&corev1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humiocluster",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioClusterSpec{
					PodSecurityContext: &corev1.PodSecurityContext{},
				},
			},
			false,
		},
		{
			"test cluster reconciliation with non-empty pod security context",
			&corev1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humiocluster",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioClusterSpec{
					PodSecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: helpers.BoolPtr(true),
					},
				},
			},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, req := reconcileInit(tt.humioCluster)
			defer r.logger.Sync()

			_, err := r.Reconcile(req)

			if err != nil {
				t.Errorf("reconcile: (%v)", err)
			}

			foundPodList, err := kubernetes.ListPods(r.client, tt.humioCluster.Namespace, kubernetes.MatchingLabelsForHumio(tt.humioCluster.Name))
			if err != nil {
				t.Errorf("failed to list pods %s", err)
			}

			foundExpectedSecurityContext := false
			if tt.wantDefaultSecurityContext {
				if reflect.DeepEqual(*foundPodList[0].Spec.SecurityContext, *podSecurityContextOrDefault(tt.humioCluster)) {
					foundExpectedSecurityContext = true
				}
			} else {
				if reflect.DeepEqual(*foundPodList[0].Spec.SecurityContext, *tt.humioCluster.Spec.PodSecurityContext) {
					foundExpectedSecurityContext = true
				}
			}

			if !foundExpectedSecurityContext {
				t.Errorf("failed to validate pod security context, expected: %v, got %v", *tt.humioCluster.Spec.PodSecurityContext, *foundPodList[0].Spec.SecurityContext)
			}
		})
	}
}

func TestReconcileHumioCluster_Reconcile_ensure_service_account_annotations(t *testing.T) {
	tests := []struct {
		name                  string
		humioCluster          *corev1alpha1.HumioCluster
		updatedPodAnnotations map[string]string
		wantPodAnnotations    map[string]string
	}{
		{
			"test cluster reconciliation with no service account annotations",
			&corev1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humiocluster",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioClusterSpec{},
			},
			map[string]string(nil),
			map[string]string(nil),
		},
		{
			"test cluster reconciliation with initial service account annotations",
			&corev1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humiocluster",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioClusterSpec{
					HumioServiceAccountAnnotations: map[string]string{"some": "annotation"},
				},
			},
			map[string]string(nil),
			map[string]string{"some": "annotation"},
		},
		{
			"test cluster reconciliation with updated service account annotations",
			&corev1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humiocluster",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioClusterSpec{},
			},
			map[string]string{"some-updated": "annotation"},
			map[string]string{"some-updated": "annotation"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, req := reconcileInit(tt.humioCluster)
			defer r.logger.Sync()

			_, err := r.Reconcile(req)
			if err != nil {
				t.Errorf("reconcile: (%v)", err)
			}

			if reflect.DeepEqual(tt.wantPodAnnotations, tt.updatedPodAnnotations) {
				// test updating the annotations
				updatedHumioCluster := &corev1alpha1.HumioCluster{}
				err = r.client.Get(context.TODO(), req.NamespacedName, updatedHumioCluster)
				if err != nil {
					t.Errorf("get HumioCluster: (%v)", err)
				}
				updatedHumioCluster.Spec.HumioServiceAccountAnnotations = tt.updatedPodAnnotations
				r.client.Update(context.TODO(), updatedHumioCluster)

				_, err := r.Reconcile(req)
				if err != nil {
					t.Errorf("reconcile: (%v)", err)
				}
			}

			serviceAccount, err := kubernetes.GetServiceAccount(context.TODO(), r.client, humioServiceAccountNameOrDefault(tt.humioCluster), tt.humioCluster.Namespace)
			if err != nil {
				t.Errorf("failed to get service account")
			}

			if !reflect.DeepEqual(serviceAccount.Annotations, tt.wantPodAnnotations) {
				t.Errorf("failed to validate updated service account annotations, expected: %v, got %v", tt.wantPodAnnotations, serviceAccount.Annotations)
			}
		})
	}
}

func TestReconcileHumioCluster_ensureIngress_create_ingress(t *testing.T) {
	tests := []struct {
		name                  string
		humioCluster          *corev1alpha1.HumioCluster
		wantNumIngressObjects int
		wantError             bool
	}{
		{
			"test nginx controller",
			&corev1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humiocluster",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioClusterSpec{
					Hostname:   "humio.example.com",
					ESHostname: "humio-es.example.com",
					Ingress: corev1alpha1.HumioClusterIngressSpec{
						Enabled:    true,
						Controller: "nginx",
						Annotations: map[string]string{
							"use-http01-solver":              "true",
							"cert-manager.io/cluster-issuer": "letsencrypt-prod",
							"kubernetes.io/ingress.class":    "nginx",
						},
					},
				},
			},
			4,
			false,
		},
		{
			"test invalid controller",
			&corev1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humiocluster",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioClusterSpec{
					Hostname:   "humio.example.com",
					ESHostname: "humio-es.example.com",
					Ingress: corev1alpha1.HumioClusterIngressSpec{
						Enabled:    true,
						Controller: "invalid",
					},
				},
			},
			0,
			true,
		},
		{
			"test without specifying controller",
			&corev1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humiocluster",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioClusterSpec{
					Hostname:   "humio.example.com",
					ESHostname: "humio-es.example.com",
					Ingress: corev1alpha1.HumioClusterIngressSpec{
						Enabled: true,
					},
				},
			},
			0,
			true,
		},
		{
			"test without ingress enabled",
			&corev1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humiocluster",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioClusterSpec{},
			},
			0,
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := reconcileInit(tt.humioCluster)
			defer r.logger.Sync()

			err := r.ensureIngress(context.TODO(), tt.humioCluster)

			foundIngressList, listErr := kubernetes.ListIngresses(r.client, tt.humioCluster.Namespace, kubernetes.MatchingLabelsForHumio(tt.humioCluster.Name))
			if listErr != nil {
				t.Errorf("failed to list pods %s", listErr)
			}

			foundExpectedIngressObjects := 0
			expectedAnnotationsFound := 0
			if tt.wantNumIngressObjects > 0 {
				if tt.humioCluster.Spec.Ingress.Enabled && tt.humioCluster.Spec.Ingress.Controller == "nginx" {
					foundExpectedIngressObjects = len(foundIngressList)
					for expectedAnnotationKey, expectedAnnotationValue := range tt.humioCluster.Spec.Ingress.Annotations {
						for _, foundIngress := range foundIngressList {
							for foundAnnotationKey, foundAnnotationValue := range foundIngress.Annotations {
								if expectedAnnotationKey == foundAnnotationKey && expectedAnnotationValue == foundAnnotationValue {
									expectedAnnotationsFound++
								}
							}
						}
					}
				}
			}

			if tt.wantError && err == nil {
				t.Errorf("did not receive error when ensuring ingress, expected: %v, got %v", tt.wantError, err)
			}

			if tt.wantNumIngressObjects > 0 && !(tt.wantNumIngressObjects == foundExpectedIngressObjects) {
				t.Errorf("failed to validate ingress, expected: %v objects, got %v", tt.wantNumIngressObjects, foundExpectedIngressObjects)
			}

			if tt.wantNumIngressObjects > 0 && !(expectedAnnotationsFound == (len(tt.humioCluster.Spec.Ingress.Annotations) * tt.wantNumIngressObjects)) {
				t.Errorf("failed to validate ingress annotations, expected to find: %v annotations, got %v", len(tt.humioCluster.Spec.Ingress.Annotations)*tt.wantNumIngressObjects, expectedAnnotationsFound)
			}
		})
	}
}

func TestReconcileHumioCluster_ensureIngress_update_ingress(t *testing.T) {
	tests := []struct {
		name           string
		humioCluster   *corev1alpha1.HumioCluster
		newAnnotations map[string]string
		newHostname    string
		newESHostname  string
	}{
		{
			"add annotation",
			&corev1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humiocluster",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioClusterSpec{
					Hostname:   "humio.example.com",
					ESHostname: "humio-es.example.com",
					Ingress: corev1alpha1.HumioClusterIngressSpec{
						Enabled:    true,
						Controller: "nginx",
						Annotations: map[string]string{
							"use-http01-solver":              "true",
							"cert-manager.io/cluster-issuer": "letsencrypt-prod",
							"kubernetes.io/ingress.class":    "nginx",
						},
					},
				},
			},
			map[string]string{
				"use-http01-solver":                  "true",
				"cert-manager.io/cluster-issuer":     "letsencrypt-prod",
				"kubernetes.io/ingress.class":        "nginx",
				"humio.com/new-important-annotation": "true",
			},
			"humio.example.com",
			"humio-es.example.com",
		},
		{
			"delete annotation",
			&corev1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humiocluster",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioClusterSpec{
					Hostname:   "humio.example.com",
					ESHostname: "humio-es.example.com",
					Ingress: corev1alpha1.HumioClusterIngressSpec{
						Enabled:    true,
						Controller: "nginx",
						Annotations: map[string]string{
							"use-http01-solver":              "true",
							"cert-manager.io/cluster-issuer": "letsencrypt-prod",
							"kubernetes.io/ingress.class":    "nginx",
						},
					},
				},
			},
			map[string]string{
				"kubernetes.io/ingress.class": "nginx",
			},
			"humio.example.com",
			"humio-es.example.com",
		},
		{
			"update hostname",
			&corev1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humiocluster",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioClusterSpec{
					Hostname:   "humio.example.com",
					ESHostname: "humio-es.example.com",
					Ingress: corev1alpha1.HumioClusterIngressSpec{
						Enabled:    true,
						Controller: "nginx",
					},
				},
			},
			map[string]string{},
			"humio2.example.com",
			"humio2-es.example.com",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := reconcileInit(tt.humioCluster)
			defer r.logger.Sync()

			r.ensureIngress(context.TODO(), tt.humioCluster)

			foundIngressList, listErr := kubernetes.ListIngresses(r.client, tt.humioCluster.Namespace, kubernetes.MatchingLabelsForHumio(tt.humioCluster.Name))
			if listErr != nil {
				t.Errorf("failed to list pods %s", listErr)
			}

			// check if we have initial hostname here in ingress objects
			if foundIngressList[0].Spec.Rules[0].Host != tt.humioCluster.Spec.Hostname {
				t.Errorf("did not validate initial hostname, expected: %v, got: %v", tt.humioCluster.Spec.Hostname, foundIngressList[0].Spec.Rules[0].Host)
			}
			// construct desired ingress objects and compare
			desiredIngresses := []*v1beta1.Ingress{
				constructGeneralIngress(tt.humioCluster),
				constructStreamingQueryIngress(tt.humioCluster),
				constructIngestIngress(tt.humioCluster),
				constructESIngestIngress(tt.humioCluster),
			}
			foundIngressCount := 0
			for _, desiredIngress := range desiredIngresses {
				for _, foundIngress := range foundIngressList {
					if desiredIngress.Name == foundIngress.Name {
						foundIngressCount++
						if !reflect.DeepEqual(desiredIngress.Annotations, foundIngress.Annotations) {
							t.Errorf("did not validate annotations, expected: %v, got: %v", desiredIngress.Annotations, foundIngress.Annotations)
						}
					}
				}
			}
			if foundIngressCount != len(desiredIngresses) {
				t.Errorf("did not find all expected ingress objects, expected: %v, got: %v", len(desiredIngresses), foundIngressCount)
			}

			tt.humioCluster.Spec.Hostname = tt.newHostname
			tt.humioCluster.Spec.Ingress.Annotations = tt.newAnnotations
			r.ensureIngress(context.TODO(), tt.humioCluster)

			foundIngressList, listErr = kubernetes.ListIngresses(r.client, tt.humioCluster.Namespace, kubernetes.MatchingLabelsForHumio(tt.humioCluster.Name))
			if listErr != nil {
				t.Errorf("failed to list pods %s", listErr)
			}

			// check if we have updated hostname here in ingress objects
			if foundIngressList[0].Spec.Rules[0].Host != tt.newHostname {
				t.Errorf("did not validate updated hostname, expected: %v, got: %v", tt.humioCluster.Spec.Hostname, foundIngressList[0].Spec.Rules[0].Host)
			}
			// construct desired ingress objects and compare
			desiredIngresses = []*v1beta1.Ingress{
				constructGeneralIngress(tt.humioCluster),
				constructStreamingQueryIngress(tt.humioCluster),
				constructIngestIngress(tt.humioCluster),
				constructESIngestIngress(tt.humioCluster),
			}
			foundIngressCount = 0
			for _, desiredIngress := range desiredIngresses {
				for _, foundIngress := range foundIngressList {
					if desiredIngress.Name == foundIngress.Name {
						foundIngressCount++
						if !reflect.DeepEqual(desiredIngress.Annotations, foundIngress.Annotations) {
							t.Errorf("did not validate annotations, expected: %v, got: %v", desiredIngress.Annotations, foundIngress.Annotations)
						}
					}
				}
			}
			if foundIngressCount != len(desiredIngresses) {
				t.Errorf("did not find all expected ingress objects, expected: %v, got: %v", len(desiredIngresses), foundIngressCount)
			}
		})
	}
}

func TestReconcileHumioCluster_ensureIngress_disable_ingress(t *testing.T) {
	tests := []struct {
		name                     string
		humioCluster             *corev1alpha1.HumioCluster
		initialNumIngressObjects int
		newIngressEnabled        bool
	}{
		{
			"validate ingress is cleaned up if changed from enabled to disabled",
			&corev1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humiocluster",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioClusterSpec{
					Hostname:   "humio.example.com",
					ESHostname: "humio-es.example.com",
					Ingress: corev1alpha1.HumioClusterIngressSpec{
						Enabled:     true,
						Controller:  "nginx",
						Annotations: map[string]string{},
					},
				},
			},
			4,
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := reconcileInit(tt.humioCluster)
			defer r.logger.Sync()

			r.ensureIngress(context.TODO(), tt.humioCluster)

			foundIngressList, listErr := kubernetes.ListIngresses(r.client, tt.humioCluster.Namespace, kubernetes.MatchingLabelsForHumio(tt.humioCluster.Name))
			if listErr != nil {
				t.Errorf("failed to list pods %s", listErr)
			}

			if len(foundIngressList) != tt.initialNumIngressObjects {
				t.Errorf("did find expected number of ingress objects, expected: %v, got: %v", tt.initialNumIngressObjects, len(foundIngressList))
			}

			tt.humioCluster.Spec.Ingress.Enabled = tt.newIngressEnabled
			r.ensureNoIngressesIfIngressNotEnabled(context.TODO(), tt.humioCluster)

			foundIngressList, listErr = kubernetes.ListIngresses(r.client, tt.humioCluster.Namespace, kubernetes.MatchingLabelsForHumio(tt.humioCluster.Name))
			if listErr != nil {
				t.Errorf("failed to list pods %s", listErr)
			}

			if len(foundIngressList) != 0 {
				t.Errorf("did find expected number of ingress objects, expected: %v, got: %v", 0, len(foundIngressList))
			}
		})
	}
}

func reconcileWithHumioClient(humioCluster *corev1alpha1.HumioCluster, humioClient *humio.MockClientConfig) (*ReconcileHumioCluster, reconcile.Request) {
	r, req := reconcileInit(humioCluster)
	r.humioClient = humioClient
	return r, req
}

func reconcileInit(humioCluster *corev1alpha1.HumioCluster) (*ReconcileHumioCluster, reconcile.Request) {
	logger, _ := zap.NewProduction()
	sugar := logger.Sugar().With("Request.Namespace", humioCluster.Namespace, "Request.Name", humioCluster.Name)

	// Objects to track in the fake client.
	objs := []runtime.Object{
		humioCluster,
	}

	// Register operator types with the runtime scheme.
	s := scheme.Scheme
	s.AddKnownTypes(corev1alpha1.SchemeGroupVersion, humioCluster)

	// Create a fake client to mock API calls.
	cl := fake.NewFakeClient(objs...)

	// Create a ReconcileHumioCluster object with the scheme and fake client.
	r := &ReconcileHumioCluster{
		client: cl,
		scheme: s,
		logger: sugar,
	}

	// Mock request to simulate Reconcile() being called on an event for a
	// watched resource .
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      humioCluster.Name,
			Namespace: humioCluster.Namespace,
		},
	}
	return r, req
}

func markPodsAsRunning(client client.Client, pods []corev1.Pod) error {
	for nodeID, pod := range pods {
		pod.Status.PodIP = fmt.Sprintf("192.168.0.%d", nodeID)
		pod.Status.Conditions = []corev1.PodCondition{
			{
				Type:   corev1.PodConditionType("Ready"),
				Status: corev1.ConditionTrue,
			},
		}
		err := client.Status().Update(context.TODO(), &pod)
		if err != nil {
			return fmt.Errorf("failed to update pods to prepare for testing the labels: %s", err)
		}
	}
	return nil
}

func buildStoragePartitionsList(numberOfPartitions int, nodesPerPartition int) []humioapi.StoragePartition {
	var storagePartitions []humioapi.StoragePartition

	for p := 1; p <= numberOfPartitions; p++ {
		var nodeIds []int
		for n := 0; n < nodesPerPartition; n++ {
			nodeIds = append(nodeIds, n)
		}
		storagePartition := humioapi.StoragePartition{Id: p, NodeIds: nodeIds}
		storagePartitions = append(storagePartitions, storagePartition)
	}
	return storagePartitions
}

func buildIngestPartitionsList(numberOfPartitions int, nodesPerPartition int) []humioapi.IngestPartition {
	var ingestPartitions []humioapi.IngestPartition

	for p := 1; p <= numberOfPartitions; p++ {
		var nodeIds []int
		for n := 0; n < nodesPerPartition; n++ {
			nodeIds = append(nodeIds, n)
		}
		ingestPartition := humioapi.IngestPartition{Id: p, NodeIds: nodeIds}
		ingestPartitions = append(ingestPartitions, ingestPartition)
	}
	return ingestPartitions
}

func buildClusterNodesList(numberOfNodes int) []humioapi.ClusterNode {
	clusterNodes := []humioapi.ClusterNode{}
	for n := 0; n < numberOfNodes; n++ {
		clusterNode := humioapi.ClusterNode{
			Uri:         fmt.Sprintf("http://192.168.0.%d:8080", n),
			Id:          n,
			IsAvailable: true,
		}
		clusterNodes = append(clusterNodes, clusterNode)
	}
	return clusterNodes
}
