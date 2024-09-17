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

// HumioRoleBindingSpec defines the desired state of HumioRoleBinding
type HumioRoleBindingSpec struct {
	// Fetch details:
	//   Query.searchDomain.usersAndGroups
	// If Group:
	//   - Mutation.assignRoleToGroup
	//       input:
	//         viewId: String!
	//         groupId: String!
	//         roleId: String!
	//         overrideExistingAssignmentsForView: Boolean
	//   - Mutation.unassignRoleFromGroup
	//       input:
	//         viewId: String!
	//         groupId: String!
	//         roleId: String!
	//   - Mutation.updateQueryPrefix
	//       input:
	//         queryPrefix: String!
	//         viewId: String!
	//         groupId: String!
	//   - Mutation.changeUserAndGroupRolesForSearchDomain
	//       searchDomainId: String!
	//       groups: [GroupRoleAssignment!]!
	//       users: [UserRoleAssignment!]!
	// If User:
	//   - Mutation.assignUserRolesInSearchDomain
	//       input:
	//         searchDomainId: String!
	//         roleAssignments: [UserRoleAssignmentInput!]!
	//   - Mutation.unassignUserRoleForSearchDomain
	//       input:
	//         userId: String!
	//         searchDomainId: String!
	//         roleId: String
	//   - Mutation.changeUserAndGroupRolesForSearchDomain
	//       searchDomainId: String!
	//       groups: [GroupRoleAssignment!]!
	//       users: [UserRoleAssignment!]!
	// TODO: Can we assign the same role twice but with different queryPrefixes?
	// TODO: Org. root and cluster root? Maybe we create HumioUser with booleans for each?
	Subjects []Subjects `json:"subjects,omitempty"`
	// RoleName is the name of the LogScale role to assign
	RoleName string `json:"roleName"`
}

type Subjects struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// HumioRoleBindingStatus defines the observed state of HumioRoleBinding
type HumioRoleBindingStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// HumioRoleBinding is the Schema for the humiorolebindings API
type HumioRoleBinding struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HumioRoleBindingSpec   `json:"spec,omitempty"`
	Status HumioRoleBindingStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// HumioRoleBindingList contains a list of HumioRoleBinding
type HumioRoleBindingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioRoleBinding `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioRoleBinding{}, &HumioRoleBindingList{})
}
