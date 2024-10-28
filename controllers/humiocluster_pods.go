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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
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
)

const (
	humioAppPath             = "/app/humio"
	HumioDataPath            = "/data/humio-data"
	sharedPath               = "/shared"
	waitForPodTimeoutSeconds = 10
)

type podAttachments struct {
	dataVolumeSource              corev1.VolumeSource
	initServiceAccountSecretName  string
	envVarSourceData              *map[string]string
	bootstrapTokenSecretReference bootstrapTokenSecret
}

type bootstrapTokenSecret struct {
	hash            string
	secretReference *corev1.SecretKeySelector
}

// ConstructContainerArgs returns the container arguments for the Humio pods. We want to grab a UUID from zookeeper
// only when using ephemeral disks. If we're using persistent storage, then we rely on Humio to generate the UUID.
// Note that relying on PVCs may not be good enough here as it's possible to have persistent storage using hostPath.
// For this reason, we rely on the USING_EPHEMERAL_DISKS environment variable.
func ConstructContainerArgs(hnp *HumioNodePool, podEnvVars []corev1.EnvVar) ([]string, error) {
	var shellCommands []string

	if !hnp.InitContainerDisabled() {
		shellCommands = append(shellCommands, fmt.Sprintf("export ZONE=$(cat %s/availability-zone)", sharedPath))
	}

	hnpResources := hnp.GetResources()
	if !EnvVarHasKey(podEnvVars, "CORES") && hnpResources.Limits.Cpu().IsZero() {
		shellCommands = append(shellCommands, "export CORES=$(getconf _NPROCESSORS_ONLN)")
		shellCommands = append(shellCommands, "export HUMIO_OPTS=\"$HUMIO_OPTS -XX:ActiveProcessorCount=$(getconf _NPROCESSORS_ONLN)\"")
	}

	sort.Strings(shellCommands)
	shellCommands = append(shellCommands, fmt.Sprintf("exec bash %s/run.sh", humioAppPath))
	return []string{"-c", strings.Join(shellCommands, " && ")}, nil
}

func ConstructPod(hnp *HumioNodePool, humioNodeName string, attachments *podAttachments) (*corev1.Pod, error) {
	var pod corev1.Pod
	mode := int32(420)
	productVersion := "unknown"
	imageSplit := strings.SplitN(hnp.GetImage(), ":", 2)
	if len(imageSplit) == 2 {
		productVersion = imageSplit[1]
	}
	userID := int64(65534)

	pod = corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        humioNodeName,
			Namespace:   hnp.GetNamespace(),
			Labels:      hnp.GetPodLabels(),
			Annotations: kubernetes.AnnotationsForHumio(hnp.GetPodAnnotations(), productVersion),
		},
		Spec: corev1.PodSpec{
			ShareProcessNamespace: hnp.GetShareProcessNamespace(),
			ServiceAccountName:    hnp.GetHumioServiceAccountName(),
			ImagePullSecrets:      hnp.GetImagePullSecrets(),
			Subdomain:             headlessServiceName(hnp.GetClusterName()),
			Hostname:              humioNodeName,
			Containers: []corev1.Container{
				{
					Name:            HumioContainerName,
					Image:           hnp.GetImage(),
					ImagePullPolicy: hnp.GetImagePullPolicy(),
					Command:         []string{"/bin/sh"},
					Ports: []corev1.ContainerPort{
						{
							Name:          "http",
							ContainerPort: HumioPort,
							Protocol:      "TCP",
						},
						{
							Name:          "es",
							ContainerPort: elasticPort,
							Protocol:      "TCP",
						},
					},
					Env: hnp.GetEnvironmentVariables(),
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "humio-data",
							MountPath: HumioDataPath,
						},
						{
							Name:      "shared",
							MountPath: sharedPath,
							ReadOnly:  true,
						},
					},
					ReadinessProbe:  hnp.GetContainerReadinessProbe(),
					LivenessProbe:   hnp.GetContainerLivenessProbe(),
					StartupProbe:    hnp.GetContainerStartupProbe(),
					Resources:       hnp.GetResources(),
					SecurityContext: hnp.GetContainerSecurityContext(),
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "shared",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{
							Medium:    hnp.GetSharedVolumeStorageMedium(),
							SizeLimit: hnp.GetSharedVolumeStorageSizeLimit(),
						},
					},
				},
			},
			Affinity:                      hnp.GetAffinity(),
			Tolerations:                   hnp.GetTolerations(),
			TopologySpreadConstraints:     hnp.GetTopologySpreadConstraints(),
			SecurityContext:               hnp.GetPodSecurityContext(),
			TerminationGracePeriodSeconds: hnp.GetTerminationGracePeriodSeconds(),
		},
	}

	pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
		Name:         "humio-data",
		VolumeSource: attachments.dataVolumeSource,
	})

	humioIdx, err := kubernetes.GetContainerIndexByName(pod, HumioContainerName)
	if err != nil {
		return &corev1.Pod{}, err
	}

	// If envFrom is set on the HumioCluster spec, add it to the pod spec. Add an annotation with the hash of the env
	// var values from the secret or configmap to trigger pod restarts when they change
	if len(hnp.GetEnvironmentVariablesSource()) > 0 {
		pod.Spec.Containers[humioIdx].EnvFrom = hnp.GetEnvironmentVariablesSource()
		if attachments.envVarSourceData != nil {
			b, err := json.Marshal(attachments.envVarSourceData)
			if err != nil {
				return &corev1.Pod{}, fmt.Errorf("error trying to JSON encode envVarSourceData: %w", err)
			}
			pod.Annotations[envVarSourceHashAnnotation] = helpers.AsSHA256(string(b))
		}
	}

	if EnvVarHasValue(pod.Spec.Containers[humioIdx].Env, "AUTHENTICATION_METHOD", "saml") {
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
					SecretName:  hnp.GetIDPCertificateSecretName(),
					DefaultMode: &mode,
				},
			},
		})
	}

	if !hnp.InitContainerDisabled() {
		pod.Spec.InitContainers = []corev1.Container{
			{
				Name:            InitContainerName,
				Image:           hnp.GetHelperImage(),
				ImagePullPolicy: hnp.GetImagePullPolicy(),
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

	if attachments.bootstrapTokenSecretReference.secretReference != nil {
		pod.Spec.Containers[humioIdx].Env = append(pod.Spec.Containers[humioIdx].Env, corev1.EnvVar{
			Name: "BOOTSTRAP_ROOT_TOKEN_HASHED",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: attachments.bootstrapTokenSecretReference.secretReference,
			},
		})
	}

	if hnp.GetExtraKafkaConfigs() != "" {
		pod.Spec.Containers[humioIdx].Env = append(pod.Spec.Containers[humioIdx].Env, corev1.EnvVar{
			Name:  "EXTRA_KAFKA_CONFIGS_FILE",
			Value: fmt.Sprintf("/var/lib/humio/extra-kafka-configs-configmap/%s", ExtraKafkaPropertiesFilename),
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
						Name: hnp.GetExtraKafkaConfigsConfigMapName(),
					},
					DefaultMode: &mode,
				},
			},
		})
	}

	if hnp.GetViewGroupPermissions() != "" {
		pod.Spec.Containers[humioIdx].Env = append(pod.Spec.Containers[humioIdx].Env, corev1.EnvVar{
			Name:  "READ_GROUP_PERMISSIONS_FROM_FILE",
			Value: "true",
		})
		pod.Spec.Containers[humioIdx].VolumeMounts = append(pod.Spec.Containers[humioIdx].VolumeMounts, corev1.VolumeMount{
			Name:      "view-group-permissions",
			ReadOnly:  true,
			MountPath: fmt.Sprintf("%s/%s", HumioDataPath, ViewGroupPermissionsFilename),
			SubPath:   ViewGroupPermissionsFilename,
		})
		pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
			Name: "view-group-permissions",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: hnp.GetViewGroupPermissionsConfigMapName(),
					},
					DefaultMode: &mode,
				},
			},
		})
	}

	if hnp.GetRolePermissions() != "" {
		pod.Spec.Containers[humioIdx].Env = append(pod.Spec.Containers[humioIdx].Env, corev1.EnvVar{
			Name:  "READ_GROUP_PERMISSIONS_FROM_FILE",
			Value: "true",
		})
		pod.Spec.Containers[humioIdx].VolumeMounts = append(pod.Spec.Containers[humioIdx].VolumeMounts, corev1.VolumeMount{
			Name:      "role-permissions",
			ReadOnly:  true,
			MountPath: fmt.Sprintf("%s/%s", HumioDataPath, RolePermissionsFilename),
			SubPath:   RolePermissionsFilename,
		})
		pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
			Name: "role-permissions",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: hnp.GetRolePermissionsConfigMapName(),
					},
					DefaultMode: &mode,
				},
			},
		})
	}

	for _, sidecar := range hnp.GetSidecarContainers() {
		for _, existingContainer := range pod.Spec.Containers {
			if sidecar.Name == existingContainer.Name {
				return &corev1.Pod{}, fmt.Errorf("sidecarContainer conflicts with existing name: %s", sidecar.Name)

			}
		}
		pod.Spec.Containers = append(pod.Spec.Containers, sidecar)
	}

	for _, volumeMount := range hnp.GetExtraHumioVolumeMounts() {
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

	for _, volume := range hnp.GetExtraVolumes() {
		for _, existingVolume := range pod.Spec.Volumes {
			if existingVolume.Name == volume.Name {
				return &corev1.Pod{}, fmt.Errorf("extraVolume conflicts with existing name: %s", existingVolume.Name)
			}
		}
		pod.Spec.Volumes = append(pod.Spec.Volumes, volume)
	}

	if hnp.TLSEnabled() {
		pod.Annotations[certHashAnnotation] = GetDesiredCertHash(hnp)
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
						Name: fmt.Sprintf("%s-keystore-passphrase", hnp.GetClusterName()),
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
						Name: fmt.Sprintf("%s-keystore-passphrase", hnp.GetClusterName()),
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
						Name: fmt.Sprintf("%s-keystore-passphrase", hnp.GetClusterName()),
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
	}

	priorityClassName := hnp.GetPriorityClassName()
	if priorityClassName != "" {
		pod.Spec.PriorityClassName = priorityClassName
	}

	containerArgs, err := ConstructContainerArgs(hnp, pod.Spec.Containers[humioIdx].Env)
	if err != nil {
		return &corev1.Pod{}, fmt.Errorf("unable to construct node container args: %w", err)
	}
	pod.Spec.Containers[humioIdx].Args = containerArgs

	pod.Annotations[PodRevisionAnnotation] = strconv.Itoa(hnp.GetDesiredPodRevision())
	pod.Annotations[PodHashAnnotation] = podSpecAsSHA256(hnp, pod)
	pod.Annotations[BootstrapTokenHashAnnotation] = attachments.bootstrapTokenSecretReference.hash
	return &pod, nil
}

func findAvailableVolumeSourceForPod(hnp *HumioNodePool, podList []corev1.Pod, pvcList []corev1.PersistentVolumeClaim, pvcClaimNamesInUse map[string]struct{}) (corev1.VolumeSource, error) {
	if hnp.PVCsEnabled() && hnp.GetDataVolumeSource() != (corev1.VolumeSource{}) {
		return corev1.VolumeSource{}, fmt.Errorf("cannot have both dataVolumePersistentVolumeClaimSpecTemplate and dataVolumeSource defined")
	}
	if hnp.PVCsEnabled() {
		pvcName, err := FindNextAvailablePvc(pvcList, podList, pvcClaimNamesInUse)
		if err != nil {
			return corev1.VolumeSource{}, err
		}
		return hnp.GetDataVolumePersistentVolumeClaimSpecTemplate(pvcName), nil
	}
	return hnp.GetDataVolumeSource(), nil
}

// EnvVarValue returns the value of the given environment variable
// if the environment variable is not preset, return empty string
func EnvVarValue(envVars []corev1.EnvVar, key string) string {
	for _, envVar := range envVars {
		if envVar.Name == key {
			return envVar.Value
		}
	}
	return ""
}

func EnvVarHasValue(envVars []corev1.EnvVar, key string, value string) bool {
	for _, envVar := range envVars {
		if envVar.Name == key && envVar.Value == value {
			return true
		}
	}
	return false
}

func EnvVarHasKey(envVars []corev1.EnvVar, key string) bool {
	for _, envVar := range envVars {
		if envVar.Name == key {
			return true
		}
	}
	return false
}

// sanitizePod removes known nondeterministic fields from a pod and returns it.
// This modifies the input pod object before returning it.
func sanitizePod(hnp *HumioNodePool, pod *corev1.Pod) *corev1.Pod {
	// TODO: For volume mount containing service account secret, set name to empty string
	sanitizedVolumes := make([]corev1.Volume, 0)
	emptyPersistentVolumeClaimSource := corev1.PersistentVolumeClaimVolumeSource{}
	hostname := fmt.Sprintf("%s-core-%s", hnp.GetNodePoolName(), "")
	mode := int32(420)

	for idx, container := range pod.Spec.Containers {
		sanitizedEnvVars := make([]corev1.EnvVar, 0)
		if container.Name == HumioContainerName {
			for _, envVar := range container.Env {
				if envVar.Name == "EXTERNAL_URL" {
					sanitizedEnvVars = append(sanitizedEnvVars, corev1.EnvVar{
						Name:  "EXTERNAL_URL",
						Value: fmt.Sprintf("%s://%s-core-%s.%s.%s:%d", strings.ToLower(string(hnp.GetProbeScheme())), hnp.GetNodePoolName(), "", headlessServiceName(hnp.GetClusterName()), hnp.GetNamespace(), HumioPort),
					})
				} else {
					sanitizedEnvVars = append(sanitizedEnvVars, envVar)
				}
			}
			container.Env = sanitizedEnvVars
		} else {
			sanitizedEnvVars = container.Env
		}
		pod.Spec.Containers[idx].Env = sanitizedEnvVars
	}

	for _, volume := range pod.Spec.Volumes {
		if volume.Name == "humio-data" && reflect.DeepEqual(volume.PersistentVolumeClaim, emptyPersistentVolumeClaimSource) {
			sanitizedVolumes = append(sanitizedVolumes, corev1.Volume{
				Name:         "humio-data",
				VolumeSource: hnp.GetDataVolumeSource(),
			})
		} else if volume.Name == "humio-data" && !reflect.DeepEqual(volume.PersistentVolumeClaim, emptyPersistentVolumeClaimSource) {
			sanitizedVolumes = append(sanitizedVolumes, corev1.Volume{
				Name:         "humio-data",
				VolumeSource: hnp.GetDataVolumePersistentVolumeClaimSpecTemplate(""),
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
						SecretName:  fmt.Sprintf("%s-init-%s", hnp.GetNodePoolName(), ""),
						DefaultMode: &mode,
					},
				},
			})

		} else if strings.HasPrefix("kube-api-access-", volume.Name) {
			sanitizedVolumes = append(sanitizedVolumes, corev1.Volume{
				Name:         "kube-api-access-",
				VolumeSource: corev1.VolumeSource{},
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
	pod.Spec.NodeName = ""
	pod.Spec.Tolerations = hnp.GetTolerations()
	pod.Spec.TopologySpreadConstraints = hnp.GetTopologySpreadConstraints()

	for i := range pod.Spec.InitContainers {
		pod.Spec.InitContainers[i].ImagePullPolicy = hnp.GetImagePullPolicy()
		pod.Spec.InitContainers[i].TerminationMessagePath = ""
		pod.Spec.InitContainers[i].TerminationMessagePolicy = ""
	}
	for i := range pod.Spec.Containers {
		pod.Spec.Containers[i].ImagePullPolicy = hnp.GetImagePullPolicy()
		pod.Spec.Containers[i].TerminationMessagePath = ""
		pod.Spec.Containers[i].TerminationMessagePolicy = ""
	}

	// Sort lists of container environment variables, so we won't get a diff because the order changes.
	for _, container := range pod.Spec.Containers {
		sort.SliceStable(container.Env, func(i, j int) bool {
			return container.Env[i].Name > container.Env[j].Name
		})
	}
	for _, container := range pod.Spec.InitContainers {
		sort.SliceStable(container.Env, func(i, j int) bool {
			return container.Env[i].Name > container.Env[j].Name
		})
	}

	return pod
}

// podSpecAsSHA256 looks at the pod spec minus known nondeterministic fields and returns a sha256 hash of the spec
func podSpecAsSHA256(hnp *HumioNodePool, sourcePod corev1.Pod) string {
	pod := sourcePod.DeepCopy()
	sanitizedPod := sanitizePod(hnp, pod)
	b, _ := json.Marshal(sanitizedPod.Spec)
	return helpers.AsSHA256(string(b))
}

func (r *HumioClusterReconciler) createPod(ctx context.Context, hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool, attachments *podAttachments, newlyCreatedPods []corev1.Pod) (*corev1.Pod, error) {
	podNameAndCertHash, err := findHumioNodeNameAndCertHash(ctx, r, hnp, newlyCreatedPods)
	if err != nil {
		return &corev1.Pod{}, r.logErrorAndReturn(err, "unable to find pod name")
	}

	bootstrapTokenHash, err := r.getDesiredBootstrapTokenHash(ctx, hc)
	if err != nil {
		return &corev1.Pod{}, r.logErrorAndReturn(err, "unable to find bootstrap token secret")
	}
	attachments.bootstrapTokenSecretReference.hash = bootstrapTokenHash

	pod, err := ConstructPod(hnp, podNameAndCertHash.podName, attachments)
	if err != nil {
		return &corev1.Pod{}, r.logErrorAndReturn(err, "unable to construct pod")
	}

	if err := controllerutil.SetControllerReference(hc, pod, r.Scheme()); err != nil {
		return &corev1.Pod{}, r.logErrorAndReturn(err, "could not set controller reference")
	}
	r.Log.Info(fmt.Sprintf("pod %s will use attachments %+v", pod.Name, attachments))
	if hnp.TLSEnabled() {
		pod.Annotations[certHashAnnotation] = podNameAndCertHash.certificateHash
	}

	r.Log.Info(fmt.Sprintf("creating pod %s with podRevision=%d and podHash=%s",
		pod.Name, hnp.GetDesiredPodRevision(), hnp.GetDesiredPodHash()))
	err = r.Create(ctx, pod)
	if err != nil {
		return &corev1.Pod{}, err
	}
	r.Log.Info(fmt.Sprintf("successfully created pod %s with revision %d", pod.Name, hnp.GetDesiredPodRevision()))
	return pod, nil
}

// waitForNewPods can be used to wait for new pods to be created after the create call is issued. It is important that
// the previousPodList contains the list of pods prior to when the new pods were created
func (r *HumioClusterReconciler) waitForNewPods(ctx context.Context, hnp *HumioNodePool, previousPodList []corev1.Pod, expectedPods []corev1.Pod) error {
	// We must check only pods that were running prior to the new pod being created, and we must only include pods that
	// were running the same revision as the newly created pods. This is because there may be pods under the previous
	// revision that were still terminating when the new pod was created
	var expectedPodCount int
	for _, pod := range previousPodList {
		if pod.Annotations[PodHashAnnotation] == expectedPods[0].Annotations[PodHashAnnotation] {
			expectedPodCount++
		}
	}

	// This will account for the newly created pods
	expectedPodCount += len(expectedPods)

	for i := 0; i < waitForPodTimeoutSeconds; i++ {
		var podsMatchingRevisionCount int
		latestPodList, err := kubernetes.ListPods(ctx, r, hnp.GetNamespace(), hnp.GetNodePoolLabels())
		if err != nil {
			return err
		}
		for _, pod := range latestPodList {
			if pod.Annotations[PodHashAnnotation] == expectedPods[0].Annotations[PodHashAnnotation] {
				podsMatchingRevisionCount++
			}
		}
		r.Log.Info(fmt.Sprintf("validating new pods were created. expected pod count %d, current pod count %d", expectedPodCount, podsMatchingRevisionCount))
		if podsMatchingRevisionCount >= expectedPodCount {
			return nil
		}
		time.Sleep(time.Second * 1)
	}
	return fmt.Errorf("timed out waiting to validate new pods was created")
}

func (r *HumioClusterReconciler) podsMatch(hnp *HumioNodePool, pod corev1.Pod, desiredPod corev1.Pod) bool {
	// if mandatory annotations are not present, we can return early indicating they need to be replaced
	if _, ok := pod.Annotations[PodHashAnnotation]; !ok {
		return false
	}
	if _, ok := pod.Annotations[PodRevisionAnnotation]; !ok {
		return false
	}
	if _, ok := pod.Annotations[BootstrapTokenHashAnnotation]; !ok {
		return false
	}

	specMatches := annotationValueIsEqualIfPresentOnBothPods(pod, desiredPod, PodHashAnnotation)
	revisionMatches := annotationValueIsEqualIfPresentOnBothPods(pod, desiredPod, PodRevisionAnnotation)
	bootstrapTokenAnnotationMatches := annotationValueIsEqualIfPresentOnBothPods(pod, desiredPod, BootstrapTokenHashAnnotation)
	envVarSourceMatches := annotationValueIsEqualIfPresentOnBothPods(pod, desiredPod, envVarSourceHashAnnotation)
	certHashAnnotationMatches := annotationValueIsEqualIfPresentOnBothPods(pod, desiredPod, certHashAnnotation)

	currentPodCopy := pod.DeepCopy()
	desiredPodCopy := desiredPod.DeepCopy()
	sanitizedCurrentPod := sanitizePod(hnp, currentPodCopy)
	sanitizedDesiredPod := sanitizePod(hnp, desiredPodCopy)
	podSpecDiff := cmp.Diff(sanitizedCurrentPod.Spec, sanitizedDesiredPod.Spec)
	if !specMatches {
		r.Log.Info(fmt.Sprintf("pod annotation %s does not match desired pod: got %+v, expected %+v", PodHashAnnotation, pod.Annotations[PodHashAnnotation], desiredPod.Annotations[PodHashAnnotation]), "podSpecDiff", podSpecDiff)
		return false
	}
	if !revisionMatches {
		r.Log.Info(fmt.Sprintf("pod annotation %s does not match desired pod: got %+v, expected %+v", PodRevisionAnnotation, pod.Annotations[PodRevisionAnnotation], desiredPod.Annotations[PodRevisionAnnotation]), "podSpecDiff", podSpecDiff)
		return false
	}
	if !bootstrapTokenAnnotationMatches {
		r.Log.Info(fmt.Sprintf("pod annotation %s does not match desired pod: got %+v, expected %+v", BootstrapTokenHashAnnotation, pod.Annotations[BootstrapTokenHashAnnotation], desiredPod.Annotations[BootstrapTokenHashAnnotation]), "podSpecDiff", podSpecDiff)
		return false
	}
	if !envVarSourceMatches {
		r.Log.Info(fmt.Sprintf("pod annotation %s does not match desired pod: got %+v, expected %+v", envVarSourceHashAnnotation, pod.Annotations[envVarSourceHashAnnotation], desiredPod.Annotations[envVarSourceHashAnnotation]), "podSpecDiff", podSpecDiff)
		return false
	}
	if !certHashAnnotationMatches {
		r.Log.Info(fmt.Sprintf("pod annotation %s does not match desired pod: got %+v, expected %+v", certHashAnnotation, pod.Annotations[certHashAnnotation], desiredPod.Annotations[certHashAnnotation]), "podSpecDiff", podSpecDiff)
		return false
	}
	return true
}

func annotationValueIsEqualIfPresentOnBothPods(x, y corev1.Pod, annotation string) bool {
	if _, foundX := x.Annotations[annotation]; foundX {
		if x.Annotations[annotation] == y.Annotations[annotation] {
			return true
		}
	} else {
		// Ignore annotation if it's not in either the current pod or the desired pod
		if _, foundY := y.Annotations[annotation]; !foundY {
			return true
		}
	}
	return false
}

// getPodDesiredLifecycleState goes through the list of pods and decides what action to take for the pods.
// It compares pods it is given with a newly-constructed pod. If they do not match, we know we have
// "at least" a configuration difference and require a rolling replacement of the pods.
// If the container image differs, it will indicate that a version difference is present.
// For very specific configuration differences it may indicate that all pods in the node pool should be
// replaced simultaneously.
// The value of podLifecycleState.pod indicates what pod should be replaced next.
func (r *HumioClusterReconciler) getPodDesiredLifecycleState(ctx context.Context, hnp *HumioNodePool, foundPodList []corev1.Pod, attachments *podAttachments, podsWithErrorsFoundSoBypassZoneAwareness bool) (podLifecycleState, *corev1.Pod, error) {
	podLifecycleStateValue := NewPodLifecycleState(*hnp)

	// if pod spec differs, we want to delete it
	desiredPod, err := ConstructPod(hnp, "", attachments)
	if err != nil {
		return podLifecycleState{}, nil, r.logErrorAndReturn(err, "could not construct pod")
	}

	if attachments.bootstrapTokenSecretReference.secretReference != nil {
		desiredPod.Annotations[BootstrapTokenHashAnnotation] = attachments.bootstrapTokenSecretReference.hash
	}

	desiredHumioContainerIdx, err := kubernetes.GetContainerIndexByName(*desiredPod, HumioContainerName)
	if err != nil {
		return podLifecycleState{}, nil, r.logErrorAndReturn(err, "could not get pod desired lifecycle state")
	}

	for _, currentPod := range foundPodList {
		// only consider pods not already being deleted
		if currentPod.DeletionTimestamp != nil {
			continue
		}

		podsMatch := r.podsMatch(hnp, currentPod, *desiredPod)

		// ignore pod if it matches the desired pod
		if podsMatch {
			continue
		}

		// pods do not match, append to list of pods to be replaced
		podLifecycleStateValue.configurationDifference = &podLifecycleStateConfigurationDifference{}

		// compare image versions and if they differ, we register a version difference with associated from/to versions
		humioContainerIdx, err := kubernetes.GetContainerIndexByName(currentPod, HumioContainerName)
		if err != nil {
			return podLifecycleState{}, nil, r.logErrorAndReturn(err, "could not get pod desired lifecycle state")
		}

		if currentPod.Spec.Containers[humioContainerIdx].Image != desiredPod.Spec.Containers[desiredHumioContainerIdx].Image {
			r.Log.Info("found version difference")
			fromVersion := HumioVersionFromString(currentPod.Spec.Containers[humioContainerIdx].Image)
			toVersion := HumioVersionFromString(desiredPod.Spec.Containers[desiredHumioContainerIdx].Image)
			podLifecycleStateValue.versionDifference = &podLifecycleStateVersionDifference{
				from: fromVersion,
				to:   toVersion,
			}
		}

		// Changes to EXTERNAL_URL means we've toggled TLS on/off and must restart all pods at the same time
		if EnvVarValue(currentPod.Spec.Containers[humioContainerIdx].Env, "EXTERNAL_URL") != EnvVarValue(desiredPod.Spec.Containers[desiredHumioContainerIdx].Env, "EXTERNAL_URL") {
			r.Log.Info("EXTERNAL_URL changed so all pods must restart at the same time")
			podLifecycleStateValue.configurationDifference.requiresSimultaneousRestart = true
		}

		// if we run with envtest, we won't have zone information available
		// if there are pods with errors that we need to prioritize first, ignore zone awareness
		if !helpers.UseEnvtest() && !podsWithErrorsFoundSoBypassZoneAwareness {
			// if zone awareness is enabled, ignore pod if zone is incorrect
			if *hnp.GetUpdateStrategy().EnableZoneAwareness {
				if currentPod.Spec.NodeName == "" {
					// current pod does not have a nodeName set
					r.Log.Info(fmt.Sprintf("pod=%s does not have a nodeName set, ignoring", currentPod.Name))
					continue
				}

				// fetch zone for node name and ignore pod if zone is not the one that is marked as under maintenance
				zoneForNodeName, err := kubernetes.GetZoneForNodeName(ctx, r, currentPod.Spec.NodeName)
				if err != nil {
					return podLifecycleState{}, nil, r.logErrorAndReturn(err, "could get zone name for node")
				}
				if hnp.GetZoneUnderMaintenance() != "" && zoneForNodeName != hnp.GetZoneUnderMaintenance() {
					r.Log.Info(fmt.Sprintf("ignoring pod=%s as zoneUnderMaintenace=%s but pod has nodeName=%s where zone=%s", currentPod.Name, hnp.GetZoneUnderMaintenance(), currentPod.Spec.NodeName, zoneForNodeName))
					continue
				}
			}
		}

		// If we didn't decide to ignore the pod by this point, we append it to the list of pods to be replaced
		podLifecycleStateValue.podsToBeReplaced = append(podLifecycleStateValue.podsToBeReplaced, currentPod)

	}
	return *podLifecycleStateValue, desiredPod, nil
}

type podNameAndCertificateHash struct {
	podName, certificateHash string
}

func (r *HumioClusterReconciler) getDesiredBootstrapTokenHash(ctx context.Context, hc *humiov1alpha1.HumioCluster) (string, error) {
	humioBootstrapTokens, err := kubernetes.ListHumioBootstrapTokens(ctx, r.Client, hc.GetNamespace(), kubernetes.LabelsForHumioBootstrapToken(hc.GetName()))
	if err != nil {
		return "", err
	}

	if len(humioBootstrapTokens) == 0 {
		return "", fmt.Errorf("could not find bootstrap token matching labels %+v: %w", kubernetes.LabelsForHumioBootstrapToken(hc.GetName()), err)
	}

	if humioBootstrapTokens[0].Status.State != humiov1alpha1.HumioBootstrapTokenStateReady {
		return "", fmt.Errorf("bootstrap token not ready. status=%s", humioBootstrapTokens[0].Status.State)
	}

	existingSecret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: hc.GetNamespace(),
		Name:      humioBootstrapTokens[0].Status.HashedTokenSecretKeyRef.SecretKeyRef.Name,
	}, existingSecret); err != nil {
		return "", fmt.Errorf("failed to get bootstrap token secret %s: %w",
			humioBootstrapTokens[0].Status.HashedTokenSecretKeyRef.SecretKeyRef.Name, err)
	}

	if ok := string(existingSecret.Data[humioBootstrapTokens[0].Status.HashedTokenSecretKeyRef.SecretKeyRef.Key]); ok != "" {
		return helpers.AsSHA256(string(existingSecret.Data[humioBootstrapTokens[0].Status.HashedTokenSecretKeyRef.SecretKeyRef.Key])), nil
	}
	return "", fmt.Errorf("bootstrap token %s does not have a value for key %s", humioBootstrapTokens[0].Name, humioBootstrapTokens[0].Status.HashedTokenSecretKeyRef.SecretKeyRef.Key)
}

// findHumioNodeNameAndCertHash looks up the name of a free node certificate to use and the hash of the certificate specification
func findHumioNodeNameAndCertHash(ctx context.Context, c client.Client, hnp *HumioNodePool, newlyCreatedPods []corev1.Pod) (podNameAndCertificateHash, error) {
	// if we do not have TLS enabled, append a random suffix
	if !hnp.TLSEnabled() {
		return podNameAndCertificateHash{
			podName: fmt.Sprintf("%s-core-%s", hnp.GetNodePoolName(), kubernetes.RandomString()),
		}, nil
	}

	// if TLS is enabled, use the first available TLS certificate
	certificates, err := kubernetes.ListCertificates(ctx, c, hnp.GetNamespace(), hnp.GetNodePoolLabels())
	if err != nil {
		return podNameAndCertificateHash{}, err
	}
	for _, certificate := range certificates {
		for _, newPod := range newlyCreatedPods {
			if certificate.Name == newPod.Name {
				// ignore any certificates that matches names of pods we've just created
				continue
			}
		}

		if certificate.Spec.Keystores == nil {
			// ignore any certificates that does not hold a keystore bundle
			continue
		}
		if certificate.Spec.Keystores.JKS == nil {
			// ignore any certificates that does not hold a JKS keystore bundle
			continue
		}

		existingPod := &corev1.Pod{}
		err = c.Get(ctx, types.NamespacedName{
			Namespace: hnp.GetNamespace(),
			Name:      certificate.Name,
		}, existingPod)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				// reuse the certificate if we know we do not have a pod that uses it
				return podNameAndCertificateHash{
					podName:         certificate.Name,
					certificateHash: certificate.Annotations[certHashAnnotation],
				}, nil
			}
			return podNameAndCertificateHash{}, err
		}
	}

	return podNameAndCertificateHash{}, fmt.Errorf("found %d certificates but none of them are available to use", len(certificates))
}

func (r *HumioClusterReconciler) newPodAttachments(ctx context.Context, hnp *HumioNodePool, foundPodList []corev1.Pod, pvcClaimNamesInUse map[string]struct{}) (*podAttachments, error) {
	pvcList, err := r.pvcList(ctx, hnp)
	if err != nil {
		return &podAttachments{}, fmt.Errorf("problem getting pvc list: %w", err)
	}
	r.Log.Info(fmt.Sprintf("attempting to get volume source, pvc count is %d, pod count is %d", len(pvcList), len(foundPodList)))
	volumeSource, err := findAvailableVolumeSourceForPod(hnp, foundPodList, pvcList, pvcClaimNamesInUse)
	if err != nil {
		return &podAttachments{}, fmt.Errorf("unable to construct data volume source for HumioCluster: %w", err)
	}
	if volumeSource.PersistentVolumeClaim != nil {
		pvcClaimNamesInUse[volumeSource.PersistentVolumeClaim.ClaimName] = struct{}{}
	}

	envVarSourceData, err := r.getEnvVarSource(ctx, hnp)
	if err != nil {
		return &podAttachments{}, fmt.Errorf("unable to create Pod for HumioCluster: %w", err)
	}

	key := types.NamespacedName{
		Name:      hnp.GetClusterName(),
		Namespace: hnp.GetNamespace(),
	}
	hbt := &humiov1alpha1.HumioBootstrapToken{}
	err = r.Client.Get(ctx, key, hbt)
	if err != nil {
		return &podAttachments{}, fmt.Errorf("unable to create Pod for HumioCluster. could not find HumioBootstrapToken: %w", err)
	}

	if hbt.Status.HashedTokenSecretKeyRef.SecretKeyRef == nil {
		return &podAttachments{}, fmt.Errorf("unable to create Pod for HumioCluster: %w", fmt.Errorf("bootstraptoken %s does not contain a status for the hashed token secret reference", hnp.GetBootstrapTokenName()))
	}

	if hnp.InitContainerDisabled() {
		return &podAttachments{
			dataVolumeSource: volumeSource,
			envVarSourceData: envVarSourceData,
			bootstrapTokenSecretReference: bootstrapTokenSecret{
				secretReference: hbt.Status.HashedTokenSecretKeyRef.SecretKeyRef,
			},
		}, nil
	}

	initSASecretName, err := r.getInitServiceAccountSecretName(ctx, hnp)
	if err != nil {
		return &podAttachments{}, fmt.Errorf("unable get init service account secret for HumioCluster: %w", err)
	}
	if initSASecretName == "" {
		return &podAttachments{}, errors.New("unable to create Pod for HumioCluster: the init service account secret does not exist")
	}

	return &podAttachments{
		dataVolumeSource:             volumeSource,
		initServiceAccountSecretName: initSASecretName,
		envVarSourceData:             envVarSourceData,
		bootstrapTokenSecretReference: bootstrapTokenSecret{
			secretReference: hbt.Status.HashedTokenSecretKeyRef.SecretKeyRef,
		},
	}, nil
}

func (r *HumioClusterReconciler) getPodStatusList(ctx context.Context, hc *humiov1alpha1.HumioCluster, hnps []*HumioNodePool) (humiov1alpha1.HumioPodStatusList, error) {
	podStatusList := humiov1alpha1.HumioPodStatusList{}

	for _, pool := range hnps {
		pods, err := kubernetes.ListPods(ctx, r, pool.GetNamespace(), pool.GetNodePoolLabels())
		if err != nil {
			return podStatusList, r.logErrorAndReturn(err, "unable to get pod status")
		}

		for _, pod := range pods {
			nodeName := pod.Spec.NodeName

			// When using pvcs and an OnNodeDelete claim policy, we don't want to lose track of which node the PVC was
			// attached to.
			if pod.Status.Phase != corev1.PodRunning && pool.PVCsEnabled() && pool.GetDataVolumePersistentVolumeClaimPolicy().ReclaimType == humiov1alpha1.HumioPersistentVolumeReclaimTypeOnNodeDelete {
				for _, currentPodStatus := range hc.Status.PodStatus {
					if currentPodStatus.PodName == pod.Name && currentPodStatus.NodeName != "" {
						nodeName = currentPodStatus.NodeName
					}
				}
			}

			podStatus := humiov1alpha1.HumioPodStatus{
				PodName:  pod.Name,
				NodeName: nodeName,
			}
			if pool.PVCsEnabled() {
				for _, volume := range pod.Spec.Volumes {
					if volume.Name == "humio-data" {
						if volume.PersistentVolumeClaim != nil {
							podStatus.PvcName = volume.PersistentVolumeClaim.ClaimName
						} else {
							// This is not actually an error in every case. If the HumioCluster resource is migrating to
							// PVCs then this will happen in a rolling fashion thus some pods will not have PVCs for a
							// short time.
							r.Log.Info(fmt.Sprintf("unable to set pod pvc status for pod %s because there is no pvc attached to the pod", pod.Name))
						}
					}
				}
			}
			podStatusList = append(podStatusList, podStatus)
		}
	}
	sort.Sort(podStatusList)
	return podStatusList, nil
}

func findPodForPvc(podList []corev1.Pod, pvc corev1.PersistentVolumeClaim) (corev1.Pod, error) {
	for _, pod := range podList {
		if _, err := FindPvcForPod([]corev1.PersistentVolumeClaim{pvc}, pod); err != nil {
			return pod, nil
		}
	}

	return corev1.Pod{}, fmt.Errorf("could not find a pod for pvc %s", pvc.Name)
}
func FilterPodsByZoneName(ctx context.Context, c client.Client, podList []corev1.Pod, zoneName string) ([]corev1.Pod, error) {
	filteredPodList := []corev1.Pod{}
	for _, pod := range podList {
		zoneForNodeName, err := kubernetes.GetZoneForNodeName(ctx, c, pod.Spec.NodeName)
		if err != nil {
			return nil, err
		}
		if zoneForNodeName == zoneName {
			filteredPodList = append(filteredPodList, pod)
		}
	}
	return filteredPodList, nil
}

func FilterPodsExcludePodsWithPodRevisionOrPodHash(podList []corev1.Pod, podRevisionToExclude int, podHashToExclude string) []corev1.Pod {
	filteredPodList := []corev1.Pod{}
	for _, pod := range podList {
		podRevision, revisionFound := pod.Annotations[PodRevisionAnnotation]
		podHash, hashFound := pod.Annotations[PodHashAnnotation]
		if revisionFound && hashFound {
			if strconv.Itoa(podRevisionToExclude) == podRevision &&
				podHashToExclude == podHash {
				continue
			}
		}
		filteredPodList = append(filteredPodList, pod)
	}
	return filteredPodList
}

func FilterPodsExcludePodsWithEmptyNodeName(podList []corev1.Pod) []corev1.Pod {
	filteredPodList := []corev1.Pod{}
	for _, pod := range podList {
		if pod.Spec.NodeName == "" {
			continue
		}
		filteredPodList = append(filteredPodList, pod)
	}
	return filteredPodList
}
