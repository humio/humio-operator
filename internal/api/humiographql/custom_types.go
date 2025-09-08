package humiographql

// TokenSecurityPolicies represents the input structure for updating token security policies
type TokenSecurityPolicies struct {
	PersonalUserTokensEnabled                          bool `json:"personalUserTokensEnabled"`
	ViewPermissionTokensEnabled                        bool `json:"viewPermissionTokensEnabled"`
	OrganizationPermissionTokensEnabled                bool `json:"organizationPermissionTokensEnabled"`
	SystemPermissionTokensEnabled                      bool `json:"systemPermissionTokensEnabled"`
	ViewPermissionTokensAllowPermissionUpdates         bool `json:"viewPermissionTokensAllowPermissionUpdates"`
	OrganizationPermissionTokensAllowPermissionUpdates bool `json:"organizationPermissionTokensAllowPermissionUpdates"`
	SystemPermissionTokensAllowPermissionUpdates       bool `json:"systemPermissionTokensAllowPermissionUpdates"`
}
