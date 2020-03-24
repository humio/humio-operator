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
			// TODO: Not sure how to use GenerateName with unit tests as this requires that the Name attribute is set by the server
			//GenerateName: fmt.Sprintf("%s-core-", hc.Name),
			Name:      fmt.Sprintf("%s-core-%s", hc.Name, generatePodSuffix()),
			Namespace: hc.Namespace,
			Labels:    labelsForHumio(hc.Name),
		},
		Spec: corev1.PodSpec{
			Subdomain: hc.Name,
			Containers: []corev1.Container{
				{
					Name:  "humio",
					Image: fmt.Sprintf("%s:%s", hc.Spec.Image, hc.Spec.Version),
					Ports: []corev1.ContainerPort{
						{
							Name:          "http",
							ContainerPort: 8080,
							Protocol:      "TCP",
						},
						{
							Name:          "es",
							ContainerPort: 9200,
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
					Name: "humio-data",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
						/*
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
								ClaimName: fmt.Sprintf("%s-core-%d", hc.Name, nodeID),
							},
						*/
					},
				},
			},
		},
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

func labelsForPod(clusterName string, nodeID int) map[string]string {
	labels := labelsForHumio(clusterName)
	labels["node_id"] = strconv.Itoa(nodeID)
	return labels
}

func generatePodSuffix() string {
	rand.Seed(time.Now().UnixNano())
	chars := []rune("ABCDEFGHIJKLMNOPQRSTUVWXYZ" +
		"abcdefghijklmnopqrstuvwxyz" +
		"0123456789")
	length := 6
	var b strings.Builder
	for i := 0; i < length; i++ {
		b.WriteRune(chars[rand.Intn(len(chars))])
	}
	return b.String()
}
