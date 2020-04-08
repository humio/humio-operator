package humiocluster

import (
	"fmt"
	"math/rand"
	"strings"
	"time"

	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	"github.com/humio/humio-operator/pkg/kubernetes"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func constructPod(hc *corev1alpha1.HumioCluster) (*corev1.Pod, error) {
	var pod corev1.Pod
	mode := int32(420)
	pod = corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-core-%s", hc.Name, generatePodSuffix()),
			Namespace: hc.Namespace,
			Labels:    kubernetes.LabelsForHumio(hc.Name),
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: serviceAccountNameOrDefault(hc),
			ImagePullSecrets:   imagePullSecretsOrDefault(hc),
			Subdomain:          hc.Name,
			InitContainers: []corev1.Container{
				{
					Name:    "zookeeper-prefix",
					Image:   "humio/strix", // TODO: perhaps use an official kubectl image or build our own and don't use latest
					Command: []string{"sh", "-c", "kubectl get node ${NODE_NAME} -o jsonpath={.metadata.labels.\"failure-domain.beta.kubernetes.io/zone\"} > /shared/zookeeper-prefix"},
					Env: []corev1.EnvVar{
						corev1.EnvVar{
							Name: "NODE_NAME",
							ValueFrom: &corev1.EnvVarSource{
								FieldRef: &corev1.ObjectFieldSelector{
									FieldPath: "spec.nodeName",
								},
							},
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						corev1.VolumeMount{
							Name:      "shared",
							MountPath: "/shared",
						},
						corev1.VolumeMount{
							Name:      "init-service-account-secret",
							MountPath: "/var/run/secrets/kubernetes.io/serviceaccount",
							ReadOnly:  true,
						},
					},
				},
			},
			Containers: []corev1.Container{
				{
					Name:    "humio",
					Image:   hc.Spec.Image,
					Command: []string{"/bin/sh"},
					Args:    []string{"-c", "export ZOOKEEPER_PREFIX_FOR_NODE_UUID=/humio_$(cat /shared/zookeeper-prefix)_ && exec bash /app/humio/run.sh"},
					Ports: []corev1.ContainerPort{
						{
							Name:          "http",
							ContainerPort: humioPort,
							Protocol:      "TCP",
						},
						{
							Name:          "es",
							ContainerPort: elasticPort,
							Protocol:      "TCP",
						},
					},
					Env:             envVarList(hc),
					ImagePullPolicy: "IfNotPresent",
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "humio-data",
							MountPath: "/data",
						},
						corev1.VolumeMount{
							Name:      "shared",
							MountPath: "/shared",
							ReadOnly:  true,
						},
					},
					ReadinessProbe: &corev1.Probe{
						Handler: corev1.Handler{
							HTTPGet: &corev1.HTTPGetAction{
								Path: "/api/v1/status",
								Port: intstr.IntOrString{IntVal: 8080},
							},
						},
						InitialDelaySeconds: 90,
						PeriodSeconds:       5,
						TimeoutSeconds:      2,
						SuccessThreshold:    1,
						FailureThreshold:    12,
					},
					LivenessProbe: &corev1.Probe{
						Handler: corev1.Handler{
							HTTPGet: &corev1.HTTPGetAction{
								Path: "/api/v1/status",
								Port: intstr.IntOrString{IntVal: 8080},
							},
						},
						InitialDelaySeconds: 90,
						PeriodSeconds:       5,
						TimeoutSeconds:      2,
						SuccessThreshold:    1,
						FailureThreshold:    12,
					},
					Resources:       podResourcesOrDefault(hc),
					SecurityContext: containerSecurityContextOrDefault(hc),
				},
			},
			Volumes: []corev1.Volume{
				{
					Name:         "humio-data",
					VolumeSource: dataVolumeSourceOrDefault(hc),
				},
				{
					Name:         "shared",
					VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
				},
				{
					Name: "init-service-account-secret",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName:  initServiceAccountSecretName,
							DefaultMode: &mode,
						},
					},
				},
			},
			Affinity:        affinityOrDefault(hc),
			SecurityContext: podSecurityContextOrDefault(hc),
		},
	}

	if hc.Spec.IdpCertificateSecretName != "" {
		pod.Spec.Containers[0].Env = append(pod.Spec.Containers[0].Env, corev1.EnvVar{
			Name:  "SAML_IDP_CERTIFICATE",
			Value: fmt.Sprintf("/var/lib/humio/idp-certificate-secret/%s", idpCertificateFilename),
		})
		pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{
			Name:      "idp-cert-volume",
			ReadOnly:  true,
			MountPath: "/var/lib/humio/idp-certificate-secret",
		})
		pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
			Name: "idp-cert-volume",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: idpCertificateSecretNameOrDefault(hc),
				},
			},
		})
	}

	if hc.Spec.ServiceAccountName != "" {
		pod.Spec.ServiceAccountName = hc.Spec.ServiceAccountName
	}

	if extraKafkaConfigsOrDefault(hc) != "" {
		pod.Spec.Containers[0].Env = append(pod.Spec.Containers[0].Env, corev1.EnvVar{
			Name:  "EXTRA_KAFKA_CONFIGS_FILE",
			Value: fmt.Sprintf("/var/lib/humio/extra-kafka-configs-configmap/%s", extraKafkaPropertiesFilename),
		})
		pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{
			Name:      "extra-kafka-configs",
			ReadOnly:  true,
			MountPath: "/var/lib/humio/extra-kafka-configs-configmap",
		})
		pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
			Name: "extra-kafka-configs",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					Items: []corev1.KeyToPath{
						corev1.KeyToPath{
							Key:  "extra-kafka-configs",
							Path: extraKafkaConfigsConfigmapName,
						},
					},
				},
			},
		})
	}
	return &pod, nil
}

func generatePodSuffix() string {
	rand.Seed(time.Now().UnixNano())
	chars := []rune("abcdefghijklmnopqrstuvwxyz")
	length := 6
	var b strings.Builder
	for i := 0; i < length; i++ {
		b.WriteRune(chars[rand.Intn(len(chars))])
	}
	return b.String()
}
