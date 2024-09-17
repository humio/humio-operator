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

// HumioGroupSpec defines the desired state of HumioGroup
type HumioGroupSpec struct {
	// - Mutation.addGroup
	// - Mutation.updateGroup (lookupName = "External provider")
	// - Mutation.removeGroup
	Name             string `json:"name"`
	ExternalProvider string `json:"externalProvider,omitempty"`
}

// HumioGroupStatus defines the observed state of HumioGroup
type HumioGroupStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// HumioGroup is the Schema for the humiogroups API
type HumioGroup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HumioGroupSpec   `json:"spec,omitempty"`
	Status HumioGroupStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// HumioGroupList contains a list of HumioGroup
type HumioGroupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioGroup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioGroup{}, &HumioGroupList{})
}
