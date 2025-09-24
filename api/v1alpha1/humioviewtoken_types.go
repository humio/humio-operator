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

// HumioViewTokenSpec defines the desired state of HumioViewToken
// +kubebuilder:validation:XValidation:rule="(has(self.managedClusterName) && self.managedClusterName != \"\") != (has(self.externalClusterName) && self.externalClusterName != \"\")",message="Must specify exactly one of managedClusterName or externalClusterName"
type HumioViewTokenSpec struct {
	HumioTokenSpec `json:",inline"`
	// ViewNames is the Humio list of View names for the token.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=100
	// +kubebuilder:validation:XValidation:rule="self.all(item, size(item) >= 1 && size(item) <= 253)",message="viewNames: each item must be 1-253 characters long"
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	// +kubebuilder:validation:Required
	ViewNames []string `json:"viewNames"`
}

// HumioViewTokenStatus defines the observed state of HumioViewToken.
type HumioViewTokenStatus struct {
	HumioTokenStatus `json:",inline"`
}

// HumioViewToken is the Schema for the humioviewtokens API
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=humioviewtokens,scope=Namespaced
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state",description="The state of the View Token"
// +kubebuilder:printcolumn:name="HumioID",type="string",JSONPath=".status.humioId",description="Humio generated ID"
// +operator-sdk:gen-csv:customresourcedefinitions.displayName="Humio View Token"
type HumioViewToken struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec   HumioViewTokenSpec   `json:"spec"`
	Status HumioViewTokenStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HumioViewTokenList contains a list of HumioViewToken
type HumioViewTokenList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioViewToken `json:"items"`
}

// GetSpec returns the configured Spec for the token
func (hvt *HumioViewToken) GetSpec() *HumioTokenSpec {
	return &hvt.Spec.HumioTokenSpec
}

// GetStatus returns the configured Status for the token
func (hvt *HumioViewToken) GetStatus() *HumioTokenStatus {
	return &hvt.Status.HumioTokenStatus
}

func init() {
	SchemeBuilder.Register(&HumioViewToken{}, &HumioViewTokenList{})
}
