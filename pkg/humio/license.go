package humio

import (
	"fmt"
	"strconv"

	"gopkg.in/square/go-jose.v2/jwt"

	humioapi "github.com/humio/cli/api"
)

type license struct {
	IDVal        string `json:"uid,omitempty"`
	ExpiresAtVal int    `json:"exp,omitempty"`
	IssuedAtVal  int    `json:"iat,omitempty"`
}

func (l *license) ID() string {
	return l.IDVal
}

func (l *license) IssuedAt() string {
	return strconv.Itoa(l.IssuedAtVal)
}

func (l license) ExpiresAt() string {
	return strconv.Itoa(l.ExpiresAtVal)
}

func (l license) LicenseType() string {
	if l.IDVal == "" {
		return "trial"
	}
	return "onprem"
}

func ParseLicense(licenseString string) (humioapi.License, error) {
	trialLicense, onPremLicense, err := ParseLicenseType(licenseString)
	if trialLicense != nil {
		return &humioapi.TrialLicense{
			ExpiresAtVal: trialLicense.ExpiresAtVal,
			IssuedAtVal:  trialLicense.IssuedAtVal,
		}, nil
	}
	if onPremLicense != nil {
		return &humioapi.OnPremLicense{
			ID:           onPremLicense.ID,
			ExpiresAtVal: onPremLicense.ExpiresAtVal,
			IssuedAtVal:  onPremLicense.IssuedAtVal,
		}, nil
	}
	return nil, fmt.Errorf("invalid license: %s", err)
}

func ParseLicenseType(licenseString string) (*humioapi.TrialLicense, *humioapi.OnPremLicense, error) {
	licenseContent := &license{}

	token, err := jwt.ParseSigned(licenseString)
	if err != nil {
		return nil, nil, fmt.Errorf("error when parsing license: %s", err)
	}
	err = token.UnsafeClaimsWithoutVerification(&licenseContent)
	if err != nil {
		return nil, nil, fmt.Errorf("error when parsing license: %s", err)
	}

	if licenseContent.LicenseType() == "trial" {
		return &humioapi.TrialLicense{
			ExpiresAtVal: strconv.Itoa(licenseContent.ExpiresAtVal),
			IssuedAtVal:  strconv.Itoa(licenseContent.IssuedAtVal),
		}, nil, nil
	}
	if licenseContent.LicenseType() == "onprem" {
		return nil, &humioapi.OnPremLicense{
			ID:           licenseContent.IDVal,
			ExpiresAtVal: strconv.Itoa(licenseContent.ExpiresAtVal),
			IssuedAtVal:  strconv.Itoa(licenseContent.IssuedAtVal),
		}, nil
	}

	return nil, nil, fmt.Errorf("invalid license type: %s", licenseContent.LicenseType())
}
