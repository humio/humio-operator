/*
Copyright 2020 Humio https://humio.com

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// Tests for waitForNewPods — the loop converted in issue #1060 from a
// fixed-iteration time.Sleep into wait.PollUntilContextTimeout. The new
// contract: (1) returns nil promptly when the expected pods are visible,
// (2) honors ctx cancellation rather than running the full 10s, (3) returns
// a timeout error when pods never appear.

const testPodHash = "test-hash-abc"

func newWaitForPodsReconciler(initial ...client.Object) *HumioClusterReconciler {
	builder := fake.NewClientBuilder()
	if len(initial) > 0 {
		builder = builder.WithObjects(initial...)
	}
	return &HumioClusterReconciler{
		Client: builder.Build(),
		Log:    logr.Discard(),
	}
}

func newHumioNodePoolForTest(t *testing.T, namespace, name string) *HumioNodePool {
	t.Helper()
	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	return NewHumioNodeManagerFromHumioCluster(hc)
}

func podWithHash(namespace, name string, labels map[string]string, hash string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Labels:      labels,
			Annotations: map[string]string{PodHashAnnotation: hash},
		},
	}
}

func TestWaitForNewPods_ReturnsWhenExpectedPodsExist(t *testing.T) {
	hnp := newHumioNodePoolForTest(t, "ns", "cluster-a")
	labels := hnp.GetNodePoolLabels()

	// Pre-populate the fake client with one pod matching the expected hash.
	existing := podWithHash("ns", "existing-pod", labels, testPodHash)
	r := newWaitForPodsReconciler(existing)

	expectedPods := []corev1.Pod{*podWithHash("ns", "new-pod", labels, testPodHash)}

	start := time.Now()
	err := r.waitForNewPods(context.Background(), hnp, nil, expectedPods)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	// `immediate: true` means the first check fires before the first sleep.
	if elapsed > 500*time.Millisecond {
		t.Fatalf("expected immediate return, took %v", elapsed)
	}
}

func TestWaitForNewPods_RespectsContextCancellation(t *testing.T) {
	hnp := newHumioNodePoolForTest(t, "ns", "cluster-b")
	r := newWaitForPodsReconciler() // no pods exist — would loop full 10s

	expectedPods := []corev1.Pod{*podWithHash("ns", "wanted-pod", hnp.GetNodePoolLabels(), testPodHash)}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := r.waitForNewPods(ctx, hnp, nil, expectedPods)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected context error, got nil")
	}
	// Pre-#1060 the loop would have slept the full 10s ignoring cancel.
	// New behavior: returns promptly. Generous CI bound, well under 10s.
	if elapsed > 3*time.Second {
		t.Fatalf("ctx cancellation should return promptly, took %v", elapsed)
	}
}
