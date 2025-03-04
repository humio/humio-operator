package controller

import (
	"testing"
)

func Test_HumioVersionFromString(t *testing.T) {
	type fields struct {
		userDefinedImageVersion string
		expectedImageVersion    string
		expectedAssumeLatest    bool
	}
	tests := []struct {
		name   string
		fields fields
	}{
		{
			"image with container image SHA",
			fields{
				userDefinedImageVersion: "humio/humio-core-dev:1.70.0--build-1023123--uaihdasiuhdiuahd23792f@sha256:4d545bbd0dc3a22d40188947f569566737657c42e4bd14327598299db2b5a38a",
				expectedImageVersion:    "1.70.0",
				expectedAssumeLatest:    false,
			},
		},
		{
			"image without container image SHA",
			fields{
				userDefinedImageVersion: "humio/humio-core-dev:1.70.0--build-1023123--uaihdasiuhdiuahd23792f",
				expectedImageVersion:    "1.70.0",
				expectedAssumeLatest:    false,
			},
		},
		{
			"image from github issue https://github.com/humio/humio-operator/issues/615",
			fields{
				userDefinedImageVersion: "humio/humio-core:1.34.0@sha256:38c78710107dc76f4f809b457328ff1c6764ae4244952a5fa7d76f6e67ea2390",
				expectedImageVersion:    "1.34.0",
				expectedAssumeLatest:    false,
			},
		},
		{
			"short image version",
			fields{
				userDefinedImageVersion: "humio/humio-core:1.34.0",
				expectedImageVersion:    "1.34.0",
				expectedAssumeLatest:    false,
			},
		},
		{
			"master image tag",
			fields{
				userDefinedImageVersion: "humio/humio-core:master",
				expectedImageVersion:    "",
				expectedAssumeLatest:    true,
			},
		},
		{
			"preview image tag",
			fields{
				userDefinedImageVersion: "humio/humio-core:preview",
				expectedImageVersion:    "",
				expectedAssumeLatest:    true,
			},
		},
		{
			"latest image tag",
			fields{
				userDefinedImageVersion: "humio/humio-core:latest",
				expectedImageVersion:    "",
				expectedAssumeLatest:    true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotVersion := HumioVersionFromString(tt.fields.userDefinedImageVersion)

			if gotVersion.IsLatest() != tt.fields.expectedAssumeLatest {
				t.Errorf("HumioVersionFromString(%s) = got IsLatest %t, expected IsLatest %t", tt.fields.userDefinedImageVersion, gotVersion.IsLatest(), tt.fields.expectedAssumeLatest)
			}

			if !tt.fields.expectedAssumeLatest && gotVersion.String() != tt.fields.expectedImageVersion {
				t.Errorf("HumioVersionFromString(%s) = got image %s, expected image %s", tt.fields.userDefinedImageVersion, gotVersion.String(), tt.fields.expectedImageVersion)
			}
		})
	}
}

func Test_humioVersion_AtLeast(t *testing.T) {
	type fields struct {
		userDefinedImageVersion string
		imageVersionOlder       string
		imageVersionExact       string
		imageVersionNewer       string
		expectedErr             bool
	}
	tests := []struct {
		name   string
		fields fields
	}{
		{
			"image with container image SHA",
			fields{
				userDefinedImageVersion: "humio/humio-core-dev:1.70.0--build-1023123--uaihdasiuhdiuahd23792f@sha256:4d545bbd0dc3a22d40188947f569566737657c42e4bd14327598299db2b5a38a",
				imageVersionOlder:       "1.69.0",
				imageVersionExact:       "1.70.0",
				imageVersionNewer:       "1.70.1",
				expectedErr:             false,
			},
		},
		{
			"image without container image SHA",
			fields{
				userDefinedImageVersion: "humio/humio-core-dev:1.70.0--build-1023123--uaihdasiuhdiuahd23792f",
				imageVersionOlder:       "1.50.5",
				imageVersionExact:       "1.70.0",
				imageVersionNewer:       "1.71.0",
				expectedErr:             false,
			},
		},
		{
			"image from github issue https://github.com/humio/humio-operator/issues/615",
			fields{
				userDefinedImageVersion: "humio/humio-core:1.34.0@sha256:38c78710107dc76f4f809b457328ff1c6764ae4244952a5fa7d76f6e67ea2390",
				imageVersionOlder:       "1.33.0",
				imageVersionExact:       "1.34.0",
				imageVersionNewer:       "1.35.0",
				expectedErr:             false,
			},
		},
		{
			"short image version",
			fields{
				userDefinedImageVersion: "humio/humio-core:1.34.0",
				imageVersionOlder:       "1.1.5",
				imageVersionExact:       "1.34.0",
				imageVersionNewer:       "1.100.0",
				expectedErr:             false,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			humioVersion := HumioVersionFromString(tt.fields.userDefinedImageVersion)
			if humioVersion.String() != tt.fields.imageVersionExact {
				t.Errorf("HumioVersion.AtLeast(%s) = got %s, expected %s", tt.fields.userDefinedImageVersion, humioVersion.String(), tt.fields.userDefinedImageVersion)
			}

			// Verify current version is newer than older image
			atLeast, err := humioVersion.AtLeast(tt.fields.imageVersionOlder)
			if (err != nil) != tt.fields.expectedErr {
				t.Errorf("HumioVersion(%s).AtLeast(%s) = got err %v, expected err %v", tt.fields.userDefinedImageVersion, tt.fields.imageVersionOlder, err, tt.fields.expectedErr)
			}
			if !atLeast {
				t.Errorf("HumioVersion(%s).AtLeast(%s) = got %t, expected true", tt.fields.userDefinedImageVersion, tt.fields.imageVersionOlder, atLeast)
			}

			// Verify version exactly the same as the specified image is reported as at least the exact
			atLeast, err = humioVersion.AtLeast(tt.fields.imageVersionExact)
			if (err != nil) != tt.fields.expectedErr {
				t.Errorf("HumioVersion(%s).AtLeast(%s) = got err %v, expected err %v", tt.fields.userDefinedImageVersion, tt.fields.imageVersionExact, err, tt.fields.expectedErr)
			}
			if !atLeast {
				t.Errorf("HumioVersion(%s).AtLeast(%s) = got %t, expected true", tt.fields.userDefinedImageVersion, tt.fields.imageVersionExact, atLeast)
			}

			// Verify current version reports false to be AtLeast for images newer
			atLeast, err = humioVersion.AtLeast(tt.fields.imageVersionNewer)
			if (err != nil) != tt.fields.expectedErr {
				t.Errorf("HumioVersion(%s).AtLeast(%s) = got err %v, expected err %v", tt.fields.userDefinedImageVersion, tt.fields.imageVersionNewer, err, tt.fields.expectedErr)
			}
			if atLeast {
				t.Errorf("HumioVersion(%s).AtLeast(%s) = got %t, expected false", tt.fields.userDefinedImageVersion, tt.fields.imageVersionNewer, atLeast)
			}
		})
	}
}
