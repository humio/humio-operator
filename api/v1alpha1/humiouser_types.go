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

// HumioUserSpec defines the desired state of HumioUser
type HumioUserSpec struct {
	// - Mutation.addUserV2:
	//     input:
	//       username: String!
	//       company: String
	//       isRoot: Boolean
	//       firstName: String
	//       lastName: String
	//       fullName: String
	//       picture: String
	//       email: String
	//       countryCode: String
	//       stateCode: String
	//       sendInvite: Boolean
	//       verificationToken: String
	//       isOrgOwner: Boolean
	// - Mutation.removeUser
	//     input:
	//       username: String!
	// - Mutation.removeUserById
	//     input:
	//       id: String!
	// - Mutation.updateUser
	//     input:
	//       username: String!
	//       company: String
	//       isRoot: Boolean
	//       firstName: String
	//       lastName: String
	//       fullName: String
	//       picture: String
	//       email: String
	//       countryCode: String
	//       stateCode: String
	// - Mutation.updateUserById
	//     input:
	//       userId: String!
	//       company: String
	//       isRoot: Boolean
	//       username: String
	//       firstName: String
	//       lastName: String
	//       fullName: String
	//       picture: String
	//       email: String
	//       countryCode: String
	//       stateCode: String
	// - Mutation.updateOrganizationRoot
	//     userId: String!
	//     organizationRoot: Boolean!
	Username   string `json:"username"`
	IsRoot     *bool  `json:"isRoot"`
	IsOrgOwner *bool  `json:"isOrgOwner"`
}

// HumioUserStatus defines the observed state of HumioUser
type HumioUserStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// HumioUser is the Schema for the humiousers API
type HumioUser struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HumioUserSpec   `json:"spec,omitempty"`
	Status HumioUserStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// HumioUserList contains a list of HumioUser
type HumioUserList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioUser `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioUser{}, &HumioUserList{})
}
