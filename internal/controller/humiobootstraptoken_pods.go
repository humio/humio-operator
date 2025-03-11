package controller

import (
	"context"

	"github.com/humio/humio-operator/internal/helpers"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ConstructBootstrapPod(ctx context.Context, bootstrapConfig *HumioBootstrapTokenConfig) *corev1.Pod {
	userID := int64(65534)
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      bootstrapConfig.podName(),
			Namespace: bootstrapConfig.namespace(),
		},
		Spec: corev1.PodSpec{
			ImagePullSecrets: bootstrapConfig.imagePullSecrets(),
			Affinity:         bootstrapConfig.affinity(),
			Containers: []corev1.Container{
				{
					Name:    HumioContainerName,
					Image:   bootstrapConfig.image(),
					Command: []string{"/bin/sleep", "900"},
					Env: []corev1.EnvVar{
						{
							Name:  "HUMIO_LOG4J_CONFIGURATION",
							Value: "log4j2-json-stdout.xml",
						},
					},
					Resources: bootstrapConfig.resources(),
					SecurityContext: &corev1.SecurityContext{
						Privileged:               helpers.BoolPtr(false),
						AllowPrivilegeEscalation: helpers.BoolPtr(false),
						ReadOnlyRootFilesystem:   helpers.BoolPtr(true),
						RunAsUser:                &userID,
						Capabilities: &corev1.Capabilities{
							Drop: []corev1.Capability{
								"ALL",
							},
						},
					},
				},
			},
		},
	}
}
