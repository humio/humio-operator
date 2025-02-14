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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// HumioPdfRenderServiceStateUnknown is the unknown state of the PDF rendering service.
	HumioPdfRenderServiceStateUnknown = "Unknown"
	// HumioPdfRenderServiceStateExists is the Exists state of the PDF rendering service.
	HumioPdfRenderServiceStateExists = "Exists"
	// HumioPdfRenderServiceStateNotFound is the NotFound state of the PDF rendering service.
	HumioPdfRenderServiceStateNotFound = "NotFound"
	// HumioPdfRenderServiceStateConfigError is the state of the PDF rendering service when user-provided specification results in configuration error, such as non-existent humio cluster
	HumioPdfRenderServiceStateConfigError = "ConfigError"
)

// HumioPdfRenderServiceSpec defines the desired state of HumioPdfRenderService
type HumioPdfRenderServiceSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	// Enable indicates whether the PDF rendering service should be created.
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// Image is the Docker image to use for the PDF rendering service.
	Image string `json:"image"`

	// Replicas is the number of desired Pod replicas.
	Replicas int32 `json:"replicas"`

	// Port is the port the service listens on.
	// +optional
	Port int32 `json:"port,omitempty"`

	// Resources defines the resource requests and limits for the container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// Env allows to specify environment variables for the service.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// Add other fields as needed, like:
	// - Configuration options (e.g., timeouts, memory settings)
	// - Storage options (e.g., volumes)
	// - Service type (e.g., ClusterIP, NodePort, LoadBalancer)

	// Affinity defines the pod's scheduling constraints.
	// +optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`

	// Annotations allows to specify custom annotations for the pods.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// Ingress defines the ingress configuration for the service.
	// +optional
	Ingress *HumioPdfRenderServiceIngressSpec `json:"ingress,omitempty"`

	// LivenessProbe defines the liveness probe configuration.
	// +optional
	LivenessProbe *corev1.Probe `json:"livenessProbe,omitempty"`

	// ReadinessProbe defines the readiness probe configuration.
	// +optional
	ReadinessProbe *corev1.Probe `json:"readinessProbe,omitempty"`

	// ServiceType is the type of service to expose.
	// +optional
	// +kubebuilder:default=ClusterIP
	ServiceType corev1.ServiceType `json:"serviceType,omitempty"`

	// NodePort is the port the service listens on when the service type is NodePort.
	// +optional
	NodePort int32 `json:"nodePort,omitempty"`

	// ServiceAccountName is the name of the Kubernetes Service Account to use.
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// ImagePullPolicy defines the pull policy for the container image
	ImagePullPolicy corev1.PullPolicy `json:"imagePullPolicy,omitempty"`

	// ImagePullSecrets is a list of references to secrets for pulling images
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// SecurityContext defines pod-level security attributes
	SecurityContext *corev1.PodSecurityContext `json:"securityContext,omitempty"`

	// ContainerSecurityContext defines container-level security attributes
	ContainerSecurityContext *corev1.SecurityContext `json:"containerSecurityContext,omitempty"`

}

// HumioPdfRenderServiceIngressSpec defines the desired state of the Ingress.
type HumioPdfRenderServiceIngressSpec struct {
	// Enabled defines if the ingress is enabled.
	Enabled bool `json:"enabled,omitempty"`

	// Hosts defines the list of hosts for the ingress.
	Hosts []HumioPdfRenderServiceIngressHost `json:"hosts,omitempty"`
}

// HumioPdfRenderServiceIngressHost defines the host configuration for the Ingress.
type HumioPdfRenderServiceIngressHost struct {
	// Host is the hostname to be used.
	Host string `json:"host,omitempty"`

	// Port is the port number to be used.
	Port int32 `json:"port,omitempty"`
}

// HumioPdfRenderServiceStatus defines the observed state of HumioPdfRenderService
type HumioPdfRenderServiceStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	// TODO: Add status fields (e.g. ObservedGeneration, Conditions, etc.)

	// Nodes are the names of the PDF render service pods.
	// +optional
	Nodes []string `json:"nodes,omitempty"`

	// ReadyReplicas is the number of ready replicas.
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// State represents the overall state of the PDF rendering service.

	// Conditions represents the latest available observations of current state.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
	State      string             `json:"state,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="Replicas",type="integer",JSONPath=".spec.replicas"
//+kubebuilder:printcolumn:name="Ready",type="integer",JSONPath=".status.readyReplicas"
//+kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.conditions[?(@.type==\"Available\")].status"
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// HumioPdfRenderService is the Schema for the humiopdfrenderservices API
type HumioPdfRenderService struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HumioPdfRenderServiceSpec   `json:"spec,omitempty"`
	Status HumioPdfRenderServiceStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// HumioPdfRenderServiceList contains a list of HumioPdfRenderService
type HumioPdfRenderServiceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioPdfRenderService `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioPdfRenderService{}, &HumioPdfRenderServiceList{})
}
