package controllers

import (
	"fmt"
	"strings"

	"github.com/Masterminds/semver"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
)

const (
	HumioVersionMinimumSupported = "1.28.0"
	HumioVersionWithNewTmpDir    = "1.33.0"
)

type HumioVersion struct {
	assumeLatest bool
	version      *semver.Version
}

func HumioVersionFromCluster(hc *humiov1alpha1.HumioCluster) (*HumioVersion, error) {
	var humioVersion HumioVersion
	clusterImage := strings.SplitN(hc.Spec.Image, ":", 2)

	// if there is no docker tag, then we can assume latest
	if len(clusterImage) == 1 {
		humioVersion.assumeLatest = true
		return &humioVersion, nil
	}

	if clusterImage[1] == "latest" || clusterImage[1] == "master" {
		humioVersion.assumeLatest = true
		return &humioVersion, nil
	}

	// strip commit SHA if it exists
	clusterImage = strings.SplitN(clusterImage[1], "-", 2)

	clusterImageVersion, err := semver.NewVersion(clusterImage[0])
	if err != nil {
		return &humioVersion, err
	}

	humioVersion.version = clusterImageVersion
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
