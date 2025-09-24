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

// HumioSystemTokenSpec defines the desired state of HumioSystemToken
// +kubebuilder:validation:XValidation:rule="(has(self.managedClusterName) && self.managedClusterName != \"\") != (has(self.externalClusterName) && self.externalClusterName != \"\")",message="Must specify exactly one of managedClusterName or externalClusterName"
type HumioSystemTokenSpec struct {
	HumioTokenSpec `json:",inline"`
}

// HumioSystemTokenStatus defines the observed state of HumioSystemToken.
type HumioSystemTokenStatus struct {
	HumioTokenStatus `json:",inline"`
}

// HumioSystemToken is the Schema for the humiosystemtokens API
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=humiosystemtokens,scope=Namespaced
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state",description="The state of the System Token"
// +kubebuilder:printcolumn:name="HumioID",type="string",JSONPath=".status.humioId",description="Humio generated ID"
// +operator-sdk:gen-csv:customresourcedefinitions.displayName="Humio System Token"
type HumioSystemToken struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec   HumioSystemTokenSpec   `json:"spec"`
	Status HumioSystemTokenStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HumioSystemTokenList contains a list of HumioSystemToken
type HumioSystemTokenList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioSystemToken `json:"items"`
}

// GetSpec returns the configured Spec for the token
func (hst *HumioSystemToken) GetSpec() *HumioTokenSpec {
	return &hst.Spec.HumioTokenSpec
}

// GetStatus returns the configured Status for the token
func (hst *HumioSystemToken) GetStatus() *HumioTokenStatus {
	return &hst.Status.HumioTokenStatus
}

func init() {
	SchemeBuilder.Register(&HumioSystemToken{}, &HumioSystemTokenList{})
}
