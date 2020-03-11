package humio

import (
	"fmt"

	humioapi "github.com/humio/cli/api"
	"github.com/prometheus/common/log"
)

// Client is the interface that can be mocked
type Client interface {
	GetClusters() (humioapi.Cluster, error)
	UpdateStoragePartitionScheme([]humioapi.StoragePartitionInput) error
	UpdateIngestPartitionScheme([]humioapi.IngestPartitionInput) error
	GetStoragePartitions() (*[]humioapi.StoragePartition, error)
	GetIngestPartitions() (*[]humioapi.IngestPartition, error)
}

// ClientConfig stores our Humio api client
type ClientConfig struct {
	apiClient *humioapi.Client
}

// NewClient returns a ClientConfig
func NewClient(config *humioapi.Config) (ClientConfig, error) {
	//humioapi.NewClient(humioapi.Config{Address: hc.Status.BaseURL, Token: hc.Status.JWTToken})
	client, err := humioapi.NewClient(*config)
	if err != nil {
		log.Info(fmt.Sprintf("could not create humio client: %v", err))
	}
	return ClientConfig{apiClient: client}, err
}

// GetClusters returns a humio cluster and can be mocked via the Client interface
func (h *ClientConfig) GetClusters() (humioapi.Cluster, error) {
	clusters, err := h.apiClient.Clusters().Get()
	if err != nil {
		log.Error(fmt.Sprintf("could not get cluster information: %v", err))
	}
	return clusters, err
}

// UpdateStoragePartitionScheme updates the storage partition scheme and can be mocked via the Client interface
func (h *ClientConfig) UpdateStoragePartitionScheme(spi []humioapi.StoragePartitionInput) error {
	err := h.apiClient.Clusters().UpdateStoragePartitionScheme(spi)
	if err != nil {
		log.Error(fmt.Sprintf("could not update storage partition scheme cluster information: %v", err))
	}
	return err
}

// UpdateIngestPartitionScheme updates the ingest partition scheme and can be mocked via the Client interface
func (h *ClientConfig) UpdateIngestPartitionScheme(ipi []humioapi.IngestPartitionInput) error {
	err := h.apiClient.Clusters().UpdateIngestPartitionScheme(ipi)
	if err != nil {
		log.Error(fmt.Sprintf("could not update ingest partition scheme cluster information: %v", err))
	}
	return err
}

func (h *ClientConfig) GetStoragePartitions() (*[]humioapi.StoragePartition, error) {
	return &[]humioapi.StoragePartition{}, fmt.Errorf("not implemented")
}

func (h *ClientConfig) GetIngestPartitions() (*[]humioapi.IngestPartition, error) {
	return &[]humioapi.IngestPartition{}, fmt.Errorf("not implemented")
}
