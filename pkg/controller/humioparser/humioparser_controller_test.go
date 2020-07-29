package humioparser

import (
	"fmt"
	corev1 "k8s.io/api/core/v1"
	"reflect"
	"testing"

	humioapi "github.com/humio/cli/api"
	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/humio"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// TODO: Add tests for updating parser

func TestReconcileHumioParser_Reconcile(t *testing.T) {
	tests := []struct {
		name        string
		humioParser *corev1alpha1.HumioParser
		humioClient *humio.MockClientConfig
	}{
		{
			"test simple parser reconciliation",
			&corev1alpha1.HumioParser{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "humioparser",
					Namespace: "logging",
				},
				Spec: corev1alpha1.HumioParserSpec{
					ManagedClusterName: "example-humiocluster",
					Name:               "example-parser",
					RepositoryName:     "example-repo",
					ParserScript:       "kvParse()",
					TagFields:          []string{"@somefield"},
					TestData:           []string{"this is an example of rawstring"},
				},
			},
			humio.NewMocklient(humioapi.Cluster{}, nil, nil, nil, ""),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, req := reconcileInitWithHumioClient(tt.humioParser, tt.humioClient)
			defer r.logger.Sync()

			_, err := r.Reconcile(req)
			if err != nil {
				t.Errorf("reconcile: (%v)", err)
			}

			updatedParser, err := r.humioClient.GetParser(tt.humioParser)
			if err != nil {
				t.Errorf("get HumioParser: (%v)", err)
			}

			expectedParser := humioapi.Parser{
				Name:      tt.humioParser.Spec.Name,
				Script:    tt.humioParser.Spec.ParserScript,
				TagFields: tt.humioParser.Spec.TagFields,
				Tests:     helpers.MapTests(tt.humioParser.Spec.TestData, helpers.ToTestCase),
			}

			if !reflect.DeepEqual(*updatedParser, expectedParser) {
				t.Errorf("parser %#v, does not match expected %#v", *updatedParser, expectedParser)
			}
		})
	}
}

func reconcileInitWithHumioClient(humioParser *corev1alpha1.HumioParser, humioClient *humio.MockClientConfig) (*ReconcileHumioParser, reconcile.Request) {
	r, req := reconcileInit(humioParser)
	r.humioClient = humioClient
	return r, req
}

func reconcileInit(humioParser *corev1alpha1.HumioParser) (*ReconcileHumioParser, reconcile.Request) {
	logger, _ := zap.NewProduction()
	sugar := logger.Sugar().With("Request.Namespace", humioParser.Namespace, "Request.Name", humioParser.Name)

	humioCluster := &corev1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      humioParser.Spec.ManagedClusterName,
			Namespace: humioParser.Namespace,
		},
	}

	apiTokenSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-admin-token", humioParser.Spec.ManagedClusterName),
			Namespace: humioParser.Namespace,
		},
		StringData: map[string]string{
			"token": "secret-api-token",
		},
	}

	// Objects to track in the fake client.
	objs := []runtime.Object{
		humioCluster,
		apiTokenSecret,
		humioParser,
	}

	// Register operator types with the runtime scheme.
	s := scheme.Scheme
	s.AddKnownTypes(corev1alpha1.SchemeGroupVersion, humioParser)
	s.AddKnownTypes(corev1alpha1.SchemeGroupVersion, humioCluster)

	// Create a fake client to mock API calls.
	cl := fake.NewFakeClient(objs...)

	// Create a ReconcileHumioParser object with the scheme and fake client.
	r := &ReconcileHumioParser{
		client: cl,
		scheme: s,
		logger: sugar,
	}

	// Mock request to simulate Reconcile() being called on an event for a
	// watched resource .
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      humioParser.Name,
			Namespace: humioParser.Namespace,
		},
	}
	return r, req
}
