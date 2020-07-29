package humioingesttoken

import (
	"context"
	"fmt"
	corev1 "k8s.io/api/core/v1"
	"reflect"
	"testing"

	humioapi "github.com/humio/cli/api"
	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	"github.com/humio/humio-operator/pkg/humio"
	"github.com/humio/humio-operator/pkg/kubernetes"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// TODO: Add tests for updating ingest token

func TestReconcileHumioIngestToken_Reconcile(t *testing.T) {
	tests := []struct {
		name             string
		humioIngestToken *corev1alpha1.HumioIngestToken
		humioClient      *humio.MockClientConfig
	}{
		{
			"test simple ingest token reconciliation",
			&corev1alpha1.HumioIngestToken{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humioingesttoken",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioIngestTokenSpec{
					ManagedClusterName: "example-humiocluster",
					Name:               "test-ingest-token",
					ParserName:         "test-parser",
					RepositoryName:     "test-repository",
				},
			},
			humio.NewMocklient(humioapi.Cluster{}, nil, nil, nil, ""),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, req := reconcileInitWithHumioClient(tt.humioIngestToken, tt.humioClient)
			defer r.logger.Sync()

			_, err := r.Reconcile(req)
			if err != nil {
				t.Errorf("reconcile: (%v)", err)
			}

			updatedIngestToken, err := r.humioClient.GetIngestToken(tt.humioIngestToken)
			if err != nil {
				t.Errorf("get HumioIngestToken: (%v)", err)
			}

			expectedToken := humioapi.IngestToken{
				Name:           tt.humioIngestToken.Spec.Name,
				AssignedParser: tt.humioIngestToken.Spec.ParserName,
				Token:          "mocktoken",
			}

			if !reflect.DeepEqual(*updatedIngestToken, expectedToken) {
				t.Errorf("token %+v, does not match expected %+v", *updatedIngestToken, expectedToken)
			}
		})
	}
}

func TestReconcileHumioIngestToken_Reconcile_ingest_token_secret(t *testing.T) {
	tests := []struct {
		name             string
		humioIngestToken *corev1alpha1.HumioIngestToken
		humioClient      *humio.MockClientConfig
		wantTokenSecret  bool
	}{
		{
			"test ingest token reconciliation without token secret",
			&corev1alpha1.HumioIngestToken{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humioingesttoken",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioIngestTokenSpec{
					ManagedClusterName: "example-humiocluster",
					Name:               "test-ingest-token",
					ParserName:         "test-parser",
					RepositoryName:     "test-repository",
				},
			},
			humio.NewMocklient(humioapi.Cluster{}, nil, nil, nil, ""),
			false,
		},
		{
			"test ingest token reconciliation with token secret",
			&corev1alpha1.HumioIngestToken{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humioingesttoken",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioIngestTokenSpec{
					ManagedClusterName: "example-humiocluster",
					Name:               "test-ingest-token",
					ParserName:         "test-parser",
					RepositoryName:     "test-repository",
					TokenSecretName:    "ingest-token-secret",
				},
			},
			humio.NewMocklient(humioapi.Cluster{}, nil, nil, nil, ""),
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, req := reconcileInitWithHumioClient(tt.humioIngestToken, tt.humioClient)
			defer r.logger.Sync()

			for i := 0; i < 2; i++ {
				_, err := r.Reconcile(req)
				if err != nil {
					t.Errorf("reconcile: (%v)", err)
				}
			}

			foundSecret := false
			if tt.wantTokenSecret {
				secret, err := kubernetes.GetSecret(context.TODO(), r.client, tt.humioIngestToken.Spec.TokenSecretName, tt.humioIngestToken.Namespace)
				if err != nil {
					t.Errorf("unable to get ingest token secret: %s", err)
				}
				if string(secret.Data["token"]) == "mocktoken" {
					foundSecret = true
				}
			}
			if tt.wantTokenSecret && !foundSecret {
				t.Errorf("failed to validate ingest token secret, want: %v, got %v", tt.wantTokenSecret, foundSecret)
			}
		})
	}
}

func reconcileInitWithHumioClient(humioIngestToken *corev1alpha1.HumioIngestToken, humioClient *humio.MockClientConfig) (*ReconcileHumioIngestToken, reconcile.Request) {
	r, req := reconcileInit(humioIngestToken)
	r.humioClient = humioClient
	return r, req
}

func reconcileInit(humioIngestToken *corev1alpha1.HumioIngestToken) (*ReconcileHumioIngestToken, reconcile.Request) {
	logger, _ := zap.NewProduction()
	sugar := logger.Sugar().With("Request.Namespace", humioIngestToken.Namespace, "Request.Name", humioIngestToken.Name)

	humioCluster := &corev1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      humioIngestToken.Spec.ManagedClusterName,
			Namespace: humioIngestToken.Namespace,
		},
	}

	apiTokenSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-admin-token", humioIngestToken.Spec.ManagedClusterName),
			Namespace: humioIngestToken.Namespace,
		},
		StringData: map[string]string{
			"token": "secret-api-token",
		},
	}

	// Objects to track in the fake client.
	objs := []runtime.Object{
		humioCluster,
		apiTokenSecret,
		humioIngestToken,
	}

	// Register operator types with the runtime scheme.
	s := scheme.Scheme
	s.AddKnownTypes(corev1alpha1.SchemeGroupVersion, humioIngestToken)
	s.AddKnownTypes(corev1alpha1.SchemeGroupVersion, humioCluster)

	// Create a fake client to mock API calls.
	cl := fake.NewFakeClient(objs...)

	// Create a ReconcilehumioIngestToken object with the scheme and fake client.
	r := &ReconcileHumioIngestToken{
		client: cl,
		scheme: s,
		logger: sugar,
	}

	// Mock request to simulate Reconcile() being called on an event for a
	// watched resource .
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      humioIngestToken.Name,
			Namespace: humioIngestToken.Namespace,
		},
	}
	return r, req
}
