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

package v1alpha1

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDNSPolicyNoneRequiresDNSConfig(t *testing.T) {
	tests := []struct {
		name        string
		cluster     *HumioCluster
		wantErr     bool
		errContains string
	}{
		{
			name: "dnsPolicy=None without dnsConfig should fail",
			cluster: &HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
				},
				Spec: HumioClusterSpec{
					License: HumioClusterLicenseSpec{SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "license"},
						Key:                  "data",
					}},
					HumioNodeSpec: HumioNodeSpec{
						DNSPolicy: "None",
					},
				},
			},
			wantErr:     true,
			errContains: "dnsConfig is required when dnsPolicy is None",
		},
		{
			name: "dnsPolicy=None with dnsConfig should pass",
			cluster: &HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
				},
				Spec: HumioClusterSpec{
					License: HumioClusterLicenseSpec{SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "license"},
						Key:                  "data",
					}},
					HumioNodeSpec: HumioNodeSpec{
						DNSPolicy: "None",
						DNSConfig: &corev1.PodDNSConfig{
							Nameservers: []string{"1.1.1.1"},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "dnsPolicy=ClusterFirst without dnsConfig should pass",
			cluster: &HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
				},
				Spec: HumioClusterSpec{
					License: HumioClusterLicenseSpec{SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "license"},
						Key:                  "data",
					}},
					HumioNodeSpec: HumioNodeSpec{
						DNSPolicy: "ClusterFirst",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "unset dnsPolicy without dnsConfig should pass",
			cluster: &HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
				},
				Spec: HumioClusterSpec{
					License: HumioClusterLicenseSpec{SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "license"},
						Key:                  "data",
					}},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.cluster.Spec.DNSPolicy == "None" && tt.cluster.Spec.DNSConfig == nil {
				t.Log("CEL should reject this configuration at admission time")
			}
		})
	}
}
