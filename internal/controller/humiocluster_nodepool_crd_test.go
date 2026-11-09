package controller

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestHumioNodePoolCRDManifestGeneration validates the generated CRD manifest
// contains correct scale subresource paths and structure per DESIGN.md requirements.
func TestHumioNodePoolCRDManifestGeneration(t *testing.T) {
	// Locate project root (walk up from this test file)
	projectRoot, err := findProjectRoot()
	if err != nil {
		t.Fatalf("failed to find project root: %v", err)
	}

	crdPath := filepath.Join(projectRoot, "config/crd/bases/core.humio.com_humionodepools.yaml")

	// Read CRD manifest
	data, err := os.ReadFile(crdPath) //nolint:gosec // test reads from known project path
	if err != nil {
		t.Fatalf("failed to read CRD manifest at %s: %v", crdPath, err)
	}

	// Parse YAML
	var crd map[string]interface{}
	if err := yaml.Unmarshal(data, &crd); err != nil {
		t.Fatalf("failed to parse CRD YAML: %v", err)
	}

	// Navigate to spec.subresources.scale
	spec, ok := crd["spec"].(map[string]interface{})
	if !ok {
		t.Fatal("CRD missing 'spec' field")
	}

	versions, ok := spec["versions"].([]interface{})
	if !ok || len(versions) == 0 {
		t.Fatal("CRD missing 'spec.versions' array")
	}

	// Check first version (v1alpha1)
	version := versions[0].(map[string]interface{})
	subresources, ok := version["subresources"].(map[string]interface{})
	if !ok {
		t.Fatal("CRD version missing 'subresources' field")
	}

	// Verify scale subresource exists
	scale, ok := subresources["scale"].(map[string]interface{})
	if !ok {
		t.Fatal("CRD subresources missing 'scale' field")
	}

	// TEST: specReplicasPath must be .spec.nodeCount (flat, not nested)
	specReplicasPath, ok := scale["specReplicasPath"].(string)
	if !ok {
		t.Fatal("scale subresource missing 'specReplicasPath' field")
	}
	if specReplicasPath != ".spec.nodeCount" {
		t.Errorf("scale.specReplicasPath = %q, want %q (must be flat, not nested)", specReplicasPath, ".spec.nodeCount")
	}

	// TEST: Must not contain nested path .spec.spec.nodeCount
	if strings.Contains(specReplicasPath, ".spec.spec.nodeCount") {
		t.Errorf("scale.specReplicasPath contains forbidden nested path '.spec.spec.nodeCount'")
	}

	// TEST: statusReplicasPath must be .status.currentReplicas
	statusReplicasPath, ok := scale["statusReplicasPath"].(string)
	if !ok {
		t.Fatal("scale subresource missing 'statusReplicasPath' field")
	}
	if statusReplicasPath != ".status.currentReplicas" {
		t.Errorf("scale.statusReplicasPath = %q, want %q", statusReplicasPath, ".status.currentReplicas")
	}

	// TEST: labelSelectorPath must be .status.selector
	labelSelectorPath, ok := scale["labelSelectorPath"].(string)
	if !ok {
		t.Fatal("scale subresource missing 'labelSelectorPath' field")
	}
	if labelSelectorPath != ".status.selector" {
		t.Errorf("scale.labelSelectorPath = %q, want %q", labelSelectorPath, ".status.selector")
	}

	// TEST: Verify status subresource exists
	if _, ok := subresources["status"].(map[string]interface{}); !ok {
		t.Error("CRD subresources missing 'status' field")
	}
}

// TestHumioNodePoolRBACGeneration validates the generated ClusterRole manifest
// contains permissions for humionodepools, humionodepools/status, and humionodepools/scale
// per DESIGN.md RBAC requirements.
func TestHumioNodePoolRBACGeneration(t *testing.T) {
	// Locate project root
	projectRoot, err := findProjectRoot()
	if err != nil {
		t.Fatalf("failed to find project root: %v", err)
	}

	rolePath := filepath.Join(projectRoot, "config/rbac/role.yaml")

	// Read ClusterRole manifest
	data, err := os.ReadFile(rolePath) //nolint:gosec // test reads from known project path
	if err != nil {
		t.Fatalf("failed to read ClusterRole manifest at %s: %v", rolePath, err)
	}

	// Parse YAML (may contain multiple documents)
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	var clusterRole map[string]interface{}

	// Find ClusterRole document
	for {
		var doc map[string]interface{}
		if err := decoder.Decode(&doc); err != nil {
			break
		}
		if kind, ok := doc["kind"].(string); ok && kind == "ClusterRole" {
			clusterRole = doc
			break
		}
	}

	if clusterRole == nil {
		t.Fatal("ClusterRole document not found in role.yaml")
	}

	// Extract rules
	rules, ok := clusterRole["rules"].([]interface{})
	if !ok {
		t.Fatal("ClusterRole missing 'rules' field")
	}

	// TEST: Find humionodepools resource rules
	var humioNodePoolRule map[string]interface{}
	var humioNodePoolStatusRule map[string]interface{}
	var humioNodePoolScaleRule map[string]interface{}

	for _, r := range rules {
		rule := r.(map[string]interface{})
		resources, ok := rule["resources"].([]interface{})
		if !ok {
			continue
		}

		for _, res := range resources {
			resStr := res.(string)
			switch resStr {
			case "humionodepools":
				humioNodePoolRule = rule
			case "humionodepools/status":
				humioNodePoolStatusRule = rule
			case "humionodepools/scale":
				humioNodePoolScaleRule = rule
			}
		}
	}

	// TEST: humionodepools resource must exist with CRUD verbs
	if humioNodePoolRule == nil {
		t.Error("ClusterRole missing rule for 'humionodepools' resource")
	} else {
		verbs := extractVerbs(humioNodePoolRule)
		requiredVerbs := []string{"get", "list", "watch", "create", "update", "patch", "delete"}
		for _, verb := range requiredVerbs {
			if !containsVerb(verbs, verb) {
				t.Errorf("humionodepools rule missing verb %q (has: %v)", verb, verbs)
			}
		}
	}

	// TEST: humionodepools/status resource must exist with status verbs
	if humioNodePoolStatusRule == nil {
		t.Error("ClusterRole missing rule for 'humionodepools/status' resource")
	} else {
		verbs := extractVerbs(humioNodePoolStatusRule)
		requiredVerbs := []string{"get", "update", "patch"}
		for _, verb := range requiredVerbs {
			if !containsVerb(verbs, verb) {
				t.Errorf("humionodepools/status rule missing verb %q (has: %v)", verb, verbs)
			}
		}
	}

	// TEST: humionodepools/scale resource must exist with scale verbs
	if humioNodePoolScaleRule == nil {
		t.Error("ClusterRole missing rule for 'humionodepools/scale' resource")
	} else {
		verbs := extractVerbs(humioNodePoolScaleRule)
		requiredVerbs := []string{"get", "update", "patch"}
		for _, verb := range requiredVerbs {
			if !containsVerb(verbs, verb) {
				t.Errorf("humionodepools/scale rule missing verb %q (has: %v)", verb, verbs)
			}
		}
	}
}

// findProjectRoot walks up from the current directory to find go.mod
func findProjectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", os.ErrNotExist
}

// extractVerbs extracts verb strings from a ClusterRole rule
func extractVerbs(rule map[string]interface{}) []string {
	verbsIface, ok := rule["verbs"].([]interface{})
	if !ok {
		return nil
	}

	var verbs []string
	for _, v := range verbsIface {
		if vStr, ok := v.(string); ok {
			verbs = append(verbs, vStr)
		}
	}
	return verbs
}

// containsVerb checks if a string slice contains a value
func containsVerb(slice []string, val string) bool {
	for _, item := range slice {
		if item == val {
			return true
		}
	}
	return false
}
