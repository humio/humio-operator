package versions

import (
	"os"
	"strings"
)

const (
	defaultHelperImageVersion = "humio/humio-operator-helper:8f5ef6c7e470226e77d985f36cf39be9a100afea"
	defaultHumioImageVersion  = "humio/humio-core:1.142.3"

	oldSupportedHumioVersion   = "humio/humio-core:1.118.0"
	upgradeJumpHumioVersion    = "humio/humio-core:1.128.0"
	oldUnsupportedHumioVersion = "humio/humio-core:1.18.4"

	upgradeHelperImageVersion = "humio/humio-operator-helper:master"

	upgradePatchBestEffortOldVersion = "humio/humio-core:1.124.1"
	upgradePatchBestEffortNewVersion = "humio/humio-core:1.124.2"

	upgradeRollingBestEffortVersionJumpOldVersion = "humio/humio-core:1.124.1"
	upgradeRollingBestEffortVersionJumpNewVersion = "humio/humio-core:1.131.1"

	sidecarWaitForGlobalImageVersion = "alpine:20240329"

	dummyImageSuffix = "-dummy"
)

func DefaultHelperImageVersion() string {
	version := []string{defaultHelperImageVersion}
	if os.Getenv("DUMMY_LOGSCALE_IMAGE") == "true" {
		version = append(version, dummyImageSuffix)
	}
	return strings.Join(version, "")
}
func DefaultHumioImageVersion() string {
	version := []string{defaultHumioImageVersion}
	if os.Getenv("DUMMY_LOGSCALE_IMAGE") == "true" {
		version = append(version, dummyImageSuffix)
	}
	return strings.Join(version, "")
}
func OldSupportedHumioVersion() string {
	version := []string{oldSupportedHumioVersion}
	if os.Getenv("DUMMY_LOGSCALE_IMAGE") == "true" {
		version = append(version, dummyImageSuffix)
	}
	return strings.Join(version, "")
}
func UpgradeJumpHumioVersion() string {
	version := []string{upgradeJumpHumioVersion}
	if os.Getenv("DUMMY_LOGSCALE_IMAGE") == "true" {
		version = append(version, dummyImageSuffix)
	}
	return strings.Join(version, "")
}
func OldUnsupportedHumioVersion() string {
	version := []string{oldUnsupportedHumioVersion}
	if os.Getenv("DUMMY_LOGSCALE_IMAGE") == "true" {
		version = append(version, dummyImageSuffix)
	}
	return strings.Join(version, "")
}
func UpgradeHelperImageVersion() string {
	version := []string{upgradeHelperImageVersion}
	if os.Getenv("DUMMY_LOGSCALE_IMAGE") == "true" {
		version = append(version, dummyImageSuffix)
	}
	return strings.Join(version, "")
}
func UpgradePatchBestEffortOldVersion() string {
	version := []string{upgradePatchBestEffortOldVersion}
	if os.Getenv("DUMMY_LOGSCALE_IMAGE") == "true" {
		version = append(version, dummyImageSuffix)
	}
	return strings.Join(version, "")
}
func UpgradePatchBestEffortNewVersion() string {
	version := []string{upgradePatchBestEffortNewVersion}
	if os.Getenv("DUMMY_LOGSCALE_IMAGE") == "true" {
		version = append(version, dummyImageSuffix)
	}
	return strings.Join(version, "")
}
func UpgradeRollingBestEffortVersionJumpOldVersion() string {
	version := []string{upgradeRollingBestEffortVersionJumpOldVersion}
	if os.Getenv("DUMMY_LOGSCALE_IMAGE") == "true" {
		version = append(version, dummyImageSuffix)
	}
	return strings.Join(version, "")
}
func UpgradeRollingBestEffortVersionJumpNewVersion() string {
	version := []string{upgradeRollingBestEffortVersionJumpNewVersion}
	if os.Getenv("DUMMY_LOGSCALE_IMAGE") == "true" {
		version = append(version, dummyImageSuffix)
	}
	return strings.Join(version, "")
}
func SidecarWaitForGlobalImageVersion() string {
	return sidecarWaitForGlobalImageVersion
}
