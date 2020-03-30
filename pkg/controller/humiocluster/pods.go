package humiocluster

import (
	"context"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"time"

	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func (r *ReconcileHumioCluster) constructPod(hc *corev1alpha1.HumioCluster) (*corev1.Pod, error) {
	var pod corev1.Pod
	pod = corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-core-%s", hc.Name, generatePodSuffix()),
			Namespace: hc.Namespace,
			Labels:    labelsForHumio(hc.Name),
		},
		Spec: corev1.PodSpec{
			ImagePullSecrets: imagePullSecretsOrDefault(hc),
			Subdomain:        hc.Name,
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
									FieldPath: "spec.NodeName",
								},
							},
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						corev1.VolumeMount{
							Name:      "shared",
							MountPath: "/shared",
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
				},
			},
			Volumes: []corev1.Volume{
				{
					Name:         "humio-data",
					VolumeSource: dataVolumeOrDefault(hc),
				},
				{
					Name:         "shared",
					VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
				},
			},
			Affinity: affinityOrDefault(hc),
		},
	}

	if hc.Spec.IdpCertificateSecretName != "" {
		pod.Spec.Containers[0].Env = append(pod.Spec.Containers[0].Env, corev1.EnvVar{Name: "SAML_IDP_CERTIFICATE", Value: "/var/lib/humio/idp-certificate"})
		pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{Name: "idp-cert-volume", ReadOnly: true, MountPath: "/var/lib/humio/idp-certificate", SubPath: "idp-certificate"})
		pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{Name: "idp-cert-volume", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: hc.Spec.IdpCertificateSecretName}}})
	}

	if hc.Spec.ServiceAccountName != "" {
		pod.Spec.ServiceAccountName = hc.Spec.ServiceAccountName
	}

	if err := controllerutil.SetControllerReference(hc, &pod, r.scheme); err != nil {
		return &corev1.Pod{}, fmt.Errorf("could not set controller reference: %v", err)
	}
	return &pod, nil
}

// ListPods grabs the list of all pods associated to a an instance of HumioCluster
func ListPods(c client.Client, hc *corev1alpha1.HumioCluster) ([]corev1.Pod, error) {
	var foundPodList corev1.PodList
	err := c.List(context.TODO(), &foundPodList, client.InNamespace(hc.Namespace), matchingLabelsForHumio(hc.Name))
	if err != nil {
		return nil, err
	}

	return foundPodList.Items, nil
}

// DeletePod deletes a given pod
func DeletePod(c client.Client, existingPod corev1.Pod) error {
	err := c.Delete(context.TODO(), &existingPod)
	if err != nil {
		return err
	}

	return nil
}

func labelsForPod(clusterName string, nodeID int) map[string]string {
	labels := labelsForHumio(clusterName)
	labels["node_id"] = strconv.Itoa(nodeID)
	return labels
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
