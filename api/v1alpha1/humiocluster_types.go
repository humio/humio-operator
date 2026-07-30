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
	"strconv"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
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
	// HumioClusterUpdateStrategyOnDelete is the update strategy that will not terminate existing pods but will allow new pods to be created with the new spec
	HumioClusterUpdateStrategyOnDelete = "OnDelete"
	// HumioClusterUpdateStrategyRollingUpdate is the update strategy that will always cause pods to be replaced one at a time
	HumioClusterUpdateStrategyRollingUpdate = "RollingUpdate"
	// HumioClusterUpdateStrategyReplaceAllOnUpdate is the update strategy that will replace all pods at the same time during an update of either image or configuration.
	HumioClusterUpdateStrategyReplaceAllOnUpdate = "ReplaceAllOnUpdate"
	// HumioClusterUpdateStrategyRollingUpdateBestEffort is the update strategy where the operator will evaluate the Humio version change and determine if the
	// Humio pods can be updated in a rolling fashion or if they must be replaced at the same time
	HumioClusterUpdateStrategyRollingUpdateBestEffort = "RollingUpdateBestEffort"
	// HumioPersistentVolumeReclaimTypeOnNodeDelete is the persistent volume reclaim type which will remove persistent volume claims when the node to which they
	// are bound is deleted. Should only be used when running using `USING_EPHEMERAL_DISKS=true`, and typically only when using a persistent volume driver that
	// binds each persistent volume claim to a specific node (BETA)
	HumioPersistentVolumeReclaimTypeOnNodeDelete = "OnNodeDelete"
)

const (
	// ClusterConditionTypeReady indicates whether the cluster is ready
	ClusterConditionTypeReady = "Ready"
	// ClusterConditionTypeSynced indicates whether the cluster configuration is synced
	ClusterConditionTypeSynced = "Synced"
)

const (
	// ClusterReasonReady indicates the cluster is running and ready
	ClusterReasonReady = "Ready"
	// ClusterReasonRunning indicates the cluster is running normally
	ClusterReasonRunning = "Running"
	// ClusterReasonRestarting indicates the cluster is restarting
	ClusterReasonRestarting = "Restarting"
	// ClusterReasonUpgrading indicates the cluster is being upgraded
	ClusterReasonUpgrading = "Upgrading"
	// ClusterReasonConfigError indicates a configuration error
	ClusterReasonConfigError = "ConfigurationError"
	// ClusterReasonPending indicates the cluster is pending
	ClusterReasonPending = "Pending"
	// ClusterReasonBootstrapping indicates the cluster is bootstrapping
	ClusterReasonBootstrapping = "Bootstrapping"
)

// HumioClusterSpec defines the desired state of HumioCluster.
type HumioClusterSpec struct {
	// AutoRebalancePartitions will enable auto-rebalancing of both digest and storage partitions assigned to humio cluster nodes.
	// If all Kubernetes worker nodes are located in the same availability zone, you must set DisableInitContainer to true to use auto rebalancing of partitions.
	// Deprecated: No longer needed as of 1.89.0 as partitions and segment distribution is now automatically managed by LogScale itself.
	AutoRebalancePartitions bool `json:"autoRebalancePartitions,omitempty"`
	// BootstrapToken controls bootstrap token creation behavior
	BootstrapToken *HumioBootstrapTokenConfig `json:"bootstrapToken,omitempty"`
	// OperatorFeatureFlags contains feature flags applied to the Humio operator.
	OperatorFeatureFlags HumioOperatorFeatureFlags `json:"featureFlags,omitempty"`
	// TargetReplicationFactor is the desired number of replicas of both storage and ingest partitions
	TargetReplicationFactor int `json:"targetReplicationFactor,omitempty"`
	// StoragePartitionsCount is the desired number of storage partitions
	// Deprecated: No longer needed as LogScale now automatically redistributes segments
	StoragePartitionsCount int `json:"storagePartitionsCount,omitempty"`
	// DigestPartitionsCount is the desired number of digest partitions
	DigestPartitionsCount int `json:"digestPartitionsCount,omitempty"`
	// License is the kubernetes secret reference which contains the Humio license
	// +kubebuilder:validation:Required
	License HumioClusterLicenseSpec `json:"license"`
	// IdpCertificateSecretName is the name of the secret that contains the IDP Certificate when using SAML authentication
	IdpCertificateSecretName string `json:"idpCertificateSecretName,omitempty"`
	// ViewGroupPermissions is a multi-line string containing view-group-permissions.json.
	// Deprecated: Use RolePermissions instead.
	ViewGroupPermissions string `json:"viewGroupPermissions,omitempty"`
	// RolePermissions is a multi-line string containing role-permissions.json
	RolePermissions string `json:"rolePermissions,omitempty"`
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
	// TLS is used to define TLS specific configuration such as intra-cluster TLS settings
	TLS *HumioClusterTLSSpec `json:"tls,omitempty"`
	// HumioHeadlessServiceAnnotations is the set of annotations added to the Kubernetes Headless Service that is used for
	// traffic between Humio pods
	HumioHeadlessServiceAnnotations map[string]string `json:"humioHeadlessServiceAnnotations,omitempty"`
	// HumioHeadlessServiceLabels is the set of labels added to the Kubernetes Headless Service that is used for
	// traffic between Humio pods
	HumioHeadlessServiceLabels map[string]string `json:"humioHeadlessServiceLabels,omitempty"`

	HumioNodeSpec `json:",inline"`

	// CommonEnvironmentVariables is the set of variables that will be applied to all nodes regardless of the node pool types.
	// See spec.nodePools[].environmentVariables to override or append variables for a node pool.
	// New installations should prefer setting this variable instead of spec.environmentVariables as the latter will be deprecated in the future.
	CommonEnvironmentVariables []corev1.EnvVar `json:"commonEnvironmentVariables,omitempty"`

	// NodePools can be used to define additional groups of Humio cluster pods that share a set of configuration.
	NodePools []HumioNodePoolSpec `json:"nodePools,omitempty"`

	// WorkloadServices defines aggregate services that span multiple node pools sharing the same workload type.
	// Each entry creates a service named {cluster}-{name} that selects pods with the matching workload-type label.
	// No aggregate services are created unless this field is populated.
	WorkloadServices []WorkloadServiceSpec `json:"workloadServices,omitempty"`

	// TelemetryConfig contains the configuration for telemetry collection when enabled
	// Telemetry is enabled if this field is not nil
	TelemetryConfig *HumioTelemetryConfig `json:"telemetryConfig,omitempty"`
}

// HumioBootstrapTokenConfig controls bootstrap token creation behavior for a HumioCluster
type HumioBootstrapTokenConfig struct {
	// AutoCreate controls whether the operator should automatically create HumioBootstrapToken CRs.
	// When false, the operator expects users to create their own HumioBootstrapToken CR.
	// When nil, defaults to true (maintains backward compatibility).
	AutoCreate *bool `json:"autoCreate,omitempty"`
}

// HumioNodeSpec contains a collection of various configurations that are specific to a given group of LogScale pods.
// +kubebuilder:validation:XValidation:rule="!has(self.dnsPolicy) || self.dnsPolicy != 'None' || has(self.dnsConfig)",message="dnsConfig is required when dnsPolicy is None"
type HumioNodeSpec struct {
	// Image is the desired humio container image, including the image tag.
	// The value from ImageSource takes precedence over Image.
	Image string `json:"image,omitempty"`

	// ImageSource is the reference to an external source identifying the image.
	// The value from ImageSource takes precedence over Image.
	// +kubebuilder:validation:Optional
	ImageSource *HumioImageSource `json:"imageSource,omitempty"`

	// NodeCount is the desired number of humio cluster nodes
	NodeCount *int32 `json:"nodeCount,omitempty"`

	// Autoscaling defines bounds for HPA-driven scaling.
	// When configured with EnableIndependentHumioNodePools, allows external autoscalers
	// (HPA, KEDA) to manage replica counts within specified bounds.
	// +optional
	Autoscaling *AutoscalingSpec `json:"autoscaling,omitempty"`

	// DataVolumePersistentVolumeClaimSpecTemplate is the PersistentVolumeClaimSpec that will be used with for the humio data volume. This conflicts with DataVolumeSource.
	DataVolumePersistentVolumeClaimSpecTemplate corev1.PersistentVolumeClaimSpec `json:"dataVolumePersistentVolumeClaimSpecTemplate,omitempty"`

	// DataVolumePersistentVolumeClaimPolicy is a policy which allows persistent volumes to be reclaimed
	DataVolumePersistentVolumeClaimPolicy HumioPersistentVolumeClaimPolicy `json:"dataVolumePersistentVolumeClaimPolicy,omitempty"`

	// DataVolumeSource is the volume that is mounted on the humio pods. This conflicts with DataVolumePersistentVolumeClaimSpecTemplate.
	DataVolumeSource corev1.VolumeSource `json:"dataVolumeSource,omitempty"`

	// AuthServiceAccountName is no longer used as the auth sidecar container has been removed.
	// Deprecated: No longer used. The value will be ignored.
	AuthServiceAccountName string `json:"authServiceAccountName,omitempty"`

	// DisableInitContainer is used to disable the init container completely which collects the availability zone from the Kubernetes worker node.
	// This is not recommended, unless you are using auto rebalancing partitions and are running in a single availability zone.
	// +kubebuilder:default=false
	DisableInitContainer bool `json:"disableInitContainer,omitempty"`

	// EnvironmentVariablesSource is the reference to an external source of environment variables that will be merged with environmentVariables
	EnvironmentVariablesSource []corev1.EnvFromSource `json:"environmentVariablesSource,omitempty"`

	// PodAnnotations can be used to specify annotations that will be added to the Humio pods
	PodAnnotations map[string]string `json:"podAnnotations,omitempty"`

	// ShareProcessNamespace can be useful in combination with SidecarContainers to be able to inspect the main Humio
	// process. This should not be enabled, unless you need this for debugging purposes.
	// https://kubernetes.io/docs/tasks/configure-pod-container/share-process-namespace/
	ShareProcessNamespace *bool `json:"shareProcessNamespace,omitempty"`

	// HumioServiceAccountName is the name of the Kubernetes Service Account that will be attached to the Humio pods
	HumioServiceAccountName string `json:"humioServiceAccountName,omitempty"`

	// ImagePullSecrets defines the imagepullsecrets for the humio pods. These secrets are not created by the operator
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// HelperImage is the desired helper container image, including image tag
	HelperImage string `json:"helperImage,omitempty"`

	// ImagePullPolicy sets the imagePullPolicy for all the containers in the humio pod
	ImagePullPolicy corev1.PullPolicy `json:"imagePullPolicy,omitempty"`

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

	// Resources is the kubernetes resource limits for the humio pod
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// TerminationGracePeriodSeconds defines the amount of time to allow cluster pods to gracefully terminate
	// before being forcefully restarted. If using bucket storage, this should allow enough time for Humio to finish
	// uploading data to bucket storage.
	TerminationGracePeriodSeconds *int64 `json:"terminationGracePeriodSeconds,omitempty"`

	// Affinity defines the affinity policies that will be attached to the humio pods
	Affinity corev1.Affinity `json:"affinity,omitempty"`

	// Tolerations defines the tolerations that will be attached to the humio pods
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// TopologySpreadConstraints defines the topologySpreadConstraints that will be attached to the humio pods
	TopologySpreadConstraints []corev1.TopologySpreadConstraint `json:"topologySpreadConstraints,omitempty"`

	// DNSPolicy sets the DNS policy for the pod.
	// Valid values: ClusterFirst, ClusterFirstWithHostNet, Default, None.
	// When unset, Kubernetes applies ClusterFirst default.
	// +kubebuilder:validation:Enum=ClusterFirst;ClusterFirstWithHostNet;Default;None
	// +optional
	DNSPolicy corev1.DNSPolicy `json:"dnsPolicy,omitempty"`

	// DNSConfig defines the DNS configuration for the pod.
	// This field is required when DNSPolicy is set to None.
	// +optional
	DNSConfig *corev1.PodDNSConfig `json:"dnsConfig,omitempty"`

	// SidecarContainers can be used in advanced use-cases where you want one or more sidecar container added to the
	// Humio pod to help out in debugging purposes.
	SidecarContainers []corev1.Container `json:"sidecarContainer,omitempty"`

	// ExtraInitContainers can be used to add additional init containers to the Humio pod.
	// These are appended after the operator-managed init container.
	ExtraInitContainers []corev1.Container `json:"extraInitContainers,omitempty"`

	// NodeUUIDPrefix is the prefix for the Humio Node's UUID. By default this does not include the zone. If it's
	// necessary to include zone, there is a special `Zone` variable that can be used. To use this, set `{{.Zone}}`. For
	// compatibility with pre-0.0.14 spec defaults, this should be set to `humio_{{.Zone}}`
	// Deprecated: LogScale 1.70.0 deprecated this option, and was later removed in LogScale 1.80.0
	NodeUUIDPrefix string `json:"nodeUUIDPrefix,omitempty"`

	// ExtraKafkaConfigs is a multi-line string containing kafka properties.
	// Deprecated: This underlying LogScale environment variable used by this field has been marked deprecated as of
	// LogScale 1.173.0 and will cause LogScale startup failure in 1.225.0+. The operator automatically disables
	// this feature for LogScale 1.225.0+ to prevent failures.
	// For more details, see the LogScale release notes
	ExtraKafkaConfigs string `json:"extraKafkaConfigs,omitempty"`

	// ExtraHumioVolumeMounts is the list of additional volume mounts that will be added to the Humio container
	ExtraHumioVolumeMounts []corev1.VolumeMount `json:"extraHumioVolumeMounts,omitempty"`

	// ExtraVolumes is the list of additional volumes that will be added to the Humio pod
	ExtraVolumes []corev1.Volume `json:"extraVolumes,omitempty"`

	// HumioServiceAccountAnnotations is the set of annotations added to the Kubernetes Service Account that will be attached to the Humio pods
	HumioServiceAccountAnnotations map[string]string `json:"humioServiceAccountAnnotations,omitempty"`

	// HumioServiceLabels is the set of labels added to the Kubernetes Service that is used to direct traffic
	// to the Humio pods
	HumioServiceLabels map[string]string `json:"humioServiceLabels,omitempty"`

	// EnvironmentVariables is the set of variables that will be supplied to all Pods in the given node pool.
	// This set is merged with fallback environment variables (for defaults in case they are not supplied in the Custom Resource),
	// and spec.commonEnvironmentVariables (for variables that should be applied to Pods of all node types).
	// Precedence is given to more environment-specific variables, i.e. spec.environmentVariables
	// (or spec.nodePools[].environmentVariables) has higher precedence than spec.commonEnvironmentVariables.
	EnvironmentVariables []corev1.EnvVar `json:"environmentVariables,omitempty"`

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

	// InitServiceAccountName is the name of the Kubernetes Service Account that will be attached to the init container in the humio pod.
	InitServiceAccountName string `json:"initServiceAccountName,omitempty"`

	// PodLabels can be used to specify labels that will be added to the Humio pods
	PodLabels map[string]string `json:"podLabels,omitempty"`

	// UpdateStrategy controls how Humio pods are updated when changes are made to the HumioCluster resource that results
	// in a change to the Humio pods
	UpdateStrategy *HumioUpdateStrategy `json:"updateStrategy,omitempty"`

	// PriorityClassName is the name of the priority class that will be used by the Humio pods
	// +kubebuilder:default=""
	PriorityClassName string `json:"priorityClassName,omitempty"`

	// NodePoolFeatures defines the features that are allowed by the node pool
	NodePoolFeatures HumioNodePoolFeatures `json:"nodePoolFeatures,omitempty"`

	// PodDisruptionBudget defines the PDB configuration for this node spec
	PodDisruptionBudget *HumioPodDisruptionBudgetSpec `json:"podDisruptionBudget,omitempty"`

	// DependencyCheck configures startup dependency checking
	// When configured, the init container will check dependency availability
	// before allowing the main Humio container to start
	// If not specified, dependency checks are disabled (backwards compatible)
	// +optional
	DependencyCheck *DependencyCheckConfig `json:"dependencyCheck,omitempty"`

	// ContainerLifecycle defines lifecycle actions for the main Humio container.
	// This can be used to configure preStop hooks for graceful shutdown.
	// +optional
	ContainerLifecycle *corev1.Lifecycle `json:"containerLifecycle,omitempty"`

	// WorkloadTypes is a list of workload types this pool participates in.
	// Each entry adds a label humio.com/workload-type/{value}: "true" to pods.
	// If nil, workload types are automatically derived from NODE_ROLES:
	//   all / "" / unset → ["digest", "ingest"]
	//   httponly / ingestonly / lightweightingestonly → ["ingest"]
	// Set explicitly to override automatic derivation (e.g., when using INITIAL_DISABLED_NODE_TASKS).
	// +kubebuilder:validation:Optional
	WorkloadTypes *[]string `json:"workloadTypes,omitempty"`

	// EnableNodePoolService controls whether the per-node-pool service is created for this pool.
	// When nil or true, the per-pool service is created as normal (backwards compatible default).
	// Set to false to opt out of per-pool service creation once workload-type aggregate services are in place.
	// +kubebuilder:validation:Optional
	EnableNodePoolService *bool `json:"enableNodePoolService,omitempty"`
}

// HumioOperatorFeatureFlags contains feature flags applied to the Humio operator.
type HumioOperatorFeatureFlags struct {
	// EnableDownscalingFeature (PREVIEW) is a feature flag for enabling the downscaling functionality of the humio operator for this humio cluster.
	// Default: false
	// Preview: this feature is in a preview state
	// +kubebuilder:default=false
	EnableDownscalingFeature bool `json:"enableDownscalingFeature,omitempty"`
	// EnableIndependentHumioNodePools enables independent HumioNodePool resource creation for embedded node pools.
	// This creates shadow HumioNodePool CRD resources that mirror embedded node pool specs,
	// enabling advanced features like HPA autoscaling and individual node pool management.
	// When disabled, the operator works as before without creating HumioNodePool resources.
	// Default: false
	// +kubebuilder:default=false
	EnableIndependentHumioNodePools bool `json:"enableIndependentHumioNodePools,omitempty"`
}

// HumioNodePoolFeatures is used to toggle certain features that are specific instance of HumioNodeSpec. This means
// that any set of pods configured by the same HumioNodeSpec instance will share these features.
type HumioNodePoolFeatures struct {
	// AllowedAPIRequestTypes is a list of API request types that are allowed by the node pool. Current options are:
	// OperatorInternal. Defaults to [OperatorInternal]. To disallow all API request types, set this to [].
	AllowedAPIRequestTypes *[]string `json:"allowedAPIRequestTypes,omitempty"`
}

// WorkloadServiceSpec defines an aggregate service that spans multiple node pools sharing the same workload type.
type WorkloadServiceSpec struct {
	// Name is the suffix for the aggregate service: {cluster}-{name}.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// WorkloadType is the workload-type label value to select on.
	// The service selector will match pods with label humio.com/workload-type/{workloadType}: "true".
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	WorkloadType string `json:"workloadType"`

	// ServiceType is the Kubernetes service type for the aggregate service. Default: ClusterIP.
	// +kubebuilder:validation:Optional
	ServiceType corev1.ServiceType `json:"serviceType,omitempty"`

	// HumioServicePort is the port number for the HTTP interface on the aggregate service. Default: 8080.
	// +kubebuilder:validation:Optional
	HumioServicePort int32 `json:"humioServicePort,omitempty"`

	// HumioESServicePort is the port number for the Elasticsearch interface on the aggregate service. Default: 9200.
	// +kubebuilder:validation:Optional
	HumioESServicePort int32 `json:"humioESServicePort,omitempty"`

	// Labels to add to the aggregate service resource.
	// +kubebuilder:validation:Optional
	Labels map[string]string `json:"labels,omitempty"`

	// Annotations to add to the aggregate service resource.
	// +kubebuilder:validation:Optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

// HumioUpdateStrategy contains a set of different toggles for defining how a set of pods should be replaced during
// pod replacements due differences between current and desired state of pods.
type HumioUpdateStrategy struct {
	// Type controls how Humio pods are updated  when changes are made to the HumioCluster resource that results
	// in a change to the Humio pods. The available values are: OnDelete, RollingUpdate, ReplaceAllOnUpdate, and
	// RollingUpdateBestEffort.
	//
	// When set to OnDelete, no Humio pods will be terminated but new pods will be created with the new spec. Replacing
	// existing pods will require each pod to be deleted by the user.
	//
	// When set to RollingUpdate, pods will always be replaced one pod at a time. There may be some Humio updates where
	// rolling updates are not supported, so it is not recommended to have this set all the time.
	//
	// When set to ReplaceAllOnUpdate, all Humio pods will be replaced at the same time during an update.
	// This is the default behavior.
	//
	// When set to RollingUpdateBestEffort, the operator will evaluate the Humio version change and determine if the
	// Humio pods can be updated in a rolling fashion or if they must be replaced at the same time.
	// +kubebuilder:validation:Enum=OnDelete;RollingUpdate;ReplaceAllOnUpdate;RollingUpdateBestEffort
	Type string `json:"type,omitempty"`

	// MinReadySeconds is the minimum time in seconds that a pod must be ready before the next pod can be deleted when doing rolling update.
	MinReadySeconds int32 `json:"minReadySeconds,omitempty"`

	// EnableZoneAwareness toggles zone awareness on or off during updates. When enabled, the pod replacement logic
	// will go through all pods in a specific zone before it starts replacing pods in the next zone.
	// If pods are failing, they bypass the zone limitation and are restarted immediately - ignoring the zone.
	// Zone awareness is enabled by default.
	EnableZoneAwareness *bool `json:"enableZoneAwareness,omitempty"`

	// MaxUnavailable is the maximum number of pods that can be unavailable during a rolling update.
	// This can be configured to an absolute number or a percentage, e.g. "maxUnavailable: 5" or "maxUnavailable: 25%".
	// +kubebuilder:default=1
	MaxUnavailable *intstr.IntOrString `json:"maxUnavailable,omitempty"`
}

// HumioNodePoolSpec is used to attach a name to an instance of HumioNodeSpec
type HumioNodePoolSpec struct {
	// Name holds a name for this specific group of cluster pods. This name is used when constructing pod names, so it
	// is useful to use a name that reflects what the pods are configured to do.
	// +kubebuilder:validation:MinLength:=1
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// ClusterName is the name of the HumioCluster this node pool belongs to.
	// Required for standalone HumioNodePool resources, optional for embedded node pools.
	// +kubebuilder:validation:Optional
	ClusterName string `json:"clusterName,omitempty"`

	// Selector is used by HPA to identify which pods belong to this node pool.
	// +kubebuilder:validation:Optional
	Selector *metav1.LabelSelector `json:"selector,omitempty"`

	// NodeCount is the top-level node count field used by the scale subresource.
	// This is set by the controller for shadow HumioNodePool CRs.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=0
	NodeCount int32 `json:"nodeCount"`

	HumioNodeSpec `json:"spec,omitempty"`
}

// HumioPodDisruptionBudgetSpec defines the desired pod disruption budget configuration
// +kubebuilder:validation:XValidation:rule="!has(self.minAvailable) || !has(self.maxUnavailable)",message="At most one of minAvailable or maxUnavailable can be specified"
// +kubebuilder:validation:XValidation:rule="has(self.minAvailable) || has(self.maxUnavailable)",message="either minAvailable or maxUnavailable must be specified"
type HumioPodDisruptionBudgetSpec struct {
	// MinAvailable is the minimum number of pods that must be available during a disruption.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Format=int-or-string
	MinAvailable *intstr.IntOrString `json:"minAvailable,omitempty"`

	// MaxUnavailable is the maximum number of pods that can be unavailable during a disruption.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Format=int-or-string
	MaxUnavailable *intstr.IntOrString `json:"maxUnavailable,omitempty"`

	// UnhealthyPodEvictionPolicy defines the policy for evicting unhealthy pods.
	// Requires Kubernetes 1.26+.
	// +kubebuilder:validation:Enum=IfHealthyBudget;AlwaysAllow
	// +kubebuilder:validation:default="IfHealthyBudget"
	// +kubebuilder:validation:Optional
	UnhealthyPodEvictionPolicy *string `json:"unhealthyPodEvictionPolicy,omitempty"`

	// Enabled indicates whether PodDisruptionBudget is enabled for this NodePool.
	// +kubebuilder:validation:Optional
	Enabled bool `json:"enabled,omitempty"`

	// FreezeDuringUpdate blocks all voluntary pod evictions when the cluster is
	// in Upgrading or Restarting state by setting minAvailable equal to the
	// replica count. When nil or false, PDB fields reflect user-configured values.
	// +kubebuilder:validation:Optional
	FreezeDuringUpdate *bool `json:"freezeDuringUpdate,omitempty"`
}

// DependencyCheckConfig configures startup dependency checks in the init container.
// When present (non-nil) on HumioNodeSpec, dependency checking is enabled.
// Dependencies (kafka, s3, gcs) are auto-discovered from environment variables.
// +kubebuilder:validation:Optional
type DependencyCheckConfig struct {
	// Enforcement controls how dependency check failures are handled:
	//   required  - (default) pod startup is blocked until all checks pass
	//   advisory  - failures are logged but pod startup is not blocked
	//   disabled  - dependency checking is completely disabled
	// +kubebuilder:validation:Enum=required;advisory;disabled
	// +kubebuilder:default="required"
	Enforcement string `json:"enforcement,omitempty"`

	// TimeoutSeconds is the maximum time for the entire dependency check process
	// +kubebuilder:default=600
	// +kubebuilder:validation:Minimum=60
	TimeoutSeconds int `json:"timeoutSeconds,omitempty"`

	// RetryIntervalSeconds is the time between retry attempts
	// +kubebuilder:default=5
	// +kubebuilder:validation:Minimum=1
	RetryIntervalSeconds int `json:"retryIntervalSeconds,omitempty"`

	// Exclude lists check types to skip even if auto-discovered from environment variables.
	// Valid values are: kafka, s3, gcs
	// +optional
	Exclude []DependencyCheckType `json:"exclude,omitempty"`
}

// DependencyCheckType represents the type of dependency check to perform.
// +kubebuilder:validation:Enum=kafka;s3;gcs
type DependencyCheckType string

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
	// +kubebuilder:default=false
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

// HumioClusterTLSSpec specifies if TLS should be configured for the HumioCluster as well as how it should be configured.
type HumioClusterTLSSpec struct {
	// Enabled can be used to toggle TLS on/off. Default behaviour is to configure TLS if cert-manager is present, otherwise we skip TLS.
	Enabled *bool `json:"enabled,omitempty"`
	// CASecretName is used to point to a Kubernetes secret that holds the CA that will be used to issue intra-cluster TLS certificates
	CASecretName string `json:"caSecretName,omitempty"`
	// ExtraHostnames holds a list of additional hostnames that will be appended to TLS certificates.
	ExtraHostnames []string `json:"extraHostnames,omitempty"`
}

// HumioClusterLicenseSpec points to the location of the Humio license
type HumioClusterLicenseSpec struct {
	// SecretKeyRef specifies which key of a secret in the namespace of the HumioCluster that holds the LogScale license key
	SecretKeyRef *corev1.SecretKeySelector `json:"secretKeyRef,omitempty"`
}

// HumioImageSource points to the external source identifying the image
type HumioImageSource struct {
	// ConfigMapRef contains the reference to the configmap name and key containing the image value
	ConfigMapRef *corev1.ConfigMapKeySelector `json:"configMapRef,omitempty"`
}

// HumioPersistentVolumeReclaimType is the type of reclaim which will occur on a persistent volume
type HumioPersistentVolumeReclaimType string

// AutoscalingSpec defines bounds for HPA-driven scaling.
type AutoscalingSpec struct {
	// MinReplicas is the lower bound for autoscaling. Must be >= 1.
	// +kubebuilder:validation:Minimum=1
	// +optional
	MinReplicas *int32 `json:"minReplicas,omitempty"`

	// MaxReplicas is the upper bound for autoscaling. Must be >= 1.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Required
	MaxReplicas int32 `json:"maxReplicas"`
}

// HumioPersistentVolumeClaimPolicy contains the policy for handling persistent volumes
type HumioPersistentVolumeClaimPolicy struct {
	// ReclaimType is used to indicate what reclaim type should be used. This e.g. allows the user to specify if the
	// operator should automatically delete persistent volume claims if they are bound to Kubernetes worker nodes
	// that no longer exists. This can be useful in scenarios where PVC's represent a type of storage where the
	// lifecycle of the storage follows the one of the Kubernetes worker node.
	// When using persistent volume claims relying on network attached storage, this can be ignored.
	// +kubebuilder:validation:Enum=None;OnNodeDelete
	ReclaimType HumioPersistentVolumeReclaimType `json:"reclaimType,omitempty"`
}

// HumioPodStatusList holds the list of HumioPodStatus types
type HumioPodStatusList []HumioPodStatus

// HumioPodStatus shows the status of individual humio pods
type HumioPodStatus struct {
	// PodName holds the name of the pod that this is the status for.
	PodName string `json:"podName,omitempty"`
	// PvcName is the name of the persistent volume claim that is mounted in to the pod
	PvcName string `json:"pvcName,omitempty"`
	// NodeId used to refer to the value of the BOOTSTRAP_HOST_ID environment variable for a Humio instance.
	// Deprecated: No longer being used.
	NodeId int `json:"nodeId,omitempty"`
	// NodeName is the name of the Kubernetes worker node where this pod is currently running
	NodeName string `json:"nodeName,omitempty"`
}

// HumioLicenseStatus shows the status of Humio license
type HumioLicenseStatus struct {
	// Type holds the type of license that is currently installed on the HumioCluster
	Type string `json:"type,omitempty"`
	// Expiration contains the timestamp of when the currently installed license expires.
	Expiration string `json:"expiration,omitempty"`
}

// HumioNodePoolStatusList holds the list of HumioNodePoolStatus types
type HumioNodePoolStatusList []HumioNodePoolStatus

// HumioNodePoolStatus shows the status of each node pool
type HumioNodePoolStatus struct {
	// Name is the name of the node pool
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// State will be empty before the cluster is bootstrapped. From there it can be "Running", "Upgrading", "Restarting" or "Pending"
	State string `json:"state,omitempty"`
	// ZoneUnderMaintenance holds the name of the availability zone currently under maintenance
	ZoneUnderMaintenance string `json:"zoneUnderMaintenance,omitempty"`
	// DesiredPodRevision holds the desired pod revision for pods of the given node pool.
	DesiredPodRevision int `json:"desiredPodRevision,omitempty"`
	// DesiredPodHash holds a hashed representation of the pod spec
	DesiredPodHash string `json:"desiredPodHash,omitempty"`
	// DesiredBootstrapTokenHash holds a SHA256 of the value set in environment variable BOOTSTRAP_ROOT_TOKEN_HASHED
	DesiredBootstrapTokenHash string `json:"desiredBootstrapTokenHash,omitempty"`
	// CurrentReplicas is the actual number of nodes running in this node pool
	CurrentReplicas int32 `json:"currentReplicas,omitempty"`
	// DesiredReplicas is the target number of nodes for this node pool
	DesiredReplicas int32 `json:"desiredReplicas,omitempty"`
	// Selector is the label selector string for the scale subresource
	Selector string `json:"selector,omitempty"`
}

// HumioClusterStatus defines the observed state of HumioCluster.
type HumioClusterStatus struct {
	// State is deprecated (use Conditions instead). Will be removed in a future release. Empty before cluster is bootstrapped, then "Running", "Upgrading", "Restarting" or "Pending"
	// +kubebuilder:validation:Optional
	State string `json:"state,omitempty"`

	// Conditions represent the latest available observations of the resource's state
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

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
	// NodePoolStatus shows the status of each node pool
	NodePoolStatus HumioNodePoolStatusList `json:"nodePoolStatus,omitempty"`
	// ObservedGeneration shows the generation of the HumioCluster which was last observed
	ObservedGeneration string `json:"observedGeneration,omitempty"` // TODO: We should change the type to int64 so we don't have to convert back and forth between int64 and string
	// EvictedNodeIds keeps track of evicted nodes for use within the downscaling functionality
	EvictedNodeIds []int `json:"evictedNodeIds,omitempty"`
	// TelemetryStatus shows the status of telemetry collection for this cluster
	TelemetryStatus *HumioTelemetryStatus `json:"telemetryStatus,omitempty"`
}

// HumioTelemetryResourceStatus represents the status of a single telemetry resource (collection or export)
type HumioTelemetryResourceStatus struct {
	// Name is the name of the telemetry resource
	Name string `json:"name"`

	// State is the current state of the resource (e.g., "Enabled", "Disabled", "ConfigError")
	State string `json:"state"`

	// LastActivity indicates when this resource last performed its function (collection or export)
	LastActivity *metav1.Time `json:"lastActivity,omitempty"`

	// ErrorCount is the number of errors encountered by this resource
	ErrorCount int `json:"errorCount,omitempty"`

	// LastError contains the most recent error message from this resource
	LastError string `json:"lastError,omitempty"`
}

// HumioTelemetryStatus represents the status of telemetry collection for a cluster
type HumioTelemetryStatus struct {
	// State represents the current state of telemetry collection
	State string `json:"state,omitempty"`

	// LastCollectionTime indicates when data was last collected
	LastCollectionTime *metav1.Time `json:"lastCollectionTime,omitempty"`

	// LastExportTime indicates when data was last exported successfully
	LastExportTime *metav1.Time `json:"lastExportTime,omitempty"`

	// ExportErrors contains any errors from data export
	ExportErrors []HumioTelemetryExportError `json:"exportErrors,omitempty"`

	// CollectionResourceName is the name of the HumioTelemetryCollection resource
	// Deprecated: Use Collections list instead. This field is only populated when there is exactly one collection resource.
	CollectionResourceName string `json:"collectionResourceName,omitempty"`

	// CollectionState is the state of the collection resource
	// Deprecated: Use Collections list instead. This field is only populated when there is exactly one collection resource.
	CollectionState string `json:"collectionState,omitempty"`

	// ExportResourceName is the name of the HumioTelemetryExport resource
	// Deprecated: Use Exports list instead. This field is only populated when there is exactly one export resource.
	ExportResourceName string `json:"exportResourceName,omitempty"`

	// ExportState is the state of the export resource
	// Deprecated: Use Exports list instead. This field is only populated when there is exactly one export resource.
	ExportState string `json:"exportState,omitempty"`

	// Collections contains status information for all HumioTelemetryCollection resources that reference this cluster
	// +optional
	Collections []HumioTelemetryResourceStatus `json:"collections,omitempty"`

	// Exports contains status information for all HumioTelemetryExport resources that reference this cluster
	// +optional
	Exports []HumioTelemetryResourceStatus `json:"exports,omitempty"`

	// TotalCollections is the total number of collection resources referencing this cluster
	// +optional
	TotalCollections int `json:"totalCollections,omitempty"`

	// ActiveCollections is the number of collection resources in an active/enabled state
	// +optional
	ActiveCollections int `json:"activeCollections,omitempty"`

	// TotalExports is the total number of export resources referencing this cluster
	// +optional
	TotalExports int `json:"totalExports,omitempty"`

	// ActiveExports is the number of export resources in an active/enabled state
	// +optional
	ActiveExports int `json:"activeExports,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=humioclusters,scope=Namespaced
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state",description="The state of the cluster"
// +kubebuilder:printcolumn:name="Nodes",type="string",JSONPath=".status.nodeCount",description="The number of nodes in the cluster"
// +kubebuilder:printcolumn:name="Version",type="string",JSONPath=".status.version",description="The version of humio"
// +operator-sdk:gen-csv:customresourcedefinitions.displayName="Humio Cluster"

// HumioCluster is the Schema for the humioclusters API.
type HumioCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec   HumioClusterSpec   `json:"spec"`
	Status HumioClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HumioClusterList contains a list of HumioCluster.
type HumioClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioCluster `json:"items"`
}

// GetObservedGeneration exposes ObservedGeneration as int64 for test helpers
func (hc *HumioCluster) GetObservedGeneration() int64 {
	if hc == nil {
		return 0
	}
	val, err := strconv.ParseInt(hc.Status.ObservedGeneration, 10, 64)
	if err != nil {
		return 0
	}
	return val
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

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:subresource:scale:specpath=.spec.nodeCount,statuspath=.status.currentReplicas,selectorpath=.status.selector
// +kubebuilder:resource:path=humionodepools,scope=Namespaced
// +kubebuilder:printcolumn:name="Cluster",type="string",JSONPath=".spec.clusterName",description="The cluster this node pool belongs to"
// +kubebuilder:printcolumn:name="Pool",type="string",JSONPath=".spec.name",description="The name of the node pool"
// +kubebuilder:printcolumn:name="Replicas",type="integer",JSONPath=".status.currentReplicas",description="Current replicas"
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state",description="The state of the node pool"

// HumioNodePool is the Schema for the humionodepools API.
// This resource represents an individual node pool within a HumioCluster and supports the scale subresource for HPA.
type HumioNodePool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec   HumioNodePoolSpec   `json:"spec"`
	Status HumioNodePoolStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HumioNodePoolList contains a list of HumioNodePool.
type HumioNodePoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioNodePool `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioCluster{}, &HumioClusterList{}, &HumioNodePool{}, &HumioNodePoolList{})
}
