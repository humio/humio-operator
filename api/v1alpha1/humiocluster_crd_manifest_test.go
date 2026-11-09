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

package v1alpha1

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCELValidationRuleInGeneratedCRD(t *testing.T) {
	crdPath := "../../config/crd/bases/core.humio.com_humioclusters.yaml"

	content, err := os.ReadFile(crdPath)
	require.NoError(t, err, "read CRD manifest")

	crdString := string(content)

	assert.True(t, strings.Contains(crdString, "x-kubernetes-validations"),
		"CRD manifest should contain x-kubernetes-validations section")

	expectedRule := "!has(self.dnsPolicy) || self.dnsPolicy != ''None'' || has(self.dnsConfig)"
	assert.True(t, strings.Contains(crdString, expectedRule),
		"CRD manifest should contain CEL rule: %s", expectedRule)

	expectedMessage := "dnsConfig is required when dnsPolicy is None"
	assert.True(t, strings.Contains(crdString, expectedMessage),
		"CRD manifest should contain validation message: %s", expectedMessage)
}
