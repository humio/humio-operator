package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	humioapi "github.com/humio/humio-operator/internal/api"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func TestCalculateBackoffDuration(t *testing.T) {
	reconciler := &HumioEventForwardingRuleReconciler{}

	tests := []struct {
		name                 string
		readyConditionStatus metav1.ConditionStatus
		readyConditionExists bool
		timeSinceTransition  time.Duration
		observedGeneration   int64
		currentGeneration    int64
		expectedBackoff      time.Duration
	}{
		{
			name:                 "No Ready condition exists",
			readyConditionExists: false,
			expectedBackoff:      5 * time.Second,
		},
		{
			name:                 "Ready condition is True",
			readyConditionStatus: metav1.ConditionTrue,
			readyConditionExists: true,
			expectedBackoff:      5 * time.Second,
		},
		{
			name:                 "Ready condition is Unknown",
			readyConditionStatus: metav1.ConditionUnknown,
			readyConditionExists: true,
			expectedBackoff:      5 * time.Second,
		},
		{
			name:                 "Generation changed - reset to 5s",
			readyConditionStatus: metav1.ConditionFalse,
			readyConditionExists: true,
			timeSinceTransition:  30 * time.Second, // Would normally be 10s
			observedGeneration:   1,
			currentGeneration:    2,
			expectedBackoff:      5 * time.Second,
		},
		{
			name:                 "Failed for 5 seconds - use 5s backoff",
			readyConditionStatus: metav1.ConditionFalse,
			readyConditionExists: true,
			timeSinceTransition:  5 * time.Second,
			observedGeneration:   1,
			currentGeneration:    1,
			expectedBackoff:      5 * time.Second,
		},
		{
			name:                 "Failed for 15 seconds - use 10s backoff",
			readyConditionStatus: metav1.ConditionFalse,
			readyConditionExists: true,
			timeSinceTransition:  15 * time.Second,
			observedGeneration:   1,
			currentGeneration:    1,
			expectedBackoff:      10 * time.Second,
		},
		{
			name:                 "Failed for 45 seconds - use 20s backoff",
			readyConditionStatus: metav1.ConditionFalse,
			readyConditionExists: true,
			timeSinceTransition:  45 * time.Second,
			observedGeneration:   1,
			currentGeneration:    1,
			expectedBackoff:      20 * time.Second,
		},
		{
			name:                 "Failed for 90 seconds - cap at 60s backoff",
			readyConditionStatus: metav1.ConditionFalse,
			readyConditionExists: true,
			timeSinceTransition:  90 * time.Second,
			observedGeneration:   1,
			currentGeneration:    1,
			expectedBackoff:      60 * time.Second,
		},
		{
			name:                 "Failed for 5 minutes - cap at 60s backoff",
			readyConditionStatus: metav1.ConditionFalse,
			readyConditionExists: true,
			timeSinceTransition:  5 * time.Minute,
			observedGeneration:   1,
			currentGeneration:    1,
			expectedBackoff:      60 * time.Second,
		},
		{
			name:                 "Boundary: exactly 10 seconds - use 10s backoff",
			readyConditionStatus: metav1.ConditionFalse,
			readyConditionExists: true,
			timeSinceTransition:  10 * time.Second,
			observedGeneration:   1,
			currentGeneration:    1,
			expectedBackoff:      10 * time.Second,
		},
		{
			name:                 "Boundary: exactly 30 seconds - use 20s backoff",
			readyConditionStatus: metav1.ConditionFalse,
			readyConditionExists: true,
			timeSinceTransition:  30 * time.Second,
			observedGeneration:   1,
			currentGeneration:    1,
			expectedBackoff:      20 * time.Second,
		},
		{
			name:                 "Boundary: exactly 60 seconds - cap at 60s backoff",
			readyConditionStatus: metav1.ConditionFalse,
			readyConditionExists: true,
			timeSinceTransition:  60 * time.Second,
			observedGeneration:   1,
			currentGeneration:    1,
			expectedBackoff:      60 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a test HumioEventForwardingRule
			hefr := &humiov1alpha1.HumioEventForwardingRule{
				ObjectMeta: metav1.ObjectMeta{
					Generation: tt.currentGeneration,
				},
			}

			// Add Ready condition if it should exist
			if tt.readyConditionExists {
				transitionTime := metav1.NewTime(time.Now().Add(-tt.timeSinceTransition))
				hefr.Status.Conditions = []metav1.Condition{
					{
						Type:               humiov1alpha1.EventForwardingRuleConditionTypeReady,
						Status:             tt.readyConditionStatus,
						ObservedGeneration: tt.observedGeneration,
						LastTransitionTime: transitionTime,
						Reason:             "TestReason",
						Message:            "Test message",
					},
				}
			}

			// Calculate backoff
			backoff := reconciler.calculateBackoffDuration(hefr)

			// Verify result
			if backoff != tt.expectedBackoff {
				t.Errorf("Expected backoff %v, got %v", tt.expectedBackoff, backoff)
			}
		})
	}
}

func TestCalculateBackoffDuration_EdgeCases(t *testing.T) {
	reconciler := &HumioEventForwardingRuleReconciler{}

	t.Run("Multiple conditions present", func(t *testing.T) {
		hefr := &humiov1alpha1.HumioEventForwardingRule{
			ObjectMeta: metav1.ObjectMeta{
				Generation: 1,
			},
			Status: humiov1alpha1.HumioEventForwardingRuleStatus{
				Conditions: []metav1.Condition{
					{
						Type:               humiov1alpha1.EventForwardingRuleConditionTypeSynced,
						Status:             metav1.ConditionTrue,
						ObservedGeneration: 1,
						LastTransitionTime: metav1.NewTime(time.Now().Add(-30 * time.Second)),
						Reason:             "Synced",
						Message:            "Synced",
					},
					{
						Type:               humiov1alpha1.EventForwardingRuleConditionTypeReady,
						Status:             metav1.ConditionFalse,
						ObservedGeneration: 1,
						LastTransitionTime: metav1.NewTime(time.Now().Add(-15 * time.Second)),
						Reason:             "NotReady",
						Message:            "Not ready",
					},
				},
			},
		}

		backoff := reconciler.calculateBackoffDuration(hefr)
		expected := 10 * time.Second // 15 seconds in failed state

		if backoff != expected {
			t.Errorf("Expected backoff %v for multiple conditions, got %v", expected, backoff)
		}
	})

	t.Run("Empty status conditions", func(t *testing.T) {
		hefr := &humiov1alpha1.HumioEventForwardingRule{
			ObjectMeta: metav1.ObjectMeta{
				Generation: 1,
			},
			Status: humiov1alpha1.HumioEventForwardingRuleStatus{
				Conditions: []metav1.Condition{},
			},
		}

		backoff := reconciler.calculateBackoffDuration(hefr)
		expected := 5 * time.Second

		if backoff != expected {
			t.Errorf("Expected backoff %v for empty conditions, got %v", expected, backoff)
		}
	})
}

func TestHumioEventForwardingRuleReconciler_isPermanentError(t *testing.T) {
	reconciler := &HumioEventForwardingRuleReconciler{
		BaseLogger: zap.New(zap.UseDevMode(true)),
	}
	reconciler.Log = reconciler.BaseLogger

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error is not permanent",
			err:  nil,
			want: false,
		},
		{
			name: "EntityNotFound error is permanent",
			err:  humioapi.EntityNotFound{},
			want: true,
		},
		{
			name: "Kubernetes NotFound error is permanent",
			err:  k8serrors.NewNotFound(schema.GroupResource{Group: "core.humio.com", Resource: "humioeventforwardingrules"}, "test"),
			want: true,
		},
		{
			name: "Kubernetes Conflict error is transient",
			err:  k8serrors.NewConflict(schema.GroupResource{Group: "core.humio.com", Resource: "humioeventforwardingrules"}, "test", errors.New("conflict")),
			want: false,
		},
		{
			name: "Context Canceled is transient",
			err:  context.Canceled,
			want: false,
		},
		{
			name: "Context DeadlineExceeded is transient",
			err:  context.DeadlineExceeded,
			want: false,
		},
		{
			name: "Could not find Repository error is permanent",
			err:  errors.New("Could not find Repository with name test-repo"),
			want: true,
		},
		{
			name: "repository does not exist error is permanent",
			err:  errors.New("repository does not exist"),
			want: true,
		},
		{
			name: "rule not found error is permanent",
			err:  errors.New("event forwarding rule not found in LogScale"),
			want: true,
		},
		{
			name: "permission denied error is permanent",
			err:  errors.New("permission denied for operation"),
			want: true,
		},
		{
			name: "connection error is transient",
			err:  errors.New("connection refused"),
			want: false,
		},
		{
			name: "timeout error is transient",
			err:  errors.New("request timeout after 30s"),
			want: false,
		},
		{
			name: "generic error defaults to transient",
			err:  errors.New("some random error"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := reconciler.isPermanentError(tt.err)
			if got != tt.want {
				t.Errorf("isPermanentError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHumioEventForwardingRuleReconciler_retryAnnotationUpdateWithBackoff(t *testing.T) {
	scheme := createTestSchemeForRule(t)

	tests := []struct {
		name        string
		updateFn    func(*humiov1alpha1.HumioEventForwardingRule) error
		wantErr     bool
		errContains string
	}{
		{
			name: "successful update on first try",
			updateFn: func(hefr *humiov1alpha1.HumioEventForwardingRule) error {
				if hefr.Annotations == nil {
					hefr.Annotations = make(map[string]string)
				}
				hefr.Annotations["test-annotation"] = "test-value"
				return nil
			},
			wantErr: false,
		},
		{
			name: "update function fails",
			updateFn: func(hefr *humiov1alpha1.HumioEventForwardingRule) error {
				return errors.New("update function error")
			},
			wantErr:     true,
			errContains: "update function failed",
		},
		{
			name: "context canceled",
			updateFn: func(hefr *humiov1alpha1.HumioEventForwardingRule) error {
				return nil
			},
			wantErr:     true,
			errContains: "context canceled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hefr := &humiov1alpha1.HumioEventForwardingRule{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rule",
					Namespace: "default",
				},
			}

			reconciler := &HumioEventForwardingRuleReconciler{
				Client:     fake.NewClientBuilder().WithScheme(scheme).WithObjects(hefr).Build(),
				BaseLogger: zap.New(zap.UseDevMode(true)),
			}
			reconciler.Log = reconciler.BaseLogger

			ctx := context.Background()
			if tt.name == "context canceled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel() // Cancel immediately
			}

			err := reconciler.retryAnnotationUpdateWithBackoff(ctx, hefr, tt.updateFn)

			if (err != nil) != tt.wantErr {
				t.Errorf("retryAnnotationUpdateWithBackoff() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.errContains != "" && err != nil {
				if !containsString(err.Error(), tt.errContains) {
					t.Errorf("retryAnnotationUpdateWithBackoff() error = %v, want error containing %v", err, tt.errContains)
				}
			}
		})
	}
}

func TestExtractQueryValidationError(t *testing.T) {
	tests := []struct {
		name      string
		fullError string
		want      string
	}{
		{
			name:      "extract from standard LogScale error format",
			fullError: "input:3: createEventForwardingRule (query: Unknown function: `INVALID_FUNCTION`.\n```\n 1: #type=accesslog | INVALID_FUNCTION()\n                      ^^^^^^^^^^^^^^^^\n```) There were errors in the input.",
			want:      "Query validation failed: Unknown function: `INVALID_FUNCTION`",
		},
		{
			name:      "extract from error with multiple lines",
			fullError: "some prefix text\nquery: Syntax error at line 5\n```\nquery code here\n```\nsuffix text",
			want:      "Query validation failed: Syntax error at line 5",
		},
		{
			name:      "fallback for Unknown function without query: marker",
			fullError: "Error: Unknown function SOME_FUNC is not recognized by the parser",
			want:      "Query validation failed: Unknown function SOME_FUNC is not recognized by the parser",
		},
		{
			name:      "very long error message gets truncated",
			fullError: "query: " + string(make([]byte, 600)),
			want:      "Query validation failed: ",
		},
		{
			name:      "error without query: marker",
			fullError: "Some other error message",
			want:      "Query validation failed: Some other error message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractQueryValidationError(tt.fullError)
			if !containsString(got, "Query validation failed:") {
				t.Errorf("extractQueryValidationError() = %v, want to contain 'Query validation failed:'", got)
			}
			// For truncation test, just check it starts correctly
			if tt.name == "very long error message gets truncated" {
				if len(got) > 550 {
					t.Errorf("extractQueryValidationError() result too long: %d chars, expected < 550", len(got))
				}
			}
		})
	}
}

// Helper function to check if a string contains a substring
func containsString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Helper to create test scheme
func createTestSchemeForRule(t *testing.T) *runtime.Scheme {
	scheme := runtime.NewScheme()
	if err := humiov1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}
	return scheme
}
