package humiocluster

import (
	"github.com/humio/humio-operator/pkg/helpers"
	"testing"

	humioClusterv1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

func Test_setEnvironmentVariableDefaults(t *testing.T) {
	type args struct {
		humioCluster *humioClusterv1alpha1.HumioCluster
	}
	tests := []struct {
		name     string
		args     args
		expected []corev1.EnvVar
	}{
		{
			"test that default env vars are set",
			args{
				&humioClusterv1alpha1.HumioCluster{
					Spec: humioClusterv1alpha1.HumioClusterSpec{
						TLS: &humioClusterv1alpha1.HumioClusterTLSSpec{
							Enabled: helpers.BoolPtr(false),
						},
					},
				},
			},
			[]corev1.EnvVar{
				{
					Name:  "PUBLIC_URL",
					Value: "http://$(THIS_POD_IP):$(HUMIO_PORT)",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setEnvironmentVariableDefaults(tt.args.humioCluster)
			if len(tt.args.humioCluster.Spec.EnvironmentVariables) < 2 {
				t.Errorf("ClusterController.setEnvironmentVariableDefaults() expected some env vars to be set, got %v", tt.args.humioCluster.Spec.EnvironmentVariables)
			}

			found := false
			for _, envVar := range tt.args.humioCluster.Spec.EnvironmentVariables {
				if tt.expected[0].Name == envVar.Name && tt.expected[0].Value == envVar.Value {
					found = true
				}
			}
			if !found {
				t.Errorf("ClusterController.setEnvironmentVariableDefaults() expected additional env vars to be set, expected list to contain %v , got %v", tt.expected, tt.args.humioCluster.Spec.EnvironmentVariables)
			}
		})
	}
}

func Test_setEnvironmentVariableDefault(t *testing.T) {
	type args struct {
		humioCluster  *humioClusterv1alpha1.HumioCluster
		defaultEnvVar corev1.EnvVar
	}
	tests := []struct {
		name     string
		args     args
		expected []corev1.EnvVar
	}{
		{
			"test that default env vars are set",
			args{
				&humioClusterv1alpha1.HumioCluster{},
				corev1.EnvVar{
					Name:  "test",
					Value: "test",
				},
			},
			[]corev1.EnvVar{
				{
					Name:  "test",
					Value: "test",
				},
			},
		},
		{
			"test that default env vars are overridden",
			args{
				&humioClusterv1alpha1.HumioCluster{},
				corev1.EnvVar{
					Name:  "PUBLIC_URL",
					Value: "test",
				},
			},
			[]corev1.EnvVar{
				{
					Name:  "PUBLIC_URL",
					Value: "test",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appendEnvironmentVariableDefault(tt.args.humioCluster, tt.args.defaultEnvVar)
			found := false
			for _, envVar := range tt.args.humioCluster.Spec.EnvironmentVariables {
				if tt.expected[0].Name == envVar.Name && tt.expected[0].Value == envVar.Value {
					found = true
				}
			}
			if !found {
				t.Errorf("ClusterController.setEnvironmentVariableDefault() expected additional env vars to be set, expected list to contain %v , got %v", tt.expected, tt.args.humioCluster.Spec.EnvironmentVariables)
			}
		})
	}
}
