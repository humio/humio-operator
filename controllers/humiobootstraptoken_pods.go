package controllers

import (
	"fmt"

	"github.com/humio/humio-operator/pkg/helpers"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ConstructBootstrapPod(bootstrapConfig *HumioBootstrapTokenConfig) (*corev1.Pod, error) {
	//var pod corev1.Pod
	//productVersion := "unknown"
	//imageSplit := strings.SplitN(bootstrapConfig.image(), ":", 2)
	//if len(imageSplit) == 2 {
	//	productVersion = imageSplit[1]
	//}
	userID := int64(65534)

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-bootstrap-token-onetime", bootstrapConfig.name()),
			Namespace: bootstrapConfig.namespace(),
		},
		Spec: corev1.PodSpec{
			//ShareProcessNamespace: hnp.GetShareProcessNamespace(),
			//ServiceAccountName:    hnp.GetHumioServiceAccountName(),
			////ImagePullSecrets: hnp.GetImagePullSecrets(),
			Containers: []corev1.Container{
				{
					Name:  HumioContainerName,
					Image: bootstrapConfig.image(),
					////ImagePullPolicy: hnp.GetImagePullPolicy(),
					Command: []string{"/bin/sh", "/bin/sleep", "900"},
					Env: []corev1.EnvVar{
						{
							Name:  "HUMIO_LOG4J_CONFIGURATION",
							Value: "log4j2-json-stdout.xml",
						},
					},
					//Resources:       hnp.GetResources(),
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
			//Affinity:                      hnp.GetAffinity(),
			//Tolerations:                   hnp.GetTolerations(),
			//TopologySpreadConstraints:     hnp.GetTopologySpreadConstraints(),
			//SecurityContext:               hnp.GetPodSecurityContext(),
			//TerminationGracePeriodSeconds: hnp.GetTerminationGracePeriodSeconds(),
		},
	}, nil

}
