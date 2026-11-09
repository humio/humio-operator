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
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestHumioNodePoolScaleSubresourcePaths verifies that the HumioNodePool CRD
// has the correct flat scale subresource paths per DESIGN.md Phase 1 requirements.
// RED: This test MUST fail initially as the current marker uses nested paths.
func TestHumioNodePoolScaleSubresourcePaths(t *testing.T) {
	crdPath := "../../config/crd/bases/core.humio.com_humionodepools.yaml"

	data, err := os.ReadFile(crdPath)
	if err != nil {
		t.Fatalf("Failed to read CRD file %s: %v", crdPath, err)
	}

	var crd map[string]interface{}
	if err := yaml.Unmarshal(data, &crd); err != nil {
		t.Fatalf("Failed to parse CRD YAML: %v", err)
	}

	spec, ok := crd["spec"].(map[string]interface{})
	if !ok {
		t.Fatal("CRD spec not found or invalid type")
	}

	versions, ok := spec["versions"].([]interface{})
	if !ok || len(versions) == 0 {
		t.Fatal("CRD versions not found or empty")
	}

	// Find v1alpha1 version
	var v1alpha1 map[string]interface{}
	for _, v := range versions {
		version := v.(map[string]interface{})
		if version["name"] == "v1alpha1" {
			v1alpha1 = version
			break
		}
	}
	if v1alpha1 == nil {
		t.Fatal("v1alpha1 version not found in CRD")
	}

	subresources, ok := v1alpha1["subresources"].(map[string]interface{})
	if !ok {
		t.Fatal("subresources not found in v1alpha1")
	}

	scale, ok := subresources["scale"].(map[string]interface{})
	if !ok {
		t.Fatal("scale subresource not found")
	}

	// Verify flat paths per DESIGN.md
	specReplicasPath, ok := scale["specReplicasPath"].(string)
	if !ok {
		t.Fatal("specReplicasPath not found or invalid type")
	}
	if specReplicasPath != ".spec.nodeCount" {
		t.Errorf("Expected specReplicasPath='.spec.nodeCount', got '%s'", specReplicasPath)
	}

	statusReplicasPath, ok := scale["statusReplicasPath"].(string)
	if !ok {
		t.Fatal("statusReplicasPath not found or invalid type")
	}
	if statusReplicasPath != ".status.currentReplicas" {
		t.Errorf("Expected statusReplicasPath='.status.currentReplicas', got '%s'", statusReplicasPath)
	}

	labelSelectorPath, ok := scale["labelSelectorPath"].(string)
	if !ok {
		t.Fatal("labelSelectorPath not found or invalid type")
	}
	if labelSelectorPath != ".status.selector" {
		t.Errorf("Expected labelSelectorPath='.status.selector', got '%s'", labelSelectorPath)
	}
}
