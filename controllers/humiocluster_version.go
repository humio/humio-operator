package controllers

import (
	"fmt"
	"strings"

	"github.com/Masterminds/semver/v3"
)

const (
	HumioVersionMinimumSupported                 = "1.70.0"
	HumioVersionWithoutOldVhostSelection         = "1.80.0"
	HumioVersionWithAutomaticPartitionManagement = "1.89.0"
)

type HumioVersion struct {
	assumeLatest bool
	version      *semver.Version
}

func HumioVersionFromString(image string) (*HumioVersion, error) {
	var humioVersion HumioVersion
	nodeImage := strings.SplitN(image, "@", 2)
	nodeImage = strings.SplitN(nodeImage[0], ":", 2)

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

func (hv *HumioVersion) SemVer() *semver.Version {
	return hv.version
}

func (hv *HumioVersion) IsLatest() bool {
	return hv.assumeLatest
}

func (hv *HumioVersion) constraint(constraintStr string) (bool, error) {
	constraint, err := semver.NewConstraint(constraintStr)
	return constraint.Check(hv.version), err
}

func (hv *HumioVersion) String() string {
	return hv.SemVer().String()
}
