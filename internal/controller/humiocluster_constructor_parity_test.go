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

	"github.com/stretchr/testify/assert"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNodeManagerConstructorsDNSFieldParity(t *testing.T) {
	dnsPolicy := corev1.DNSNone
	dnsConfig := &corev1.PodDNSConfig{
		Nameservers: []string{"8.8.8.8"},
		Searches:    []string{"example.com"},
	}

	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
		},
		Spec: humiov1alpha1.HumioClusterSpec{
			HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
				Image:     "humio/humio:1.0.0",
				DNSPolicy: dnsPolicy,
				DNSConfig: dnsConfig,
			},
		},
	}

	hnp := &humiov1alpha1.HumioNodePoolSpec{
		Name: "test-pool",
		HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
			Image:     "humio/humio:1.0.0",
			DNSPolicy: dnsPolicy,
			DNSConfig: dnsConfig,
		},
	}

	managerFromCluster := NewHumioNodeManagerFromHumioCluster(hc)
	managerFromPool := NewHumioNodeManagerFromHumioNodePool(hc, hnp)

	assert.Equal(t, dnsPolicy, managerFromCluster.humioNodeSpec.DNSPolicy, "NewHumioNodeManagerFromHumioCluster DNSPolicy")
	assert.Equal(t, dnsPolicy, managerFromPool.humioNodeSpec.DNSPolicy, "NewHumioNodeManagerFromHumioNodePool DNSPolicy")

	assert.NotNil(t, managerFromCluster.humioNodeSpec.DNSConfig, "NewHumioNodeManagerFromHumioCluster DNSConfig")
	assert.Equal(t, []string{"8.8.8.8"}, managerFromCluster.humioNodeSpec.DNSConfig.Nameservers, "cluster DNSConfig.Nameservers")

	assert.NotNil(t, managerFromPool.humioNodeSpec.DNSConfig, "NewHumioNodeManagerFromHumioNodePool DNSConfig")
	assert.Equal(t, []string{"8.8.8.8"}, managerFromPool.humioNodeSpec.DNSConfig.Nameservers, "pool DNSConfig.Nameservers")

	assert.Equal(t, managerFromCluster.humioNodeSpec.DNSPolicy, managerFromPool.humioNodeSpec.DNSPolicy, "DNSPolicy parity between constructors")
}
