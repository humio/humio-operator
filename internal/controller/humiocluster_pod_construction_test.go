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
	"github.com/stretchr/testify/require"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

func TestConstructBasePodDNSFields(t *testing.T) {
	tests := []struct {
		name              string
		dnsPolicy         corev1.DNSPolicy
		dnsConfig         *corev1.PodDNSConfig
		expectedDNSPolicy corev1.DNSPolicy
		expectedDNSConfig *corev1.PodDNSConfig
	}{
		{
			name:              "empty dnsPolicy returns empty string",
			dnsPolicy:         "",
			dnsConfig:         nil,
			expectedDNSPolicy: "",
			expectedDNSConfig: nil,
		},
		{
			name:              "dnsPolicy=None returns None",
			dnsPolicy:         "None",
			dnsConfig:         nil,
			expectedDNSPolicy: "None",
			expectedDNSConfig: nil,
		},
		{
			name:      "dnsConfig set returns pointer",
			dnsPolicy: "None",
			dnsConfig: &corev1.PodDNSConfig{
				Nameservers: []string{"1.1.1.1"},
			},
			expectedDNSPolicy: "None",
			expectedDNSConfig: &corev1.PodDNSConfig{
				Nameservers: []string{"1.1.1.1"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hc := &humiov1alpha1.HumioCluster{
				Spec: humiov1alpha1.HumioClusterSpec{
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						Image:     "humio/humio:latest",
						DNSPolicy: tt.dnsPolicy,
						DNSConfig: tt.dnsConfig,
					},
				},
			}
			hnp := NewHumioNodeManagerFromHumioCluster(hc)

			pod, err := constructBasePod(hnp, "test-pod", &podAttachments{})
			require.NoError(t, err, "constructBasePod()")

			assert.Equal(t, tt.expectedDNSPolicy, pod.Spec.DNSPolicy, "pod.Spec.DNSPolicy")

			if tt.expectedDNSConfig == nil {
				assert.Nil(t, pod.Spec.DNSConfig, "pod.Spec.DNSConfig should be nil")
			} else {
				assert.NotNil(t, pod.Spec.DNSConfig, "pod.Spec.DNSConfig should not be nil")
				assert.Equal(t, len(tt.expectedDNSConfig.Nameservers), len(pod.Spec.DNSConfig.Nameservers), "Nameservers length")
			}
		})
	}
}
