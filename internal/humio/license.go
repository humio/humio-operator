package humio

import (
	"fmt"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

type license struct {
	UID string `json:"uid,omitempty"`
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
