package controller

import (
	"testing"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestShouldUseExtraKafkaConfigsFile(t *testing.T) {
	testCases := []struct {
		name                string
		image               string
		extraKafkaConfigs   string
		expectedUseFile     bool
		expectedDescription string
	}{
		{
			name:                "Empty extraKafkaConfigs should not use file",
			image:               "humio/humio-core:1.172.0",
			extraKafkaConfigs:   "",
			expectedUseFile:     false,
			expectedDescription: "No configuration provided",
		},
		{
			name:                "LogScale < 1.173.0 should use file",
			image:               "humio/humio-core:1.172.0",
			extraKafkaConfigs:   "security.protocol=SSL",
			expectedUseFile:     true,
			expectedDescription: "Pre-deprecation version should use file",
		},
		{
			name:                "LogScale 1.173.0 (deprecation start) should use file",
			image:               "humio/humio-core:1.173.0",
			extraKafkaConfigs:   "security.protocol=SSL",
			expectedUseFile:     true,
			expectedDescription: "Deprecation version should still use file",
		},
		{
			name:                "LogScale 1.200.0 (between deprecation and removal) should use file",
			image:               "humio/humio-core:1.200.0",
			extraKafkaConfigs:   "security.protocol=SSL",
			expectedUseFile:     true,
			expectedDescription: "Between deprecation and removal should use file",
		},
		{
			name:                "LogScale 1.224.0 (last version before removal) should use file",
			image:               "humio/humio-core:1.224.0",
			extraKafkaConfigs:   "security.protocol=SSL",
			expectedUseFile:     true,
			expectedDescription: "Last version before removal should use file",
		},
		{
			name:                "LogScale 1.225.0 (removal version) should not use file",
			image:               "humio/humio-core:1.225.0",
			extraKafkaConfigs:   "security.protocol=SSL",
			expectedUseFile:     false,
			expectedDescription: "Removal version should not use file to prevent startup failure",
		},
		{
			name:                "LogScale 1.230.0 (after removal) should not use file",
			image:               "humio/humio-core:1.230.0",
			extraKafkaConfigs:   "security.protocol=SSL",
			expectedUseFile:     false,
			expectedDescription: "Post-removal version should not use file",
		},
		{
			name:                "Latest image (no version tag) should not use file (assumes future version)",
			image:               "humio/humio-core:latest",
			extraKafkaConfigs:   "security.protocol=SSL",
			expectedUseFile:     false,
			expectedDescription: "Latest image assumes latest version (>= 1.225.0), should not use file",
		},
		{
			name:                "Image without tag should not use file (assumes future version)",
			image:               "humio/humio-core",
			extraKafkaConfigs:   "security.protocol=SSL",
			expectedUseFile:     false,
			expectedDescription: "No tag assumes latest version (>= 1.225.0), should not use file",
		},
		{
			name:                "Commit hash image before removal should use file",
			image:               "humio/humio-core:1.224.0-abc123def",
			extraKafkaConfigs:   "security.protocol=SSL",
			expectedUseFile:     true,
			expectedDescription: "Version with commit hash before removal should use file",
		},
		{
			name:                "Commit hash image at removal should not use file",
			image:               "humio/humio-core:1.225.0-abc123def",
			extraKafkaConfigs:   "security.protocol=SSL",
			expectedUseFile:     false,
			expectedDescription: "Version with commit hash at removal version should not use file",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a basic HumioCluster with the test image and config
			hc := &humiov1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
				},
				Spec: humiov1alpha1.HumioClusterSpec{
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						Image:             tc.image,
						ExtraKafkaConfigs: tc.extraKafkaConfigs,
					},
				},
			}

			// Create HumioNodePool from the cluster
			hnp := NewHumioNodeManagerFromHumioCluster(hc)

			// Test the method
			result := hnp.ShouldUseExtraKafkaConfigsFile()

			if result != tc.expectedUseFile {
				t.Errorf("Test %s failed: expected %v, got %v. %s",
					tc.name, tc.expectedUseFile, result, tc.expectedDescription)
			}
		})
	}
}

func TestHumioVersionExtraKafkaConfigsVersionChecks(t *testing.T) {
	testCases := []struct {
		name                string
		image               string
		expectedDeprecated  bool
		expectedRemoved     bool
		expectedDescription string
	}{
		{
			name:                "Version 1.172.0 - not deprecated",
			image:               "humio/humio-core:1.172.0",
			expectedDeprecated:  false,
			expectedRemoved:     false,
			expectedDescription: "Pre-deprecation version",
		},
		{
			name:                "Version 1.173.0 - exactly at deprecation",
			image:               "humio/humio-core:1.173.0",
			expectedDeprecated:  true,
			expectedRemoved:     false,
			expectedDescription: "Exactly at deprecation version",
		},
		{
			name:                "Version 1.200.0 - deprecated but not removed",
			image:               "humio/humio-core:1.200.0",
			expectedDeprecated:  true,
			expectedRemoved:     false,
			expectedDescription: "Between deprecation and removal",
		},
		{
			name:                "Version 1.224.0 - deprecated but not removed",
			image:               "humio/humio-core:1.224.0",
			expectedDeprecated:  true,
			expectedRemoved:     false,
			expectedDescription: "Last version before removal",
		},
		{
			name:                "Version 1.225.0 - exactly at removal",
			image:               "humio/humio-core:1.225.0",
			expectedDeprecated:  true,
			expectedRemoved:     true,
			expectedDescription: "Exactly at removal version",
		},
		{
			name:                "Version 1.230.0 - after removal",
			image:               "humio/humio-core:1.230.0",
			expectedDeprecated:  true,
			expectedRemoved:     true,
			expectedDescription: "After removal version",
		},
		{
			name:                "Latest version (assumes future, 1.225.0+)",
			image:               "humio/humio-core:latest",
			expectedDeprecated:  true,
			expectedRemoved:     true,
			expectedDescription: "Latest version should assume current latest (>= 1.225.0)",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			humioVersion := HumioVersionFromString(tc.image)

			// Check deprecated status
			deprecated, err := humioVersion.AtLeast(HumioVersionExtraKafkaConfigsDeprecated)
			if err != nil {
				t.Errorf("Error checking deprecated version: %v", err)
			}
			if deprecated != tc.expectedDeprecated {
				t.Errorf("Test %s failed for deprecated check: expected %v, got %v. %s",
					tc.name, tc.expectedDeprecated, deprecated, tc.expectedDescription)
			}

			// Check removed status
			removed, err := humioVersion.AtLeast(HumioVersionExtraKafkaConfigsRemoved)
			if err != nil {
				t.Errorf("Error checking removed version: %v", err)
			}
			if removed != tc.expectedRemoved {
				t.Errorf("Test %s failed for removed check: expected %v, got %v. %s",
					tc.name, tc.expectedRemoved, removed, tc.expectedDescription)
			}
		})
	}
}

func TestConstructPodExtraKafkaConfigsVersionAware(t *testing.T) {
	testCases := []struct {
		name                       string
		image                      string
		extraKafkaConfigs          string
		expectedEnvVarPresent      bool
		expectedVolumeMountPresent bool
		expectedVolumePresent      bool
		expectedDescription        string
	}{
		{
			name:                       "LogScale 1.172.0 with config should have EXTRA_KAFKA_CONFIGS_FILE",
			image:                      "humio/humio-core:1.172.0",
			extraKafkaConfigs:          "security.protocol=SSL",
			expectedEnvVarPresent:      true,
			expectedVolumeMountPresent: true,
			expectedVolumePresent:      true,
			expectedDescription:        "Pre-deprecation version should have all components",
		},
		{
			name:                       "LogScale 1.173.0 with config should have EXTRA_KAFKA_CONFIGS_FILE",
			image:                      "humio/humio-core:1.173.0",
			extraKafkaConfigs:          "security.protocol=SSL",
			expectedEnvVarPresent:      true,
			expectedVolumeMountPresent: true,
			expectedVolumePresent:      true,
			expectedDescription:        "Deprecation version should still have all components",
		},
		{
			name:                       "LogScale 1.225.0 with config should not have EXTRA_KAFKA_CONFIGS_FILE",
			image:                      "humio/humio-core:1.225.0",
			extraKafkaConfigs:          "security.protocol=SSL",
			expectedEnvVarPresent:      false,
			expectedVolumeMountPresent: false,
			expectedVolumePresent:      false,
			expectedDescription:        "Removal version should not have any components",
		},
		{
			name:                       "LogScale 1.230.0 with config should not have EXTRA_KAFKA_CONFIGS_FILE",
			image:                      "humio/humio-core:1.230.0",
			extraKafkaConfigs:          "security.protocol=SSL",
			expectedEnvVarPresent:      false,
			expectedVolumeMountPresent: false,
			expectedVolumePresent:      false,
			expectedDescription:        "Post-removal version should not have any components",
		},
		{
			name:                       "Latest image should not have EXTRA_KAFKA_CONFIGS_FILE (assumes future version)",
			image:                      "humio/humio-core:latest",
			extraKafkaConfigs:          "security.protocol=SSL",
			expectedEnvVarPresent:      false,
			expectedVolumeMountPresent: false,
			expectedVolumePresent:      false,
			expectedDescription:        "Latest tag should assume future behavior (>= 1.225.0)",
		},
		{
			name:                       "Any version without config should not have EXTRA_KAFKA_CONFIGS_FILE",
			image:                      "humio/humio-core:1.172.0",
			extraKafkaConfigs:          "",
			expectedEnvVarPresent:      false,
			expectedVolumeMountPresent: false,
			expectedVolumePresent:      false,
			expectedDescription:        "No config means no components regardless of version",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a basic HumioCluster with the test image and config
			hc := &humiov1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
				},
				Spec: humiov1alpha1.HumioClusterSpec{
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						Image:             tc.image,
						ExtraKafkaConfigs: tc.extraKafkaConfigs,
					},
				},
			}

			// Create HumioNodePool from the cluster
			hnp := NewHumioNodeManagerFromHumioCluster(hc)

			// Create pod attachments (empty for this test)
			attachments := &podAttachments{}

			// Construct the pod
			pod, err := ConstructPod(hnp, "test-node", attachments)
			if err != nil {
				t.Fatalf("Error constructing pod: %v", err)
			}

			// Find the humio container
			var humioContainer *corev1.Container
			for i := range pod.Spec.Containers {
				if pod.Spec.Containers[i].Name == HumioContainerName {
					humioContainer = &pod.Spec.Containers[i]
					break
				}
			}

			if humioContainer == nil {
				t.Fatal("Humio container not found in pod")
			}

			// Check environment variable
			envVarFound := false
			for _, env := range humioContainer.Env {
				if env.Name == "EXTRA_KAFKA_CONFIGS_FILE" {
					envVarFound = true
					break
				}
			}

			if envVarFound != tc.expectedEnvVarPresent {
				t.Errorf("Test %s failed for env var: expected %v, got %v. %s",
					tc.name, tc.expectedEnvVarPresent, envVarFound, tc.expectedDescription)
			}

			// Check volume mount
			volumeMountFound := false
			for _, mount := range humioContainer.VolumeMounts {
				if mount.Name == "extra-kafka-configs" {
					volumeMountFound = true
					break
				}
			}

			if volumeMountFound != tc.expectedVolumeMountPresent {
				t.Errorf("Test %s failed for volume mount: expected %v, got %v. %s",
					tc.name, tc.expectedVolumeMountPresent, volumeMountFound, tc.expectedDescription)
			}

			// Check volume
			volumeFound := false
			for _, volume := range pod.Spec.Volumes {
				if volume.Name == "extra-kafka-configs" {
					volumeFound = true
					break
				}
			}

			if volumeFound != tc.expectedVolumePresent {
				t.Errorf("Test %s failed for volume: expected %v, got %v. %s",
					tc.name, tc.expectedVolumePresent, volumeFound, tc.expectedDescription)
			}
		})
	}
}

func TestVersionConstants(t *testing.T) {
	// Test that our version constants are properly defined
	if HumioVersionExtraKafkaConfigsDeprecated != "1.173.0" {
		t.Errorf("Expected deprecation version to be 1.173.0, got %s", HumioVersionExtraKafkaConfigsDeprecated)
	}

	if HumioVersionExtraKafkaConfigsRemoved != "1.225.0" {
		t.Errorf("Expected removal version to be 1.225.0, got %s", HumioVersionExtraKafkaConfigsRemoved)
	}

	// Test that deprecation version is less than removal version
	deprecatedVersion := HumioVersionFromString("humio/humio-core:" + HumioVersionExtraKafkaConfigsDeprecated)
	removedVersion := HumioVersionFromString("humio/humio-core:" + HumioVersionExtraKafkaConfigsRemoved)

	if deprecatedVersion.SemVer() == nil || removedVersion.SemVer() == nil {
		t.Fatal("Failed to parse version constants")
	}

	if !deprecatedVersion.SemVer().LessThan(removedVersion.SemVer()) {
		t.Errorf("Deprecation version %s should be less than removal version %s",
			HumioVersionExtraKafkaConfigsDeprecated, HumioVersionExtraKafkaConfigsRemoved)
	}
}

// Test helper functions for version parsing edge cases
func TestHumioVersionFromStringEdgeCases(t *testing.T) {
	testCases := []struct {
		name                string
		image               string
		expectedLatest      bool
		expectedVersion     string
		expectedDescription string
	}{
		{
			name:                "Image without tag defaults to latest",
			image:               "humio/humio-core",
			expectedLatest:      true,
			expectedVersion:     "",
			expectedDescription: "No tag assumes latest version (>= 1.225.0), should not use file",
		},
		{
			name:                "Image with latest tag",
			image:               "humio/humio-core:latest",
			expectedLatest:      true,
			expectedVersion:     "",
			expectedDescription: "Latest tag assumes latest version (>= 1.225.0), should not use file",
		},
		{
			name:                "Image with semantic version",
			image:               "humio/humio-core:1.173.0",
			expectedLatest:      false,
			expectedVersion:     "1.173.0",
			expectedDescription: "Semantic version should be parsed",
		},
		{
			name:                "Image with version and commit hash",
			image:               "humio/humio-core:1.173.0-abc123",
			expectedLatest:      false,
			expectedVersion:     "1.173.0",
			expectedDescription: "Version with commit should use base version",
		},
		{
			name:                "Image with invalid version",
			image:               "humio/humio-core:invalid-version",
			expectedLatest:      true,
			expectedVersion:     "",
			expectedDescription: "Invalid version should assume latest",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			version := HumioVersionFromString(tc.image)

			if version.IsLatest() != tc.expectedLatest {
				t.Errorf("Test %s failed for IsLatest(): expected %v, got %v. %s",
					tc.name, tc.expectedLatest, version.IsLatest(), tc.expectedDescription)
			}

			if !tc.expectedLatest {
				if version.SemVer() == nil {
					t.Errorf("Test %s failed: expected valid semver, got nil. %s",
						tc.name, tc.expectedDescription)
				} else if version.SemVer().String() != tc.expectedVersion {
					t.Errorf("Test %s failed for version: expected %s, got %s. %s",
						tc.name, tc.expectedVersion, version.SemVer().String(), tc.expectedDescription)
				}
			}
		})
	}
}

// Test that the ShouldUseExtraKafkaConfigsFile method properly handles edge cases
func TestShouldUseExtraKafkaConfigsFileEdgeCases(t *testing.T) {
	testCases := []struct {
		name              string
		image             string
		extraKafkaConfigs string
		expected          bool
		description       string
	}{
		{
			name:              "Empty config string should return false",
			image:             "humio/humio-core:1.173.0",
			extraKafkaConfigs: "",
			expected:          false,
			description:       "Empty configuration should not use file",
		},
		{
			name:              "Whitespace only config should return true (not trimmed)",
			image:             "humio/humio-core:1.173.0",
			extraKafkaConfigs: "   \n\t  ",
			expected:          true,
			description:       "Whitespace-only configuration is not trimmed, so it's treated as non-empty",
		},
		{
			name:              "Valid config with removal version should return false",
			image:             "humio/humio-core:1.225.0",
			extraKafkaConfigs: "security.protocol=SSL",
			expected:          false,
			description:       "Valid config with removal version should not use file",
		},
		{
			name:              "Valid config with pre-removal version should return true",
			image:             "humio/humio-core:1.224.0",
			extraKafkaConfigs: "security.protocol=SSL",
			expected:          true,
			description:       "Valid config with pre-removal version should use file",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hc := &humiov1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
				},
				Spec: humiov1alpha1.HumioClusterSpec{
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						Image:             tc.image,
						ExtraKafkaConfigs: tc.extraKafkaConfigs,
					},
				},
			}

			hnp := NewHumioNodeManagerFromHumioCluster(hc)
			result := hnp.ShouldUseExtraKafkaConfigsFile()

			if result != tc.expected {
				t.Errorf("Test %s failed: expected %v, got %v. %s",
					tc.name, tc.expected, result, tc.description)
			}
		})
	}
}
