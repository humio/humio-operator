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
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/humio/humio-operator/pkg/kubernetes"

	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/humio/humio-operator/pkg/helpers"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

const (
	Image                        = "humio/humio-core:1.131.1"
	SidecarImage                 = "humio/humio-core:1.131.1"
	HelperImage                  = "humio/humio-operator-helper:8f5ef6c7e470226e77d985f36cf39be9a100afea"
	targetReplicationFactor      = 2
	digestPartitionsCount        = 24
	HumioPort                    = 8080
	elasticPort                  = 9200
	idpCertificateFilename       = "idp-certificate.pem"
	ExtraKafkaPropertiesFilename = "extra-kafka-properties.properties"
	ViewGroupPermissionsFilename = "view-group-permissions.json"
	RolePermissionsFilename      = "role-permissions.json"
	HumioContainerName           = "humio"
	AuthContainerName            = "humio-auth"
	InitContainerName            = "humio-init"

	// cluster-wide resources:
	initClusterRoleSuffix        = "init"
	initClusterRoleBindingSuffix = "init"

	// namespaced resources:
	HumioServiceAccountNameSuffix           = "humio"
	initServiceAccountNameSuffix            = "init"
	initServiceAccountSecretNameIdentifier  = "init"
	authServiceAccountNameSuffix            = "auth"
	authServiceAccountSecretNameIdentifier  = "auth"
	authRoleSuffix                          = "auth"
	authRoleBindingSuffix                   = "auth"
	extraKafkaConfigsConfigMapNameSuffix    = "extra-kafka-configs"
	viewGroupPermissionsConfigMapNameSuffix = "view-group-permissions"
	rolePermissionsConfigMapNameSuffix      = "role-permissions"
	idpCertificateSecretNameSuffix          = "idp-certificate"

	// nodepool internal
	NodePoolFeatureAllowedAPIRequestType = "OperatorInternal"
)

type HumioNodePool struct {
	clusterName              string
	nodePoolName             string
	namespace                string
	hostname                 string
	esHostname               string
	hostnameSource           humiov1alpha1.HumioHostnameSource
	esHostnameSource         humiov1alpha1.HumioESHostnameSource
	humioNodeSpec            humiov1alpha1.HumioNodeSpec
	tls                      *humiov1alpha1.HumioClusterTLSSpec
	idpCertificateSecretName string
	viewGroupPermissions     string // Deprecated: Replaced by rolePermissions
	rolePermissions          string
	targetReplicationFactor  int
	digestPartitionsCount    int
	path                     string
	ingress                  humiov1alpha1.HumioClusterIngressSpec
	clusterAnnotations       map[string]string
}

func NewHumioNodeManagerFromHumioCluster(hc *humiov1alpha1.HumioCluster) *HumioNodePool {
	return &HumioNodePool{
		namespace:        hc.Namespace,
		clusterName:      hc.Name,
		hostname:         hc.Spec.Hostname,
		esHostname:       hc.Spec.ESHostname,
		hostnameSource:   hc.Spec.HostnameSource,
		esHostnameSource: hc.Spec.ESHostnameSource,
		humioNodeSpec: humiov1alpha1.HumioNodeSpec{
			Image:     hc.Spec.Image,
			NodeCount: hc.Spec.NodeCount,
			DataVolumePersistentVolumeClaimSpecTemplate: hc.Spec.DataVolumePersistentVolumeClaimSpecTemplate,
			DataVolumePersistentVolumeClaimPolicy:       hc.Spec.DataVolumePersistentVolumeClaimPolicy,
			DataVolumeSource:                            hc.Spec.DataVolumeSource,
			AuthServiceAccountName:                      hc.Spec.AuthServiceAccountName,
			DisableInitContainer:                        hc.Spec.DisableInitContainer,
			EnvironmentVariablesSource:                  hc.Spec.EnvironmentVariablesSource,
			PodAnnotations:                              hc.Spec.PodAnnotations,
			ShareProcessNamespace:                       hc.Spec.ShareProcessNamespace,
			HumioServiceAccountName:                     hc.Spec.HumioServiceAccountName,
			ImagePullSecrets:                            hc.Spec.ImagePullSecrets,
			HelperImage:                                 hc.Spec.HelperImage,
			ImagePullPolicy:                             hc.Spec.ImagePullPolicy,
			ContainerSecurityContext:                    hc.Spec.ContainerSecurityContext,
			ContainerStartupProbe:                       hc.Spec.ContainerStartupProbe,
			ContainerLivenessProbe:                      hc.Spec.ContainerLivenessProbe,
			ContainerReadinessProbe:                     hc.Spec.ContainerReadinessProbe,
			PodSecurityContext:                          hc.Spec.PodSecurityContext,
			Resources:                                   hc.Spec.Resources,
			Tolerations:                                 hc.Spec.Tolerations,
			TopologySpreadConstraints:                   hc.Spec.TopologySpreadConstraints,
			TerminationGracePeriodSeconds:               hc.Spec.TerminationGracePeriodSeconds,
			Affinity:                                    hc.Spec.Affinity,
			SidecarContainers:                           hc.Spec.SidecarContainers,
			ExtraKafkaConfigs:                           hc.Spec.ExtraKafkaConfigs,
			ExtraHumioVolumeMounts:                      hc.Spec.ExtraHumioVolumeMounts,
			ExtraVolumes:                                hc.Spec.ExtraVolumes,
			HumioServiceAccountAnnotations:              hc.Spec.HumioServiceAccountAnnotations,
			HumioServiceLabels:                          hc.Spec.HumioServiceLabels,
			EnvironmentVariables:                        mergeEnvVars(hc.Spec.CommonEnvironmentVariables, hc.Spec.EnvironmentVariables),
			ImageSource:                                 hc.Spec.ImageSource,
			HumioESServicePort:                          hc.Spec.HumioESServicePort,
			HumioServicePort:                            hc.Spec.HumioServicePort,
			HumioServiceType:                            hc.Spec.HumioServiceType,
			HumioServiceAnnotations:                     hc.Spec.HumioServiceAnnotations,
			InitServiceAccountName:                      hc.Spec.InitServiceAccountName,
			PodLabels:                                   hc.Spec.PodLabels,
			UpdateStrategy:                              hc.Spec.UpdateStrategy,
			PriorityClassName:                           hc.Spec.PriorityClassName,
		},
		tls:                      hc.Spec.TLS,
		idpCertificateSecretName: hc.Spec.IdpCertificateSecretName,
		viewGroupPermissions:     hc.Spec.ViewGroupPermissions,
		rolePermissions:          hc.Spec.RolePermissions,
		targetReplicationFactor:  hc.Spec.TargetReplicationFactor,
		digestPartitionsCount:    hc.Spec.DigestPartitionsCount,
		path:                     hc.Spec.Path,
		ingress:                  hc.Spec.Ingress,
		clusterAnnotations:       hc.Annotations,
	}
}

func NewHumioNodeManagerFromHumioNodePool(hc *humiov1alpha1.HumioCluster, hnp *humiov1alpha1.HumioNodePoolSpec) *HumioNodePool {
	return &HumioNodePool{
		namespace:        hc.Namespace,
		clusterName:      hc.Name,
		nodePoolName:     hnp.Name,
		hostname:         hc.Spec.Hostname,
		esHostname:       hc.Spec.ESHostname,
		hostnameSource:   hc.Spec.HostnameSource,
		esHostnameSource: hc.Spec.ESHostnameSource,
		humioNodeSpec: humiov1alpha1.HumioNodeSpec{
			Image:     hnp.Image,
			NodeCount: hnp.NodeCount,
			DataVolumePersistentVolumeClaimSpecTemplate: hnp.DataVolumePersistentVolumeClaimSpecTemplate,
			DataVolumeSource:               hnp.DataVolumeSource,
			AuthServiceAccountName:         hnp.AuthServiceAccountName,
			DisableInitContainer:           hnp.DisableInitContainer,
			EnvironmentVariablesSource:     hnp.EnvironmentVariablesSource,
			PodAnnotations:                 hnp.PodAnnotations,
			ShareProcessNamespace:          hnp.ShareProcessNamespace,
			HumioServiceAccountName:        hnp.HumioServiceAccountName,
			ImagePullSecrets:               hnp.ImagePullSecrets,
			HelperImage:                    hnp.HelperImage,
			ImagePullPolicy:                hnp.ImagePullPolicy,
			ContainerSecurityContext:       hnp.ContainerSecurityContext,
			ContainerStartupProbe:          hnp.ContainerStartupProbe,
			ContainerLivenessProbe:         hnp.ContainerLivenessProbe,
			ContainerReadinessProbe:        hnp.ContainerReadinessProbe,
			PodSecurityContext:             hnp.PodSecurityContext,
			Resources:                      hnp.Resources,
			Tolerations:                    hnp.Tolerations,
			TopologySpreadConstraints:      hnp.TopologySpreadConstraints,
			TerminationGracePeriodSeconds:  hnp.TerminationGracePeriodSeconds,
			Affinity:                       hnp.Affinity,
			SidecarContainers:              hnp.SidecarContainers,
			ExtraKafkaConfigs:              hnp.ExtraKafkaConfigs,
			ExtraHumioVolumeMounts:         hnp.ExtraHumioVolumeMounts,
			ExtraVolumes:                   hnp.ExtraVolumes,
			HumioServiceAccountAnnotations: hnp.HumioServiceAccountAnnotations,
			HumioServiceLabels:             hnp.HumioServiceLabels,
			EnvironmentVariables:           mergeEnvVars(hc.Spec.CommonEnvironmentVariables, hnp.EnvironmentVariables),
			ImageSource:                    hnp.ImageSource,
			HumioESServicePort:             hnp.HumioESServicePort,
			HumioServicePort:               hnp.HumioServicePort,
			HumioServiceType:               hnp.HumioServiceType,
			HumioServiceAnnotations:        hnp.HumioServiceAnnotations,
			InitServiceAccountName:         hnp.InitServiceAccountName,
			PodLabels:                      hnp.PodLabels,
			UpdateStrategy:                 hnp.UpdateStrategy,
			PriorityClassName:              hnp.PriorityClassName,
		},
		tls:                      hc.Spec.TLS,
		idpCertificateSecretName: hc.Spec.IdpCertificateSecretName,
		viewGroupPermissions:     hc.Spec.ViewGroupPermissions,
		rolePermissions:          hc.Spec.RolePermissions,
		targetReplicationFactor:  hc.Spec.TargetReplicationFactor,
		digestPartitionsCount:    hc.Spec.DigestPartitionsCount,
		path:                     hc.Spec.Path,
		ingress:                  hc.Spec.Ingress,
		clusterAnnotations:       hc.Annotations,
	}
}

func (hnp *HumioNodePool) GetClusterName() string {
	return hnp.clusterName
}

func (hnp *HumioNodePool) GetNodePoolName() string {
	if hnp.nodePoolName == "" {
		return hnp.GetClusterName()
	}
	return strings.Join([]string{hnp.GetClusterName(), hnp.nodePoolName}, "-")
}

func (hnp *HumioNodePool) GetNamespace() string {
	return hnp.namespace
}

func (hnp *HumioNodePool) GetHostname() string {
	return hnp.hostname
}

func (hnp *HumioNodePool) SetImage(image string) {
	hnp.humioNodeSpec.Image = image
}

func (hnp *HumioNodePool) GetImage() string {
	if hnp.humioNodeSpec.Image != "" {
		return hnp.humioNodeSpec.Image
	}
	return Image
}

func (hnp *HumioNodePool) GetImageSource() *humiov1alpha1.HumioImageSource {
	return hnp.humioNodeSpec.ImageSource
}

func (hnp *HumioNodePool) GetHelperImage() string {
	if hnp.humioNodeSpec.HelperImage != "" {
		return hnp.humioNodeSpec.HelperImage
	}
	return HelperImage
}

func (hnp *HumioNodePool) GetImagePullSecrets() []corev1.LocalObjectReference {
	return hnp.humioNodeSpec.ImagePullSecrets
}

func (hnp *HumioNodePool) GetImagePullPolicy() corev1.PullPolicy {
	return hnp.humioNodeSpec.ImagePullPolicy
}

func (hnp *HumioNodePool) GetSidecarImage() string {
	if hnp.humioNodeSpec.SidecarContainers[0].Image == "" {
		return hnp.GetImage()
	}
	return hnp.humioNodeSpec.SidecarContainers[0].Image
}

func (hnp *HumioNodePool) GetEnvironmentVariablesSource() []corev1.EnvFromSource {
	return hnp.humioNodeSpec.EnvironmentVariablesSource
}

func (hnp *HumioNodePool) GetTargetReplicationFactor() int {
	if hnp.targetReplicationFactor != 0 {
		return hnp.targetReplicationFactor
	}
	return targetReplicationFactor
}

func (hnp *HumioNodePool) GetDigestPartitionsCount() int {
	if hnp.digestPartitionsCount != 0 {
		return hnp.digestPartitionsCount
	}
	return digestPartitionsCount
}

func (hnp *HumioNodePool) SetHumioClusterNodePoolRevisionAnnotation(newRevision int) {
	if hnp.clusterAnnotations == nil {
		hnp.clusterAnnotations = map[string]string{}
	}
	revisionKey, _ := hnp.GetHumioClusterNodePoolRevisionAnnotation()
	hnp.clusterAnnotations[revisionKey] = strconv.Itoa(newRevision)
}

func (hnp *HumioNodePool) GetHumioClusterNodePoolRevisionAnnotation() (string, int) {
	annotations := map[string]string{}
	if len(hnp.clusterAnnotations) > 0 {
		annotations = hnp.clusterAnnotations
	}
	podAnnotationKey := strings.Join([]string{PodRevisionAnnotation, hnp.GetNodePoolName()}, "-")
	revision, ok := annotations[podAnnotationKey]
	if !ok {
		revision = "0"
	}
	existingRevision, err := strconv.Atoi(revision)
	if err != nil {
		return "", -1
	}
	return podAnnotationKey, existingRevision
}

func (hnp *HumioNodePool) GetIngress() humiov1alpha1.HumioClusterIngressSpec {
	return hnp.ingress
}

func (hnp *HumioNodePool) GetEnvironmentVariables() []corev1.EnvVar {
	envVars := make([]corev1.EnvVar, len(hnp.humioNodeSpec.EnvironmentVariables))
	copy(envVars, hnp.humioNodeSpec.EnvironmentVariables)

	scheme := "https"
	if !hnp.TLSEnabled() {
		scheme = "http"
	}

	envDefaults := []corev1.EnvVar{
		{
			Name: "THIS_POD_IP",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					APIVersion: "v1",
					FieldPath:  "status.podIP",
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
			Name: "POD_NAMESPACE",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					APIVersion: "v1",
					FieldPath:  "metadata.namespace",
				},
			},
		},

		{Name: "HUMIO_PORT", Value: strconv.Itoa(HumioPort)},
		{Name: "ELASTIC_PORT", Value: strconv.Itoa(elasticPort)},
		{Name: "DEFAULT_DIGEST_REPLICATION_FACTOR", Value: strconv.Itoa(hnp.GetTargetReplicationFactor())},
		{Name: "DEFAULT_SEGMENT_REPLICATION_FACTOR", Value: strconv.Itoa(hnp.GetTargetReplicationFactor())},
		{Name: "INGEST_QUEUE_INITIAL_PARTITIONS", Value: strconv.Itoa(hnp.GetDigestPartitionsCount())},
		{Name: "HUMIO_LOG4J_CONFIGURATION", Value: "log4j2-json-stdout.xml"},
		{
			Name:  "EXTERNAL_URL", // URL used by other Humio hosts.
			Value: fmt.Sprintf("%s://$(POD_NAME).%s.$(POD_NAMESPACE):$(HUMIO_PORT)", strings.ToLower(scheme), headlessServiceName(hnp.GetClusterName())),
		},
		{
			Name:  "HUMIO_JVM_LOG_OPTS",
			Value: "-Xlog:gc+jni=debug:stdout -Xlog:gc*:stdout:time,tags",
		},
		{
			Name:  "HUMIO_OPTS",
			Value: "-Dakka.log-config-on-start=on -Dlog4j2.formatMsgNoLookups=true",
		},
	}

	for _, defaultEnvVar := range envDefaults {
		envVars = AppendEnvVarToEnvVarsIfNotAlreadyPresent(envVars, defaultEnvVar)
	}

	// Allow overriding PUBLIC_URL. This may be useful when other methods of exposing the cluster are used other than
	// ingress
	if !EnvVarHasKey(envDefaults, "PUBLIC_URL") {
		// Only include the path suffix if it's non-root. It likely wouldn't harm anything, but it's unnecessary
		pathSuffix := ""
		if hnp.GetPath() != "/" {
			pathSuffix = hnp.GetPath()
		}
		if hnp.GetIngress().Enabled {
			envVars = AppendEnvVarToEnvVarsIfNotAlreadyPresent(envVars, corev1.EnvVar{
				Name:  "PUBLIC_URL", // URL used by users/browsers.
				Value: fmt.Sprintf("https://%s%s", hnp.GetHostname(), pathSuffix),
			})
		} else {
			envVars = AppendEnvVarToEnvVarsIfNotAlreadyPresent(envVars, corev1.EnvVar{
				Name:  "PUBLIC_URL", // URL used by users/browsers.
				Value: fmt.Sprintf("%s://$(THIS_POD_IP):$(HUMIO_PORT)%s", scheme, pathSuffix),
			})
		}
	}

	if hnp.GetPath() != "/" {
		envVars = AppendEnvVarToEnvVarsIfNotAlreadyPresent(envVars, corev1.EnvVar{
			Name:  "PROXY_PREFIX_URL",
			Value: hnp.GetPath(),
		})
	}

	return envVars
}

func (hnp *HumioNodePool) GetContainerSecurityContext() *corev1.SecurityContext {
	if hnp.humioNodeSpec.ContainerSecurityContext == nil {
		return &corev1.SecurityContext{
			AllowPrivilegeEscalation: helpers.BoolPtr(false),
			Privileged:               helpers.BoolPtr(false),
			ReadOnlyRootFilesystem:   helpers.BoolPtr(true),
			RunAsUser:                helpers.Int64Ptr(65534),
			RunAsNonRoot:             helpers.BoolPtr(true),
			Capabilities: &corev1.Capabilities{
				Add: []corev1.Capability{
					"SYS_NICE",
				},
				Drop: []corev1.Capability{
					"ALL",
				},
			},
		}
	}
	return hnp.humioNodeSpec.ContainerSecurityContext
}

func (hnp *HumioNodePool) GetNodePoolLabels() map[string]string {
	labels := hnp.GetCommonClusterLabels()
	labels[kubernetes.NodePoolLabelName] = hnp.GetNodePoolName()
	return labels
}

func (hnp *HumioNodePool) GetPodLabels() map[string]string {
	labels := hnp.GetNodePoolLabels()
	for k, v := range hnp.humioNodeSpec.PodLabels {
		if _, ok := labels[k]; !ok {
			labels[k] = v
		}
	}
	for _, feature := range hnp.GetNodePoolFeatureAllowedAPIRequestTypes() {
		if feature == NodePoolFeatureAllowedAPIRequestType {
			labels[kubernetes.FeatureLabelName] = NodePoolFeatureAllowedAPIRequestType
		}
	}
	return labels
}

func (hnp *HumioNodePool) GetCommonClusterLabels() map[string]string {
	return kubernetes.LabelsForHumio(hnp.clusterName)
}

func (hnp *HumioNodePool) GetCASecretName() string {
	if hnp.tls != nil && hnp.tls.CASecretName != "" {
		return hnp.tls.CASecretName
	}
	return fmt.Sprintf("%s-ca-keypair", hnp.GetClusterName())
}

func (hnp *HumioNodePool) UseExistingCA() bool {
	return hnp.tls != nil && hnp.tls.CASecretName != ""
}

func (hnp *HumioNodePool) GetLabelsForSecret(secretName string) map[string]string {
	labels := hnp.GetCommonClusterLabels()
	labels[kubernetes.SecretNameLabelName] = secretName
	return labels
}

func (hnp *HumioNodePool) GetNodeCount() int {
	return hnp.humioNodeSpec.NodeCount
}

func (hnp *HumioNodePool) GetDataVolumePersistentVolumeClaimSpecTemplate(pvcName string) corev1.VolumeSource {
	if hnp.PVCsEnabled() {
		return corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: pvcName,
			},
		}
	}
	return corev1.VolumeSource{}
}

func (hnp *HumioNodePool) GetDataVolumePersistentVolumeClaimSpecTemplateRAW() corev1.PersistentVolumeClaimSpec {
	return hnp.humioNodeSpec.DataVolumePersistentVolumeClaimSpecTemplate
}

func (hnp *HumioNodePool) DataVolumePersistentVolumeClaimSpecTemplateIsSetByUser() bool {
	return !reflect.DeepEqual(hnp.humioNodeSpec.DataVolumePersistentVolumeClaimSpecTemplate, corev1.PersistentVolumeClaimSpec{})
}

func (hnp *HumioNodePool) GetDataVolumePersistentVolumeClaimPolicy() humiov1alpha1.HumioPersistentVolumeClaimPolicy {
	if hnp.PVCsEnabled() {
		return hnp.humioNodeSpec.DataVolumePersistentVolumeClaimPolicy
	}
	return humiov1alpha1.HumioPersistentVolumeClaimPolicy{}
}

func (hnp *HumioNodePool) GetDataVolumeSource() corev1.VolumeSource {
	return hnp.humioNodeSpec.DataVolumeSource
}

func (hnp *HumioNodePool) GetPodAnnotations() map[string]string {
	return hnp.humioNodeSpec.PodAnnotations
}

func (hnp *HumioNodePool) GetAuthServiceAccountSecretName() string {
	return fmt.Sprintf("%s-%s", hnp.GetNodePoolName(), authServiceAccountSecretNameIdentifier)
}

func (hnp *HumioNodePool) GetInitServiceAccountSecretName() string {
	return fmt.Sprintf("%s-%s", hnp.GetNodePoolName(), initServiceAccountSecretNameIdentifier)
}

func (hnp *HumioNodePool) GetInitServiceAccountName() string {
	if hnp.humioNodeSpec.InitServiceAccountName != "" {
		return hnp.humioNodeSpec.InitServiceAccountName
	}
	return fmt.Sprintf("%s-%s", hnp.GetNodePoolName(), initServiceAccountNameSuffix)
}

func (hnp *HumioNodePool) InitServiceAccountIsSetByUser() bool {
	return hnp.humioNodeSpec.InitServiceAccountName != ""
}

func (hnp *HumioNodePool) GetAuthServiceAccountName() string {
	if hnp.humioNodeSpec.AuthServiceAccountName != "" {
		return hnp.humioNodeSpec.AuthServiceAccountName
	}
	return fmt.Sprintf("%s-%s", hnp.GetNodePoolName(), authServiceAccountNameSuffix)
}

func (hnp *HumioNodePool) AuthServiceAccountIsSetByUser() bool {
	return hnp.humioNodeSpec.AuthServiceAccountName != ""
}

func (hnp *HumioNodePool) GetInitClusterRoleName() string {
	return fmt.Sprintf("%s-%s-%s", hnp.GetNamespace(), hnp.GetNodePoolName(), initClusterRoleSuffix)
}

func (hnp *HumioNodePool) GetInitClusterRoleBindingName() string {
	return fmt.Sprintf("%s-%s-%s", hnp.GetNamespace(), hnp.GetNodePoolName(), initClusterRoleBindingSuffix)
}

func (hnp *HumioNodePool) GetAuthRoleName() string {
	return fmt.Sprintf("%s-%s", hnp.GetNodePoolName(), authRoleSuffix)
}

func (hnp *HumioNodePool) GetAuthRoleBindingName() string {
	return fmt.Sprintf("%s-%s", hnp.GetNodePoolName(), authRoleBindingSuffix)
}

func (hnp *HumioNodePool) GetShareProcessNamespace() *bool {
	if hnp.humioNodeSpec.ShareProcessNamespace == nil {
		return helpers.BoolPtr(false)
	}
	return hnp.humioNodeSpec.ShareProcessNamespace
}

func (hnp *HumioNodePool) HumioServiceAccountIsSetByUser() bool {
	return hnp.humioNodeSpec.HumioServiceAccountName != ""
}

func (hnp *HumioNodePool) GetHumioServiceAccountName() string {
	if hnp.humioNodeSpec.HumioServiceAccountName != "" {
		return hnp.humioNodeSpec.HumioServiceAccountName
	}
	return fmt.Sprintf("%s-%s", hnp.GetNodePoolName(), HumioServiceAccountNameSuffix)
}

func (hnp *HumioNodePool) GetHumioServiceAccountAnnotations() map[string]string {
	return hnp.humioNodeSpec.HumioServiceAccountAnnotations
}

func (hnp *HumioNodePool) GetContainerReadinessProbe() *corev1.Probe {
	if hnp.humioNodeSpec.ContainerReadinessProbe != nil && (*hnp.humioNodeSpec.ContainerReadinessProbe == (corev1.Probe{})) {
		return nil
	}

	if hnp.humioNodeSpec.ContainerReadinessProbe == nil {
		return &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   "/api/v1/is-node-up",
					Port:   intstr.IntOrString{IntVal: HumioPort},
					Scheme: hnp.GetProbeScheme(),
				},
			},
			InitialDelaySeconds: 30,
			PeriodSeconds:       5,
			TimeoutSeconds:      5,
			SuccessThreshold:    1,
			FailureThreshold:    10,
		}
	}
	return hnp.humioNodeSpec.ContainerReadinessProbe
}

func (hnp *HumioNodePool) GetContainerLivenessProbe() *corev1.Probe {
	if hnp.humioNodeSpec.ContainerLivenessProbe != nil && (*hnp.humioNodeSpec.ContainerLivenessProbe == (corev1.Probe{})) {
		return nil
	}

	if hnp.humioNodeSpec.ContainerLivenessProbe == nil {
		return &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   "/api/v1/is-node-up",
					Port:   intstr.IntOrString{IntVal: HumioPort},
					Scheme: hnp.GetProbeScheme(),
				},
			},
			InitialDelaySeconds: 30,
			PeriodSeconds:       5,
			TimeoutSeconds:      5,
			SuccessThreshold:    1,
			FailureThreshold:    80,
		}
	}
	return hnp.humioNodeSpec.ContainerLivenessProbe
}

func (hnp *HumioNodePool) GetContainerStartupProbe() *corev1.Probe {
	if hnp.humioNodeSpec.ContainerStartupProbe != nil && (*hnp.humioNodeSpec.ContainerStartupProbe == (corev1.Probe{})) {
		return nil
	}

	if hnp.humioNodeSpec.ContainerStartupProbe == nil {
		return &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   "/api/v1/is-node-up",
					Port:   intstr.IntOrString{IntVal: HumioPort},
					Scheme: hnp.GetProbeScheme(),
				},
			},
			PeriodSeconds:    5,
			TimeoutSeconds:   5,
			SuccessThreshold: 1,
			FailureThreshold: 120,
		}
	}
	return hnp.humioNodeSpec.ContainerStartupProbe
}

func (hnp *HumioNodePool) GetPodSecurityContext() *corev1.PodSecurityContext {
	if hnp.humioNodeSpec.PodSecurityContext == nil {
		return &corev1.PodSecurityContext{
			RunAsUser:    helpers.Int64Ptr(65534),
			RunAsNonRoot: helpers.BoolPtr(true),
			RunAsGroup:   helpers.Int64Ptr(0), // TODO: We probably want to move away from this.
			FSGroup:      helpers.Int64Ptr(0), // TODO: We probably want to move away from this.
		}
	}
	return hnp.humioNodeSpec.PodSecurityContext
}

func (hnp *HumioNodePool) GetAffinity() *corev1.Affinity {
	if hnp.humioNodeSpec.Affinity == (corev1.Affinity{}) {
		return &corev1.Affinity{
			NodeAffinity: &corev1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      corev1.LabelArchStable,
									Operator: corev1.NodeSelectorOpIn,
									Values: []string{
										"amd64",
									},
								},
								{
									Key:      corev1.LabelOSStable,
									Operator: corev1.NodeSelectorOpIn,
									Values: []string{
										"linux",
									},
								},
							},
						},
					},
				},
			},
		}
	}
	return &hnp.humioNodeSpec.Affinity
}

func (hnp *HumioNodePool) GetSidecarContainers() []corev1.Container {
	return hnp.humioNodeSpec.SidecarContainers
}

func (hnp *HumioNodePool) GetTolerations() []corev1.Toleration {
	return hnp.humioNodeSpec.Tolerations
}

func (hnp *HumioNodePool) GetTopologySpreadConstraints() []corev1.TopologySpreadConstraint {
	return hnp.humioNodeSpec.TopologySpreadConstraints
}

func (hnp *HumioNodePool) GetResources() corev1.ResourceRequirements {
	return hnp.humioNodeSpec.Resources
}

func (hnp *HumioNodePool) GetExtraKafkaConfigs() string {
	return hnp.humioNodeSpec.ExtraKafkaConfigs
}

func (hnp *HumioNodePool) GetExtraKafkaConfigsConfigMapName() string {
	return fmt.Sprintf("%s-%s", hnp.GetNodePoolName(), extraKafkaConfigsConfigMapNameSuffix)
}

func (hnp *HumioNodePool) GetViewGroupPermissions() string {
	return hnp.viewGroupPermissions
}

func (hnp *HumioNodePool) GetViewGroupPermissionsConfigMapName() string {
	return fmt.Sprintf("%s-%s", hnp.GetClusterName(), viewGroupPermissionsConfigMapNameSuffix)
}

func (hnp *HumioNodePool) GetRolePermissions() string {
	return hnp.rolePermissions
}

func (hnp *HumioNodePool) GetRolePermissionsConfigMapName() string {
	return fmt.Sprintf("%s-%s", hnp.GetClusterName(), rolePermissionsConfigMapNameSuffix)
}

func (hnp *HumioNodePool) GetPath() string {
	if hnp.path != "" {
		if strings.HasPrefix(hnp.path, "/") {
			return hnp.path
		} else {
			return fmt.Sprintf("/%s", hnp.path)
		}
	}
	return "/"
}

func (hnp *HumioNodePool) GetHumioServiceLabels() map[string]string {
	return hnp.humioNodeSpec.HumioServiceLabels
}

func (hnp *HumioNodePool) GetTerminationGracePeriodSeconds() *int64 {
	if hnp.humioNodeSpec.TerminationGracePeriodSeconds == nil {
		return helpers.Int64Ptr(300)
	}
	return hnp.humioNodeSpec.TerminationGracePeriodSeconds
}

func (hnp *HumioNodePool) GetIDPCertificateSecretName() string {
	if hnp.idpCertificateSecretName != "" {
		return hnp.idpCertificateSecretName
	}
	return fmt.Sprintf("%s-%s", hnp.GetClusterName(), idpCertificateSecretNameSuffix)
}

func (hnp *HumioNodePool) GetExtraHumioVolumeMounts() []corev1.VolumeMount {
	return hnp.humioNodeSpec.ExtraHumioVolumeMounts
}

func (hnp *HumioNodePool) GetExtraVolumes() []corev1.Volume {
	return hnp.humioNodeSpec.ExtraVolumes
}

func (hnp *HumioNodePool) GetHumioServiceAnnotations() map[string]string {
	return hnp.humioNodeSpec.HumioServiceAnnotations
}

func (hnp *HumioNodePool) GetHumioServicePort() int32 {
	if hnp.humioNodeSpec.HumioServicePort != 0 {
		return hnp.humioNodeSpec.HumioServicePort
	}
	return HumioPort
}

func (hnp *HumioNodePool) GetHumioESServicePort() int32 {
	if hnp.humioNodeSpec.HumioESServicePort != 0 {
		return hnp.humioNodeSpec.HumioESServicePort
	}
	return elasticPort
}

func (hnp *HumioNodePool) GetServiceType() corev1.ServiceType {
	if hnp.humioNodeSpec.HumioServiceType != "" {
		return hnp.humioNodeSpec.HumioServiceType
	}
	return corev1.ServiceTypeClusterIP
}

func (hnp *HumioNodePool) GetServiceName() string {
	if hnp.nodePoolName == "" {
		return hnp.clusterName
	}
	return fmt.Sprintf("%s-%s", hnp.clusterName, hnp.nodePoolName)
}

func (hnp *HumioNodePool) InitContainerDisabled() bool {
	return hnp.humioNodeSpec.DisableInitContainer
}

func (hnp *HumioNodePool) PVCsEnabled() bool {
	emptyPersistentVolumeClaimSpec := corev1.PersistentVolumeClaimSpec{}
	return !reflect.DeepEqual(hnp.humioNodeSpec.DataVolumePersistentVolumeClaimSpecTemplate, emptyPersistentVolumeClaimSpec)

}

func (hnp *HumioNodePool) TLSEnabled() bool {
	if hnp.tls == nil {
		return helpers.UseCertManager()
	}
	if hnp.tls.Enabled == nil {
		return helpers.UseCertManager()
	}

	return helpers.UseCertManager() && *hnp.tls.Enabled
}

func (hnp *HumioNodePool) GetTLSSpec() *humiov1alpha1.HumioClusterTLSSpec {
	return hnp.tls
}

func (hnp *HumioNodePool) GetProbeScheme() corev1.URIScheme {
	if !hnp.TLSEnabled() {
		return corev1.URISchemeHTTP
	}

	return corev1.URISchemeHTTPS
}

func (hnp *HumioNodePool) GetUpdateStrategy() *humiov1alpha1.HumioUpdateStrategy {
	if hnp.humioNodeSpec.UpdateStrategy != nil {
		return hnp.humioNodeSpec.UpdateStrategy
	}

	return &humiov1alpha1.HumioUpdateStrategy{
		Type:            humiov1alpha1.HumioClusterUpdateStrategyReplaceAllOnUpdate,
		MinReadySeconds: 0,
	}
}

func (hnp *HumioNodePool) GetPriorityClassName() string {
	return hnp.humioNodeSpec.PriorityClassName
}

func (hnp *HumioNodePool) OkToDeletePvc() bool {
	return hnp.GetDataVolumePersistentVolumeClaimPolicy().ReclaimType == humiov1alpha1.HumioPersistentVolumeReclaimTypeOnNodeDelete
}

func (hnp *HumioNodePool) GetNodePoolFeatureAllowedAPIRequestTypes() []string {
	if hnp.humioNodeSpec.NodePoolFeatures.AllowedAPIRequestTypes != nil {
		return *hnp.humioNodeSpec.NodePoolFeatures.AllowedAPIRequestTypes
	}
	return []string{NodePoolFeatureAllowedAPIRequestType}
}

func viewGroupPermissionsOrDefault(hc *humiov1alpha1.HumioCluster) string {
	return hc.Spec.ViewGroupPermissions
}

func ViewGroupPermissionsConfigMapName(hc *humiov1alpha1.HumioCluster) string {
	return fmt.Sprintf("%s-%s", hc.Name, viewGroupPermissionsConfigMapNameSuffix)
}

func rolePermissionsOrDefault(hc *humiov1alpha1.HumioCluster) string {
	return hc.Spec.RolePermissions
}

func RolePermissionsConfigMapName(hc *humiov1alpha1.HumioCluster) string {
	return fmt.Sprintf("%s-%s", hc.Name, rolePermissionsConfigMapNameSuffix)
}

func AppendEnvVarToEnvVarsIfNotAlreadyPresent(envVars []corev1.EnvVar, defaultEnvVar corev1.EnvVar) []corev1.EnvVar {
	for _, envVar := range envVars {
		if envVar.Name == defaultEnvVar.Name {
			return envVars
		}
	}
	return append(envVars, defaultEnvVar)
}

func certificateSecretNameOrDefault(hc *humiov1alpha1.HumioCluster) string {
	if hc.Spec.Ingress.SecretName != "" {
		return hc.Spec.Ingress.SecretName
	}
	return fmt.Sprintf("%s-certificate", hc.Name)
}

func esCertificateSecretNameOrDefault(hc *humiov1alpha1.HumioCluster) string {
	if hc.Spec.Ingress.ESSecretName != "" {
		return hc.Spec.Ingress.ESSecretName
	}
	return fmt.Sprintf("%s-es-certificate", hc.Name)
}

func ingressTLSOrDefault(hc *humiov1alpha1.HumioCluster) bool {
	if hc.Spec.Ingress.TLS == nil {
		return true
	}
	return *hc.Spec.Ingress.TLS
}

func humioHeadlessServiceAnnotationsOrDefault(hc *humiov1alpha1.HumioCluster) map[string]string {
	return hc.Spec.HumioHeadlessServiceAnnotations
}

func humioPathOrDefault(hc *humiov1alpha1.HumioCluster) string {
	if hc.Spec.Path != "" {
		if strings.HasPrefix(hc.Spec.Path, "/") {
			return hc.Spec.Path
		} else {
			return fmt.Sprintf("/%s", hc.Spec.Path)
		}
	}
	return "/"
}

func licenseSecretKeyRefOrDefault(hc *humiov1alpha1.HumioCluster) *corev1.SecretKeySelector {
	return hc.Spec.License.SecretKeyRef
}

type HumioNodePoolList struct {
	Items []*HumioNodePool
}

func (n *HumioNodePoolList) Filter(f func(*HumioNodePool) bool) []*HumioNodePool {
	var filteredNodePools []*HumioNodePool
	for _, nodePool := range n.Items {
		if f(nodePool) {
			filteredNodePools = append(filteredNodePools, nodePool)
		}
	}
	return filteredNodePools
}

func (n *HumioNodePoolList) Add(hnp *HumioNodePool) {
	n.Items = append(n.Items, hnp)
}

func NodePoolFilterHasNode(nodePool *HumioNodePool) bool {
	return nodePool.GetNodeCount() > 0
}

func NodePoolFilterDoesNotHaveNodes(nodePool *HumioNodePool) bool {
	return !NodePoolFilterHasNode(nodePool)
}
