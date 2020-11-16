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

package kubernetes

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GetService returns the given service if it exists
func GetService(ctx context.Context, c client.Client, humioClusterName, humioClusterNamespace string) (*corev1.Service, error) {
	var existingService corev1.Service
	err := c.Get(ctx, types.NamespacedName{
		Namespace: humioClusterNamespace,
		Name:      humioClusterName,
	}, &existingService)
	return &existingService, err
}
