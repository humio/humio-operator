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

package openshift

import (
	"context"
	"fmt"
	"os"

	openshiftsecurityv1 "github.com/openshift/api/security/v1"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GetSecurityContextConstraints returns the security context constraints configured as environment variable on the operator container
func GetSecurityContextConstraints(ctx context.Context, c client.Client) (*openshiftsecurityv1.SecurityContextConstraints, error) {
	sccName, found := os.LookupEnv("OPENSHIFT_SCC_NAME")
	if !found || sccName == "" {
		return &openshiftsecurityv1.SecurityContextConstraints{}, fmt.Errorf("environment variable OPENSHIFT_SCC_NAME is either empty or not set")
	}
	var existingSCC openshiftsecurityv1.SecurityContextConstraints
	err := c.Get(ctx, types.NamespacedName{
		Name: sccName,
	}, &existingSCC)
	return &existingSCC, err
}
