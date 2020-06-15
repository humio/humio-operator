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
