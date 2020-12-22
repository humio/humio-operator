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

	"github.com/humio/humio-operator/pkg/helpers"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

const (
	image                        = "humio/humio-core:1.18.1"
	helperImage                  = "humio/humio-operator-helper:0.2.0"
	targetReplicationFactor      = 2
	storagePartitionsCount       = 24
	digestPartitionsCount        = 24
	nodeCount                    = 3
	humioPort                    = 8080
	elasticPort                  = 9200
	idpCertificateFilename       = "idp-certificate.pem"
	extraKafkaPropertiesFilename = "extra-kafka-properties.properties"
	viewGroupPermissionsFilename = "view-group-permissions.json"
	nodeUUIDPrefix               = "humio_"
	humioContainerName           = "humio"
	authContainerName            = "auth"
	initContainerName            = "init"

	// cluster-wide resources:
	initClusterRoleSuffix        = "init"
	initClusterRoleBindingSuffix = "init"

	// namespaced resources:
	humioServiceAccountNameSuffix           = "humio"
	initServiceAccountNameSuffix            = "init"
	initServiceAccountSecretNameIdentifier  = "init"
	authServiceAccountNameSuffix            = "auth"
	authServiceAccountSecretNameIdentifier  = "auth"
	authRoleSuffix                          = "auth"
	authRoleBindingSuffix                   = "auth"
	extraKafkaConfigsConfigMapNameSuffix    = "extra-kafka-configs"
	viewGroupPermissionsConfigMapNameSuffix = "view-group-permissions"
	idpCertificateSecretNameSuffix          = "idp-certificate"
)

func setDefaults(hc *humiov1alpha1.HumioCluster) {
	if hc.Spec.Image == "" {
		hc.Spec.Image = image
	}
	if hc.Spec.TargetReplicationFactor == 0 {
		hc.Spec.TargetReplicationFactor = targetReplicationFactor
	}
	if hc.Spec.StoragePartitionsCount == 0 {
		hc.Spec.StoragePartitionsCount = storagePartitionsCount
	}
	if hc.Spec.DigestPartitionsCount == 0 {
		hc.Spec.DigestPartitionsCount = digestPartitionsCount
	}

}

func helperImageOrDefault(hc *humiov1alpha1.HumioCluster) string {
	if hc.Spec.HelperImage == "" {
		return helperImage
	}
	return hc.Spec.HelperImage
}

func nodeCountOrDefault(hc *humiov1alpha1.HumioCluster) int {
	if hc.Spec.NodeCount == nil {
		return nodeCount
	}
	return *hc.Spec.NodeCount
}

func imagePullSecretsOrDefault(hc *humiov1alpha1.HumioCluster) []corev1.LocalObjectReference {
	emptyImagePullSecrets := []corev1.LocalObjectReference{}
	if reflect.DeepEqual(hc.Spec.ImagePullSecrets, emptyImagePullSecrets) {
		return emptyImagePullSecrets
	}
	return hc.Spec.ImagePullSecrets
}

func dataVolumePersistentVolumeClaimSpecTemplateOrDefault(hc *humiov1alpha1.HumioCluster, pvcName string) corev1.VolumeSource {
	if pvcsEnabled(hc) {
		return corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: pvcName,
			},
		}
	}
	return corev1.VolumeSource{}
}

func dataVolumeSourceOrDefault(hc *humiov1alpha1.HumioCluster) corev1.VolumeSource {
	emptyDataVolume := corev1.VolumeSource{}
	if reflect.DeepEqual(hc.Spec.DataVolumeSource, emptyDataVolume) {
		return corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		}
	}
	return hc.Spec.DataVolumeSource
}

func affinityOrDefault(hc *humiov1alpha1.HumioCluster) *corev1.Affinity {
	emptyAffinity := corev1.Affinity{}
	if reflect.DeepEqual(hc.Spec.Affinity, emptyAffinity) {
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
	return &hc.Spec.Affinity
}

func tolerationsOrDefault(hc *humiov1alpha1.HumioCluster) []corev1.Toleration {
	emptyTolerations := []corev1.Toleration{}
	if reflect.DeepEqual(hc.Spec.Tolerations, emptyTolerations) {
		return emptyTolerations
	}
	return hc.Spec.Tolerations
}

func shareProcessNamespaceOrDefault(hc *humiov1alpha1.HumioCluster) *bool {
	if hc.Spec.ShareProcessNamespace == nil {
		return helpers.BoolPtr(false)
	}
	return hc.Spec.ShareProcessNamespace
}

func humioServiceAccountAnnotationsOrDefault(hc *humiov1alpha1.HumioCluster) map[string]string {
	if hc.Spec.HumioServiceAccountAnnotations != nil {
		return hc.Spec.HumioServiceAccountAnnotations
	}
	return map[string]string(nil)
}

func humioServiceAccountNameOrDefault(hc *humiov1alpha1.HumioCluster) string {
	if hc.Spec.HumioServiceAccountName != "" {
		return hc.Spec.HumioServiceAccountName
	}
	return fmt.Sprintf("%s-%s", hc.Name, humioServiceAccountNameSuffix)
}

func initServiceAccountNameOrDefault(hc *humiov1alpha1.HumioCluster) string {
	if hc.Spec.InitServiceAccountName != "" {
		return hc.Spec.InitServiceAccountName
	}
	return fmt.Sprintf("%s-%s", hc.Name, initServiceAccountNameSuffix)
}

func initServiceAccountSecretName(hc *humiov1alpha1.HumioCluster) string {
	return fmt.Sprintf("%s-%s", hc.Name, initServiceAccountSecretNameIdentifier)
}

func authServiceAccountNameOrDefault(hc *humiov1alpha1.HumioCluster) string {
	if hc.Spec.AuthServiceAccountName != "" {
		return hc.Spec.AuthServiceAccountName
	}
	return fmt.Sprintf("%s-%s", hc.Name, authServiceAccountNameSuffix)
}

func authServiceAccountSecretName(hc *humiov1alpha1.HumioCluster) string {
	return fmt.Sprintf("%s-%s", hc.Name, authServiceAccountSecretNameIdentifier)
}

func extraKafkaConfigsOrDefault(hc *humiov1alpha1.HumioCluster) string {
	return hc.Spec.ExtraKafkaConfigs
}

func viewGroupPermissionsOrDefault(hc *humiov1alpha1.HumioCluster) string {
	return hc.Spec.ViewGroupPermissions
}

func extraKafkaConfigsConfigMapName(hc *humiov1alpha1.HumioCluster) string {
	return fmt.Sprintf("%s-%s", hc.Name, extraKafkaConfigsConfigMapNameSuffix)
}

func viewGroupPermissionsConfigMapName(hc *humiov1alpha1.HumioCluster) string {
	return fmt.Sprintf("%s-%s", hc.Name, viewGroupPermissionsConfigMapNameSuffix)
}

func idpCertificateSecretNameOrDefault(hc *humiov1alpha1.HumioCluster) string {
	if hc.Spec.IdpCertificateSecretName != "" {
		return hc.Spec.IdpCertificateSecretName
	}
	return fmt.Sprintf("%s-%s", hc.Name, idpCertificateSecretNameSuffix)
}

func initClusterRoleName(hc *humiov1alpha1.HumioCluster) string {
	return fmt.Sprintf("%s-%s-%s", hc.Namespace, hc.Name, initClusterRoleSuffix)
}

func initClusterRoleBindingName(hc *humiov1alpha1.HumioCluster) string {
	return fmt.Sprintf("%s-%s-%s", hc.Namespace, hc.Name, initClusterRoleBindingSuffix)
}

func authRoleName(hc *humiov1alpha1.HumioCluster) string {
	return fmt.Sprintf("%s-%s", hc.Name, authRoleSuffix)
}

func authRoleBindingName(hc *humiov1alpha1.HumioCluster) string {
	return fmt.Sprintf("%s-%s", hc.Name, authRoleBindingSuffix)
}

func podResourcesOrDefault(hc *humiov1alpha1.HumioCluster) corev1.ResourceRequirements {
	emptyResources := corev1.ResourceRequirements{}
	if reflect.DeepEqual(hc.Spec.Resources, emptyResources) {
		return emptyResources
	}
	return hc.Spec.Resources
}

func containerSecurityContextOrDefault(hc *humiov1alpha1.HumioCluster) *corev1.SecurityContext {
	if hc.Spec.ContainerSecurityContext == nil {
		return &corev1.SecurityContext{
			AllowPrivilegeEscalation: helpers.BoolPtr(false),
			Privileged:               helpers.BoolPtr(false),
			ReadOnlyRootFilesystem:   helpers.BoolPtr(true),
			RunAsUser:                helpers.Int64Ptr(65534),
			RunAsNonRoot:             helpers.BoolPtr(true),
			Capabilities: &corev1.Capabilities{
				Add: []corev1.Capability{
					"NET_BIND_SERVICE",
					"SYS_NICE",
				},
				Drop: []corev1.Capability{
					"ALL",
				},
			},
		}
	}
	return hc.Spec.ContainerSecurityContext
}

func podSecurityContextOrDefault(hc *humiov1alpha1.HumioCluster) *corev1.PodSecurityContext {
	if hc.Spec.PodSecurityContext == nil {
		return &corev1.PodSecurityContext{
			RunAsUser:    helpers.Int64Ptr(65534),
			RunAsNonRoot: helpers.BoolPtr(true),
			RunAsGroup:   helpers.Int64Ptr(0), // TODO: We probably want to move away from this.
			FSGroup:      helpers.Int64Ptr(0), // TODO: We probably want to move away from this.
		}
	}
	return hc.Spec.PodSecurityContext
}

func terminationGracePeriodSecondsOrDefault(hc *humiov1alpha1.HumioCluster) *int64 {
	if hc.Spec.TerminationGracePeriodSeconds == nil {
		return helpers.Int64Ptr(300)
	}
	return hc.Spec.TerminationGracePeriodSeconds
}

func setEnvironmentVariableDefaults(hc *humiov1alpha1.HumioCluster) {
	scheme := "https"
	if !helpers.TLSEnabled(hc) {
		scheme = "http"
	}

	envDefaults := []corev1.EnvVar{
		{
			Name: "THIS_POD_IP",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "status.podIP",
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
			Name: "POD_NAMESPACE",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.namespace",
				},
			},
		},

		{Name: "HUMIO_JVM_ARGS", Value: "-Xss2m -Xms256m -Xmx1536m -server -XX:+UseParallelOldGC -XX:+ScavengeBeforeFullGC -XX:+DisableExplicitGC"},
		{Name: "HUMIO_PORT", Value: strconv.Itoa(humioPort)},
		{Name: "ELASTIC_PORT", Value: strconv.Itoa(elasticPort)},
		{Name: "KAFKA_MANAGED_BY_HUMIO", Value: "true"},
		{Name: "AUTHENTICATION_METHOD", Value: "single-user"},
		{
			Name:  "EXTERNAL_URL", // URL used by other Humio hosts.
			Value: fmt.Sprintf("%s://$(POD_NAME).%s.$(POD_NAMESPACE):$(HUMIO_PORT)", strings.ToLower(scheme), hc.Name),
		},
	}

	if envVarHasValue(hc.Spec.EnvironmentVariables, "USING_EPHEMERAL_DISKS", "true") {
		envDefaults = append(envDefaults, corev1.EnvVar{
			Name:  "ZOOKEEPER_URL_FOR_NODE_UUID",
			Value: "$(ZOOKEEPER_URL)",
		})
	}

	humioVersion, _ := HumioVersionFromCluster(hc)
	if ok, _ := humioVersion.AtLeast(HumioVersionWhichContainsHumioLog4JEnvVar); ok {
		envDefaults = append(envDefaults, corev1.EnvVar{
			Name:  "HUMIO_LOG4J_CONFIGURATION",
			Value: "log4j2-stdout-json.xml",
		})
	} else {
		envDefaults = append(envDefaults, corev1.EnvVar{
			Name:  "LOG4J_CONFIGURATION",
			Value: "log4j2-stdout-json.xml",
		})
	}

	for _, defaultEnvVar := range envDefaults {
		appendEnvironmentVariableDefault(hc, defaultEnvVar)
	}

	// Allow overriding PUBLIC_URL. This may be useful when other methods of exposing the cluster are used other than
	// ingress
	if !envVarHasKey(envDefaults, "PUBLIC_URL") {
		// Only include the path suffix if it's non-root. It likely wouldn't harm anything, but it's unnecessary
		pathSuffix := ""
		if humioPathOrDefault(hc) != "/" {
			pathSuffix = humioPathOrDefault(hc)
		}
		if hc.Spec.Ingress.Enabled {
			appendEnvironmentVariableDefault(hc, corev1.EnvVar{
				Name:  "PUBLIC_URL", // URL used by users/browsers.
				Value: fmt.Sprintf("https://%s%s", hc.Spec.Hostname, pathSuffix),
			})
		} else {
			appendEnvironmentVariableDefault(hc, corev1.EnvVar{
				Name:  "PUBLIC_URL", // URL used by users/browsers.
				Value: fmt.Sprintf("%s://$(THIS_POD_IP):$(HUMIO_PORT)%s", scheme, pathSuffix),
			})
		}
	}

	if humioPathOrDefault(hc) != "/" {
		appendEnvironmentVariableDefault(hc, corev1.EnvVar{
			Name:  "PROXY_PREFIX_URL",
			Value: humioPathOrDefault(hc),
		})
	}
}

func appendEnvironmentVariableDefault(hc *humiov1alpha1.HumioCluster, defaultEnvVar corev1.EnvVar) {
	for _, envVar := range hc.Spec.EnvironmentVariables {
		if envVar.Name == defaultEnvVar.Name {
			return
		}
	}
	hc.Spec.EnvironmentVariables = append(hc.Spec.EnvironmentVariables, defaultEnvVar)
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

func extraHumioVolumeMountsOrDefault(hc *humiov1alpha1.HumioCluster) []corev1.VolumeMount {
	emptyVolumeMounts := []corev1.VolumeMount{}
	if reflect.DeepEqual(hc.Spec.ExtraHumioVolumeMounts, emptyVolumeMounts) {
		return emptyVolumeMounts
	}
	return hc.Spec.ExtraHumioVolumeMounts
}

func extraVolumesOrDefault(hc *humiov1alpha1.HumioCluster) []corev1.Volume {
	emptyVolumes := []corev1.Volume{}
	if reflect.DeepEqual(hc.Spec.ExtraVolumes, emptyVolumes) {
		return emptyVolumes
	}
	return hc.Spec.ExtraVolumes
}

func nodeUUIDPrefixOrDefault(hc *humiov1alpha1.HumioCluster) string {
	if hc.Spec.NodeUUIDPrefix != "" {
		return hc.Spec.NodeUUIDPrefix
	}
	return nodeUUIDPrefix
}

func sidecarContainersOrDefault(hc *humiov1alpha1.HumioCluster) []corev1.Container {
	emptySidecarContainers := []corev1.Container{}
	if reflect.DeepEqual(hc.Spec.SidecarContainers, emptySidecarContainers) {
		return emptySidecarContainers
	}
	return hc.Spec.SidecarContainers
}

func humioServiceTypeOrDefault(hc *humiov1alpha1.HumioCluster) corev1.ServiceType {
	if hc.Spec.HumioServiceType != "" {
		return hc.Spec.HumioServiceType
	}
	return corev1.ServiceTypeClusterIP
}

func humioServicePortOrDefault(hc *humiov1alpha1.HumioCluster) int32 {
	if hc.Spec.HumioServicePort != 0 {
		return hc.Spec.HumioServicePort
	}
	return humioPort

}

func humioESServicePortOrDefault(hc *humiov1alpha1.HumioCluster) int32 {
	if hc.Spec.HumioESServicePort != 0 {
		return hc.Spec.HumioESServicePort
	}
	return elasticPort
}

func humioServiceAnnotationsOrDefault(hc *humiov1alpha1.HumioCluster) map[string]string {
	if hc.Spec.HumioServiceAnnotations != nil {
		return hc.Spec.HumioServiceAnnotations
	}
	return map[string]string(nil)
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
