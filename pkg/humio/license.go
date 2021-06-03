package humio

import (
	"fmt"
	"time"

	"gopkg.in/square/go-jose.v2/jwt"

	humioapi "github.com/humio/cli/api"
)

type license struct {
	IDVal         string `json:"uid,omitempty"`
	ValidUntilVal int    `json:"validUntil,omitempty"`
	IssuedAtVal   int    `json:"iat,omitempty"`
}

func (l license) LicenseType() string {
	if l.IDVal == "" {
		return "trial"
	}
	return "onprem"
}

func ParseLicense(licenseString string) (humioapi.License, error) {
	onPremLicense, err := ParseLicenseType(licenseString)
	if onPremLicense != nil {
		return &humioapi.OnPremLicense{
			ID:           onPremLicense.ID,
			ExpiresAtVal: onPremLicense.ExpiresAtVal,
			IssuedAtVal:  onPremLicense.IssuedAtVal,
		}, nil
	}
	return nil, fmt.Errorf("invalid license: %s", err)
}

func ParseLicenseType(licenseString string) (*humioapi.OnPremLicense, error) {
	licenseContent := &license{}

	token, err := jwt.ParseSigned(licenseString)
	if err != nil {
		return nil, fmt.Errorf("error when parsing license: %s", err)
	}
	err = token.UnsafeClaimsWithoutVerification(&licenseContent)
	if err != nil {
		return nil, fmt.Errorf("error when parsing license: %s", err)
	}

	locUTC, _ := time.LoadLocation("UTC")

	expiresAtVal := time.Unix(int64(licenseContent.ValidUntilVal), 0).In(locUTC)
	issuedAtVal := time.Unix(int64(licenseContent.IssuedAtVal), 0).In(locUTC)

	if licenseContent.LicenseType() == "onprem" {
		return &humioapi.OnPremLicense{
			ID:           licenseContent.IDVal,
			ExpiresAtVal: expiresAtVal.Format(time.RFC3339),
			IssuedAtVal:  issuedAtVal.Format(time.RFC3339),
		}, nil
	}

	return nil, fmt.Errorf("invalid license type: %s", licenseContent.LicenseType())
}
