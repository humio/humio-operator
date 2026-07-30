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

func TestHumioNodePoolDNSPolicy(t *testing.T) {
	tests := []struct {
		name     string
		pool     *HumioNodePool
		expected corev1.DNSPolicy
	}{
		{
			name: "unset dnsPolicy returns empty string",
			pool: &HumioNodePool{
				humioNodeSpec: humiov1alpha1.HumioNodeSpec{},
			},
			expected: "",
		},
		{
			name: "dnsPolicy=None returns None",
			pool: &HumioNodePool{
				humioNodeSpec: humiov1alpha1.HumioNodeSpec{
					DNSPolicy: "None",
				},
			},
			expected: "None",
		},
		{
			name: "dnsPolicy=ClusterFirst returns ClusterFirst",
			pool: &HumioNodePool{
				humioNodeSpec: humiov1alpha1.HumioNodeSpec{
					DNSPolicy: "ClusterFirst",
				},
			},
			expected: "ClusterFirst",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.pool.DNSPolicy()
			assert.Equal(t, tt.expected, result, "DNSPolicy()")
		})
	}
}

func TestHumioNodePoolDNSConfig(t *testing.T) {
	tests := []struct {
		name        string
		pool        *HumioNodePool
		expectedNil bool
	}{
		{
			name: "unset dnsConfig returns nil",
			pool: &HumioNodePool{
				humioNodeSpec: humiov1alpha1.HumioNodeSpec{},
			},
			expectedNil: true,
		},
		{
			name: "set dnsConfig returns pointer",
			pool: &HumioNodePool{
				humioNodeSpec: humiov1alpha1.HumioNodeSpec{
					DNSConfig: &corev1.PodDNSConfig{
						Nameservers: []string{"1.1.1.1"},
					},
				},
			},
			expectedNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.pool.DNSConfig()
			if tt.expectedNil {
				assert.Nil(t, result, "DNSConfig() should be nil")
			} else {
				assert.NotNil(t, result, "DNSConfig() should not be nil")
				assert.Len(t, result.Nameservers, 1, "DNSConfig().Nameservers length")
			}
		})
	}
}
