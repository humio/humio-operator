package humiocluster

import (
	"fmt"
	"reflect"
	"strconv"

	humioClusterv1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

const (
	image                        = "humio/humio-core:1.12.0"
	targetReplicationFactor      = 2
	storagePartitionsCount       = 24
	digestPartitionsCount        = 24
	nodeCount                    = 3
	humioPort                    = 8080
	elasticPort                  = 9200
	idpCertificateFilename       = "idp-certificate.pem"
	extraKafkaPropertiesFilename = "extra-kafka-properties.properties"
	podHashAnnotation            = "humio_pod_hash"

	// cluster-wide resources:
	initClusterRoleSuffix        = "init"
	initClusterRoleBindingSuffix = "init"

	// namespaced resources:
	humioServiceAccountNameSuffix        = "humio"
	initServiceAccountNameSuffix         = "init"
	initServiceAccountSecretNameSuffix   = "init"
	authServiceAccountNameSuffix         = "auth"
	authServiceAccountSecretNameSuffix   = "auth"
	authRoleSuffix                       = "auth"
	authRoleBindingSuffix                = "auth"
	extraKafkaConfigsConfigMapNameSuffix = "extra-kafka-configs"
	idpCertificateSecretNameSuffix       = "idp-certificate"
)

func setDefaults(hc *humioClusterv1alpha1.HumioCluster) {
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
	if hc.Spec.NodeCount == 0 {
		hc.Spec.NodeCount = nodeCount
	}
}

func imagePullSecretsOrDefault(hc *humioClusterv1alpha1.HumioCluster) []corev1.LocalObjectReference {
	emptyImagePullSecrets := []corev1.LocalObjectReference{}
	if reflect.DeepEqual(hc.Spec.ImagePullSecrets, emptyImagePullSecrets) {
		return emptyImagePullSecrets
	}
	return hc.Spec.ImagePullSecrets
}

func dataVolumeSourceOrDefault(hc *humioClusterv1alpha1.HumioCluster) corev1.VolumeSource {
	emptyDataVolume := corev1.VolumeSource{}
	if reflect.DeepEqual(hc.Spec.DataVolumeSource, emptyDataVolume) {
		return corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		}
	}
	return hc.Spec.DataVolumeSource
}

func affinityOrDefault(hc *humioClusterv1alpha1.HumioCluster) *corev1.Affinity {
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

func humioServiceAccountAnnotationsOrDefault(hc *humioClusterv1alpha1.HumioCluster) map[string]string {
	if hc.Spec.HumioServiceAccountAnnotations != nil {
		return hc.Spec.HumioServiceAccountAnnotations
	}
	return map[string]string{}
}

func humioServiceAccountNameOrDefault(hc *humioClusterv1alpha1.HumioCluster) string {
	if hc.Spec.HumioServiceAccountName != "" {
		return hc.Spec.HumioServiceAccountName
	}
	return fmt.Sprintf("%s-%s", hc.Name, humioServiceAccountNameSuffix)
}

func initServiceAccountNameOrDefault(hc *humioClusterv1alpha1.HumioCluster) string {
	if hc.Spec.InitServiceAccountName != "" {
		return hc.Spec.InitServiceAccountName
	}
	return fmt.Sprintf("%s-%s", hc.Name, initServiceAccountNameSuffix)
}

func initServiceAccountSecretName(hc *humioClusterv1alpha1.HumioCluster) string {
	return fmt.Sprintf("%s-%s", hc.Name, initServiceAccountSecretNameSuffix)
}

func authServiceAccountNameOrDefault(hc *humioClusterv1alpha1.HumioCluster) string {
	if hc.Spec.AuthServiceAccountName != "" {
		return hc.Spec.AuthServiceAccountName
	}
	return fmt.Sprintf("%s-%s", hc.Name, authServiceAccountNameSuffix)
}

func authServiceAccountSecretName(hc *humioClusterv1alpha1.HumioCluster) string {
	return fmt.Sprintf("%s-%s", hc.Name, authServiceAccountSecretNameSuffix)
}

func extraKafkaConfigsOrDefault(hc *humioClusterv1alpha1.HumioCluster) string {
	return hc.Spec.ExtraKafkaConfigs
}

func extraKafkaConfigsConfigMapName(hc *humioClusterv1alpha1.HumioCluster) string {
	return fmt.Sprintf("%s-%s", hc.Name, extraKafkaConfigsConfigMapNameSuffix)
}

func idpCertificateSecretNameOrDefault(hc *humioClusterv1alpha1.HumioCluster) string {
	if hc.Spec.IdpCertificateSecretName != "" {
		return hc.Spec.IdpCertificateSecretName
	}
	return fmt.Sprintf("%s-%s", hc.Name, idpCertificateSecretNameSuffix)
}

func initClusterRoleName(hc *humioClusterv1alpha1.HumioCluster) string {
	return fmt.Sprintf("%s-%s-%s", hc.Namespace, hc.Name, initClusterRoleSuffix)
}

func initClusterRoleBindingName(hc *humioClusterv1alpha1.HumioCluster) string {
	return fmt.Sprintf("%s-%s-%s", hc.Namespace, hc.Name, initClusterRoleBindingSuffix)
}

func authRoleName(hc *humioClusterv1alpha1.HumioCluster) string {
	return fmt.Sprintf("%s-%s", hc.Name, authRoleSuffix)
}

func authRoleBindingName(hc *humioClusterv1alpha1.HumioCluster) string {
	return fmt.Sprintf("%s-%s", hc.Name, authRoleBindingSuffix)
}

func podResourcesOrDefault(hc *humioClusterv1alpha1.HumioCluster) corev1.ResourceRequirements {
	emptyResources := corev1.ResourceRequirements{}
	if reflect.DeepEqual(hc.Spec.Resources, emptyResources) {
		return emptyResources
	}
	return hc.Spec.Resources
}

func containerSecurityContextOrDefault(hc *humioClusterv1alpha1.HumioCluster) *corev1.SecurityContext {
	boolFalse := bool(false)
	boolTrue := bool(true)
	userID := int64(65534)
	if hc.Spec.ContainerSecurityContext == nil {
		return &corev1.SecurityContext{
			AllowPrivilegeEscalation: &boolFalse,
			Privileged:               &boolFalse,
			ReadOnlyRootFilesystem:   &boolTrue,
			RunAsUser:                &userID,
			RunAsNonRoot:             &boolTrue,
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

func podSecurityContextOrDefault(hc *humioClusterv1alpha1.HumioCluster) *corev1.PodSecurityContext {
	boolTrue := bool(true)
	userID := int64(65534)
	if hc.Spec.PodSecurityContext == nil {
		return &corev1.PodSecurityContext{
			RunAsUser:    &userID,
			RunAsNonRoot: &boolTrue,
		}
	}
	return hc.Spec.PodSecurityContext
}

func setEnvironmentVariableDefaults(hc *humioClusterv1alpha1.HumioCluster) {
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
			Name:  "ZOOKEEPER_URL_FOR_NODE_UUID",
			Value: "$(ZOOKEEPER_URL)",
		},
		{
			Name:  "LOG4J_CONFIGURATION",
			Value: "log4j2-stdout-json.xml",
		},
	}

	for _, defaultEnvVar := range envDefaults {
		appendEnvironmentVariableDefault(hc, defaultEnvVar)
	}

	if hc.Spec.Ingress.Enabled {
		appendEnvironmentVariableDefault(hc, corev1.EnvVar{
			Name:  "PUBLIC_URL", // URL used by users/browsers.
			Value: fmt.Sprintf("https://%s", hc.Spec.Hostname),
		})
	} else {
		appendEnvironmentVariableDefault(hc, corev1.EnvVar{
			Name:  "PUBLIC_URL", // URL used by users/browsers.
			Value: "http://$(THIS_POD_IP):$(HUMIO_PORT)",
		})
	}
}

func appendEnvironmentVariableDefault(hc *humioClusterv1alpha1.HumioCluster, defaultEnvVar corev1.EnvVar) {
	for _, envVar := range hc.Spec.EnvironmentVariables {
		if envVar.Name == defaultEnvVar.Name {
			return
		}
	}
	hc.Spec.EnvironmentVariables = append(hc.Spec.EnvironmentVariables, defaultEnvVar)
}

func certificateSecretNameOrDefault(hc *humioClusterv1alpha1.HumioCluster) string {
	if hc.Spec.Ingress.SecretName != "" {
		return hc.Spec.Ingress.SecretName
	}
	return fmt.Sprintf("%s-certificate", hc.Name)
}

func esCertificateSecretNameOrDefault(hc *humioClusterv1alpha1.HumioCluster) string {
	if hc.Spec.Ingress.ESSecretName != "" {
		return hc.Spec.Ingress.ESSecretName
	}
	return fmt.Sprintf("%s-es-certificate", hc.Name)
}
