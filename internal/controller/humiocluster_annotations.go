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

package controller

const (
	// Set on Pod and Certificate objects
	CertificateHashAnnotation = "humio.com/certificate-hash"

	// Set on Pod objects
	PodHashAnnotation                      = "humio.com/pod-hash"
	PodOperatorManagedFieldsHashAnnotation = "humio.com/pod-operator-managed-fields-hash"
	PodRevisionAnnotation                  = "humio.com/pod-revision"
	BootstrapTokenHashAnnotation           = "humio.com/bootstrap-token-hash" // #nosec G101
	EnvVarSourceHashAnnotation             = "humio.com/env-var-source-hash"
)
