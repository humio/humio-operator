package humiocluster

import (
	humioClusterv1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
)

const (
	name                    = "humiocluster"
	namespace               = "logging"
	image                   = "humio/humio-core"
	version                 = "1.9.0"
	targetReplicationFactor = 3
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
}
