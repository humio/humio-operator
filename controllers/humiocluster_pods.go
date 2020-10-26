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
}

// nodeUUIDTemplateVars contains the variables that are allowed to be rendered for the nodeUUID string
type nodeUUIDTemplateVars struct {
	Zone string
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
	helperImageTag := "humio/humio-operator-helper:0.1.0"

	nodeUUIDPrefix, err := constructNodeUUIDPrefix(hc)
	if err != nil {
		return &pod, fmt.Errorf("unable to construct node UUID: %s", err)
	}

	pod = corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        humioNodeName,
			Namespace:   hc.Namespace,
			Labels:      kubernetes.LabelsForHumio(hc.Name),
			Annotations: kubernetes.AnnotationsForHumio(hc.Spec.PodAnnotations, productVersion),
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: humioServiceAccountNameOrDefault(hc),
			ImagePullSecrets:   imagePullSecretsOrDefault(hc),
			Subdomain:          hc.Name,
			Hostname:           humioNodeName,
			InitContainers: []corev1.Container{
				{
					Name:  "init",
					Image: helperImageTag,
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
									FieldPath: "spec.nodeName",
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
			},
			Containers: []corev1.Container{
				{
					Name:  "auth",
					Image: helperImageTag,
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
							Name: "POD_NAME",
							ValueFrom: &corev1.EnvVarSource{
								FieldRef: &corev1.ObjectFieldSelector{
									FieldPath: "metadata.name",
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
					Name:    "humio",
					Image:   hc.Spec.Image,
					Command: []string{"/bin/sh"},
					Args: []string{"-c",
						fmt.Sprintf("export ZONE=$(cat %s/availability-zone) && export ZOOKEEPER_PREFIX_FOR_NODE_UUID=%s && exec bash %s/run.sh",
							sharedPath, nodeUUIDPrefix, humioAppPath)},
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
					ReadinessProbe: &corev1.Probe{
						Handler: corev1.Handler{
							HTTPGet: &corev1.HTTPGetAction{
								Path:   "/api/v1/status",
								Port:   intstr.IntOrString{IntVal: humioPort},
								Scheme: getProbeScheme(hc),
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

								Path:   "/api/v1/status",
								Port:   intstr.IntOrString{IntVal: humioPort},
								Scheme: getProbeScheme(hc),
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
					Name: "init-service-account-secret",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName:  attachments.initServiceAccountSecretName,
							DefaultMode: &mode,
						},
					},
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
			Affinity:        affinityOrDefault(hc),
			SecurityContext: podSecurityContextOrDefault(hc),
		},
	}

	pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
		Name:         "humio-data",
		VolumeSource: attachments.dataVolumeSource,
	})

	humioIdx, err := kubernetes.GetContainerIndexByName(pod, "humio")
	if err != nil {
		return &corev1.Pod{}, err
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

	if hc.Spec.ImagePullPolicy != "" {
		for i := range pod.Spec.InitContainers {
			pod.Spec.InitContainers[i].ImagePullPolicy = hc.Spec.ImagePullPolicy
		}
		for i := range pod.Spec.Containers {
			pod.Spec.Containers[i].ImagePullPolicy = hc.Spec.ImagePullPolicy
		}
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
		authIdx, err := kubernetes.GetContainerIndexByName(pod, "auth")
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
		authIdx, err := kubernetes.GetContainerIndexByName(pod, "auth")
		if err != nil {
			return &corev1.Pod{}, err
		}
		pod.Spec.Containers[authIdx].Env = append(pod.Spec.Containers[authIdx].Env, corev1.EnvVar{
			Name:  "ORGANIZATION_MODE",
			Value: envVarValue(pod.Spec.Containers[humioIdx].Env, "ORGANIZATION_MODE"),
		})
	}

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

// podSpecAsSHA256 looks at the pod spec minus known nondeterministic fields and returns a sha256 hash of the spec
func podSpecAsSHA256(hc *humiov1alpha1.HumioCluster, sourcePod corev1.Pod) string {
	pod := sourcePod.DeepCopy()
	sanitizedVolumes := make([]corev1.Volume, 0)
	emptyPersistentVolumeClaimSource := corev1.PersistentVolumeClaimVolumeSource{}
	hostname := fmt.Sprintf("%s-core-%s", hc.Name, "")
	mode := int32(420)

	for idx, container := range pod.Spec.Containers {
		sanitizedEnvVars := make([]corev1.EnvVar, 0)
		if container.Name == "humio" {
			for _, envVar := range container.Env {
				if envVar.Name == "EXTERNAL_URL" {
					sanitizedEnvVars = append(sanitizedEnvVars, corev1.EnvVar{
						Name:  "EXTERNAL_URL",
						Value: fmt.Sprintf("%s://%s-core-%s.%s:%d", strings.ToLower(string(getProbeScheme(hc))), hc.Name, "", hc.Namespace, humioPort),
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
		} else if container.Name == "auth" {
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

	b, _ := json.Marshal(pod.Spec)
	return helpers.AsSHA256(string(b))
}

func (r *HumioClusterReconciler) createPod(ctx context.Context, hc *humiov1alpha1.HumioCluster, attachments *podAttachments) error {
	podName, err := findHumioNodeName(ctx, r, hc)
	if err != nil {
		r.Log.Error(err, "unable to find pod name")
		return err
	}

	pod, err := constructPod(hc, podName, attachments)
	if err != nil {
		r.Log.Error(err, "unable to construct pod")
		return err
	}

	if err := controllerutil.SetControllerReference(hc, pod, r.Scheme); err != nil {
		r.Log.Error(err, "could not set controller reference")
		return err
	}
	r.Log.Info(fmt.Sprintf("pod %s will use attachments %+v", pod.Name, attachments))
	pod.Annotations[podHashAnnotation] = podSpecAsSHA256(hc, *pod)
	if err := controllerutil.SetControllerReference(hc, pod, r.Scheme); err != nil {
		r.Log.Error(err, "could not set controller reference")
		return err
	}

	podRevision, err := r.getHumioClusterPodRevision(hc)
	if err != nil {
		return err
	}
	r.Log.Info(fmt.Sprintf("setting pod %s revision to %d", pod.Name, podRevision))
	err = r.setPodRevision(pod, podRevision)
	if err != nil {
		return err
	}

	r.Log.Info(fmt.Sprintf("creating pod %s", pod.Name))
	err = r.Create(ctx, pod)
	if err != nil {
		return err
	}
	r.Log.Info(fmt.Sprintf("successfully created pod %s", pod.Name))
	return nil
}

func (r *HumioClusterReconciler) waitForNewPod(hc *humiov1alpha1.HumioCluster, expectedPodCount int) error {
	for i := 0; i < waitForPodTimeoutSeconds; i++ {
		latestPodList, err := kubernetes.ListPods(r, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))
		if err != nil {
			return err
		}
		r.Log.Info(fmt.Sprintf("validating new pod was created. expected pod count %d, current pod count %d", expectedPodCount, len(latestPodList)))
		if len(latestPodList) >= expectedPodCount {
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
	if !specMatches {
		r.Log.Info(fmt.Sprintf("pod annotation %s does not match desired pod: got %+v, expected %+v", podHashAnnotation, pod.Annotations[podHashAnnotation], desiredPodHash))
		return false, nil
	}
	if !revisionMatches {
		r.Log.Info(fmt.Sprintf("pod annotation %s does not match desired pod: got %+v, expected %+v", podRevisionAnnotation, pod.Annotations[podRevisionAnnotation], desiredPod.Annotations[podRevisionAnnotation]))
		return false, nil
	}
	return true, nil
}

func (r *HumioClusterReconciler) getRestartPolicyFromPodInspection(pod, desiredPod corev1.Pod) (string, error) {
	humioContainerIdx, err := kubernetes.GetContainerIndexByName(pod, "humio")
	if err != nil {
		return "", err
	}
	desiredHumioContainerIdx, err := kubernetes.GetContainerIndexByName(desiredPod, "humio")
	if err != nil {
		return "", err
	}
	if pod.Spec.Containers[humioContainerIdx].Image != desiredPod.Spec.Containers[desiredHumioContainerIdx].Image {
		return PodRestartPolicyRecreate, nil
	}

	if podHasTLSEnabled(pod) != podHasTLSEnabled(desiredPod) {
		return PodRestartPolicyRecreate, nil
	}

	return PodRestartPolicyRolling, nil
}

func (r *HumioClusterReconciler) podsReady(foundPodList []corev1.Pod) (int, int) {
	var podsReadyCount int
	var podsNotReadyCount int
	for _, pod := range foundPodList {
		podsNotReadyCount++
		// pods that were just deleted may still have a status of Ready, but we should not consider them ready
		if pod.DeletionTimestamp == nil {
			for _, condition := range pod.Status.Conditions {
				if condition.Type == "Ready" {
					if condition.Status == "True" {
						r.Log.Info(fmt.Sprintf("pod %s is ready", pod.Name))
						podsReadyCount++
						podsNotReadyCount--
					} else {
						r.Log.Info(fmt.Sprintf("pod %s is not ready", pod.Name))
					}
				}
			}
		}
	}
	return podsReadyCount, podsNotReadyCount
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
		} else {
			return podLifecycleState{}, nil
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
	certificates, err := kubernetes.ListCertificates(c, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))
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
	pvcList, err := r.pvcList(hc)
	if err != nil {
		return &podAttachments{}, fmt.Errorf("problem getting pvc list: %s", err)
	}
	r.Log.Info(fmt.Sprintf("attempting to get volume source, pvc count is %d, pod count is %d", len(pvcList), len(foundPodList)))
	volumeSource, err := volumeSource(hc, foundPodList, pvcList)
	if err != nil {
		return &podAttachments{}, fmt.Errorf("unable to construct data volume source for HumioCluster: %s", err)
	}
	initSASecretName, err := r.getInitServiceAccountSecretName(ctx, hc)
	if err != nil {
		return &podAttachments{}, fmt.Errorf("unable get init service account secret for HumioCluster: %s", err)
	}
	if initSASecretName == "" {
		return &podAttachments{}, errors.New("unable to create Pod for HumioCluster: the init service account secret does not exist")
	}
	authSASecretName, err := r.getAuthServiceAccountSecretName(ctx, hc)
	if err != nil {
		return &podAttachments{}, fmt.Errorf("unable get auth service account secret for HumioCluster: %s", err)

	}
	if authSASecretName == "" {
		return &podAttachments{}, errors.New("unable to create Pod for HumioCluster: the auth service account secret does not exist")
	}

	return &podAttachments{
		dataVolumeSource:             volumeSource,
		initServiceAccountSecretName: initSASecretName,
		authServiceAccountSecretName: authSASecretName,
	}, nil
}
