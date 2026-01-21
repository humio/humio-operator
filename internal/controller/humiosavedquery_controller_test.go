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

	humioapi "github.com/humio/humio-operator/internal/api"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func TestHumioSavedQueryReconciler_isPermanentError(t *testing.T) {
	reconciler := &HumioSavedQueryReconciler{
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
			err:  k8serrors.NewNotFound(schema.GroupResource{Group: "core.humio.com", Resource: "humiosavedqueries"}, "test"),
			want: true,
		},
		{
			name: "Kubernetes Conflict error is transient",
			err:  k8serrors.NewConflict(schema.GroupResource{Group: "core.humio.com", Resource: "humiosavedqueries"}, "test", errors.New("conflict")),
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
			name: "Could not find view error is permanent",
			err:  errors.New("Could not find view with name test-view"),
			want: true,
		},
		{
			name: "view does not exist error is permanent",
			err:  errors.New("view does not exist"),
			want: true,
		},
		{
			name: "saved query not found error is permanent",
			err:  errors.New("saved query not found in LogScale"),
			want: true,
		},
		{
			name: "permission denied error is permanent",
			err:  errors.New("permission denied for operation"),
			want: true,
		},
		{
			name: "unauthorized error is permanent",
			err:  errors.New("unauthorized access to view"),
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
			err:  errors.New("invalid saved query configuration"),
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
