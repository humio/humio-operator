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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// Tests for waitForBootstrapPodRunning — the loop replaced in issue #1055.
// The original loop was a fixed-iteration time.Sleep that ignored ctx, so
// these tests cover the new contract: (1) returns the pod when it reaches
// Running, (2) respects ctx cancellation promptly, (3) returns a timeout
// error rather than hanging forever when the pod never starts.

func newBootstrapPodReconciler(initial ...*corev1.Pod) *HumioBootstrapTokenReconciler {
	builder := fake.NewClientBuilder()
	for _, p := range initial {
		builder = builder.WithObjects(p)
	}
	return &HumioBootstrapTokenReconciler{
		Client: builder.Build(),
		Log:    logr.Discard(),
	}
}

func bootstrapPod(phase corev1.PodPhase) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "humio-bootstrap-pod",
			Namespace: "default",
		},
		Status: corev1.PodStatus{Phase: phase},
	}
}

func TestWaitForBootstrapPodRunning_ReturnsPodWhenAlreadyRunning(t *testing.T) {
	pod := bootstrapPod(corev1.PodRunning)
	r := newBootstrapPodReconciler(pod)

	start := time.Now()
	got, err := r.waitForBootstrapPodRunning(context.Background(), pod, 5)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got == nil || got.Name != pod.Name {
		t.Fatalf("expected pod %q, got %+v", pod.Name, got)
	}
	// With `immediate: true`, the first check fires before the first sleep,
	// so a pod already in Running should return well under one full poll
	// interval (1s).
	if elapsed > 500*time.Millisecond {
		t.Fatalf("expected immediate return, took %v", elapsed)
	}
}

func TestWaitForBootstrapPodRunning_RespectsContextCancellation(t *testing.T) {
	// Pod exists but is stuck in Pending — without ctx-honoring poll,
	// this would block the full timeout (5s).
	pod := bootstrapPod(corev1.PodPending)
	r := newBootstrapPodReconciler(pod)

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel partway through, well before the timeout would fire.
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	got, err := r.waitForBootstrapPodRunning(ctx, pod, 30)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected context error, got nil")
	}
	if got != nil {
		t.Fatalf("expected nil pod on cancellation, got %+v", got)
	}
	// Should return promptly after cancel — generous bound for CI noise,
	// but well under the 30s timeout the pre-#1055 code would have waited.
	if elapsed > 3*time.Second {
		t.Fatalf("ctx cancellation should return promptly, took %v", elapsed)
	}
}

func TestWaitForBootstrapPodRunning_TimesOutWhenPodMissing(t *testing.T) {
	// No pod created in the fake client — every Get returns NotFound.
	// The poll should tolerate the Get error (the comment in production
	// says transient Get errors don't fail the poll) and time out cleanly.
	r := newBootstrapPodReconciler()
	missing := bootstrapPod(corev1.PodPending)

	start := time.Now()
	got, err := r.waitForBootstrapPodRunning(context.Background(), missing, 2)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if got != nil {
		t.Fatalf("expected nil pod on timeout, got %+v", got)
	}
	// 2s timeout + a bit of jitter — should not blow past 5s.
	if elapsed > 5*time.Second {
		t.Fatalf("timeout should fire near the configured bound, took %v", elapsed)
	}
}
