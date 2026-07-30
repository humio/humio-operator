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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// DependencyCheckConditionTypeConfigured indicates the dependency check is configured
	DependencyCheckConditionTypeConfigured = "Configured"
	// DependencyCheckReasonConfigured indicates the check was configured via auto-discovery
	DependencyCheckReasonConfigured = "Configured"
)

// HumioDependencyCheckSpec defines the desired state of HumioDependencyCheck
type HumioDependencyCheckSpec struct {
	// ManagedClusterName refers to the HumioCluster that owns this dependency check
	// +kubebuilder:validation:MinLength=1
	ManagedClusterName string `json:"managedClusterName"`
	// CheckType is the type of dependency check (kafka, s3, gcs)
	// +kubebuilder:validation:Enum=kafka;s3;gcs
	CheckType string `json:"checkType"`
	// NodePoolName is the node pool this check belongs to, empty for default pool
	NodePoolName string `json:"nodePoolName,omitempty"`
}

// HumioDependencyCheckStatus defines the observed state of HumioDependencyCheck
type HumioDependencyCheckStatus struct {
	// Conditions represent the latest available observations of the resource's state
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
	// DiscoverySource indicates how this check was discovered (e.g. "auto")
	DiscoverySource string `json:"discoverySource,omitempty"`
	// ConfiguredEnvVars lists the environment variables that were forwarded for this check
	ConfiguredEnvVars []string `json:"configuredEnvVars,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=humiodependencychecks,scope=Namespaced,shortName=hdc,categories={humio,all}
// +kubebuilder:printcolumn:name="CheckType",type="string",JSONPath=".spec.checkType",description="The type of dependency check"
// +kubebuilder:printcolumn:name="Cluster",type="string",JSONPath=".spec.managedClusterName",description="The HumioCluster this check belongs to"
// +kubebuilder:printcolumn:name="Source",type="string",JSONPath=".status.discoverySource",description="How the check was discovered"
// +kubebuilder:printcolumn:name="Configured",type="string",JSONPath=".status.conditions[?(@.type=='Configured')].status"
// +operator-sdk:gen-csv:customresourcedefinitions.displayName="Humio Dependency Check"

// HumioDependencyCheck is the Schema for the humiodependencychecks API.
type HumioDependencyCheck struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HumioDependencyCheckSpec   `json:"spec"`
	Status HumioDependencyCheckStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HumioDependencyCheckList contains a list of HumioDependencyCheck.
type HumioDependencyCheckList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioDependencyCheck `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioDependencyCheck{}, &HumioDependencyCheckList{})
}
