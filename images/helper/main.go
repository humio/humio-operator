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

package main

import (
	"context"
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8s "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	// load all auth plugins
	_ "k8s.io/client-go/plugin/pkg/client/auth"
)

var (
	// We override these using ldflags when running "go build"
	commit  = "none"
	date    = "unknown"
	version = "master"
)

func newKubernetesClientset() *k8s.Clientset {
	config, err := rest.InClusterConfig()
	if err != nil {
		panic(err.Error())
	}

	clientset, err := k8s.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}
	return clientset
}

// initMode looks up the availability zone of the Kubernetes node defined in environment variable NODE_NAME and saves
// the result to the file defined in environment variable TARGET_FILE
func initMode() {
	nodeName, found := os.LookupEnv("NODE_NAME")
	if !found || nodeName == "" {
		panic("environment variable NODE_NAME not set or empty")
	}

	targetFile, found := os.LookupEnv("TARGET_FILE")
	if !found || targetFile == "" {
		panic("environment variable TARGET_FILE not set or empty")
	}

	ctx := context.Background()

	clientset := newKubernetesClientset()

	node, err := clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		panic(err.Error())
	} else {
		zone, found := node.Labels[corev1.LabelZoneFailureDomainStable]
		if !found {
			zone = node.Labels[corev1.LabelZoneFailureDomain]
		}
		err := os.WriteFile(targetFile, []byte(zone), 0644) // #nosec G306
		if err != nil {
			panic(fmt.Sprintf("unable to write file with availability zone information: %s", err))
		}
	}
}

func main() {
	fmt.Printf("Starting humio-operator-helper %s (%s on %s)\n", version, commit, date)
	mode, found := os.LookupEnv("MODE")
	if !found || mode == "" {
		panic("environment variable MODE not set or empty")
	}
	switch mode {
	case "init":
		initMode()
	default:
		panic("unsupported mode")
	}
}
