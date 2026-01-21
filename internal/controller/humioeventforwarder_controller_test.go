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
	"errors"
	"testing"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	humioapi "github.com/humio/humio-operator/internal/api"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func TestHumioEventForwarderReconciler_isPermanentError(t *testing.T) {
	reconciler := &HumioEventForwarderReconciler{
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
			err:  k8serrors.NewNotFound(schema.GroupResource{Group: "core.humio.com", Resource: "humioeventforwarders"}, "test"),
			want: true,
		},
		{
			name: "Kubernetes Conflict error is transient",
			err:  k8serrors.NewConflict(schema.GroupResource{Group: "core.humio.com", Resource: "humioeventforwarders"}, "test", errors.New("conflict")),
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
			name: "Could not find error is permanent",
			err:  errors.New("Could not find forwarder with ID abc123"),
			want: true,
		},
		{
			name: "does not exist error is permanent",
			err:  errors.New("forwarder does not exist"),
			want: true,
		},
		{
			name: "not found error is permanent",
			err:  errors.New("resource not found in LogScale"),
			want: true,
		},
		{
			name: "permission denied error is permanent",
			err:  errors.New("permission denied for operation"),
			want: true,
		},
		{
			name: "unauthorized error is permanent",
			err:  errors.New("unauthorized access to resource"),
			want: true,
		},
		{
			name: "forbidden error is permanent",
			err:  errors.New("forbidden: insufficient permissions"),
			want: true,
		},
		{
			name: "authentication error is permanent",
			err:  errors.New("authentication failed"),
			want: true,
		},
		{
			name: "invalid configuration error is permanent",
			err:  errors.New("invalid forwarder configuration"),
			want: true,
		},
		{
			name: "malformed error is permanent",
			err:  errors.New("malformed request body"),
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
			name: "dial error is transient",
			err:  errors.New("dial tcp: connection refused"),
			want: false,
		},
		{
			name: "EOF error is transient",
			err:  errors.New("unexpected EOF"),
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

func TestHumioEventForwarderReconciler_retryStatusUpdateWithBackoff(t *testing.T) {
	scheme := createTestScheme(t)

	tests := []struct {
		name          string
		setupClient   func() client.Client
		updateFn      func(*humiov1alpha1.HumioEventForwarder) error
		wantErr       bool
		errContains   string
		expectRetries bool
	}{
		{
			name: "successful update on first try",
			setupClient: func() client.Client {
				hef := &humiov1alpha1.HumioEventForwarder{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-forwarder",
						Namespace: "default",
					},
					Status: humiov1alpha1.HumioEventForwarderStatus{},
				}
				return fake.NewClientBuilder().WithScheme(scheme).WithObjects(hef).WithStatusSubresource(hef).Build()
			},
			updateFn: func(hef *humiov1alpha1.HumioEventForwarder) error {
				hef.Status.EventForwarderID = "new-id"
				return nil
			},
			wantErr:       false,
			expectRetries: false,
		},
		{
			name: "update function fails",
			setupClient: func() client.Client {
				hef := &humiov1alpha1.HumioEventForwarder{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-forwarder",
						Namespace: "default",
					},
				}
				return fake.NewClientBuilder().WithScheme(scheme).WithObjects(hef).WithStatusSubresource(hef).Build()
			},
			updateFn: func(hef *humiov1alpha1.HumioEventForwarder) error {
				return errors.New("update function error")
			},
			wantErr:     true,
			errContains: "update function failed",
		},
		{
			name: "context canceled",
			setupClient: func() client.Client {
				hef := &humiov1alpha1.HumioEventForwarder{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-forwarder",
						Namespace: "default",
					},
				}
				return fake.NewClientBuilder().WithScheme(scheme).WithObjects(hef).WithStatusSubresource(hef).Build()
			},
			updateFn: func(hef *humiov1alpha1.HumioEventForwarder) error {
				return nil
			},
			wantErr:     true,
			errContains: "context canceled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reconciler := &HumioEventForwarderReconciler{
				Client:     tt.setupClient(),
				BaseLogger: zap.New(zap.UseDevMode(true)),
			}
			reconciler.Log = reconciler.BaseLogger

			ctx := context.Background()
			if tt.name == "context canceled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel() // Cancel immediately
			}

			hef := &humiov1alpha1.HumioEventForwarder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-forwarder",
					Namespace: "default",
				},
			}

			err := reconciler.retryStatusUpdateWithBackoff(ctx, hef, tt.updateFn)

			if (err != nil) != tt.wantErr {
				t.Errorf("retryStatusUpdateWithBackoff() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.errContains != "" && err != nil {
				if !contains(err.Error(), tt.errContains) {
					t.Errorf("retryStatusUpdateWithBackoff() error = %v, want error containing %v", err, tt.errContains)
				}
			}
		})
	}
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Helper to create test scheme
func createTestScheme(t *testing.T) *runtime.Scheme {
	scheme := runtime.NewScheme()
	if err := humiov1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}
	return scheme
}
