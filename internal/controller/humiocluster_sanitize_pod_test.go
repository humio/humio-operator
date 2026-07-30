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
)

func TestSanitizePodDNSPolicyHashStability(t *testing.T) {
	tests := []struct {
		name                string
		inputDNSPolicy      corev1.DNSPolicy
		hnpDNSPolicy        corev1.DNSPolicy
		expectedInSanitized corev1.DNSPolicy
	}{
		{
			name:                "empty input, empty HNP getter",
			inputDNSPolicy:      "",
			hnpDNSPolicy:        "",
			expectedInSanitized: "",
		},
		{
			name:                "ClusterFirst input, empty HNP getter",
			inputDNSPolicy:      corev1.DNSClusterFirst,
			hnpDNSPolicy:        "",
			expectedInSanitized: "",
		},
		{
			name:                "empty input, None HNP getter",
			inputDNSPolicy:      "",
			hnpDNSPolicy:        corev1.DNSNone,
			expectedInSanitized: corev1.DNSNone,
		},
		{
			name:                "ClusterFirst input, None HNP getter",
			inputDNSPolicy:      corev1.DNSClusterFirst,
			hnpDNSPolicy:        corev1.DNSNone,
			expectedInSanitized: corev1.DNSNone,
		},
		{
			name:                "None input, None HNP getter",
			inputDNSPolicy:      corev1.DNSNone,
			hnpDNSPolicy:        corev1.DNSNone,
			expectedInSanitized: corev1.DNSNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hc := &humiov1alpha1.HumioCluster{
				Spec: humiov1alpha1.HumioClusterSpec{},
			}

			if tt.hnpDNSPolicy != "" {
				hc.Spec.DNSPolicy = tt.hnpDNSPolicy
			}

			hnp := NewHumioNodeManagerFromHumioCluster(hc)

			pod := &corev1.Pod{
				Spec: corev1.PodSpec{
					DNSPolicy: tt.inputDNSPolicy,
					Containers: []corev1.Container{
						{
							Name:  HumioContainerName,
							Image: "humio/humio:latest",
						},
					},
				},
			}

			sanitized := sanitizePod(hnp, pod)
			assert.Equal(t, tt.expectedInSanitized, sanitized.Spec.DNSPolicy, "DNSPolicy")
		})
	}
}

func TestSanitizePodDNSConfigPreservation(t *testing.T) {
	tests := []struct {
		name                string
		inputDNSConfig      *corev1.PodDNSConfig
		hnpDNSConfig        *corev1.PodDNSConfig
		expectedInSanitized *corev1.PodDNSConfig
	}{
		{
			name:                "nil input, nil HNP getter",
			inputDNSConfig:      nil,
			hnpDNSConfig:        nil,
			expectedInSanitized: nil,
		},
		{
			name: "non-nil input, nil HNP getter",
			inputDNSConfig: &corev1.PodDNSConfig{
				Nameservers: []string{"8.8.8.8"},
			},
			hnpDNSConfig:        nil,
			expectedInSanitized: nil,
		},
		{
			name:           "nil input, non-nil HNP getter",
			inputDNSConfig: nil,
			hnpDNSConfig: &corev1.PodDNSConfig{
				Nameservers: []string{"1.1.1.1"},
			},
			expectedInSanitized: &corev1.PodDNSConfig{
				Nameservers: []string{"1.1.1.1"},
			},
		},
		{
			name: "non-nil input, non-nil HNP getter",
			inputDNSConfig: &corev1.PodDNSConfig{
				Nameservers: []string{"8.8.8.8"},
			},
			hnpDNSConfig: &corev1.PodDNSConfig{
				Nameservers: []string{"1.1.1.1"},
			},
			expectedInSanitized: &corev1.PodDNSConfig{
				Nameservers: []string{"1.1.1.1"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hc := &humiov1alpha1.HumioCluster{
				Spec: humiov1alpha1.HumioClusterSpec{},
			}

			if tt.hnpDNSConfig != nil {
				hc.Spec.DNSConfig = tt.hnpDNSConfig
			}

			hnp := NewHumioNodeManagerFromHumioCluster(hc)

			pod := &corev1.Pod{
				Spec: corev1.PodSpec{
					DNSConfig: tt.inputDNSConfig,
					Containers: []corev1.Container{
						{
							Name:  HumioContainerName,
							Image: "humio/humio:latest",
						},
					},
				},
			}

			sanitized := sanitizePod(hnp, pod)

			if tt.expectedInSanitized == nil {
				assert.Nil(t, sanitized.Spec.DNSConfig, "DNSConfig should be nil")
			} else {
				assert.NotNil(t, sanitized.Spec.DNSConfig, "DNSConfig should not be nil")
				assert.Equal(t, tt.expectedInSanitized.Nameservers, sanitized.Spec.DNSConfig.Nameservers, "DNSConfig.Nameservers")
			}
		})
	}
}
