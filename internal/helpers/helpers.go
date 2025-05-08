/*
Copyright 2020 Humio https://humio.com

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package helpers

import (
	"crypto/sha256"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"

	uberzap "go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
)

const (
	TrueStr string = "true"
)

// GetTypeName returns the name of the type of object which is obtained by using reflection
func GetTypeName(myvar interface{}) string {
	t := reflect.TypeOf(myvar)
	if t.Kind() == reflect.Ptr {
		return t.Elem().Name()
	}
	return t.Name()
}

// ContainsElement returns true if 's' is an element in the list
func ContainsElement(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// RemoveElement returns a list where the element 's' has been removed
func RemoveElement(list []string, s string) []string {
	for i, v := range list {
		if v == s {
			list = append(list[:i], list[i+1:]...)
		}
	}
	return list
}

// TLSEnabled returns whether we a cluster should configure TLS or not
func TLSEnabled(hc *humiov1alpha1.HumioCluster) bool {
	if hc.Spec.TLS == nil {
		return UseCertManager()
	}
	if hc.Spec.TLS.Enabled == nil {
		return UseCertManager()
	}

	return UseCertManager() && *hc.Spec.TLS.Enabled
}

// AsSHA256 does a sha 256 hash on an object and returns the result
func AsSHA256(o interface{}) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%v", o)
	return fmt.Sprintf("%x", h.Sum(nil))
}

// BoolPtr returns a bool pointer to the specified boolean value
func BoolPtr(val bool) *bool {
	return &val
}

// Int64Ptr returns a int64 pointer to the specified int64 value
func Int64Ptr(val int64) *int64 {
	return &val
}

// Int32Ptr returns a int pointer to the specified int32 value
func Int32Ptr(val int32) *int32 {
	return &val
}

// StringPtr returns a string pointer to the specified string value
func StringPtr(val string) *string {
	return &val
}

func Int32PtrToFloat64Ptr(val *int32) *float64 {
	if val != nil {
		f := float64(*val)
		return &f
	}
	return nil
}

// BoolTrue returns true if the pointer is nil or true
func BoolTrue(val *bool) bool {
	return val == nil || *val
}

// BoolFalse returns false if the pointer is nil or false
func BoolFalse(val *bool) bool {
	if val == nil {
		return false
	}
	return *val
}

// MapToSortedString prettifies a string map, so it's more suitable for readability when logging.
// The output is constructed by sorting the slice.
func MapToSortedString(m map[string]string) string {
	if len(m) == 0 {
		return `"":""`
	}
	a := make([]string, len(m))
	idx := 0
	for k, v := range m {
		a[idx] = fmt.Sprintf("%s=%s", k, v)
		idx++
	}
	sort.SliceStable(a, func(i, j int) bool {
		return a[i] > a[j]
	})
	return strings.Join(a, ",")
}

// NewLogger returns a JSON logger with references to the origin of the log entry.
// All log entries also includes a field "ts" containing the timestamp in RFC3339 format.
func NewLogger() (*uberzap.Logger, error) {
	loggerCfg := uberzap.NewProductionConfig()
	loggerCfg.EncoderConfig.EncodeTime = zapcore.RFC3339NanoTimeEncoder
	loggerCfg.EncoderConfig.FunctionKey = "func"
	return loggerCfg.Build(uberzap.AddCaller())
}

// UseCertManager returns whether the operator will use cert-manager
func UseCertManager() bool {
	return !UseEnvtest() && os.Getenv("USE_CERTMANAGER") == TrueStr
}

// GetDefaultHumioCoreImageFromEnvVar returns the user-defined default image for humio-core containers
func GetDefaultHumioCoreImageFromEnvVar() string {
	return os.Getenv("HUMIO_OPERATOR_DEFAULT_HUMIO_CORE_IMAGE")
}

// GetDefaultHumioHelperImageFromEnvVar returns the user-defined default image for helper containers
func GetDefaultHumioHelperImageFromEnvVar() string {
	return os.Getenv("HUMIO_OPERATOR_DEFAULT_HUMIO_HELPER_IMAGE")
}

// UseEnvtest returns whether the Kubernetes API is provided by envtest
func UseEnvtest() bool {
	return os.Getenv("TEST_USING_ENVTEST") == TrueStr
}

// UseDummyImage returns whether we are using a dummy image replacement instead of real container images
func UseDummyImage() bool {
	return os.Getenv("DUMMY_LOGSCALE_IMAGE") == TrueStr
}

// GetE2ELicenseFromEnvVar returns the E2E license set as an environment variable
func GetE2ELicenseFromEnvVar() string {
	return os.Getenv("HUMIO_E2E_LICENSE")
}

// PreserveKindCluster returns true if the intention is to not delete kind cluster after test execution.
// This is to allow reruns of tests to be performed where resources can be reused.
func PreserveKindCluster() bool {
	return os.Getenv("PRESERVE_KIND_CLUSTER") == TrueStr
}
