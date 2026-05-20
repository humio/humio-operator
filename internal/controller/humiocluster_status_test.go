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
	"testing"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
)

// Tests for the shared-builder mutation pattern introduced for issue #1056.
// Reconcile sets up a single `finalStatus := statusOptions()` and then three
// defers either mutate it via withX() or read it for the terminal Update.
// These tests pin the two invariants the pattern relies on:
//
//   1. withX() must return the same builder pointer (so the closure
//      reassignment `finalStatus = finalStatus.withX(...)` is effectively
//      a no-op and doesn't desync the outer variable from the in-place
//      mutation).
//   2. Sequential withX() calls on one builder accumulate all options,
//      so a single terminal Update() emits the union of every mutation.

func TestStatusOptionsBuilder_WithReturnsSameBuilder(t *testing.T) {
	// PR #1059's defers do `finalStatus = finalStatus.withX(...)`. That
	// reassignment is safe only if withX returns the same pointer. If a
	// future refactor switched to value receivers or returned a copy, the
	// in-place mutation from one defer wouldn't be visible to the next.
	b := statusOptions()
	if got := b.withVersion("v1"); got != b {
		t.Errorf("withVersion: expected same pointer, got different (%p vs %p)", got, b)
	}
	if got := b.withNodeCount(3); got != b {
		t.Errorf("withNodeCount: expected same pointer, got different (%p vs %p)", got, b)
	}
	if got := b.withObservedGeneration(7); got != b {
		t.Errorf("withObservedGeneration: expected same pointer, got different (%p vs %p)", got, b)
	}
	if got := b.withPods(humiov1alpha1.HumioPodStatusList{}); got != b {
		t.Errorf("withPods: expected same pointer, got different (%p vs %p)", got, b)
	}
}

func TestStatusOptionsBuilder_AccumulatesAcrossMutations(t *testing.T) {
	// Simulate the PR #1059 deferred chain: three independent closures
	// each call withX on the same shared builder. The terminal "update"
	// reads the accumulated options exactly once. Before the refactor
	// each defer constructed its own builder and emitted its own
	// Status().Update() — three writes. After, the shared builder must
	// contain all the options from all three defers.
	shared := statusOptions()

	// Defer 1 (registered first, runs last in LIFO order) — terminal,
	// adds observedGeneration:
	deferTerminal := func() {
		shared = shared.withObservedGeneration(42)
	}
	// Defer 2 — pods/nodeCount:
	deferPods := func() {
		shared = shared.withPods(humiov1alpha1.HumioPodStatusList{}).withNodeCount(5)
	}
	// Defer 3 — version (only when Running, but for the unit test we
	// always run it):
	deferVersion := func() {
		shared = shared.withVersion("1.2.3")
	}

	// LIFO execution order — last registered runs first.
	deferVersion()
	deferPods()
	deferTerminal()

	got := shared.Get()
	// 4 options: version + pods + nodeCount + observedGeneration.
	if len(got) != 4 {
		t.Fatalf("expected 4 accumulated options, got %d", len(got))
	}

	// Spot-check the option types are present. Order doesn't matter for
	// the Apply phase — each Option just mutates a distinct field on the
	// HumioCluster status.
	sawVersion, sawPods, sawNodeCount, sawObservedGen := false, false, false, false
	for _, opt := range got {
		switch opt.(type) {
		case versionOption:
			sawVersion = true
		case podsOption:
			sawPods = true
		case nodeCountOption:
			sawNodeCount = true
		case observedGenerationOption:
			sawObservedGen = true
		}
	}
	if !sawVersion {
		t.Error("missing versionOption — defer chain didn't accumulate version")
	}
	if !sawPods {
		t.Error("missing podsOption — defer chain didn't accumulate pods")
	}
	if !sawNodeCount {
		t.Error("missing nodeCountOption — defer chain didn't accumulate nodeCount")
	}
	if !sawObservedGen {
		t.Error("missing observedGenerationOption — defer chain didn't accumulate observed generation")
	}
}

// Belt-and-suspenders: the terminal defer applies every accumulated option
// to the cluster status in a single Apply pass. This test confirms that
// after accumulating across the simulated deferred chain, every field
// lands on hc.Status — which is what the single terminal Status().Update()
// will ultimately write.
func TestStatusOptionsBuilder_AppliesAllFieldsInOnePass(t *testing.T) {
	hc := &humiov1alpha1.HumioCluster{}

	shared := statusOptions().
		withVersion("9.9.9").
		withNodeCount(11).
		withObservedGeneration(99)

	for _, opt := range shared.Get() {
		opt.Apply(hc)
	}

	if hc.Status.Version != "9.9.9" {
		t.Errorf("Version: want 9.9.9, got %q", hc.Status.Version)
	}
	if hc.Status.NodeCount != 11 {
		t.Errorf("NodeCount: want 11, got %d", hc.Status.NodeCount)
	}
	if hc.Status.ObservedGeneration != "99" {
		// ObservedGeneration is rendered as a string in HumioClusterStatus.
		// If this changes upstream, update the assertion.
		t.Errorf("ObservedGeneration: want %q, got %q", "99", hc.Status.ObservedGeneration)
	}
}
