package humio

import (
	"fmt"

	humioapi "github.com/humio/cli/api"
	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	"go.uber.org/zap"
)

// Client is the interface that can be mocked
type Client interface {
	GetClusters() (humioapi.Cluster, error)
	UpdateStoragePartitionScheme([]humioapi.StoragePartitionInput) error
	UpdateIngestPartitionScheme([]humioapi.IngestPartitionInput) error
	StartDataRedistribution() error
	ClusterMoveStorageRouteAwayFromNode(int) error
	ClusterMoveIngestRoutesAwayFromNode(int) error
	Unregister(int) error
	GetStoragePartitions() (*[]humioapi.StoragePartition, error)
	GetIngestPartitions() (*[]humioapi.IngestPartition, error)
	ApiToken() (string, error)
	Authenticate(*humioapi.Config) error
	GetBaseURL(*corev1alpha1.HumioCluster) string
	Status() (humioapi.StatusResponse, error)
}

// ClientConfig stores our Humio api client
type ClientConfig struct {
	apiClient *humioapi.Client
	logger    *zap.SugaredLogger
}

// NewClient returns a ClientConfig
func NewClient(logger *zap.SugaredLogger, config *humioapi.Config) *ClientConfig {
	client, err := humioapi.NewClient(*config)
	if err != nil {
		logger.Infof("could not create humio client: %s", err)
	}
	return &ClientConfig{
		apiClient: client,
		logger:    logger,
	}
}

func (h *ClientConfig) Authenticate(config *humioapi.Config) error {
	if config.Token == "" {
		config.Token = h.apiClient.Token()
	}
	if config.Address == "" {
		config.Address = h.apiClient.Address()
	}

	newClient, err := humioapi.NewClient(*config)
	if err != nil {
		return fmt.Errorf("could not create new humio client: %s", err)
	}

	h.apiClient = newClient
	return nil
}

// Status returns the status of the humio cluster
func (h *ClientConfig) Status() (humioapi.StatusResponse, error) {
	status, err := h.apiClient.Status()
	if err != nil {
		h.logger.Errorf("could not get status: %s", err)
		return humioapi.StatusResponse{}, err
	}
	return *status, err
}

// GetClusters returns a humio cluster and can be mocked via the Client interface
func (h *ClientConfig) GetClusters() (humioapi.Cluster, error) {
	clusters, err := h.apiClient.Clusters().Get()
	if err != nil {
		h.logger.Errorf("could not get cluster information: %s", err)
	}
	return clusters, err
}

// UpdateStoragePartitionScheme updates the storage partition scheme and can be mocked via the Client interface
func (h *ClientConfig) UpdateStoragePartitionScheme(spi []humioapi.StoragePartitionInput) error {
	err := h.apiClient.Clusters().UpdateStoragePartitionScheme(spi)
	if err != nil {
		h.logger.Errorf("could not update storage partition scheme cluster information: %s", err)
	}
	return err
}

// UpdateIngestPartitionScheme updates the ingest partition scheme and can be mocked via the Client interface
func (h *ClientConfig) UpdateIngestPartitionScheme(ipi []humioapi.IngestPartitionInput) error {
	err := h.apiClient.Clusters().UpdateIngestPartitionScheme(ipi)
	if err != nil {
		h.logger.Errorf("could not update ingest partition scheme cluster information: %s", err)
	}
	return err
}

// StartDataRedistribution notifies the Humio cluster that it should start redistributing data to match current assignments
func (h *ClientConfig) StartDataRedistribution() error {
	return h.apiClient.Clusters().StartDataRedistribution()
}

// ClusterMoveStorageRouteAwayFromNode notifies the Humio cluster that a node ID should be removed from handling any storage partitions
func (h *ClientConfig) ClusterMoveStorageRouteAwayFromNode(id int) error {
	return h.apiClient.Clusters().ClusterMoveStorageRouteAwayFromNode(id)
}

// ClusterMoveIngestRoutesAwayFromNode notifies the Humio cluster that a node ID should be removed from handling any ingest partitions
func (h *ClientConfig) ClusterMoveIngestRoutesAwayFromNode(id int) error {
	return h.apiClient.Clusters().ClusterMoveIngestRoutesAwayFromNode(id)
}

// Unregister tells the Humio cluster that we want to unregister a node
func (h *ClientConfig) Unregister(id int) error {
	return h.apiClient.ClusterNodes().Unregister(int64(id), false)
}

// GetStoragePartitions is not implemented. It is only used in the mock to validate partition layout
func (h *ClientConfig) GetStoragePartitions() (*[]humioapi.StoragePartition, error) {
	return &[]humioapi.StoragePartition{}, fmt.Errorf("not implemented")
}

// GetIngestPartitions is not implemented. It is only used in the mock to validate partition layout
func (h *ClientConfig) GetIngestPartitions() (*[]humioapi.IngestPartition, error) {
	return &[]humioapi.IngestPartition{}, fmt.Errorf("not implemented")
}

// ApiToken returns the api token for the current logged in user
func (h *ClientConfig) ApiToken() (string, error) {
	return h.apiClient.Viewer().ApiToken()
}

// ApiToken returns the api token for the current logged in user
func (h *ClientConfig) GetBaseURL(hc *corev1alpha1.HumioCluster) string {
	return fmt.Sprintf("http://%s.%s:%d/", hc.Name, hc.Namespace, 8080)
}
