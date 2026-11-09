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

package controller

import (
	"testing"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/kubernetes"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestConstructWorkloadTypeService(t *testing.T) {
	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: "default",
		},
		Spec: humiov1alpha1.HumioClusterSpec{},
	}

	ws := humiov1alpha1.WorkloadServiceSpec{
		Name:         "digest",
		WorkloadType: "digest",
	}

	svc := constructWorkloadTypeService(hc, ws)

	assert.Equal(t, "my-cluster-digest", svc.Name)
	assert.Equal(t, "default", svc.Namespace)
	assert.Equal(t, corev1.ServiceTypeClusterIP, svc.Spec.Type)

	expectedSelector := kubernetes.WorkloadTypeLabelPrefix + "digest"
	assert.Equal(t, "true", svc.Spec.Selector[expectedSelector])
	assert.Equal(t, "my-cluster", svc.Spec.Selector["app.kubernetes.io/instance"])

	assert.Len(t, svc.Spec.Ports, 2)
	assert.Equal(t, int32(8080), svc.Spec.Ports[0].Port)
	assert.Equal(t, int32(8080), svc.Spec.Ports[0].TargetPort.IntVal)
	assert.Equal(t, int32(9200), svc.Spec.Ports[1].Port)
	assert.Equal(t, int32(9200), svc.Spec.Ports[1].TargetPort.IntVal)
}

func TestConstructWorkloadTypeService_CustomOverrides(t *testing.T) {
	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: "default",
		},
	}

	ws := humiov1alpha1.WorkloadServiceSpec{
		Name:               "ingest",
		WorkloadType:       "ingest",
		ServiceType:        corev1.ServiceTypeLoadBalancer,
		HumioServicePort:   443,
		HumioESServicePort: 9201,
		Labels:             map[string]string{"custom": "label"},
		Annotations:        map[string]string{"custom": "annotation"},
	}

	svc := constructWorkloadTypeService(hc, ws)

	assert.Equal(t, "my-cluster-ingest", svc.Name)
	assert.Equal(t, corev1.ServiceTypeLoadBalancer, svc.Spec.Type)
	assert.Equal(t, "label", svc.Labels["custom"])
	assert.Equal(t, "annotation", svc.Annotations["custom"])
	assert.Equal(t, int32(443), svc.Spec.Ports[0].Port)
	assert.Equal(t, int32(8080), svc.Spec.Ports[0].TargetPort.IntVal)
	assert.Equal(t, int32(9201), svc.Spec.Ports[1].Port)
	assert.Equal(t, int32(9200), svc.Spec.Ports[1].TargetPort.IntVal)
}

func TestWorkloadTypeServiceName(t *testing.T) {
	assert.Equal(t, "cluster-digest", workloadTypeServiceName("cluster", "digest"))
	assert.Equal(t, "my-cluster-ingest", workloadTypeServiceName("my-cluster", "ingest"))
}

func TestHasWorkloadTypeSelector(t *testing.T) {
	t.Run("service with workload type selector", func(t *testing.T) {
		svc := &corev1.Service{
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{
					"app.kubernetes.io/instance":                  "cluster",
					kubernetes.WorkloadTypeLabelPrefix + "digest": "true",
				},
			},
		}
		assert.True(t, hasWorkloadTypeSelector(svc))
	})

	t.Run("service without workload type selector", func(t *testing.T) {
		svc := &corev1.Service{
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{
					"app.kubernetes.io/instance": "cluster",
					"humio.com/node-pool":        "my-pool",
				},
			},
		}
		assert.False(t, hasWorkloadTypeSelector(svc))
	})
}

func TestConstructWorkloadTypeServices_MultiPool(t *testing.T) {
	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "prod",
			Namespace: "logscale",
		},
		Spec: humiov1alpha1.HumioClusterSpec{
			WorkloadServices: []humiov1alpha1.WorkloadServiceSpec{
				{
					Name:         "digest",
					WorkloadType: "digest",
				},
				{
					Name:         "ingest",
					WorkloadType: "ingest",
					ServiceType:  corev1.ServiceTypeLoadBalancer,
					Annotations:  map[string]string{"service.beta.kubernetes.io/aws-load-balancer-type": "nlb"},
				},
			},
		},
	}

	digestSvc := constructWorkloadTypeService(hc, hc.Spec.WorkloadServices[0])
	ingestSvc := constructWorkloadTypeService(hc, hc.Spec.WorkloadServices[1])

	// Digest service: defaults match per-pool service
	assert.Equal(t, "prod-digest", digestSvc.Name)
	assert.Equal(t, "logscale", digestSvc.Namespace)
	assert.Equal(t, corev1.ServiceTypeClusterIP, digestSvc.Spec.Type)
	assert.Equal(t, "true", digestSvc.Spec.Selector[kubernetes.WorkloadTypeLabelPrefix+"digest"])
	assert.Equal(t, "prod", digestSvc.Spec.Selector["app.kubernetes.io/instance"])
	assert.Equal(t, int32(8080), digestSvc.Spec.Ports[0].Port)
	assert.Equal(t, int32(9200), digestSvc.Spec.Ports[1].Port)
	assert.Empty(t, digestSvc.Annotations)

	// Ingest service: LoadBalancer with annotation
	assert.Equal(t, "prod-ingest", ingestSvc.Name)
	assert.Equal(t, "logscale", ingestSvc.Namespace)
	assert.Equal(t, corev1.ServiceTypeLoadBalancer, ingestSvc.Spec.Type)
	assert.Equal(t, "true", ingestSvc.Spec.Selector[kubernetes.WorkloadTypeLabelPrefix+"ingest"])
	assert.Equal(t, "prod", ingestSvc.Spec.Selector["app.kubernetes.io/instance"])
	assert.Equal(t, int32(8080), ingestSvc.Spec.Ports[0].Port)
	assert.Equal(t, int32(9200), ingestSvc.Spec.Ports[1].Port)
	assert.Equal(t, "nlb", ingestSvc.Annotations["service.beta.kubernetes.io/aws-load-balancer-type"])

	// Selectors are distinct
	assert.NotEqual(t, digestSvc.Spec.Selector, ingestSvc.Spec.Selector)
	_, hasIngestLabel := digestSvc.Spec.Selector[kubernetes.WorkloadTypeLabelPrefix+"ingest"]
	assert.False(t, hasIngestLabel)
	_, hasDigestLabel := ingestSvc.Spec.Selector[kubernetes.WorkloadTypeLabelPrefix+"digest"]
	assert.False(t, hasDigestLabel)
}
