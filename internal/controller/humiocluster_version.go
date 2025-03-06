package controller

import (
	"fmt"
	"strings"

	"github.com/Masterminds/semver/v3"
)

const (
	HumioVersionMinimumSupported = "1.130.0"
)

type HumioVersion struct {
	assumeLatest bool
	version      *semver.Version
}

func HumioVersionFromString(image string) *HumioVersion {
	var humioVersion HumioVersion
	nodeImage := strings.SplitN(image, "@", 2)
	nodeImage = strings.SplitN(nodeImage[0], ":", 2)

	// if there is no docker tag, then we can assume latest
	if len(nodeImage) == 1 {
		humioVersion.assumeLatest = true
		return &humioVersion
	}

	// strip commit SHA if it exists
	nodeImage = strings.SplitN(nodeImage[1], "-", 2)

	nodeImageVersion, err := semver.NewVersion(nodeImage[0])
	humioVersion.version = nodeImageVersion
	if err != nil {
		// since image does not include any version hints, we assume bleeding edge version
		humioVersion.assumeLatest = true
		return &humioVersion
	}

	return &humioVersion
}

func (hv *HumioVersion) AtLeast(version string) (bool, error) {
	if hv.assumeLatest {
		return true, nil
	}

	return hv.constraint(fmt.Sprintf(">= %s", version))
}

func (hv *HumioVersion) SemVer() *semver.Version {
	return hv.version
}

func (hv *HumioVersion) IsLatest() bool {
	return hv.assumeLatest
}

func (hv *HumioVersion) constraint(constraintStr string) (bool, error) {
	constraint, err := semver.NewConstraint(constraintStr)
	if err != nil {
		return false, fmt.Errorf("could not parse constraint of `%s`: %w", constraintStr, err)
	}

	return constraint.Check(hv.version), nil
}

func (hv *HumioVersion) String() string {
	return hv.SemVer().String()
}
