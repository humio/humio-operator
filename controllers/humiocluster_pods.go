/*
Copyright 2020 Humio https://humio.com

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"reflect"
	"strings"
	"time"

	"github.com/google/go-cmp/cmp"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/humio/humio-operator/pkg/helpers"

	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/kubernetes"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	humioAppPath             = "/app/humio"
	humioDataPath            = "/data/humio-data"
	humioDataTmpPath         = "/app/humio/humio-data/tmp"
	sharedPath               = "/shared"
	tmpPath                  = "/tmp"
	waitForPodTimeoutSeconds = 30
)

type podLifecycleState struct {
	pod           corev1.Pod
	restartPolicy string
	delete        bool
}

func getProbeScheme(hc *humiov1alpha1.HumioCluster) corev1.URIScheme {
	if !helpers.TLSEnabled(hc) {
		return corev1.URISchemeHTTP
	}

	return corev1.URISchemeHTTPS
}

type podAttachments struct {
	dataVolumeSource             corev1.VolumeSource
	initServiceAccountSecretName string
	authServiceAccountSecretName string
	envVarSourceData             *map[string]string
}

// nodeUUIDTemplateVars contains the variables that are allowed to be rendered for the nodeUUID string
type nodeUUIDTemplateVars struct {
	Zone string
}

// constructContainerArgs returns the container arguments for the Humio pods. We want to grab a UUID from zookeeper
// only when using ephemeral disks. If we're using persistent storage, then we rely on Humio to generate the UUID.
// Note that relying on PVCs may not be good enough here as it's possible to have persistent storage using hostPath.
// For this reason, we rely on the USING_EPHEMERAL_DISKS environment variable.
func constructContainerArgs(hc *humiov1alpha1.HumioCluster, podEnvVars []corev1.EnvVar) ([]string, error) {
	containerArgs := []string{"-c"}
	if envVarHasValue(podEnvVars, "USING_EPHEMERAL_DISKS", "true") {
		nodeUUIDPrefix, err := constructNodeUUIDPrefix(hc)
		if err != nil {
			return []string{""}, fmt.Errorf("unable to construct node UUID: %s", err)
		}
		if hc.Spec.DisableInitContainer {
			containerArgs = append(containerArgs, fmt.Sprintf("export ZOOKEEPER_PREFIX_FOR_NODE_UUID=%s && exec bash %s/run.sh",
				nodeUUIDPrefix, humioAppPath))
		} else {
			containerArgs = append(containerArgs, fmt.Sprintf("export ZONE=$(cat %s/availability-zone) && export ZOOKEEPER_PREFIX_FOR_NODE_UUID=%s && exec bash %s/run.sh",
				sharedPath, nodeUUIDPrefix, humioAppPath))
		}
	} else {
		if hc.Spec.DisableInitContainer {
			containerArgs = append(containerArgs, fmt.Sprintf("exec bash %s/run.sh",
				humioAppPath))
		} else {
			containerArgs = append(containerArgs, fmt.Sprintf("export ZONE=$(cat %s/availability-zone) && exec bash %s/run.sh",
				sharedPath, humioAppPath))
		}
	}
	return containerArgs, nil
}

// constructNodeUUIDPrefix checks the value of the nodeUUID prefix and attempts to render it as a template. If the template
// renders {{.Zone}} as the string set to containsZoneIdentifier, then we can be assured that the desired outcome is
// that the zone in included inside the nodeUUID prefix.
func constructNodeUUIDPrefix(hc *humiov1alpha1.HumioCluster) (string, error) {
	prefix := nodeUUIDPrefixOrDefault(hc)
	containsZoneIdentifier := "containsZone"

	t := template.Must(template.New("prefix").Parse(prefix))
	data := nodeUUIDTemplateVars{Zone: containsZoneIdentifier}

	var tpl bytes.Buffer
	if err := t.Execute(&tpl, data); err != nil {
		return "", err
	}
	nodeUUIDPrefix := tpl.String()

	if strings.Contains(nodeUUIDPrefix, containsZoneIdentifier) {
		nodeUUIDPrefix = strings.Replace(nodeUUIDPrefix, containsZoneIdentifier, fmt.Sprintf("$(cat %s/availability-zone)", sharedPath), 1)
	}

	if !strings.HasPrefix(nodeUUIDPrefix, "/") {
		nodeUUIDPrefix = fmt.Sprintf("/%s", nodeUUIDPrefix)
	}
	if !strings.HasSuffix(nodeUUIDPrefix, "_") {
		nodeUUIDPrefix = fmt.Sprintf("%s_", nodeUUIDPrefix)
	}

	return nodeUUIDPrefix, nil
}

func constructPod(hc *humiov1alpha1.HumioCluster, humioNodeName string, attachments *podAttachments) (*corev1.Pod, error) {
	var pod corev1.Pod
	mode := int32(420)
	productVersion := "unknown"
	imageSplit := strings.SplitN(hc.Spec.Image, ":", 2)
	if len(imageSplit) == 2 {
		productVersion = imageSplit[1]
	}
	userID := int64(65534)

	pod = corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        humioNodeName,
			Namespace:   hc.Namespace,
			Labels:      kubernetes.LabelsForHumio(hc.Name),
			Annotations: kubernetes.AnnotationsForHumio(hc.Spec.PodAnnotations, productVersion),
		},
		Spec: corev1.PodSpec{
			ShareProcessNamespace: shareProcessNamespaceOrDefault(hc),
			ServiceAccountName:    humioServiceAccountNameOrDefault(hc),
			ImagePullSecrets:      imagePullSecretsOrDefault(hc),
			Subdomain:             fmt.Sprintf("%s-headless", hc.Name),
			Hostname:              humioNodeName,
			Containers: []corev1.Container{
				{
					Name:            authContainerName,
					Image:           helperImageOrDefault(hc),
					ImagePullPolicy: imagePullPolicyOrDefault(hc),
					Env: []corev1.EnvVar{
						{
							Name: "NAMESPACE",
							ValueFrom: &corev1.EnvVarSource{
								FieldRef: &corev1.ObjectFieldSelector{
									APIVersion: "v1",
									FieldPath:  "metadata.namespace",
								},
							},
						},
						{
							Name: "POD_NAME",
							ValueFrom: &corev1.EnvVarSource{
								FieldRef: &corev1.ObjectFieldSelector{
									APIVersion: "v1",
									FieldPath:  "metadata.name",
								},
							},
						},
						{
							Name:  "MODE",
							Value: "auth",
						},
						{
							Name:  "ADMIN_SECRET_NAME_SUFFIX",
							Value: kubernetes.ServiceTokenSecretNameSuffix,
						},
						{
							Name:  "CLUSTER_NAME",
							Value: hc.Name,
						},
						{
							Name:  "HUMIO_NODE_URL",
							Value: fmt.Sprintf("%s://$(POD_NAME):%d/", strings.ToLower(string(getProbeScheme(hc))), humioPort),
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "humio-data",
							MountPath: humioDataPath,
							ReadOnly:  true,
						},
						{
							Name:      "auth-service-account-secret",
							MountPath: "/var/run/secrets/kubernetes.io/serviceaccount",
							ReadOnly:  true,
						},
					},
					ReadinessProbe: &corev1.Probe{
						FailureThreshold: 3,
						Handler: corev1.Handler{
							HTTPGet: &corev1.HTTPGetAction{
								Path:   "/",
								Port:   intstr.IntOrString{IntVal: 8180},
								Scheme: corev1.URISchemeHTTP,
							},
						},
						PeriodSeconds:    10,
						SuccessThreshold: 1,
						TimeoutSeconds:   1,
					},
					LivenessProbe: &corev1.Probe{
						FailureThreshold: 3,
						Handler: corev1.Handler{
							HTTPGet: &corev1.HTTPGetAction{
								Path:   "/",
								Port:   intstr.IntOrString{IntVal: 8180},
								Scheme: corev1.URISchemeHTTP,
							},
						},
						PeriodSeconds:    10,
						SuccessThreshold: 1,
						TimeoutSeconds:   1,
					},
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    *resource.NewMilliQuantity(100, resource.DecimalSI),
							corev1.ResourceMemory: *resource.NewQuantity(750*1024*1024, resource.BinarySI),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    *resource.NewMilliQuantity(100, resource.DecimalSI),
							corev1.ResourceMemory: *resource.NewQuantity(150*1024*1024, resource.BinarySI),
						},
					},
					SecurityContext: containerSecurityContextOrDefault(hc),
				},
				{
					Name:            humioContainerName,
					Image:           hc.Spec.Image,
					ImagePullPolicy: imagePullPolicyOrDefault(hc),
					Command:         []string{"/bin/sh"},
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
							MountPath: humioDataPath,
						},
						{
							Name:      "humio-tmp",
							MountPath: humioDataTmpPath,
							ReadOnly:  false,
						},
						{
							Name:      "shared",
							MountPath: sharedPath,
							ReadOnly:  true,
						},
						{
							Name:      "tmp",
							MountPath: tmpPath,
							ReadOnly:  false,
						},
					},
					ReadinessProbe:  containerReadinessProbeOrDefault(hc),
					LivenessProbe:   containerLivenessProbeOrDefault(hc),
					StartupProbe:    containerStartupProbeOrDefault(hc),
					Resources:       podResourcesOrDefault(hc),
					SecurityContext: containerSecurityContextOrDefault(hc),
				},
			},
			Volumes: []corev1.Volume{
				{
					Name:         "shared",
					VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
				},
				{
					Name:         "tmp",
					VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
				},
				{
					Name:         "humio-tmp",
					VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
				},
				{
					Name: "auth-service-account-secret",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName:  attachments.authServiceAccountSecretName,
							DefaultMode: &mode,
						},
					},
				},
			},
			Affinity:                      affinityOrDefault(hc),
			Tolerations:                   tolerationsOrDefault(hc),
			SecurityContext:               podSecurityContextOrDefault(hc),
			TerminationGracePeriodSeconds: terminationGracePeriodSecondsOrDefault(hc),
		},
	}

	pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
		Name:         "humio-data",
		VolumeSource: attachments.dataVolumeSource,
	})

	humioIdx, err := kubernetes.GetContainerIndexByName(pod, humioContainerName)
	if err != nil {
		return &corev1.Pod{}, err
	}

	// If envFrom is set on the HumioCluster spec, add it to the pod spec. Add an annotation with the hash of the env
	// var values from the secret or configmap to trigger pod restarts when they change
	if len(hc.Spec.EnvironmentVariablesSource) > 0 {
		pod.Spec.Containers[humioIdx].EnvFrom = hc.Spec.EnvironmentVariablesSource
		if attachments.envVarSourceData != nil {
			b, err := json.Marshal(attachments.envVarSourceData)
			if err != nil {
				return &corev1.Pod{}, fmt.Errorf("error trying to JSON encode envVarSourceData: %s", err)
			}
			pod.Annotations[envVarSourceHashAnnotation] = helpers.AsSHA256(string(b))
		}
	}

	if envVarHasValue(pod.Spec.Containers[humioIdx].Env, "AUTHENTICATION_METHOD", "saml") {
		pod.Spec.Containers[humioIdx].Env = append(pod.Spec.Containers[humioIdx].Env, corev1.EnvVar{
			Name:  "SAML_IDP_CERTIFICATE",
			Value: fmt.Sprintf("/var/lib/humio/idp-certificate-secret/%s", idpCertificateFilename),
		})
		pod.Spec.Containers[humioIdx].VolumeMounts = append(pod.Spec.Containers[humioIdx].VolumeMounts, corev1.VolumeMount{
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

	if !hc.Spec.DisableInitContainer {
		pod.Spec.InitContainers = []corev1.Container{
			{
				Name:            initContainerName,
				Image:           helperImageOrDefault(hc),
				ImagePullPolicy: imagePullPolicyOrDefault(hc),
				Env: []corev1.EnvVar{
					{
						Name:  "MODE",
						Value: "init",
					},
					{
						Name:  "TARGET_FILE",
						Value: fmt.Sprintf("%s/availability-zone", sharedPath),
					},
					{
						Name: "NODE_NAME",
						ValueFrom: &corev1.EnvVarSource{
							FieldRef: &corev1.ObjectFieldSelector{
								APIVersion: "v1",
								FieldPath:  "spec.nodeName",
							},
						},
					},
				},
				VolumeMounts: []corev1.VolumeMount{
					{
						Name:      "shared",
						MountPath: sharedPath,
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
		}
		pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
			Name: "init-service-account-secret",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  attachments.initServiceAccountSecretName,
					DefaultMode: &mode,
				},
			},
		})
	}

	if extraKafkaConfigsOrDefault(hc) != "" {
		pod.Spec.Containers[humioIdx].Env = append(pod.Spec.Containers[humioIdx].Env, corev1.EnvVar{
			Name:  "EXTRA_KAFKA_CONFIGS_FILE",
			Value: fmt.Sprintf("/var/lib/humio/extra-kafka-configs-configmap/%s", extraKafkaPropertiesFilename),
		})
		pod.Spec.Containers[humioIdx].VolumeMounts = append(pod.Spec.Containers[humioIdx].VolumeMounts, corev1.VolumeMount{
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

	if viewGroupPermissionsOrDefault(hc) != "" {
		pod.Spec.Containers[humioIdx].Env = append(pod.Spec.Containers[humioIdx].Env, corev1.EnvVar{
			Name:  "READ_GROUP_PERMISSIONS_FROM_FILE",
			Value: "true",
		})
		pod.Spec.Containers[humioIdx].VolumeMounts = append(pod.Spec.Containers[humioIdx].VolumeMounts, corev1.VolumeMount{
			Name:      "view-group-permissions",
			ReadOnly:  true,
			MountPath: fmt.Sprintf("%s/%s", humioDataPath, viewGroupPermissionsFilename),
			SubPath:   viewGroupPermissionsFilename,
		})
		pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
			Name: "view-group-permissions",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: viewGroupPermissionsConfigMapName(hc),
					},
					DefaultMode: &mode,
				},
			},
		})
	}

	for _, sidecar := range sidecarContainersOrDefault(hc) {
		for _, existingContainer := range pod.Spec.Containers {
			if sidecar.Name == existingContainer.Name {
				return &corev1.Pod{}, fmt.Errorf("sidecarContainer conflicts with existing name: %s", sidecar.Name)

			}
		}
		pod.Spec.Containers = append(pod.Spec.Containers, sidecar)
	}

	for _, volumeMount := range extraHumioVolumeMountsOrDefault(hc) {
		for _, existingVolumeMount := range pod.Spec.Containers[humioIdx].VolumeMounts {
			if existingVolumeMount.Name == volumeMount.Name {
				return &corev1.Pod{}, fmt.Errorf("extraHumioVolumeMount conflicts with existing name: %s", existingVolumeMount.Name)
			}
			if strings.HasPrefix(existingVolumeMount.MountPath, volumeMount.MountPath) {
				return &corev1.Pod{}, fmt.Errorf("extraHumioVolumeMount conflicts with existing mount path: %s", existingVolumeMount.MountPath)
			}
		}
		pod.Spec.Containers[humioIdx].VolumeMounts = append(pod.Spec.Containers[humioIdx].VolumeMounts, volumeMount)
	}

	for _, volume := range extraVolumesOrDefault(hc) {
		for _, existingVolume := range pod.Spec.Volumes {
			if existingVolume.Name == volume.Name {
				return &corev1.Pod{}, fmt.Errorf("extraVolume conflicts with existing name: %s", existingVolume.Name)
			}
		}
		pod.Spec.Volumes = append(pod.Spec.Volumes, volume)
	}

	if helpers.TLSEnabled(hc) {
		pod.Spec.Containers[humioIdx].Env = append(pod.Spec.Containers[humioIdx].Env, corev1.EnvVar{
			Name:  "TLS_TRUSTSTORE_LOCATION",
			Value: fmt.Sprintf("/var/lib/humio/tls-certificate-secret/%s", "truststore.jks"),
		})
		pod.Spec.Containers[humioIdx].Env = append(pod.Spec.Containers[humioIdx].Env, corev1.EnvVar{
			Name:  "TLS_KEYSTORE_LOCATION",
			Value: fmt.Sprintf("/var/lib/humio/tls-certificate-secret/%s", "keystore.jks"),
		})
		pod.Spec.Containers[humioIdx].Env = append(pod.Spec.Containers[humioIdx].Env, corev1.EnvVar{
			Name: "TLS_TRUSTSTORE_PASSWORD",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: fmt.Sprintf("%s-keystore-passphrase", hc.Name),
					},
					Key: "passphrase",
				},
			},
		})
		pod.Spec.Containers[humioIdx].Env = append(pod.Spec.Containers[humioIdx].Env, corev1.EnvVar{
			Name: "TLS_KEYSTORE_PASSWORD",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: fmt.Sprintf("%s-keystore-passphrase", hc.Name),
					},
					Key: "passphrase",
				},
			},
		})
		pod.Spec.Containers[humioIdx].Env = append(pod.Spec.Containers[humioIdx].Env, corev1.EnvVar{
			Name: "TLS_KEY_PASSWORD",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: fmt.Sprintf("%s-keystore-passphrase", hc.Name),
					},
					Key: "passphrase",
				},
			},
		})
		pod.Spec.Containers[humioIdx].VolumeMounts = append(pod.Spec.Containers[humioIdx].VolumeMounts, corev1.VolumeMount{
			Name:      "tls-cert",
			ReadOnly:  true,
			MountPath: "/var/lib/humio/tls-certificate-secret",
		})

		// Configuration specific to auth container
		authIdx, err := kubernetes.GetContainerIndexByName(pod, authContainerName)
		if err != nil {
			return &corev1.Pod{}, err
		}
		// We mount in the certificate on top of default system root certs so auth container automatically uses it:
		// https://golang.org/src/crypto/x509/root_linux.go
		pod.Spec.Containers[authIdx].VolumeMounts = append(pod.Spec.Containers[authIdx].VolumeMounts, corev1.VolumeMount{
			Name:      "ca-cert",
			ReadOnly:  true,
			MountPath: "/etc/pki/tls",
		})

		// Common configuration for all containers
		pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
			Name: "tls-cert",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  humioNodeName,
					DefaultMode: &mode,
				},
			},
		})
		pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
			Name: "ca-cert",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  hc.Name,
					DefaultMode: &mode,
					Items: []corev1.KeyToPath{
						{
							Key:  "ca.crt",
							Path: "certs/ca-bundle.crt",
							Mode: &mode,
						},
					},
				},
			},
		})
	}

	if envVarHasValue(pod.Spec.Containers[humioIdx].Env, "ENABLE_ORGANIZATIONS", "true") && envVarHasKey(pod.Spec.Containers[humioIdx].Env, "ORGANIZATION_MODE") {
		authIdx, err := kubernetes.GetContainerIndexByName(pod, authContainerName)
		if err != nil {
			return &corev1.Pod{}, err
		}
		pod.Spec.Containers[authIdx].Env = append(pod.Spec.Containers[authIdx].Env, corev1.EnvVar{
			Name:  "ORGANIZATION_MODE",
			Value: envVarValue(pod.Spec.Containers[humioIdx].Env, "ORGANIZATION_MODE"),
		})
	}

	containerArgs, err := constructContainerArgs(hc, pod.Spec.Containers[humioIdx].Env)
	if err != nil {
		return &corev1.Pod{}, fmt.Errorf("unable to construct node container args: %s", err)
	}
	pod.Spec.Containers[humioIdx].Args = containerArgs

	return &pod, nil
}

func volumeSource(hc *humiov1alpha1.HumioCluster, podList []corev1.Pod, pvcList []corev1.PersistentVolumeClaim) (corev1.VolumeSource, error) {
	emptyDataVolume := corev1.VolumeSource{}

	if pvcsEnabled(hc) && !reflect.DeepEqual(hc.Spec.DataVolumeSource, emptyDataVolume) {
		return corev1.VolumeSource{}, fmt.Errorf("cannot have both dataVolumePersistentVolumeClaimSpecTemplate and dataVolumeSource defined")
	}
	if pvcsEnabled(hc) {
		pvcName, err := findNextAvailablePvc(pvcList, podList)
		if err != nil {
			return corev1.VolumeSource{}, err
		}
		return dataVolumePersistentVolumeClaimSpecTemplateOrDefault(hc, pvcName), nil
	}
	return dataVolumeSourceOrDefault(hc), nil
}

// envVarValue returns the value of the given environment variable
// if the environment varible is not preset, return empty string
func envVarValue(envVars []corev1.EnvVar, key string) string {
	for _, envVar := range envVars {
		if envVar.Name == key {
			return envVar.Value
		}
	}
	return ""
}

func envVarHasValue(envVars []corev1.EnvVar, key string, value string) bool {
	for _, envVar := range envVars {
		if envVar.Name == key && envVar.Value == value {
			return true
		}
	}
	return false
}

func envVarHasKey(envVars []corev1.EnvVar, key string) bool {
	for _, envVar := range envVars {
		if envVar.Name == key {
			return true
		}
	}
	return false
}

// sanitizePod removes known nondeterministic fields from a pod and returns it.
// This modifies the input pod object before returning it.
func sanitizePod(hc *humiov1alpha1.HumioCluster, pod *corev1.Pod) *corev1.Pod {
	// TODO: For volume mount containing service account secret, set name to empty string
	sanitizedVolumes := make([]corev1.Volume, 0)
	emptyPersistentVolumeClaimSource := corev1.PersistentVolumeClaimVolumeSource{}
	hostname := fmt.Sprintf("%s-core-%s", hc.Name, "")
	mode := int32(420)

	for idx, container := range pod.Spec.Containers {
		sanitizedEnvVars := make([]corev1.EnvVar, 0)
		if container.Name == humioContainerName {
			for _, envVar := range container.Env {
				if envVar.Name == "EXTERNAL_URL" {
					sanitizedEnvVars = append(sanitizedEnvVars, corev1.EnvVar{
						Name:  "EXTERNAL_URL",
						Value: fmt.Sprintf("%s://%s-core-%s.%s.%s-headless:%d", strings.ToLower(string(getProbeScheme(hc))), hc.Name, "", hc.Name, hc.Namespace, humioPort),
					})
				} else {
					sanitizedEnvVars = append(sanitizedEnvVars, corev1.EnvVar{
						Name:      envVar.Name,
						Value:     envVar.Value,
						ValueFrom: envVar.ValueFrom,
					})
				}
			}
			container.Env = sanitizedEnvVars
		} else if container.Name == authContainerName {
			for _, envVar := range container.Env {
				if envVar.Name == "HUMIO_NODE_URL" {
					sanitizedEnvVars = append(sanitizedEnvVars, corev1.EnvVar{
						Name:  "HUMIO_NODE_URL",
						Value: fmt.Sprintf("%s://%s-core-%s.%s:%d/", strings.ToLower(string(getProbeScheme(hc))), hc.Name, "", hc.Namespace, humioPort),
					})
				} else {
					sanitizedEnvVars = append(sanitizedEnvVars, envVar)
				}
			}
		} else {
			sanitizedEnvVars = container.Env
		}
		pod.Spec.Containers[idx].Env = sanitizedEnvVars
	}

	for _, volume := range pod.Spec.Volumes {
		if volume.Name == "humio-data" && reflect.DeepEqual(volume.PersistentVolumeClaim, emptyPersistentVolumeClaimSource) {
			sanitizedVolumes = append(sanitizedVolumes, corev1.Volume{
				Name:         "humio-data",
				VolumeSource: dataVolumeSourceOrDefault(hc),
			})
		} else if volume.Name == "humio-data" && !reflect.DeepEqual(volume.PersistentVolumeClaim, emptyPersistentVolumeClaimSource) {
			sanitizedVolumes = append(sanitizedVolumes, corev1.Volume{
				Name:         "humio-data",
				VolumeSource: dataVolumePersistentVolumeClaimSpecTemplateOrDefault(hc, ""),
			})
		} else if volume.Name == "tls-cert" {
			sanitizedVolumes = append(sanitizedVolumes, corev1.Volume{
				Name: "tls-cert",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName:  hostname,
						DefaultMode: &mode,
					},
				},
			})
		} else if volume.Name == "init-service-account-secret" {
			sanitizedVolumes = append(sanitizedVolumes, corev1.Volume{
				Name: "init-service-account-secret",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName:  fmt.Sprintf("%s-init-%s", hc.Name, ""),
						DefaultMode: &mode,
					},
				},
			})
		} else if volume.Name == "auth-service-account-secret" {
			sanitizedVolumes = append(sanitizedVolumes, corev1.Volume{
				Name: "auth-service-account-secret",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName:  fmt.Sprintf("%s-auth-%s", hc.Name, ""),
						DefaultMode: &mode,
					},
				},
			})
		} else {
			sanitizedVolumes = append(sanitizedVolumes, volume)
		}
	}
	pod.Spec.Volumes = sanitizedVolumes
	pod.Spec.Hostname = hostname

	// Values we don't set ourselves but which gets default values set.
	// To get a cleaner diff we can set these values to their zero values,
	// or to the values as obtained by our functions returning our own defaults.
	pod.Spec.RestartPolicy = ""
	pod.Spec.DNSPolicy = ""
	pod.Spec.SchedulerName = ""
	pod.Spec.Priority = nil
	pod.Spec.EnableServiceLinks = nil
	pod.Spec.PreemptionPolicy = nil
	pod.Spec.DeprecatedServiceAccount = ""
	pod.Spec.Tolerations = tolerationsOrDefault(hc)
	for i, _ := range pod.Spec.InitContainers {
		pod.Spec.InitContainers[i].ImagePullPolicy = imagePullPolicyOrDefault(hc)
		pod.Spec.InitContainers[i].TerminationMessagePath = ""
		pod.Spec.InitContainers[i].TerminationMessagePolicy = ""
	}
	for i, _ := range pod.Spec.Containers {
		pod.Spec.Containers[i].ImagePullPolicy = imagePullPolicyOrDefault(hc)
		pod.Spec.Containers[i].TerminationMessagePath = ""
		pod.Spec.Containers[i].TerminationMessagePolicy = ""
	}

	return pod
}

// podSpecAsSHA256 looks at the pod spec minus known nondeterministic fields and returns a sha256 hash of the spec
func podSpecAsSHA256(hc *humiov1alpha1.HumioCluster, sourcePod corev1.Pod) string {
	pod := sourcePod.DeepCopy()
	sanitizedPod := sanitizePod(hc, pod)
	b, _ := json.Marshal(sanitizedPod.Spec)
	return helpers.AsSHA256(string(b))
}

func (r *HumioClusterReconciler) createPod(ctx context.Context, hc *humiov1alpha1.HumioCluster, attachments *podAttachments) (*corev1.Pod, error) {
	podName, err := findHumioNodeName(ctx, r, hc)
	if err != nil {
		r.Log.Error(err, "unable to find pod name")
		return &corev1.Pod{}, err
	}

	pod, err := constructPod(hc, podName, attachments)
	if err != nil {
		r.Log.Error(err, "unable to construct pod")
		return &corev1.Pod{}, err
	}

	if err := controllerutil.SetControllerReference(hc, pod, r.Scheme()); err != nil {
		r.Log.Error(err, "could not set controller reference")
		return &corev1.Pod{}, err
	}
	r.Log.Info(fmt.Sprintf("pod %s will use attachments %+v", pod.Name, attachments))
	pod.Annotations[podHashAnnotation] = podSpecAsSHA256(hc, *pod)
	if err := controllerutil.SetControllerReference(hc, pod, r.Scheme()); err != nil {
		r.Log.Error(err, "could not set controller reference")
		return &corev1.Pod{}, err
	}

	if attachments.envVarSourceData != nil {
		b, err := json.Marshal(attachments.envVarSourceData)
		if err != nil {
			return &corev1.Pod{}, fmt.Errorf("error trying to JSON encode envVarSourceData: %s", err)
		}
		pod.Annotations[envVarSourceHashAnnotation] = helpers.AsSHA256(string(b))
	}

	podRevision, err := r.getHumioClusterPodRevision(hc)
	if err != nil {
		return &corev1.Pod{}, err
	}
	r.Log.Info(fmt.Sprintf("setting pod %s revision to %d", pod.Name, podRevision))
	err = r.setPodRevision(pod, podRevision)
	if err != nil {
		return &corev1.Pod{}, err
	}

	r.Log.Info(fmt.Sprintf("creating pod %s", pod.Name))
	err = r.Create(ctx, pod)
	if err != nil {
		return &corev1.Pod{}, err
	}
	r.Log.Info(fmt.Sprintf("successfully created pod %s", pod.Name))
	return pod, nil
}

// waitForNewPod can be used to wait for a new pod to be created after the create call is issued. It is important that
// the previousPodList contains the list of pods prior to when the new pod was created
func (r *HumioClusterReconciler) waitForNewPod(ctx context.Context, hc *humiov1alpha1.HumioCluster, previousPodList []corev1.Pod, expectedPod *corev1.Pod) error {
	// We must check only pods that were running prior to the new pod being created, and we must only include pods that
	// were running the same revision as the newly created pod. This is because there may be pods under the previous
	// revision that were still terminating when the new pod was created
	var expectedPodCount int
	for _, pod := range previousPodList {
		if pod.Annotations[podHashAnnotation] == expectedPod.Annotations[podHashAnnotation] {
			expectedPodCount++
		}
	}
	// This will account for the newly created pod
	expectedPodCount++

	for i := 0; i < waitForPodTimeoutSeconds; i++ {
		var podsMatchingRevisionCount int
		latestPodList, err := kubernetes.ListPods(ctx, r, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))
		if err != nil {
			return err
		}
		for _, pod := range latestPodList {
			if pod.Annotations[podHashAnnotation] == expectedPod.Annotations[podHashAnnotation] {
				podsMatchingRevisionCount++
			}
		}
		r.Log.Info(fmt.Sprintf("validating new pod was created. expected pod count %d, current pod count %d", expectedPodCount, podsMatchingRevisionCount))
		if podsMatchingRevisionCount >= expectedPodCount {
			return nil
		}
		time.Sleep(time.Second * 1)
	}
	return fmt.Errorf("timed out waiting to validate new pod was created")
}

func (r *HumioClusterReconciler) podsMatch(hc *humiov1alpha1.HumioCluster, pod corev1.Pod, desiredPod corev1.Pod) (bool, error) {
	if _, ok := pod.Annotations[podHashAnnotation]; !ok {
		return false, fmt.Errorf("did not find annotation with pod hash")
	}
	if _, ok := pod.Annotations[podRevisionAnnotation]; !ok {
		return false, fmt.Errorf("did not find annotation with pod revision")
	}
	var specMatches bool
	var revisionMatches bool
	var envVarSourceMatches bool

	desiredPodHash := podSpecAsSHA256(hc, desiredPod)
	existingPodRevision, err := r.getHumioClusterPodRevision(hc)
	if err != nil {
		return false, err
	}
	err = r.setPodRevision(&desiredPod, existingPodRevision)
	if err != nil {
		return false, err
	}
	if pod.Annotations[podHashAnnotation] == desiredPodHash {
		specMatches = true
	}
	if pod.Annotations[podRevisionAnnotation] == desiredPod.Annotations[podRevisionAnnotation] {
		revisionMatches = true
	}
	if _, ok := pod.Annotations[envVarSourceHashAnnotation]; ok {
		if pod.Annotations[envVarSourceHashAnnotation] == desiredPod.Annotations[envVarSourceHashAnnotation] {
			envVarSourceMatches = true
		}
	} else {
		// Ignore envVarSource hash if it's not in either the current pod or the desired pod
		if _, ok := desiredPod.Annotations[envVarSourceHashAnnotation]; !ok {
			envVarSourceMatches = true
		}
	}

	currentPodCopy := pod.DeepCopy()
	desiredPodCopy := desiredPod.DeepCopy()
	sanitizedCurrentPod := sanitizePod(hc, currentPodCopy)
	sanitizedDesiredPod := sanitizePod(hc, desiredPodCopy)
	podSpecDiff := cmp.Diff(sanitizedCurrentPod.Spec, sanitizedDesiredPod.Spec)
	if !specMatches {
		r.Log.Info(fmt.Sprintf("pod annotation %s does not match desired pod: got %+v, expected %+v", podHashAnnotation, pod.Annotations[podHashAnnotation], desiredPodHash), "podSpecDiff", podSpecDiff)
		return false, nil
	}
	if !revisionMatches {
		r.Log.Info(fmt.Sprintf("pod annotation %s does not match desired pod: got %+v, expected %+v", podRevisionAnnotation, pod.Annotations[podRevisionAnnotation], desiredPod.Annotations[podRevisionAnnotation]), "podSpecDiff", podSpecDiff)
		return false, nil
	}
	if !envVarSourceMatches {
		r.Log.Info(fmt.Sprintf("pod annotation %s does not match desired pod: got %+v, expected %+v", envVarSourceHashAnnotation, pod.Annotations[envVarSourceHashAnnotation], desiredPod.Annotations[envVarSourceHashAnnotation]), "podSpecDiff", podSpecDiff)
		return false, nil
	}
	return true, nil
}

func (r *HumioClusterReconciler) getRestartPolicyFromPodInspection(pod, desiredPod corev1.Pod) (string, error) {
	humioContainerIdx, err := kubernetes.GetContainerIndexByName(pod, humioContainerName)
	if err != nil {
		return "", err
	}
	desiredHumioContainerIdx, err := kubernetes.GetContainerIndexByName(desiredPod, humioContainerName)
	if err != nil {
		return "", err
	}
	if pod.Spec.Containers[humioContainerIdx].Image != desiredPod.Spec.Containers[desiredHumioContainerIdx].Image {
		return PodRestartPolicyRecreate, nil
	}

	if podHasTLSEnabled(pod) != podHasTLSEnabled(desiredPod) {
		return PodRestartPolicyRecreate, nil
	}

	if envVarValue(pod.Spec.Containers[humioContainerIdx].Env, "EXTERNAL_URL") != envVarValue(desiredPod.Spec.Containers[desiredHumioContainerIdx].Env, "EXTERNAL_URL") {
		return PodRestartPolicyRecreate, nil
	}

	return PodRestartPolicyRolling, nil
}

func (r *HumioClusterReconciler) getPodDesiredLifecycleState(hc *humiov1alpha1.HumioCluster, foundPodList []corev1.Pod, attachments *podAttachments) (podLifecycleState, error) {
	for _, pod := range foundPodList {
		// only consider pods not already being deleted
		if pod.DeletionTimestamp == nil {
			// if pod spec differs, we want to delete it
			desiredPod, err := constructPod(hc, "", attachments)
			if err != nil {
				r.Log.Error(err, "could not construct pod")
				return podLifecycleState{}, err
			}

			podsMatchTest, err := r.podsMatch(hc, pod, *desiredPod)
			if err != nil {
				r.Log.Error(err, "failed to check if pods match")
			}
			if !podsMatchTest {
				// TODO: figure out if we should only allow upgrades and not downgrades
				restartPolicy, err := r.getRestartPolicyFromPodInspection(pod, *desiredPod)
				if err != nil {
					r.Log.Error(err, "could not get restart policy")
					return podLifecycleState{}, err
				}
				return podLifecycleState{
					pod:           pod,
					restartPolicy: restartPolicy,
					delete:        true,
				}, err
			}
		}
	}
	return podLifecycleState{}, nil
}

func podHasTLSEnabled(pod corev1.Pod) bool {
	// TODO: perhaps we need to add a couple more checks to validate TLS is fully enabled
	podConfiguredWithTLS := false
	for _, vol := range pod.Spec.Volumes {
		if vol.Name == "tls-cert" {
			podConfiguredWithTLS = true
		}
	}
	return podConfiguredWithTLS
}

func findHumioNodeName(ctx context.Context, c client.Client, hc *humiov1alpha1.HumioCluster) (string, error) {
	// if we do not have TLS enabled, append a random suffix
	if !helpers.TLSEnabled(hc) {
		return fmt.Sprintf("%s-core-%s", hc.Name, kubernetes.RandomString()), nil
	}

	// if TLS is enabled, use the first available TLS certificate
	certificates, err := kubernetes.ListCertificates(ctx, c, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))
	if err != nil {
		return "", err
	}
	for _, certificate := range certificates {
		if certificate.Spec.Keystores == nil {
			// ignore any certificates that does not hold a keystore bundle
			continue
		}
		if certificate.Spec.Keystores.JKS == nil {
			// ignore any certificates that does not hold a JKS keystore bundle
			continue
		}

		existingPod := &corev1.Pod{}
		err := c.Get(ctx, types.NamespacedName{
			Namespace: hc.Namespace,
			Name:      certificate.Name,
		}, existingPod)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				// reuse the certificate if we know we do not have a pod that uses it
				return certificate.Name, nil
			}
			return "", err
		}
	}

	return "", fmt.Errorf("found %d certificates but none of them are available to use", len(certificates))
}

func (r *HumioClusterReconciler) newPodAttachments(ctx context.Context, hc *humiov1alpha1.HumioCluster, foundPodList []corev1.Pod) (*podAttachments, error) {
	pvcList, err := r.pvcList(ctx, hc)
	if err != nil {
		return &podAttachments{}, fmt.Errorf("problem getting pvc list: %s", err)
	}
	r.Log.Info(fmt.Sprintf("attempting to get volume source, pvc count is %d, pod count is %d", len(pvcList), len(foundPodList)))
	volumeSource, err := volumeSource(hc, foundPodList, pvcList)
	if err != nil {
		return &podAttachments{}, fmt.Errorf("unable to construct data volume source for HumioCluster: %s", err)
	}
	authSASecretName, err := r.getAuthServiceAccountSecretName(ctx, hc)
	if err != nil {
		return &podAttachments{}, fmt.Errorf("unable get auth service account secret for HumioCluster: %s", err)

	}
	if authSASecretName == "" {
		return &podAttachments{}, errors.New("unable to create Pod for HumioCluster: the auth service account secret does not exist")
	}
	if hc.Spec.DisableInitContainer {
		return &podAttachments{
			dataVolumeSource:             volumeSource,
			authServiceAccountSecretName: authSASecretName,
		}, nil
	}

	initSASecretName, err := r.getInitServiceAccountSecretName(ctx, hc)
	if err != nil {
		return &podAttachments{}, fmt.Errorf("unable get init service account secret for HumioCluster: %s", err)
	}
	if initSASecretName == "" {
		return &podAttachments{}, errors.New("unable to create Pod for HumioCluster: the init service account secret does not exist")
	}

	envVarSourceData, err := r.getEnvVarSource(ctx, hc)
	if err != nil {
		return &podAttachments{}, fmt.Errorf("unable to create Pod for HumioCluster: %s", err)
	}

	return &podAttachments{
		dataVolumeSource:             volumeSource,
		initServiceAccountSecretName: initSASecretName,
		authServiceAccountSecretName: authSASecretName,
		envVarSourceData:             envVarSourceData,
	}, nil
}
