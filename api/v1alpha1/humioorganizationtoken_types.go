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

// HumioOrganizationTokenSpec defines the desired state of HumioOrganizationToken
// +kubebuilder:validation:XValidation:rule="(has(self.managedClusterName) && self.managedClusterName != \"\") != (has(self.externalClusterName) && self.externalClusterName != \"\")",message="Must specify exactly one of managedClusterName or externalClusterName"
type HumioOrganizationTokenSpec struct {
	HumioTokenSpec `json:",inline"`
}

// HumioOrganizationTokenStatus defines the observed state of HumioOrganizationToken.
type HumioOrganizationTokenStatus struct {
	HumioTokenStatus `json:",inline"`
}

// HumioOrganizationToken is the Schema for the humioOrganizationtokens API
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=humioorganizationtokens,scope=Namespaced
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state",description="The state of the Organization Token"
// +kubebuilder:printcolumn:name="HumioID",type="string",JSONPath=".status.humioId",description="Humio generated ID"
// +operator-sdk:gen-csv:customresourcedefinitions.displayName="Humio Organization Token"
type HumioOrganizationToken struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec   HumioOrganizationTokenSpec   `json:"spec"`
	Status HumioOrganizationTokenStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HumioOrganizationTokenList contains a list of HumioOrganizationToken
type HumioOrganizationTokenList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioOrganizationToken `json:"items"`
}

// GetSpec returns the configured Spec for the token
func (hot *HumioOrganizationToken) GetSpec() *HumioTokenSpec {
	return &hot.Spec.HumioTokenSpec
}

// GetStatus returns the configured Status for the token
func (hot *HumioOrganizationToken) GetStatus() *HumioTokenStatus {
	return &hot.Status.HumioTokenStatus
}

func init() {
	SchemeBuilder.Register(&HumioOrganizationToken{}, &HumioOrganizationTokenList{})
}
