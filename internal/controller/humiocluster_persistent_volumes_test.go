package controller

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestFilterSchedulablePVCs(t *testing.T) {
	tests := []struct {
		name          string
		inputPVCs     []corev1.PersistentVolumeClaim
		expectedPVCs  []corev1.PersistentVolumeClaim
		mockPV        *corev1.PersistentVolume
		mockNode      *corev1.Node
		expectedError bool
	}{
		{
			name:          "Empty PVC list",
			inputPVCs:     []corev1.PersistentVolumeClaim{},
			expectedPVCs:  []corev1.PersistentVolumeClaim{},
			expectedError: false,
		},
		{
			name: "PVC with deletion timestamp",
			inputPVCs: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "pvc-1",
						DeletionTimestamp: &metav1.Time{Time: time.Now()},
					},
				},
			},
			expectedPVCs:  []corev1.PersistentVolumeClaim{},
			expectedError: false,
		},
		{
			name: "Pending PVC",
			inputPVCs: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pvc-2"},
					Status: corev1.PersistentVolumeClaimStatus{
						Phase: corev1.ClaimPending,
					},
				},
			},
			expectedPVCs: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pvc-2"},
					Status: corev1.PersistentVolumeClaimStatus{
						Phase: corev1.ClaimPending,
					},
				},
			},
			expectedError: false,
		},
		{
			name: "Non-local PV",
			inputPVCs: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pvc-3"},
					Spec: corev1.PersistentVolumeClaimSpec{
						VolumeName: "pv-3",
					},
				},
			},
			mockPV: &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "pv-3"},
				Spec: corev1.PersistentVolumeSpec{
					PersistentVolumeSource: corev1.PersistentVolumeSource{},
				},
			},
			expectedPVCs: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pvc-3"},
					Spec: corev1.PersistentVolumeClaimSpec{
						VolumeName: "pv-3",
					},
				},
			},
			expectedError: false,
		},
		{
			name: "Local PV with schedulable node",
			inputPVCs: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pvc-4"},
					Spec: corev1.PersistentVolumeClaimSpec{
						VolumeName: "pv-4",
					},
				},
			},
			mockPV: &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "pv-4"},
				Spec: corev1.PersistentVolumeSpec{
					PersistentVolumeSource: corev1.PersistentVolumeSource{Local: &corev1.LocalVolumeSource{}},
					NodeAffinity: &corev1.VolumeNodeAffinity{
						Required: &corev1.NodeSelector{
							NodeSelectorTerms: []corev1.NodeSelectorTerm{
								{
									MatchExpressions: []corev1.NodeSelectorRequirement{
										{
											Values: []string{"node-1"},
										},
									},
								},
							},
						},
					},
				},
			},
			mockNode: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
				Spec: corev1.NodeSpec{
					Unschedulable: false,
				},
			},
			expectedPVCs: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pvc-4"},
					Spec: corev1.PersistentVolumeClaimSpec{
						VolumeName: "pv-4",
					},
				},
			},
			expectedError: false,
		},
		{
			name: "Local PV with unschedulable node",
			inputPVCs: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pvc-5"},
					Spec: corev1.PersistentVolumeClaimSpec{
						VolumeName: "pv-5",
					},
				},
			},
			mockPV: &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "pv-5"},
				Spec: corev1.PersistentVolumeSpec{
					PersistentVolumeSource: corev1.PersistentVolumeSource{Local: &corev1.LocalVolumeSource{}},
					NodeAffinity: &corev1.VolumeNodeAffinity{
						Required: &corev1.NodeSelector{
							NodeSelectorTerms: []corev1.NodeSelectorTerm{
								{
									MatchExpressions: []corev1.NodeSelectorRequirement{
										{
											Values: []string{"node-2"},
										},
									},
								},
							},
						},
					},
				},
			},
			mockNode: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-2"},
				Spec: corev1.NodeSpec{
					Unschedulable: true,
				},
			},
			expectedPVCs:  []corev1.PersistentVolumeClaim{},
			expectedError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a fake client with the mock objects
			client := fake.NewFakeClient()
			if tt.mockPV != nil {
				if err := client.Create(context.TODO(), tt.mockPV); err != nil {
					t.Errorf("failed to create mock PV")
				}
			}
			if tt.mockNode != nil {
				if err := client.Create(context.TODO(), tt.mockNode); err != nil {
					t.Errorf("failed to create mock node")
				}
			}

			// Create reconciler with the fake client
			r := &HumioClusterReconciler{
				Client: client,
				Log:    logr.Discard(),
			}

			// Call the function
			result, err := r.FilterSchedulablePVCs(context.TODO(), tt.inputPVCs)

			// Check error
			if tt.expectedError && err == nil {
				t.Error("expected error but got none")
			}
			if !tt.expectedError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			// Check result
			if !reflect.DeepEqual(result, tt.expectedPVCs) {
				t.Errorf("expected %v but got %v", tt.expectedPVCs, result)
			}
		})
	}
}
