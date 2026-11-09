package controller

import (
	"testing"
	"time"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/helpers"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func boolPtr(b bool) *bool { return &b }

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := humiov1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}
	return scheme
}

func TestEnableFinalizers_SkipsAddingFinalizer(t *testing.T) {
	scheme := newTestScheme(t)

	action := &humiov1alpha1.HumioAction{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-action",
			Namespace: "default",
		},
		Spec: humiov1alpha1.HumioActionSpec{
			Name:     "test-action",
			ViewName: "test-view",
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(action).Build()

	config := CommonConfig{
		RequeuePeriod:    15 * time.Second,
		EnableFinalizers: boolPtr(false),
	}

	// With EnableFinalizers=false, the finalizer should NOT be added
	if ShouldSkipFinalizer(config, action) {
		// Verify that with the flag set, ContainsElement is false (no finalizer present)
		if helpers.ContainsElement(action.GetFinalizers(), HumioFinalizer) {
			t.Fatal("Expected no finalizer on fresh resource")
		}
		// The condition `!ShouldSkipFinalizer(...) && !ContainsElement(...)` is false,
		// so the add-finalizer block is skipped — this is the expected behavior.
	} else {
		t.Fatal("ShouldSkipFinalizer should return true when EnableFinalizers is false")
	}

	// Verify that with EnableFinalizers=true, a fresh resource WOULD get a finalizer
	configEnabled := CommonConfig{
		RequeuePeriod:    15 * time.Second,
		EnableFinalizers: boolPtr(true),
	}
	if ShouldSkipFinalizer(configEnabled, action) {
		t.Fatal("ShouldSkipFinalizer should return false when EnableFinalizers is true")
	}

	_ = cl // verify client was created successfully
}

func TestEnableFinalizers_RemovesFinalizerOnDeletion(t *testing.T) {
	now := metav1.Now()

	action := &humiov1alpha1.HumioAction{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-action",
			Namespace:         "default",
			Finalizers:        []string{HumioFinalizer},
			DeletionTimestamp: &now,
		},
		Spec: humiov1alpha1.HumioActionSpec{
			Name:     "test-action",
			ViewName: "test-view",
		},
	}

	config := CommonConfig{
		RequeuePeriod:    15 * time.Second,
		EnableFinalizers: boolPtr(false),
	}

	// Simulate the controller's deletion logic:
	// 1. Resource has deletion timestamp
	// 2. Resource has the finalizer
	// 3. ShouldSkipFinalizer returns true -> remove finalizer without API call
	if action.GetDeletionTimestamp() == nil {
		t.Fatal("Expected deletion timestamp to be set")
	}
	if !helpers.ContainsElement(action.GetFinalizers(), HumioFinalizer) {
		t.Fatal("Expected finalizer to be present")
	}
	if !ShouldSkipFinalizer(config, action) {
		t.Fatal("ShouldSkipFinalizer should return true when EnableFinalizers=false")
	}

	// Remove finalizer (simulating what the controller does)
	action.SetFinalizers(helpers.RemoveElement(action.GetFinalizers(), HumioFinalizer))

	if helpers.ContainsElement(action.GetFinalizers(), HumioFinalizer) {
		t.Error("Expected finalizer to be removed")
	}
	if len(action.GetFinalizers()) != 0 {
		t.Errorf("Expected empty finalizers, got %v", action.GetFinalizers())
	}
}

func TestEnableFinalizers_AnnotationOverridesPerResource(t *testing.T) {
	now := metav1.Now()

	// Resource with force-finalize annotation but global flag ON
	action := &humiov1alpha1.HumioAction{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "stuck-action",
			Namespace:         "default",
			Finalizers:        []string{HumioFinalizer, "other-finalizer"},
			DeletionTimestamp: &now,
			Annotations: map[string]string{
				ForceFinalizerAnnotation: ForceFinalizerAnnotationValue,
			},
		},
		Spec: humiov1alpha1.HumioActionSpec{
			Name:     "stuck-action",
			ViewName: "test-view",
		},
	}

	config := CommonConfig{
		RequeuePeriod:    15 * time.Second,
		EnableFinalizers: boolPtr(true), // global flag is ON
	}

	// Even with global flag on, annotation should trigger skip
	if !ShouldSkipFinalizer(config, action) {
		t.Fatal("ShouldSkipFinalizer should return true when force-finalize annotation is set")
	}

	// Simulate removal — only HumioFinalizer should be removed
	action.SetFinalizers(helpers.RemoveElement(action.GetFinalizers(), HumioFinalizer))

	if helpers.ContainsElement(action.GetFinalizers(), HumioFinalizer) {
		t.Error("Expected HumioFinalizer to be removed")
	}
	if !helpers.ContainsElement(action.GetFinalizers(), "other-finalizer") {
		t.Error("Expected other-finalizer to remain")
	}
}

func TestEnableFinalizers_NoEffectWhenEnabled(t *testing.T) {
	config := CommonConfig{
		RequeuePeriod:    15 * time.Second,
		EnableFinalizers: boolPtr(true),
	}

	tests := []struct {
		name        string
		annotations map[string]string
		expected    bool
	}{
		{
			name:        "no annotations",
			annotations: nil,
			expected:    false,
		},
		{
			name:        "unrelated annotation",
			annotations: map[string]string{"foo": "bar"},
			expected:    false,
		},
		{
			name:        "force-finalize set to false",
			annotations: map[string]string{ForceFinalizerAnnotation: "false"},
			expected:    false,
		},
		{
			name:        "force-finalize set to True (wrong case)",
			annotations: map[string]string{ForceFinalizerAnnotation: "True"},
			expected:    false,
		},
		{
			name:        "force-finalize set to true",
			annotations: map[string]string{ForceFinalizerAnnotation: "true"},
			expected:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := &humiov1alpha1.HumioRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-repo",
					Namespace:   "default",
					Annotations: tt.annotations,
				},
			}
			got := ShouldSkipFinalizer(config, obj)
			if got != tt.expected {
				t.Errorf("ShouldSkipFinalizer() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestEnableFinalizers_MultipleResourceTypes(t *testing.T) {
	config := CommonConfig{EnableFinalizers: boolPtr(false)}

	resources := []struct {
		name string
		obj  metav1.Object
	}{
		{"HumioAction", &humiov1alpha1.HumioAction{ObjectMeta: metav1.ObjectMeta{Name: "a"}}},
		{"HumioAlert", &humiov1alpha1.HumioAlert{ObjectMeta: metav1.ObjectMeta{Name: "b"}}},
		{"HumioRepository", &humiov1alpha1.HumioRepository{ObjectMeta: metav1.ObjectMeta{Name: "c"}}},
		{"HumioView", &humiov1alpha1.HumioView{ObjectMeta: metav1.ObjectMeta{Name: "d"}}},
		{"HumioParser", &humiov1alpha1.HumioParser{ObjectMeta: metav1.ObjectMeta{Name: "e"}}},
		{"HumioIngestToken", &humiov1alpha1.HumioIngestToken{ObjectMeta: metav1.ObjectMeta{Name: "f"}}},
		{"HumioFilterAlert", &humiov1alpha1.HumioFilterAlert{ObjectMeta: metav1.ObjectMeta{Name: "g"}}},
		{"HumioScheduledSearch", &humiov1alpha1.HumioScheduledSearch{ObjectMeta: metav1.ObjectMeta{Name: "h"}}},
		{"HumioUser", &humiov1alpha1.HumioUser{ObjectMeta: metav1.ObjectMeta{Name: "i"}}},
	}

	for _, r := range resources {
		t.Run(r.name, func(t *testing.T) {
			// All types should respect the global flag via the client.Object interface
			if !ShouldSkipFinalizer(config, r.obj.(clientObject)) {
				t.Errorf("ShouldSkipFinalizer should return true for %s with EnableFinalizers=false", r.name)
			}
		})
	}
}

func TestEnableFinalizers_NilDefaultsToEnabled(t *testing.T) {
	config := CommonConfig{
		RequeuePeriod: 15 * time.Second,
		// EnableFinalizers intentionally not set (nil)
	}

	obj := &humiov1alpha1.HumioAction{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-action",
			Namespace: "default",
		},
	}

	// Nil EnableFinalizers should default to enabled (ShouldSkipFinalizer returns false)
	if ShouldSkipFinalizer(config, obj) {
		t.Fatal("ShouldSkipFinalizer should return false when EnableFinalizers is nil (defaults to enabled)")
	}
}

// clientObject is a type alias to avoid import confusion
type clientObject interface {
	metav1.Object
	runtime.Object
}
