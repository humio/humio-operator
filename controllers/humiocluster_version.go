package controllers

import (
	"fmt"
	"strings"

	"github.com/Masterminds/semver"
)

const (
	HumioVersionMinimumSupported = "1.28.0"
	HumioVersionWithNewTmpDir    = "1.33.0"
)

type HumioVersion struct {
	assumeLatest bool
	version      *semver.Version
}

func HumioVersionFromString(image string) (*HumioVersion, error) {
	var humioVersion HumioVersion
	nodeImage := strings.SplitN(image, ":", 2)

	// if there is no docker tag, then we can assume latest
	if len(nodeImage) == 1 {
		humioVersion.assumeLatest = true
		return &humioVersion, nil
	}

	if nodeImage[1] == "latest" || nodeImage[1] == "master" {
		humioVersion.assumeLatest = true
		return &humioVersion, nil
	}

	// strip commit SHA if it exists
	nodeImage = strings.SplitN(nodeImage[1], "-", 2)

	nodeImageVersion, err := semver.NewVersion(nodeImage[0])
	if err != nil {
		return &humioVersion, err
	}

	humioVersion.version = nodeImageVersion
	return &humioVersion, err
}

func (hv *HumioVersion) AtLeast(version string) (bool, error) {
	if hv.assumeLatest {
		return true, nil
	}

	return hv.constraint(fmt.Sprintf(">= %s", version))
}

func (hv *HumioVersion) constraint(constraintStr string) (bool, error) {
	constraint, err := semver.NewConstraint(constraintStr)
	return constraint.Check(hv.version), err
}
