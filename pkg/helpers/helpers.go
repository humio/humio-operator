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
	"github.com/shurcooL/graphql"
	uberzap "go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"os"
	"reflect"
	"strings"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"

	humioapi "github.com/humio/cli/api"
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

func MapStoragePartition(vs []humioapi.StoragePartition, f func(partition humioapi.StoragePartition) humioapi.StoragePartitionInput) []humioapi.StoragePartitionInput {
	vsm := make([]humioapi.StoragePartitionInput, len(vs))
	for i, v := range vs {
		vsm[i] = f(v)
	}
	return vsm
}

func ToStoragePartitionInput(line humioapi.StoragePartition) humioapi.StoragePartitionInput {
	var input humioapi.StoragePartitionInput
	nodeIds := make([]graphql.Int, len(line.NodeIds))
	for i, v := range line.NodeIds {
		nodeIds[i] = graphql.Int(v)
	}
	input.ID = graphql.Int(line.Id)
	input.NodeIDs = nodeIds

	return input
}

func MapIngestPartition(vs []humioapi.IngestPartition, f func(partition humioapi.IngestPartition) humioapi.IngestPartitionInput) []humioapi.IngestPartitionInput {
	vsm := make([]humioapi.IngestPartitionInput, len(vs))
	for i, v := range vs {
		vsm[i] = f(v)
	}
	return vsm
}

func ToIngestPartitionInput(line humioapi.IngestPartition) humioapi.IngestPartitionInput {
	var input humioapi.IngestPartitionInput
	nodeIds := make([]graphql.Int, len(line.NodeIds))
	for i, v := range line.NodeIds {
		nodeIds[i] = graphql.Int(v)
	}
	input.ID = graphql.Int(line.Id)
	input.NodeIDs = nodeIds

	return input
}

// TODO: refactor, this is copied from the humio/cli/api/parsers.go
// MapTests returns a matching slice of ParserTestCase, which is generated using the slice of strings and a function
// for obtaining the ParserTestCase elements from each string.
func MapTests(vs []string, f func(string) humioapi.ParserTestCase) []humioapi.ParserTestCase {
	vsm := make([]humioapi.ParserTestCase, len(vs))
	for i, v := range vs {
		vsm[i] = f(v)
	}
	return vsm
}

// TODO: refactor, this is copied from the humio/cli/api/parsers.go
// ToTestCase takes the input string of a ParserTestCase and returns a ParserTestCase object using the input string
func ToTestCase(line string) humioapi.ParserTestCase {
	return humioapi.ParserTestCase{
		Input:  line,
		Output: map[string]string{},
	}
}

// IsOpenShift returns whether the operator is running in OpenShift-mode
func IsOpenShift() bool {
	sccName, found := os.LookupEnv("OPENSHIFT_SCC_NAME")
	return found && sccName != ""
}

// UseCertManager returns whether the operator will use cert-manager
func UseCertManager() bool {
	certmanagerEnabled, found := os.LookupEnv("USE_CERTMANAGER")
	return found && certmanagerEnabled == "true"
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
	h.Write([]byte(fmt.Sprintf("%v", o)))
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

// IntPtr returns a int pointer to the specified int value
func IntPtr(val int) *int {
	return &val
}

// MapToString prettifies a string map so it's more suitable for readability when logging
func MapToString(m map[string]string) string {
	if len(m) == 0 {
		return `"":""`
	}
	var a []string
	for k, v := range m {
		a = append(a, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(a, ",")
}

// NewLogger returns a JSON logger with references to the origin of the log entry.
// All log entries also includes a field "ts" containing the timestamp in RFC3339 format.
func NewLogger() (*uberzap.Logger, error) {
	loggerCfg := uberzap.NewProductionConfig()
	loggerCfg.EncoderConfig.EncodeTime = zapcore.RFC3339NanoTimeEncoder
	return loggerCfg.Build(uberzap.AddCaller())
}
