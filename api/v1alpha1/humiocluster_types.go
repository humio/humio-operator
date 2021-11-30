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
	// HumioClusterStateRunning is the Running state of the cluster
	HumioClusterStateRunning = "Running"
	// HumioClusterStateRestarting is the state of the cluster when Humio pods are being restarted
	HumioClusterStateRestarting = "Restarting"
	// HumioClusterStateUpgrading is the state of the cluster when Humio pods are being upgraded
	HumioClusterStateUpgrading = "Upgrading"
	// HumioClusterStateConfigError is the state of the cluster when user-provided cluster specification results in configuration error
	HumioClusterStateConfigError = "ConfigError"
	// HumioClusterStatePending is the state of the cluster when waiting on resources to be provisioned
	HumioClusterStatePending = "Pending"
)

// HumioClusterSpec defines the desired state of HumioCluster
type HumioClusterSpec struct {
	// Image is the desired humio container image, including the image tag
	Image string `json:"image,omitempty"`
	// ImageSource is the reference to an external source identifying the image
	ImageSource *HumioImageSource `json:"imageSource,omitempty"`
	// HelperImage is the desired helper container image, including image tag
	HelperImage string `json:"helperImage,omitempty"`
	// DisableInitContainer is used to disable the init container completely which collects the availability zone from the Kubernetes worker node.
	// This is not recommended, unless you are using auto rebalancing partitions and are running in a single single availability zone.
	DisableInitContainer bool `json:"disableInitContainer,omitempty"`
	// AutoRebalancePartitions will enable auto-rebalancing of both digest and storage partitions assigned to humio cluster nodes.
	// If all Kubernetes worker nodes are located in the same availability zone, you must set DisableInitContainer to true to use auto rebalancing of partitions.
	AutoRebalancePartitions bool `json:"autoRebalancePartitions,omitempty"`
	// TargetReplicationFactor is the desired number of replicas of both storage and ingest partitions
	TargetReplicationFactor int `json:"targetReplicationFactor,omitempty"`
	// StoragePartitionsCount is the desired number of storage partitions
	StoragePartitionsCount int `json:"storagePartitionsCount,omitempty"`
	// DigestPartitionsCount is the desired number of digest partitions
	DigestPartitionsCount int `json:"digestPartitionsCount,omitempty"`
	// NodeCount is the desired number of humio cluster nodes
	NodeCount *int `json:"nodeCount,omitempty"`
	// License is the kubernetes secret reference which contains the Humio license
	License HumioClusterLicenseSpec `json:"license,omitempty"`
	// EnvironmentVariables that will be merged with default environment variables then set on the humio container
	EnvironmentVariables []corev1.EnvVar `json:"environmentVariables,omitempty"`
	// EnvironmentVariablesSource is the reference to an external source of environment variables that will be merged with environmentVariables
	EnvironmentVariablesSource []corev1.EnvFromSource `json:"environmentVariablesSource,omitempty"`
	// DataVolumeSource is the volume that is mounted on the humio pods. This conflicts with DataVolumePersistentVolumeClaimSpecTemplate.
	DataVolumeSource corev1.VolumeSource `json:"dataVolumeSource,omitempty"`
	// DataVolumePersistentVolumeClaimSpecTemplate is the PersistentVolumeClaimSpec that will be used with for the humio data volume. This conflicts with DataVolumeSource.
	DataVolumePersistentVolumeClaimSpecTemplate corev1.PersistentVolumeClaimSpec `json:"dataVolumePersistentVolumeClaimSpecTemplate,omitempty"`
	// ImagePullSecrets defines the imagepullsecrets for the humio pods. These secrets are not created by the operator
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`
	// Affinity defines the affinity policies that will be attached to the humio pods
	Affinity corev1.Affinity `json:"affinity,omitempty"`
	// Tolerations defines the tolerations that will be attached to the humio pods
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
	// IdpCertificateSecretName is the name of the secret that contains the IDP Certificate when using SAML authentication
	IdpCertificateSecretName string `json:"idpCertificateSecretName,omitempty"`
	// HumioServiceAccountAnnotations is the set of annotations added to the Kubernetes Service Account that will be attached to the Humio pods
	HumioServiceAccountAnnotations map[string]string `json:"humioServiceAccountAnnotations,omitempty"`
	// HumioServiceAccountName is the name of the Kubernetes Service Account that will be attached to the Humio pods
	HumioServiceAccountName string `json:"humioServiceAccountName,omitempty"`
	// InitServiceAccountName is the name of the Kubernetes Service Account that will be attached to the init container in the humio pod.
	InitServiceAccountName string `json:"initServiceAccountName,omitempty"`
	// AuthServiceAccountName is the name of the Kubernetes Service Account that will be attached to the auth container in the humio pod.
	AuthServiceAccountName string `json:"authServiceAccountName,omitempty"`
	// Resources is the kubernetes resource limits for the humio pod
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
	// ExtraKafkaConfigs is a multi-line string containing kafka properties
	ExtraKafkaConfigs string `json:"extraKafkaConfigs,omitempty"`
	// ViewGroupPermissions is a multi-line string containing view-group-permissions.json
	ViewGroupPermissions string `json:"viewGroupPermissions,omitempty"`
	// ContainerSecurityContext is the security context applied to the Humio container
	ContainerSecurityContext *corev1.SecurityContext `json:"containerSecurityContext,omitempty"`
	// ContainerReadinessProbe is the readiness probe applied to the Humio container.
	// If specified and non-empty, the user-specified readiness probe will be used.
	// If specified and empty, the pod will be created without a readiness probe set.
	// Otherwise, use the built in default readiness probe configuration.
	ContainerReadinessProbe *corev1.Probe `json:"containerReadinessProbe,omitempty"`
	// ContainerLivenessProbe is the liveness probe applied to the Humio container
	// If specified and non-empty, the user-specified liveness probe will be used.
	// If specified and empty, the pod will be created without a liveness probe set.
	// Otherwise, use the built in default liveness probe configuration.
	ContainerLivenessProbe *corev1.Probe `json:"containerLivenessProbe,omitempty"`
	// ContainerStartupProbe is the startup probe applied to the Humio container
	// If specified and non-empty, the user-specified startup probe will be used.
	// If specified and empty, the pod will be created without a startup probe set.
	// Otherwise, use the built in default startup probe configuration.
	ContainerStartupProbe *corev1.Probe `json:"containerStartupProbe,omitempty"`
	// PodSecurityContext is the security context applied to the Humio pod
	PodSecurityContext *corev1.PodSecurityContext `json:"podSecurityContext,omitempty"`
	// PodAnnotations can be used to specify annotations that will be added to the Humio pods
	PodAnnotations map[string]string `json:"podAnnotations,omitempty"`
	// Hostname is the public hostname used by clients to access Humio
	Hostname string `json:"hostname,omitempty"`
	// ESHostname is the public hostname used by log shippers with support for ES bulk API to access Humio
	ESHostname string `json:"esHostname,omitempty"`
	// HostnameSource is the reference to the public hostname used by clients to access Humio
	HostnameSource HumioHostnameSource `json:"hostnameSource,omitempty"`
	// ESHostnameSource is the reference to the public hostname used by log shippers with support for ES bulk API to
	// access Humio
	ESHostnameSource HumioESHostnameSource `json:"esHostnameSource,omitempty"`
	// Path is the root URI path of the Humio cluster
	Path string `json:"path,omitempty"`
	// Ingress is used to set up ingress-related objects in order to reach Humio externally from the kubernetes cluster
	Ingress HumioClusterIngressSpec `json:"ingress,omitempty"`
	// ImagePullPolicy sets the imagePullPolicy for all the containers in the humio pod
	ImagePullPolicy corev1.PullPolicy `json:"imagePullPolicy,omitempty"`
	// ExtraHumioVolumeMounts is the list of additional volume mounts that will be added to the Humio container
	ExtraHumioVolumeMounts []corev1.VolumeMount `json:"extraHumioVolumeMounts,omitempty"`
	// ExtraVolumes is the list of additional volumes that will be added to the Humio pod
	ExtraVolumes []corev1.Volume `json:"extraVolumes,omitempty"`
	// TLS is used to define TLS specific configuration such as intra-cluster TLS settings
	TLS *HumioClusterTLSSpec `json:"tls,omitempty"`
	// NodeUUIDPrefix is the prefix for the Humio Node's UUID. By default this does not include the zone. If it's
	// necessary to include zone, there is a special `Zone` variable that can be used. To use this, set `{{.Zone}}`. For
	// compatibility with pre-0.0.14 spec defaults, this should be set to `humio_{{.Zone}}`
	NodeUUIDPrefix string `json:"nodeUUIDPrefix,omitempty"`
	// HumioServiceType is the ServiceType of the Humio Service that is used to direct traffic to the Humio pods
	HumioServiceType corev1.ServiceType `json:"humioServiceType,omitempty"`
	// HumioServicePort is the port number of the Humio Service that is used to direct traffic to the http interface of
	// the Humio pods.
	HumioServicePort int32 `json:"humioServicePort,omitempty"`
	// HumioESServicePort is the port number of the Humio Service that is used to direct traffic to the ES interface of
	// the Humio pods.
	HumioESServicePort int32 `json:"humioESServicePort,omitempty"`
	// HumioServiceAnnotations is the set of annotations added to the Kubernetes Service that is used to direct traffic
	// to the Humio pods
	HumioServiceAnnotations map[string]string `json:"humioServiceAnnotations,omitempty"`
	// HumioServiceLabels is the set of labels added to the Kubernetes Service that is used to direct traffic
	// to the Humio pods
	HumioServiceLabels map[string]string `json:"humioServiceLabels,omitempty"`
	// HumioHeadlessAnnotations is the set of annotations added to the Kubernetes Headless Service that is used for
	// traffic between Humio pods
	HumioHeadlessServiceAnnotations map[string]string `json:"humioHeadlessServiceAnnotations,omitempty"`
	// HumioHeadlessServiceLabels is the set of labels added to the Kubernetes Headless Service that is used for
	// traffic between Humio pods
	HumioHeadlessServiceLabels map[string]string `json:"humioHeadlessServiceLabels,omitempty"`
	// SidecarContainers can be used in advanced use-cases where you want one or more sidecar container added to the
	// Humio pod to help out in debugging purposes.
	SidecarContainers []corev1.Container `json:"sidecarContainer,omitempty"`
	// ShareProcessNamespace can be useful in combination with SidecarContainers to be able to inspect the main Humio
	// process. This should not be enabled, unless you need this for debugging purposes.
	// https://kubernetes.io/docs/tasks/configure-pod-container/share-process-namespace/
	ShareProcessNamespace *bool `json:"shareProcessNamespace,omitempty"`
	// TerminationGracePeriodSeconds defines the amount of time to allow cluster pods to gracefully terminate
	// before being forcefully restarted. If using bucket storage, this should allow enough time for Humio to finish
	// uploading data to bucket storage.
	TerminationGracePeriodSeconds *int64 `json:"terminationGracePeriodSeconds,omitempty"`
}

// HumioHostnameSource is the possible references to a hostname value that is stored outside of the HumioCluster resource
type HumioHostnameSource struct {
	// SecretKeyRef contains the secret key reference when a hostname is pulled from a secret
	SecretKeyRef *corev1.SecretKeySelector `json:"secretKeyRef,omitempty"`
}

// HumioESHostnameSource is the possible references to a es hostname value that is stored outside of the HumioCluster resource
type HumioESHostnameSource struct {
	// SecretKeyRef contains the secret key reference when an es hostname is pulled from a secret
	SecretKeyRef *corev1.SecretKeySelector `json:"secretKeyRef,omitempty"`
}

// HumioClusterIngressSpec is used to set up ingress-related objects in order to reach Humio externally from the kubernetes cluster
type HumioClusterIngressSpec struct {
	// Enabled enables the logic for the Humio operator to create ingress-related objects. Requires one of the following
	// to be set: spec.hostname, spec.hostnameSource, spec.esHostname or spec.esHostnameSource
	Enabled bool `json:"enabled,omitempty"`
	// Controller is used to specify the controller used for ingress in the Kubernetes cluster. For now, only nginx is supported.
	Controller string `json:"controller,omitempty"`
	// TLS is used to specify whether the ingress controller will be using TLS for requests from external clients
	TLS *bool `json:"tls,omitempty"`
	// SecretName is used to specify the Kubernetes secret that contains the TLS certificate that should be used
	SecretName string `json:"secretName,omitempty"`
	// ESSecretName is used to specify the Kubernetes secret that contains the TLS certificate that should be used, specifically for the ESHostname
	ESSecretName string `json:"esSecretName,omitempty"`
	// Annotations can be used to specify annotations appended to the annotations set by the operator when creating ingress-related objects
	Annotations map[string]string `json:"annotations,omitempty"`
}

type HumioClusterTLSSpec struct {
	// Enabled can be used to toggle TLS on/off. Default behaviour is to configure TLS if cert-manager is present, otherwise we skip TLS.
	Enabled *bool `json:"enabled,omitempty"`
	// CASecretName is used to point to a Kubernetes secret that holds the CA that will be used to issue intra-cluster TLS certificates
	CASecretName string `json:"caSecretName,omitempty"`
}

// HumioClusterLicenseSpec points to the optional location of the Humio license
type HumioClusterLicenseSpec struct {
	SecretKeyRef *corev1.SecretKeySelector `json:"secretKeyRef,omitempty"`
}

// HumioImageSource points to the external source identifying the image
type HumioImageSource struct {
	// ConfigMapRef contains the reference to the configmap name and key containing the image value
	ConfigMapRef *corev1.ConfigMapKeySelector `json:"configMapRef,omitempty"`
}

// HumioPodStatus shows the status of individual humio pods
type HumioPodStatus struct {
	PodName string `json:"podName,omitempty"`
	PvcName string `json:"pvcName,omitempty"`
	NodeId  int    `json:"nodeId,omitempty"`
}

// HumioPodStatusList holds the list of HumioPodStatus types
type HumioPodStatusList []HumioPodStatus

// HumioLicenseStatus shows the status of Humio license
type HumioLicenseStatus struct {
	Type       string `json:"type,omitempty"`
	Expiration string `json:"expiration,omitempty"`
}

// HumioClusterStatus defines the observed state of HumioCluster
type HumioClusterStatus struct {
	// State will be empty before the cluster is bootstrapped. From there it can be "Running", "Upgrading" or "Restarting"
	State string `json:"state,omitempty"`
	// Message contains additional information about the state of the cluster
	Message string `json:"message,omitempty"`
	// Version is the version of humio running
	Version string `json:"version,omitempty"`
	// NodeCount is the number of nodes of humio running
	NodeCount int `json:"nodeCount,omitempty"`
	// PodStatus shows the status of individual humio pods
	PodStatus HumioPodStatusList `json:"podStatus,omitempty"`
	// LicenseStatus shows the status of the Humio license attached to the cluster
	LicenseStatus HumioLicenseStatus `json:"licenseStatus,omitempty"`
	// ObservedGeneration shows the generation of the HumioCluster which was last observed
	ObservedGeneration string `json:"observedGeneration,omitempty"` // TODO: We should change the type to int64 so we don't have to convert back and forth between int64 and string
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:path=humioclusters,scope=Namespaced
//+kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state",description="The state of the cluster"
//+kubebuilder:printcolumn:name="Nodes",type="string",JSONPath=".status.nodeCount",description="The number of nodes in the cluster"
//+kubebuilder:printcolumn:name="Version",type="string",JSONPath=".status.version",description="The version of humior"
//+operator-sdk:gen-csv:customresourcedefinitions.displayName="Humio Cluster"

// HumioCluster is the Schema for the humioclusters API
type HumioCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HumioClusterSpec   `json:"spec,omitempty"`
	Status HumioClusterStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// HumioClusterList contains a list of HumioCluster
type HumioClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioCluster `json:"items"`
}

// Len is the number of elements in the collection
func (l HumioPodStatusList) Len() int {
	return len(l)
}

// Less reports whether the element with index i must sort before the element with index j.
func (l HumioPodStatusList) Less(i, j int) bool {
	return l[i].PodName < l[j].PodName
}

// Swap swaps the elements with indexes i and j
func (l HumioPodStatusList) Swap(i, j int) {
	l[i], l[j] = l[j], l[i]
}

func init() {
	SchemeBuilder.Register(&HumioCluster{}, &HumioClusterList{})
}
