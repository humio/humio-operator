// ...copyright and package/imports...
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
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// HumioPdfRenderServiceStateUnknown is the unknown state of the PDF rendering service.
	HumioPdfRenderServiceStateUnknown = "Unknown"
	// HumioPdfRenderServiceStateExists is the Exists state of the PDF rendering service.
	// Deprecated: Use more specific states like Running, Configuring.
	HumioPdfRenderServiceStateExists = "Exists"
	// HumioPdfRenderServiceStateNotFound is the NotFound state of the PDF rendering service.
	// Deprecated: Controller should handle resource absence.
	HumioPdfRenderServiceStateNotFound = "NotFound"
	// DefaultPdfRenderServiceLiveness is the default liveness path for the PDF rendering service.
	DefaultPdfRenderServiceLiveness = "/health"
	// DefaultPdfRenderServiceReadiness is the default readiness path for the PDF rendering service.
	DefaultPdfRenderServiceReadiness = "/ready"
	// HumioPdfRenderServiceStateConfigError is the state of the PDF rendering service when user-provided specification results in configuration error, such as non-existent humio cluster or missing TLS secrets.
	HumioPdfRenderServiceStateConfigError = "ConfigError"
	// HumioPdfRenderServiceStateRunning is the state of the PDF rendering service when it is running, all replicas are ready and the deployment is stable.
	HumioPdfRenderServiceStateRunning = "Running"
	// HumioPdfRenderServiceStateScalingUp is the state of the PDF rendering service when it is scaling up.
	// Deprecated: Covered by Configuring.
	HumioPdfRenderServiceStateScalingUp = "ScalingUp"
	// HumioPdfRenderServiceStateScaledDown is the state of the PDF rendering service when it is scaled down to zero replicas.
	HumioPdfRenderServiceStateScaledDown = "ScaledDown"
	// HumioPdfRenderServiceStateConfiguring is the state of the PDF rendering service when it is being configured, (e.g. deployment updating, scaling, waiting for pods to become ready).
	HumioPdfRenderServiceStateConfiguring = "Configuring"
	// HumioPdfRenderServiceStatePending is the state of the PDF rendering service when it is pending.
	// Deprecated: Covered by Configuring.
	HumioPdfRenderServiceStatePending = "Pending"
	// HumioPdfRenderServiceStateUpgrading is the state of the PDF rendering service when it is upgrading.
	// Deprecated: Covered by Configuring.
	HumioPdfRenderServiceStateUpgrading = "Upgrading"
	// HumioPdfRenderServiceStateError is a generic error state if not covered by ConfigError.
	HumioPdfRenderServiceStateError = "Error"
)

// HumioPdfRenderServiceConditionType represents a condition type of a HumioPdfRenderService.
type HumioPdfRenderServiceConditionType string

// These are valid conditions of a HumioPdfRenderService.
const (
	// HumioPdfRenderServiceAvailable means the PDF rendering service is available.
	HumioPdfRenderServiceAvailable HumioPdfRenderServiceConditionType = "Available"
	// HumioPdfRenderServiceProgressing means the PDF rendering service is progressing.
	HumioPdfRenderServiceProgressing HumioPdfRenderServiceConditionType = "Progressing"
	// HumioPdfRenderServiceDegraded means the PDF rendering service is degraded.
	HumioPdfRenderServiceDegraded HumioPdfRenderServiceConditionType = "Degraded"
	// HumioPdfRenderServiceScaledDown means the PDF rendering service is scaled down.
	HumioPdfRenderServiceScaledDown HumioPdfRenderServiceConditionType = "ScaledDown"
)

// HumioPdfRenderServiceSpec defines the desired state of HumioPdfRenderService
type HumioPdfRenderServiceSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Image is the Docker image to use for the PDF rendering service.
	Image string `json:"image"`

	// ImagePullPolicy specifies the image pull policy for the PDF render service.
	// +optional
	ImagePullPolicy corev1.PullPolicy `json:"imagePullPolicy,omitempty"`

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

	// Labels allows to specify custom labels for the pods.
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// ServiceAnnotations allows to specify custom annotations for the service.
	// +optional
	ServiceAnnotations map[string]string `json:"serviceAnnotations,omitempty"`

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

	// ImagePullSecrets is a list of references to secrets for pulling images
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// SecurityContext defines pod-level security attributes
	SecurityContext *corev1.PodSecurityContext `json:"securityContext,omitempty"`

	// ContainerSecurityContext defines container-level security attributes
	ContainerSecurityContext *corev1.SecurityContext `json:"containerSecurityContext,omitempty"`

	// PodSecurityContext defines pod-level security attributes
	// +optional
	PodSecurityContext *corev1.PodSecurityContext `json:"podSecurityContext,omitempty"`

	// Volumes allows specification of custom volumes
	// +optional
	Volumes []corev1.Volume `json:"volumes,omitempty"`

	// VolumeMounts allows specification of custom volume mounts
	// +optional
	VolumeMounts []corev1.VolumeMount `json:"volumeMounts,omitempty"`

	// TLS configuration for the PDF Render Service
	// +optional
	TLS *HumioPdfRenderServiceTLSSpec `json:"tls,omitempty"`

	// Autoscaling configuration for the PDF Render Service
	// +optional
	Autoscaling *HumioPdfRenderServiceAutoscalingSpec `json:"autoscaling,omitempty"`
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
	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// Conditions represents the latest available observations of current state.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// State represents the overall state of the PDF rendering service.
	// Possible values include: "Running", "Configuring", "ConfigError", "ScaledDown", "Error", "Unknown".
	// +optional
	State string `json:"state,omitempty"`

	// ObservedGeneration is the most recent generation observed for this resource
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Replicas",type="integer",JSONPath=".spec.replicas"
// +kubebuilder:printcolumn:name="Ready",type="integer",JSONPath=".status.readyReplicas"
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.conditions[?(@.type==\"Available\")].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// HumioPdfRenderService is the Schema for the humiopdfrenderservices API
type HumioPdfRenderService struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the desired state of HumioPdfRenderService
	// +kubebuilder:validation:Required
	Spec HumioPdfRenderServiceSpec `json:"spec"`

	// Status reflects the observed state of HumioPdfRenderService
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

// SetDefaults sets default values for the HumioPdfRenderService
func (hprs *HumioPdfRenderService) SetDefaults() {
	if hprs.Spec.Port == 0 {
		hprs.Spec.Port = 5123
	}
	if hprs.Spec.ServiceType == "" {
		hprs.Spec.ServiceType = corev1.ServiceTypeClusterIP
	}
	if hprs.Spec.ImagePullPolicy == "" {
		hprs.Spec.ImagePullPolicy = corev1.PullIfNotPresent
	}
}

// HumioPdfRenderServiceTLSSpec defines TLS configuration for the PDF Render Service
type HumioPdfRenderServiceTLSSpec struct {
	// Enabled toggles TLS on or off
	Enabled *bool `json:"enabled,omitempty"`
	// CASecretName is the name of the secret containing the CA certificate
	CASecretName string `json:"caSecretName,omitempty"`
	// ExtraHostnames is a list of additional hostnames to include in the certificate
	ExtraHostnames []string `json:"extraHostnames,omitempty"`
}

// HumioPdfRenderServiceAutoscalingSpec defines autoscaling configuration for the PDF Render Service
type HumioPdfRenderServiceAutoscalingSpec struct {
	// Enabled toggles autoscaling on or off
	Enabled *bool `json:"enabled,omitempty"`
	// MinReplicas is the minimum number of replicas
	MinReplicas *int32 `json:"minReplicas,omitempty"`
	// MaxReplicas is the maximum number of replicas
	MaxReplicas int32 `json:"maxReplicas,omitempty"`
	// TargetCPUUtilizationPercentage is the target average CPU utilization
	TargetCPUUtilizationPercentage *int32 `json:"targetCPUUtilizationPercentage,omitempty"`
	// TargetMemoryUtilizationPercentage is the target average memory utilization
	TargetMemoryUtilizationPercentage *int32 `json:"targetMemoryUtilizationPercentage,omitempty"`
	// Metrics contains the specifications for scaling metrics
	Metrics []autoscalingv2.MetricSpec `json:"metrics,omitempty"`
	// Behavior configures the scaling behavior of the target
	Behavior *autoscalingv2.HorizontalPodAutoscalerBehavior `json:"behavior,omitempty"`
}
