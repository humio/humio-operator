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

// HumioRoleSpec defines the desired state of HumioRole
type HumioRoleSpec struct {
	// - Mutation.createRole
	// - Mutation.updateRole
	// - Mutation.removeRole
	Name              string   `json:"name"`
	ViewPermissions   []string `json:"viewPermissions,omitempty"`
	OrgPermissions    []string `json:"orgPermissions,omitempty"`
	SystemPermissions []string `json:"systemPermissions,omitempty"`
}

// HumioRoleStatus defines the observed state of HumioRole
type HumioRoleStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// HumioRole is the Schema for the humioroles API
type HumioRole struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HumioRoleSpec   `json:"spec,omitempty"`
	Status HumioRoleStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// HumioRoleList contains a list of HumioRole
type HumioRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioRole{}, &HumioRoleList{})
}
