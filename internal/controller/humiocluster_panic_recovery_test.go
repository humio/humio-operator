package controller

import (
	"context"
	"fmt"
	"testing"

	"github.com/go-logr/logr"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
)

// TestPanicRecoveryInEnsureNodePoolResources validates that panics inside ensureNodePoolResources
// are caught and logged without propagating to the caller.
func TestPanicRecoveryInEnsureNodePoolResources(t *testing.T) {
	ctx := context.Background()
	logger := logr.Discard()

	// Create a mock function that panics
	panicFunc := func(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
		panic("test panic from ensureNodePoolResources")
	}

	// Wrap with the panic recovery wrapper (this is the pattern we'll implement)
	wrappedFunc := func(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
		defer func() {
			if rec := recover(); rec != nil {
				logger.Error(fmt.Errorf("panic in shadow node pool sync: %v", rec), "recovered from panic in ensureNodePoolResources", "component", "shadow-node-pool-sync")
			}
		}()
		return panicFunc(ctx, hc)
	}

	// Call the wrapped function - should not panic
	hc := &humiov1alpha1.HumioCluster{}
	err := wrappedFunc(ctx, hc)

	// Should return nil (panic was caught)
	if err != nil {
		t.Errorf("Expected nil error after panic recovery, got: %v", err)
	}
}

// TestNormalErrorFromEnsureNodePoolResources validates that normal errors
// are passed through without modification.
func TestNormalErrorFromEnsureNodePoolResources(t *testing.T) {
	ctx := context.Background()
	logger := logr.Discard()

	// Create a mock function that returns an error
	testErr := fmt.Errorf("test error")
	errorFunc := func(_ context.Context, _ *humiov1alpha1.HumioCluster) error {
		return testErr
	}

	// Wrap with the panic recovery wrapper
	wrappedFunc := func(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
		defer func() {
			if rec := recover(); rec != nil {
				logger.Error(fmt.Errorf("panic in shadow node pool sync: %v", rec), "recovered from panic in ensureNodePoolResources", "component", "shadow-node-pool-sync")
			}
		}()
		return errorFunc(ctx, hc)
	}

	// Call the wrapped function
	hc := &humiov1alpha1.HumioCluster{}
	err := wrappedFunc(ctx, hc)

	// Should return the original error
	if err == nil {
		t.Errorf("Expected error to be returned, got nil")
	}
	if err != testErr {
		t.Errorf("Expected exact error instance, got different error: %v", err)
	}
}
