package humiocluster

import (
	"fmt"
	"k8s.io/apimachinery/pkg/api/resource"
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
	productVersion := "unknown"
	imageSplit := strings.SplitN(hc.Spec.Image, ":", 2)
	if len(imageSplit) == 2 {
		productVersion = imageSplit[1]
	}
	pod = corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-core-%s", hc.Name, generatePodSuffix()),
			Namespace: hc.Namespace,
			Labels:    kubernetes.LabelsForHumio(hc.Name),
			Annotations: map[string]string{
				"productID":      "none",
				"productName":    "humio",
				"productVersion": productVersion,
			},
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: humioServiceAccountNameOrDefault(hc),
			ImagePullSecrets:   imagePullSecretsOrDefault(hc),
			Subdomain:          hc.Name,
			InitContainers: []corev1.Container{
				{
					Name:  "zookeeper-prefix",
					Image: "humio/humio-operator-helper:0.0.1",
					Env: []corev1.EnvVar{
						{
							Name:  "MODE",
							Value: "init",
						},
						{
							Name:  "TARGET_FILE",
							Value: "/shared/zookeeper-prefix",
						},
						{
							Name: "NODE_NAME",
							ValueFrom: &corev1.EnvVarSource{
								FieldRef: &corev1.ObjectFieldSelector{
									FieldPath: "spec.nodeName",
								},
							},
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "shared",
							MountPath: "/shared",
						},
						{
							Name:      "init-service-account-secret",
							MountPath: "/var/run/secrets/kubernetes.io/serviceaccount",
							ReadOnly:  true,
						},
					},
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    *resource.NewMilliQuantity(100, resource.DecimalSI),
							corev1.ResourceMemory: *resource.NewQuantity(50*1024*1024, resource.BinarySI),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    *resource.NewMilliQuantity(100, resource.DecimalSI),
							corev1.ResourceMemory: *resource.NewQuantity(50*1024*1024, resource.BinarySI),
						},
					},
					SecurityContext: &corev1.SecurityContext{
						Capabilities: &corev1.Capabilities{
							Drop: []corev1.Capability{
								"ALL",
							},
						},
					},
				},
			},
			Containers: []corev1.Container{
				{
					Name:  "auth",
					Image: "humio/humio-operator-helper:0.0.1",
					Env: []corev1.EnvVar{
						{
							Name: "NAMESPACE",
							ValueFrom: &corev1.EnvVarSource{
								FieldRef: &corev1.ObjectFieldSelector{
									FieldPath: "metadata.namespace",
								},
							},
						},
						{
							Name:  "MODE",
							Value: "auth",
						},
						{
							Name:  "ADMIN_SECRET_NAME",
							Value: "admin-token", // TODO: get this from code
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "humio-data",
							MountPath: "/data",
							ReadOnly:  true,
						},
						{
							Name:      "auth-service-account-secret",
							MountPath: "/var/run/secrets/kubernetes.io/serviceaccount",
							ReadOnly:  true,
						},
					},
					ReadinessProbe: &corev1.Probe{
						Handler: corev1.Handler{
							HTTPGet: &corev1.HTTPGetAction{
								Path: "/",
								Port: intstr.IntOrString{IntVal: 8180},
							},
						},
					},
					LivenessProbe: &corev1.Probe{
						Handler: corev1.Handler{
							HTTPGet: &corev1.HTTPGetAction{
								Path: "/",
								Port: intstr.IntOrString{IntVal: 8180},
							},
						},
					},
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    *resource.NewMilliQuantity(100, resource.DecimalSI),
							corev1.ResourceMemory: *resource.NewQuantity(50*1024*1024, resource.BinarySI),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    *resource.NewMilliQuantity(100, resource.DecimalSI),
							corev1.ResourceMemory: *resource.NewQuantity(50*1024*1024, resource.BinarySI),
						},
					},
					SecurityContext: containerSecurityContextOrDefault(hc),
				},
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
					Env: envVarList(hc),
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "humio-data",
							MountPath: "/data",
						},
						{
							Name:      "shared",
							MountPath: "/shared",
							ReadOnly:  true,
						},
						{
							Name:      "tmp",
							MountPath: "/tmp",
							ReadOnly:  false,
						},
					},
					ReadinessProbe: &corev1.Probe{
						Handler: corev1.Handler{
							HTTPGet: &corev1.HTTPGetAction{
								Path: "/api/v1/status",
								Port: intstr.IntOrString{IntVal: 8080},
							},
						},
						InitialDelaySeconds: 30,
						PeriodSeconds:       5,
						TimeoutSeconds:      2,
						SuccessThreshold:    1,
						FailureThreshold:    10,
					},
					LivenessProbe: &corev1.Probe{
						Handler: corev1.Handler{
							HTTPGet: &corev1.HTTPGetAction{
								Path: "/api/v1/status",
								Port: intstr.IntOrString{IntVal: 8080},
							},
						},
						InitialDelaySeconds: 30,
						PeriodSeconds:       5,
						TimeoutSeconds:      2,
						SuccessThreshold:    1,
						FailureThreshold:    10,
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
					Name:         "tmp",
					VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
				},
				{
					Name: "init-service-account-secret",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName:  initServiceAccountSecretName(hc),
							DefaultMode: &mode,
						},
					},
				},
				{
					Name: "auth-service-account-secret",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName:  authServiceAccountSecretName(hc),
							DefaultMode: &mode,
						},
					},
				},
			},
			Affinity:        affinityOrDefault(hc),
			SecurityContext: podSecurityContextOrDefault(hc),
		},
	}

	idx, err := kubernetes.GetContainerIndexByName(pod, "humio")
	if err != nil {
		return &corev1.Pod{}, err
	}
	if envVarHasValue(pod.Spec.Containers[idx].Env, "AUTHENTICATION_METHOD", "saml") {
		idx, err := kubernetes.GetContainerIndexByName(pod, "humio")
		if err != nil {
			return &corev1.Pod{}, err
		}
		pod.Spec.Containers[idx].Env = append(pod.Spec.Containers[idx].Env, corev1.EnvVar{
			Name:  "SAML_IDP_CERTIFICATE",
			Value: fmt.Sprintf("/var/lib/humio/idp-certificate-secret/%s", idpCertificateFilename),
		})
		pod.Spec.Containers[idx].VolumeMounts = append(pod.Spec.Containers[idx].VolumeMounts, corev1.VolumeMount{
			Name:      "idp-cert-volume",
			ReadOnly:  true,
			MountPath: "/var/lib/humio/idp-certificate-secret",
		})
		pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
			Name: "idp-cert-volume",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  idpCertificateSecretNameOrDefault(hc),
					DefaultMode: &mode,
				},
			},
		})
	}

	if hc.Spec.HumioServiceAccountName != "" {
		pod.Spec.ServiceAccountName = hc.Spec.HumioServiceAccountName
	}

	if extraKafkaConfigsOrDefault(hc) != "" {
		idx, err := kubernetes.GetContainerIndexByName(pod, "humio")
		if err != nil {
			return &corev1.Pod{}, err
		}
		pod.Spec.Containers[idx].Env = append(pod.Spec.Containers[idx].Env, corev1.EnvVar{
			Name:  "EXTRA_KAFKA_CONFIGS_FILE",
			Value: fmt.Sprintf("/var/lib/humio/extra-kafka-configs-configmap/%s", extraKafkaPropertiesFilename),
		})
		pod.Spec.Containers[idx].VolumeMounts = append(pod.Spec.Containers[idx].VolumeMounts, corev1.VolumeMount{
			Name:      "extra-kafka-configs",
			ReadOnly:  true,
			MountPath: "/var/lib/humio/extra-kafka-configs-configmap",
		})
		pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
			Name: "extra-kafka-configs",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: extraKafkaConfigsConfigMapName(hc),
					},
					DefaultMode: &mode,
				},
			},
		})
	}

	if hc.Spec.ImagePullPolicy != "" {
		for idx := range pod.Spec.InitContainers {
			pod.Spec.InitContainers[idx].ImagePullPolicy = hc.Spec.ImagePullPolicy
		}
		for idx := range pod.Spec.Containers {
			pod.Spec.Containers[idx].ImagePullPolicy = hc.Spec.ImagePullPolicy
		}
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

func envVarHasValue(envVars []corev1.EnvVar, key string, value string) bool {
	for _, envVar := range envVars {
		if envVar.Name == key && envVar.Value == value {
			return true
		}
	}
	return false
}
