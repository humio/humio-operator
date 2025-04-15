package versions

import (
	"strings"

	"github.com/humio/humio-operator/internal/helpers"
)

const (
	defaultHelperImageVersion = "humio/humio-operator-helper:18b8d8df927ae03ead82162ba8f1171960c1b275"
	defaultHumioImageVersion  = "humio/humio-core:1.159.1"

	oldSupportedHumioVersion   = "humio/humio-core:1.130.0"
	upgradeJumpHumioVersion    = "humio/humio-core:1.142.3"
	oldUnsupportedHumioVersion = "humio/humio-core:1.18.4"

	upgradeHelperImageVersion = "humio/humio-operator-helper:master"

	upgradePatchBestEffortOldVersion = "humio/humio-core:1.136.1"
	upgradePatchBestEffortNewVersion = "humio/humio-core:1.136.2"

	upgradeRollingBestEffortVersionJumpOldVersion = "humio/humio-core:1.136.1"
	upgradeRollingBestEffortVersionJumpNewVersion = "humio/humio-core:1.142.3"

	sidecarWaitForGlobalImageVersion = "alpine:20240329"

	defaultPDFRenderServiceImage = "humio/pdf-render-service:0.0.60--build-102--sha-c8eb95329236ba5fc65659b83af1d84b4703cb1e"

	dummyImageSuffix = "-dummy"
)

func DefaultHelperImageVersion() string {
	version := []string{defaultHelperImageVersion}
	if helpers.UseDummyImage() {
		version = append(version, dummyImageSuffix)
	}
	return strings.Join(version, "")
}
func DefaultHumioImageVersion() string {
	version := []string{defaultHumioImageVersion}
	if helpers.UseDummyImage() {
		version = append(version, dummyImageSuffix)
	}
	return strings.Join(version, "")
}
func OldSupportedHumioVersion() string {
	version := []string{oldSupportedHumioVersion}
	if helpers.UseDummyImage() {
		version = append(version, dummyImageSuffix)
	}
	return strings.Join(version, "")
}
func UpgradeJumpHumioVersion() string {
	version := []string{upgradeJumpHumioVersion}
	if helpers.UseDummyImage() {
		version = append(version, dummyImageSuffix)
	}
	return strings.Join(version, "")
}
func OldUnsupportedHumioVersion() string {
	version := []string{oldUnsupportedHumioVersion}
	if helpers.UseDummyImage() {
		version = append(version, dummyImageSuffix)
	}
	return strings.Join(version, "")
}
func UpgradeHelperImageVersion() string {
	version := []string{upgradeHelperImageVersion}
	if helpers.UseDummyImage() {
		version = append(version, dummyImageSuffix)
	}
	return strings.Join(version, "")
}
func UpgradePatchBestEffortOldVersion() string {
	version := []string{upgradePatchBestEffortOldVersion}
	if helpers.UseDummyImage() {
		version = append(version, dummyImageSuffix)
	}
	return strings.Join(version, "")
}
func UpgradePatchBestEffortNewVersion() string {
	version := []string{upgradePatchBestEffortNewVersion}
	if helpers.UseDummyImage() {
		version = append(version, dummyImageSuffix)
	}
	return strings.Join(version, "")
}
func UpgradeRollingBestEffortVersionJumpOldVersion() string {
	version := []string{upgradeRollingBestEffortVersionJumpOldVersion}
	if helpers.UseDummyImage() {
		version = append(version, dummyImageSuffix)
	}
	return strings.Join(version, "")
}
func UpgradeRollingBestEffortVersionJumpNewVersion() string {
	version := []string{upgradeRollingBestEffortVersionJumpNewVersion}
	if helpers.UseDummyImage() {
		version = append(version, dummyImageSuffix)
	}
	return strings.Join(version, "")
}
func SidecarWaitForGlobalImageVersion() string {
	return sidecarWaitForGlobalImageVersion
}

func DefaultPDFRenderServiceImage() string {
	version := []string{defaultPDFRenderServiceImage}
	if helpers.UseDummyImage() {
		version = append(version, dummyImageSuffix)
	}
	return strings.Join(version, "")
}
