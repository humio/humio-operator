package humio

import (
	"fmt"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

type license struct {
	UID string `json:"uid,omitempty"`
}

// JWTLicenseFields contains JWT-specific fields that are NOT available through GraphQL API
type JWTLicenseFields struct {
	// JWT-exclusive fields (not exposed via GraphQL for security reasons)
	MaxIngestGbPerDay *float64 `json:"maxIngestGbPerDay,omitempty"` // Daily ingestion limit in GB - KEY FIELD!
	MaxCores          *int     `json:"maxCores,omitempty"`          // Maximum CPU cores allowed
	ValidUntil        *int64   `json:"validUntil,omitempty"`        // License validity end (separate from JWT exp)
	Subject           string   `json:"sub,omitempty"`               // License holder/subject name

	// For verification against GraphQL data
	UID string `json:"uid,omitempty"` // Must match GraphQL UID for validation
}

// GetLicenseUIDFromLicenseString parses the user-specified license string and returns the id of the license
func GetLicenseUIDFromLicenseString(licenseString string) (string, error) {
	token, err := jwt.ParseSigned(licenseString, []jose.SignatureAlgorithm{jose.ES256, jose.ES512})
	if err != nil {
		return "", fmt.Errorf("error when parsing license: %w", err)
	}

	licenseContent := &license{}
	err = token.UnsafeClaimsWithoutVerification(&licenseContent)
	if err != nil {
		return "", fmt.Errorf("error when parsing license: %w", err)
	}
	if licenseContent.UID == "" {
		return "", fmt.Errorf("error when parsing license, license was valid jwt string but missing uid")
	}

	return licenseContent.UID, nil
}

// GetJWTLicenseFields extracts JWT-specific fields that are not available through GraphQL
// Returns nil if JWT parsing fails - caller should handle gracefully
func GetJWTLicenseFields(licenseString string) (*JWTLicenseFields, error) {
	token, err := jwt.ParseSigned(licenseString, []jose.SignatureAlgorithm{jose.ES256, jose.ES512})
	if err != nil {
		return nil, fmt.Errorf("error parsing license JWT: %w", err)
	}

	jwtFields := &JWTLicenseFields{}
	err = token.UnsafeClaimsWithoutVerification(jwtFields)
	if err != nil {
		return nil, fmt.Errorf("error extracting JWT license claims: %w", err)
	}

	if jwtFields.UID == "" {
		return nil, fmt.Errorf("JWT license missing required uid field")
	}

	return jwtFields, nil
}
