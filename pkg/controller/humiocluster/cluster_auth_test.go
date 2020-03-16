package humiocluster

import (
	"net/http"
	"net/http/httptest"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
)

func TestGetJWTForSingleUser(t *testing.T) {
	type args struct {
		hc *corev1alpha1.HumioCluster
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			"test login",
			args{
				&corev1alpha1.HumioCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "humiocluster",
						Namespace: "logging",
					},
					Spec: corev1alpha1.HumioClusterSpec{
						Image:                   "humio/humio-core",
						Version:                 "1.9.0",
						TargetReplicationFactor: 3,
						NodeCount:               3,
					},
				},
			},
			"sometoken",
			false,
		},
	}
	for _, tt := range tests {
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			//fmt.Sprintf("http://%s.%s/api/v1/login", tt.args.hc.Name))
			rw.Write([]byte(`{"token": "sometoken"}`))
		}))
		defer server.Close()

		t.Run(tt.name, func(t *testing.T) {
			got, err := getJWTForSingleUser(tt.args.hc, server.URL)
			if (err != nil) != tt.wantErr {
				t.Errorf("getJWTForSingleUser() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("getJWTForSingleUser() = %v, want %v", got, tt.want)
			}
		})
	}
}
