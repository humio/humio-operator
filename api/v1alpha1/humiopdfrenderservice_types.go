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
	// DefaultPdfRenderServiceLiveness is the default liveness path for the PDF rendering service.
	DefaultPdfRenderServiceLiveness = "/health"
	// DefaultPdfRenderServiceReadiness is the default readiness path for the PDF rendering service.
	DefaultPdfRenderServiceReadiness = "/ready"
	// HumioPdfRenderServiceStateConfigError is the state of the PDF rendering service when user-provided specification results in configuration error, such as non-existent humio cluster
	HumioPdfRenderServiceStateConfigError = "ConfigError"
	// HumioPdfRenderServiceStateRunning is the state of the PDF rendering service when it is running and healthy
	HumioPdfRenderServiceStateRunning = "Running"
)

// HumioPdfRenderServiceSpec defines the desired state of HumioPdfRenderService
type HumioPdfRenderServiceSpec struct {
	// Image is the container image to use for the PDF rendering service.
	// +kubebuilder:validation:MinLength=1
	// +required
	Image string `json:"image"`

	// Replicas is the number of desired Pod replicas.
	Replicas int32 `json:"replicas"`

	// Port is the port the service listens on.
	// +optional
	// +kubebuilder:default=5123
	Port int32 `json:"port,omitempty"`

	// Resources defines the resource requests and limits for the container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// EnvironmentVariables allows to specify environment variables for the service.
	// +optional
	EnvironmentVariables []corev1.EnvVar `json:"environmentVariables,omitempty"`

	// Affinity defines the pod's scheduling constraints.
	// +optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`

	// Annotations allows to specify custom annotations for the pods.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// Labels allows to specify custom labels for the pods.
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// LivenessProbe defines the liveness probe configuration.
	// +optional
	LivenessProbe *corev1.Probe `json:"livenessProbe,omitempty"`

	// ReadinessProbe defines the readiness probe configuration.
	// +optional
	ReadinessProbe *corev1.Probe `json:"readinessProbe,omitempty"`

	// ServiceType is the type of service to expose.
	// +optional
	// +kubebuilder:default=ClusterIP
	// +kubebuilder:validation:Enum=ClusterIP
	ServiceType corev1.ServiceType `json:"serviceType,omitempty"`

	// ServiceAccountName is the name of the Kubernetes Service Account to use.
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// ImagePullSecrets is a list of references to secrets in the same namespace to use for pulling the image.
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// VolumeMounts defines the volume mounts for the container.
	// +optional
	VolumeMounts []corev1.VolumeMount `json:"volumeMounts,omitempty"`

	// Volumes defines the volumes to be mounted in the pod.
	// +optional
	Volumes []corev1.Volume `json:"volumes,omitempty"`

	// SecurityContext defines the security context for the container.
	// +optional
	SecurityContext *corev1.SecurityContext `json:"securityContext,omitempty"`

	// PodSecurityContext defines the security context for the pod.
	// +optional
	PodSecurityContext *corev1.PodSecurityContext `json:"podSecurityContext,omitempty"`

	// TLS specifies if TLS should be configured for the PDF Render Service as well as how it should be configured.
	// When enabled, this configures:
	// - A TLS certificate volume mounted from the secret named <name>-certificate (by default)
	// - Environment variables for TLS certificate paths and enabling secure connections
	// - HTTPS protocol for service ports instead of HTTP
	// - HTTPS scheme for liveness and readiness probes
	// +optional
	TLS *HumioClusterTLSSpec `json:"tls,omitempty"`
}

// HumioPdfRenderServiceStatus defines the observed state of HumioPdfRenderService
type HumioPdfRenderServiceStatus struct {
	// Nodes are the names of the PDF render service pods.
	// +optional
	Nodes []string `json:"nodes,omitempty"`

	// ReadyReplicas is the number of ready replicas.
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// State reflects the current state of the HumioPdfRenderService
	State string `json:"state,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=humiopdfrenderservices,scope=Namespaced
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state",description="The state of the PDF rendering service"
// +kubebuilder:printcolumn:name="Replicas",type="integer",JSONPath=".spec.replicas"
// +kubebuilder:printcolumn:name="Ready",type="integer",JSONPath=".status.readyReplicas"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// HumioPdfRenderService is the Schema for the humiopdfrenderservices API
type HumioPdfRenderService struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HumioPdfRenderServiceSpec   `json:"spec,omitempty"`
	Status HumioPdfRenderServiceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HumioPdfRenderServiceList contains a list of HumioPdfRenderService
type HumioPdfRenderServiceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioPdfRenderService `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioPdfRenderService{}, &HumioPdfRenderServiceList{})
}
