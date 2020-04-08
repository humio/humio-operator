package humiocluster

import (
	"fmt"
	"reflect"
	"strconv"

	humioClusterv1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	"github.com/humio/humio-operator/pkg/kubernetes"
	corev1 "k8s.io/api/core/v1"
)

const (
	name                           = "humiocluster"
	namespace                      = "logging"
	image                          = "humio/humio-core:1.9.1"
	targetReplicationFactor        = 2
	storagePartitionsCount         = 24
	digestPartitionsCount          = 24
	nodeCount                      = 3
	humioPort                      = 8080
	elasticPort                    = 9200
	initServiceAccountName         = "init-service-account"
	initServiceAccountSecretName   = "init-service-account"
	initClusterRolePrefix          = "init-cluster-role"
	initClusterRoleBindingPrefix   = "init-cluster-role-binding"
	extraKafkaConfigsConfigmapName = "extra-kafka-configs-configmap"
	idpCertificateSecretName       = "idp-certificate-secret"
	idpCertificateFilename         = "idp-certificate.pem"
	extraKafkaPropertiesFilename   = "extra-kafka-properties.properties"
)

func setDefaults(humioCluster *humioClusterv1alpha1.HumioCluster) {
	if humioCluster.ObjectMeta.Name == "" {
		humioCluster.ObjectMeta.Name = name
	}
	if humioCluster.ObjectMeta.Namespace == "" {
		humioCluster.ObjectMeta.Namespace = namespace
	}
	if humioCluster.Spec.Image == "" {
		humioCluster.Spec.Image = image
	}
	if humioCluster.Spec.TargetReplicationFactor == 0 {
		humioCluster.Spec.TargetReplicationFactor = targetReplicationFactor
	}
	if humioCluster.Spec.StoragePartitionsCount == 0 {
		humioCluster.Spec.StoragePartitionsCount = storagePartitionsCount
	}
	if humioCluster.Spec.DigestPartitionsCount == 0 {
		humioCluster.Spec.DigestPartitionsCount = digestPartitionsCount
	}
	if humioCluster.Spec.NodeCount == 0 {
		humioCluster.Spec.NodeCount = nodeCount
	}
}

func imagePullSecretsOrDefault(humioCluster *humioClusterv1alpha1.HumioCluster) []corev1.LocalObjectReference {
	emptyImagePullSecrets := []corev1.LocalObjectReference{}
	if reflect.DeepEqual(humioCluster.Spec.ImagePullSecrets, emptyImagePullSecrets) {
		return emptyImagePullSecrets
	}
	return humioCluster.Spec.ImagePullSecrets
}

func dataVolumeSourceOrDefault(humioCluster *humioClusterv1alpha1.HumioCluster) corev1.VolumeSource {
	emptyDataVolume := corev1.VolumeSource{}
	if reflect.DeepEqual(humioCluster.Spec.DataVolumeSource, emptyDataVolume) {
		return corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		}
	}
	return humioCluster.Spec.DataVolumeSource
}

func affinityOrDefault(humioCluster *humioClusterv1alpha1.HumioCluster) *corev1.Affinity {
	emptyAffinity := corev1.Affinity{}
	if reflect.DeepEqual(humioCluster.Spec.Affinity, emptyAffinity) {
		return &emptyAffinity
	}
	return &humioCluster.Spec.Affinity
}

func serviceAccountNameOrDefault(humioCluster *humioClusterv1alpha1.HumioCluster) string {
	if humioCluster.Spec.ServiceAccountName != "" {
		return humioCluster.Spec.ServiceAccountName
	}
	return "default"
}

func initServiceAccountNameOrDefault(humioCluster *humioClusterv1alpha1.HumioCluster) string {
	if humioCluster.Spec.InitServiceAccountName != "" {
		return humioCluster.Spec.InitServiceAccountName
	}
	return initServiceAccountName
}

func extraKafkaConfigsOrDefault(humioCluster *humioClusterv1alpha1.HumioCluster) string {
	return humioCluster.Spec.ExtraKafkaConfigs
}

func idpCertificateSecretNameOrDefault(humioCluster *humioClusterv1alpha1.HumioCluster) string {
	if humioCluster.Spec.IdpCertificateSecretName != "" {
		return humioCluster.Spec.IdpCertificateSecretName
	}
	return idpCertificateSecretName
}

func initClusterRoleName(humioCluster *humioClusterv1alpha1.HumioCluster) string {
	return fmt.Sprintf("%s-%s-%s", initClusterRolePrefix, humioCluster.Namespace, humioCluster.Name)
}

func initClusterRoleBindingName(humioCluster *humioClusterv1alpha1.HumioCluster) string {
	return fmt.Sprintf("%s-%s-%s", initClusterRoleBindingPrefix, humioCluster.Namespace, humioCluster.Name)
}

func podResourcesOrDefault(humioCluster *humioClusterv1alpha1.HumioCluster) corev1.ResourceRequirements {
	emptyResources := corev1.ResourceRequirements{}
	if reflect.DeepEqual(humioCluster.Spec.Resources, emptyResources) {
		return emptyResources
	}
	return humioCluster.Spec.Resources
}

func containerSecurityContextOrDefault(humioCluster *humioClusterv1alpha1.HumioCluster) *corev1.SecurityContext {
	if humioCluster.Spec.ContainerSecurityContext == nil {
		return &corev1.SecurityContext{
			Capabilities: &corev1.Capabilities{
				Add: []corev1.Capability{
					"SYS_NICE",
				},
			},
		}
	}
	return humioCluster.Spec.ContainerSecurityContext
}

func podSecurityContextOrDefault(humioCluster *humioClusterv1alpha1.HumioCluster) *corev1.PodSecurityContext {
	if humioCluster.Spec.PodSecurityContext == nil {
		return &corev1.PodSecurityContext{}
	}
	return humioCluster.Spec.PodSecurityContext
}

func setEnvironmentVariableDefaults(humioCluster *humioClusterv1alpha1.HumioCluster) {
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

		{Name: "HUMIO_JVM_ARGS", Value: "-Xss2m -Xms256m -Xmx1536m -server -XX:+UseParallelOldGC -XX:+ScavengeBeforeFullGC -XX:+DisableExplicitGC"},
		{Name: "HUMIO_PORT", Value: strconv.Itoa(humioPort)},
		{Name: "ELASTIC_PORT", Value: strconv.Itoa(elasticPort)},
		{Name: "KAFKA_MANAGED_BY_HUMIO", Value: "true"},
		{Name: "AUTHENTICATION_METHOD", Value: "single-user"},
		{
			Name:  "EXTERNAL_URL", // URL used by other Humio hosts.
			Value: "http://$(THIS_POD_IP):$(HUMIO_PORT)",
		},
		{
			Name:  "PUBLIC_URL", // URL used by users/browsers.
			Value: "http://$(THIS_POD_IP):$(HUMIO_PORT)",
		},
		{
			Name: "SINGLE_USER_PASSWORD",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					Key: "password",
					LocalObjectReference: corev1.LocalObjectReference{
						Name: kubernetes.ServiceAccountSecretName,
					},
				},
			},
		},
		{
			Name:  "ZOOKEEPER_URL_FOR_NODE_UUID",
			Value: "$(ZOOKEEPER_URL)",
		},
		{
			Name:  "LOG4J_CONFIGURATION",
			Value: "log4j2-stdout-json.xml",
		},
	}

	for _, defaultEnvVar := range envDefaults {
		setEnvironmentVariableDefault(humioCluster, defaultEnvVar)
	}
}

func setEnvironmentVariableDefault(humioCluster *humioClusterv1alpha1.HumioCluster, defaultEnvVar corev1.EnvVar) {
	for _, envVar := range humioCluster.Spec.EnvironmentVariables {
		if envVar.Name == defaultEnvVar.Name {
			return
		}
	}
	humioCluster.Spec.EnvironmentVariables = append(humioCluster.Spec.EnvironmentVariables, defaultEnvVar)
}
