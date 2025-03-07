package controller

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	corev1 "k8s.io/api/core/v1"
)

func Test_podsStatusState_waitingOnPods(t *testing.T) {
	type fields struct {
		nodeCount     int
		readyCount    int
		notReadyCount int
		podRevisions  []int
		podErrors     []corev1.Pod
	}
	tests := []struct {
		name   string
		fields fields
		want   bool
	}{
		{
			"ready",
			fields{
				3,
				3,
				0,
				[]int{1, 1, 1},
				[]corev1.Pod{},
			},
			false,
		},
		{
			"ready but has a pod with errors",
			fields{
				3,
				2,
				1,
				[]int{1, 1, 1},
				[]corev1.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test",
						},
					},
				},
			},
			false,
		},
		{
			"not ready",
			fields{
				3,
				2,
				1,
				[]int{1, 1, 1},
				[]corev1.Pod{},
			},
			true,
		},
		{
			"ready but mismatched revisions",
			fields{
				3,
				2,
				1,
				[]int{1, 1, 2},
				[]corev1.Pod{},
			},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &podsStatusState{
				nodeCount:     tt.fields.nodeCount,
				readyCount:    tt.fields.readyCount,
				notReadyCount: tt.fields.notReadyCount,
				podRevisions:  tt.fields.podRevisions,
				podAreUnschedulableOrHaveBadStatusConditions: tt.fields.podErrors,
			}
			if got := s.waitingOnPods(); got != tt.want {
				t.Errorf("waitingOnPods() = %v, want %v", got, tt.want)
			}
		})
	}
}