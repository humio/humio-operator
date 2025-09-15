package controller

import (
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestVersions(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Version suite")
}

// Helper functions for consistent error message formatting

// formatVersionParsingMessage formats error messages for version parsing tests
func formatVersionParsingMessage(input, field string, got, expected interface{}) string {
	return fmt.Sprintf("HumioVersionFromString(%s) = got %s %v, expected %s %v", input, field, got, field, expected)
}

// formatVersionComparisonMessage formats error messages for version comparison setup
func formatVersionComparisonMessage(input, got, expected string) string {
	return fmt.Sprintf("HumioVersion.AtLeast(%s) = got %s, expected %s", input, got, expected)
}

// formatAtLeastErrorMessage formats error messages for AtLeast error checks
func formatAtLeastErrorMessage(inputVersion, compareVersion string, gotErr error, expectedErr bool) string {
	return fmt.Sprintf("HumioVersion(%s).AtLeast(%s) = got err %v, expected err %v", inputVersion, compareVersion, gotErr, expectedErr)
}

// formatAtLeastBoolMessage formats error messages for AtLeast boolean result checks
func formatAtLeastBoolMessage(inputVersion, compareVersion string, got, expected bool) string {
	return fmt.Sprintf("HumioVersion(%s).AtLeast(%s) = got %t, expected %t", inputVersion, compareVersion, got, expected)
}

// versionParsingTestCase defines test cases for HumioVersionFromString function
type versionParsingTestCase struct {
	userDefinedImageVersion string // Input Docker image string to parse
	expectedImageVersion    string // Expected semantic version output
	expectedAssumeLatest    bool   // Expected IsLatest() result
}

var _ = DescribeTable("HumioVersionFromString", Label("unit"),
	func(testCase versionParsingTestCase) {
		gotVersion := HumioVersionFromString(testCase.userDefinedImageVersion)

		Expect(testCase.expectedAssumeLatest).To(Equal(gotVersion.IsLatest()),
			formatVersionParsingMessage(testCase.userDefinedImageVersion, "IsLatest", gotVersion.IsLatest(), testCase.expectedAssumeLatest))

		if !testCase.expectedAssumeLatest {
			Expect(testCase.expectedImageVersion).To(Equal(gotVersion.String()),
				formatVersionParsingMessage(testCase.userDefinedImageVersion, "image", gotVersion.String(), testCase.expectedImageVersion))
		}
	},
	Entry("image with container image SHA",
		versionParsingTestCase{
			userDefinedImageVersion: "humio/humio-core-dev:1.70.0--build-1023123--uaihdasiuhdiuahd23792f@sha256:4d545bbd0dc3a22d40188947f569566737657c42e4bd14327598299db2b5a38a",
			expectedImageVersion:    "1.70.0",
			expectedAssumeLatest:    false,
		},
	),
	Entry("image without container image SHA",
		versionParsingTestCase{
			userDefinedImageVersion: "humio/humio-core-dev:1.70.0--build-1023123--uaihdasiuhdiuahd23792f",
			expectedImageVersion:    "1.70.0",
			expectedAssumeLatest:    false,
		},
	),
	Entry("image from github issue https://github.com/humio/humio-operator/issues/615",
		versionParsingTestCase{
			userDefinedImageVersion: "humio/humio-core:1.34.0@sha256:38c78710107dc76f4f809b457328ff1c6764ae4244952a5fa7d76f6e67ea2390",
			expectedImageVersion:    "1.34.0",
			expectedAssumeLatest:    false,
		},
	),
	Entry("short image version",
		versionParsingTestCase{
			userDefinedImageVersion: "humio/humio-core:1.34.0",
			expectedImageVersion:    "1.34.0",
			expectedAssumeLatest:    false,
		},
	),
	Entry("master image tag",
		versionParsingTestCase{
			userDefinedImageVersion: "humio/humio-core:master",
			expectedImageVersion:    "",
			expectedAssumeLatest:    true,
		},
	),
	Entry("preview image tag",
		versionParsingTestCase{
			userDefinedImageVersion: "humio/humio-core:preview",
			expectedImageVersion:    "",
			expectedAssumeLatest:    true,
		},
	),
	Entry("latest image tag",
		versionParsingTestCase{
			userDefinedImageVersion: "humio/humio-core:latest",
			expectedImageVersion:    "",
			expectedAssumeLatest:    true,
		},
	))

// versionComparisonTestCase defines test cases for HumioVersion.AtLeast function
type versionComparisonTestCase struct {
	userDefinedImageVersion string // Input Docker image string to parse
	imageVersionOlder       string // Version that should be older than parsed version
	imageVersionExact       string // Version that should be exactly equal to parsed version
	imageVersionNewer       string // Version that should be newer than parsed version
	expectedErr             bool   // Expected error result
}

var _ = DescribeTable("HumioVersionAtLeast",
	func(testCase versionComparisonTestCase) {
		humioVersion := HumioVersionFromString(testCase.userDefinedImageVersion)

		Expect(testCase.imageVersionExact).To(Equal(humioVersion.String()),
			formatVersionComparisonMessage(testCase.userDefinedImageVersion, humioVersion.String(), testCase.userDefinedImageVersion))

		// Verify current version is newer than older image
		atLeast, err := humioVersion.AtLeast(testCase.imageVersionOlder)
		Expect(testCase.expectedErr).To(Equal(err != nil),
			formatAtLeastErrorMessage(testCase.userDefinedImageVersion, testCase.imageVersionOlder, err, testCase.expectedErr))

		Expect(atLeast).To(BeTrue(),
			formatAtLeastBoolMessage(testCase.userDefinedImageVersion, testCase.imageVersionOlder, atLeast, true))

		// Verify version exactly the same as the specified image is reported as at least the exact
		atLeast, err = humioVersion.AtLeast(testCase.imageVersionExact)
		Expect(testCase.expectedErr).To(Equal(err != nil),
			formatAtLeastErrorMessage(testCase.userDefinedImageVersion, testCase.imageVersionExact, err, testCase.expectedErr))

		Expect(atLeast).To(BeTrue(),
			formatAtLeastBoolMessage(testCase.userDefinedImageVersion, testCase.imageVersionExact, atLeast, true))

		// Verify current version reports false to be AtLeast for images newer
		atLeast, err = humioVersion.AtLeast(testCase.imageVersionNewer)
		Expect(testCase.expectedErr).To(Equal(err != nil),
			formatAtLeastErrorMessage(testCase.userDefinedImageVersion, testCase.imageVersionNewer, err, testCase.expectedErr))

		Expect(atLeast).To(BeFalse(),
			formatAtLeastBoolMessage(testCase.userDefinedImageVersion, testCase.imageVersionNewer, atLeast, false))
	},
	Entry("image with container image SHA",
		versionComparisonTestCase{
			userDefinedImageVersion: "humio/humio-core-dev:1.70.0--build-1023123--uaihdasiuhdiuahd23792f@sha256:4d545bbd0dc3a22d40188947f569566737657c42e4bd14327598299db2b5a38a",
			imageVersionOlder:       "1.69.0",
			imageVersionExact:       "1.70.0",
			imageVersionNewer:       "1.70.1",
			expectedErr:             false,
		}),
	Entry("image without container image SHA",
		versionComparisonTestCase{
			userDefinedImageVersion: "humio/humio-core-dev:1.70.0--build-1023123--uaihdasiuhdiuahd23792f",
			imageVersionOlder:       "1.50.5",
			imageVersionExact:       "1.70.0",
			imageVersionNewer:       "1.71.0",
			expectedErr:             false,
		}),
	Entry("image from github issue https://github.com/humio/humio-operator/issues/615",
		versionComparisonTestCase{
			userDefinedImageVersion: "humio/humio-core:1.34.0@sha256:38c78710107dc76f4f809b457328ff1c6764ae4244952a5fa7d76f6e67ea2390",
			imageVersionOlder:       "1.33.0",
			imageVersionExact:       "1.34.0",
			imageVersionNewer:       "1.35.0",
			expectedErr:             false,
		}),
	Entry("short image version",
		versionComparisonTestCase{
			userDefinedImageVersion: "humio/humio-core:1.34.0",
			imageVersionOlder:       "1.1.5",
			imageVersionExact:       "1.34.0",
			imageVersionNewer:       "1.100.0",
			expectedErr:             false,
		}),
)
