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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestHashStabilityAcrossOperatorUpgrade(t *testing.T) {
	t.Run("pod created with no DNS fields on old operator", func(t *testing.T) {
		oldPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod-old",
				Namespace: "default",
			},
			Spec: corev1.PodSpec{},
		}

		newPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod-new",
				Namespace: "default",
			},
			Spec: corev1.PodSpec{
				DNSPolicy:         "",
				DNSConfig:         nil,
				SetHostnameAsFQDN: nil,
			},
		}

		oldHasher := NewPodHasher(oldPod, nil)
		newHasher := NewPodHasher(newPod, nil)

		oldHash, err := oldHasher.PodHashMinusManagedFields()
		require.NoError(t, err, "old hash calculation")

		newHash, err := newHasher.PodHashMinusManagedFields()
		require.NoError(t, err, "new hash calculation")

		assert.Equal(t, oldHash, newHash, "hash should be stable across operator upgrade")
	})

	t.Run("pod created with DNS fields set", func(t *testing.T) {
		dnsPolicy := corev1.DNSClusterFirst
		setHostnameAsFQDN := true

		pod1 := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod-dns",
				Namespace: "default",
			},
			Spec: corev1.PodSpec{
				DNSPolicy: dnsPolicy,
				DNSConfig: &corev1.PodDNSConfig{
					Nameservers: []string{"8.8.8.8"},
				},
				SetHostnameAsFQDN: &setHostnameAsFQDN,
			},
		}

		pod2 := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod-dns",
				Namespace: "default",
			},
			Spec: corev1.PodSpec{
				DNSPolicy: dnsPolicy,
				DNSConfig: &corev1.PodDNSConfig{
					Nameservers: []string{"8.8.8.8"},
				},
				SetHostnameAsFQDN: &setHostnameAsFQDN,
			},
		}

		hasher1 := NewPodHasher(pod1, nil)
		hasher2 := NewPodHasher(pod2, nil)

		hash1, err := hasher1.PodHashMinusManagedFields()
		require.NoError(t, err, "first hash calculation")

		hash2, err := hasher2.PodHashMinusManagedFields()
		require.NoError(t, err, "second hash calculation")

		assert.Equal(t, hash1, hash2, "hash should be stable between reads")
	})
}
