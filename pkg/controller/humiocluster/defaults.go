package humiocluster

import (
	"fmt"
	"strconv"

	humioClusterv1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

const (
	name                    = "humiocluster"
	namespace               = "logging"
	image                   = "humio/humio-core"
	version                 = "1.9.0"
	targetReplicationFactor = 2
	storagePartitionsCount  = 24
	digestPartitionsCount   = 24
	nodeCount               = 3
	humioPort               = 8080
	elasticPort             = 9200
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

func setEnvironmentVariableDefaults(humioCluster *humioClusterv1alpha1.HumioCluster) {
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
			Value: fmt.Sprintf("http://$(POD_NAME).core.$(POD_NAMESPACE).svc.cluster.local:$(HUMIO_PORT)"),
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
						Name: serviceAccountSecretName,
					},
				},
			},
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
