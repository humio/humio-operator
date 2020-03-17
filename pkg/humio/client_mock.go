package humio

import (
	humioapi "github.com/humio/cli/api"
	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
)

type ClientMock struct {
	Cluster                           humioapi.Cluster
	ClusterError                      error
	StoragePartitions                 *[]humioapi.StoragePartition
	IngestPartitions                  *[]humioapi.IngestPartition
	UpdateStoragePartitionSchemeError error
	UpdateIngestPartitionSchemeError  error
}

type MockClientConfig struct {
	apiClient *ClientMock
	url       string
}

func NewMocklient(cluster humioapi.Cluster, clusterError error, updateStoragePartitionSchemeError error, updateIngestPartitionSchemeError error, url string) *MockClientConfig {
	storagePartition := humioapi.StoragePartition{}
	ingestPartition := humioapi.IngestPartition{}

	return &MockClientConfig{
		apiClient: &ClientMock{
			Cluster:                           cluster,
			ClusterError:                      clusterError,
			StoragePartitions:                 &[]humioapi.StoragePartition{storagePartition},
			IngestPartitions:                  &[]humioapi.IngestPartition{ingestPartition},
			UpdateStoragePartitionSchemeError: updateStoragePartitionSchemeError,
			UpdateIngestPartitionSchemeError:  updateIngestPartitionSchemeError,
		},
		url: url,
	}
}

func (h *MockClientConfig) Authenticate(config *humioapi.Config) error {
	return nil
}

func (h *MockClientConfig) GetClusters() (humioapi.Cluster, error) {
	if h.apiClient.ClusterError != nil {
		return humioapi.Cluster{}, h.apiClient.ClusterError
	}
	return h.apiClient.Cluster, nil
}

func (h *MockClientConfig) UpdateStoragePartitionScheme(sps []humioapi.StoragePartitionInput) error {
	if h.apiClient.UpdateStoragePartitionSchemeError != nil {
		return h.apiClient.UpdateStoragePartitionSchemeError
	}

	var storagePartitions []humioapi.StoragePartition
	for _, storagePartitionInput := range sps {
		var nodeIdsList []int
		for _, nodeID := range storagePartitionInput.NodeIDs {
			nodeIdsList = append(nodeIdsList, int(nodeID))
		}
		storagePartitions = append(storagePartitions, humioapi.StoragePartition{Id: int(storagePartitionInput.ID), NodeIds: nodeIdsList})
	}
	h.apiClient.StoragePartitions = &storagePartitions

	return nil
}

func (h *MockClientConfig) UpdateIngestPartitionScheme(ips []humioapi.IngestPartitionInput) error {
	if h.apiClient.UpdateIngestPartitionSchemeError != nil {
		return h.apiClient.UpdateIngestPartitionSchemeError
	}

	var ingestPartitions []humioapi.IngestPartition
	for _, ingestPartitionInput := range ips {
		var nodeIdsList []int
		for _, nodeID := range ingestPartitionInput.NodeIDs {
			nodeIdsList = append(nodeIdsList, int(nodeID))
		}
		ingestPartitions = append(ingestPartitions, humioapi.IngestPartition{Id: int(ingestPartitionInput.ID), NodeIds: nodeIdsList})
	}
	h.apiClient.IngestPartitions = &ingestPartitions

	return nil
}

func (h *MockClientConfig) ClusterMoveStorageRouteAwayFromNode(int) error {
	return nil
}

func (h *MockClientConfig) ClusterMoveIngestRoutesAwayFromNode(int) error {
	return nil
}

func (h *MockClientConfig) Unregister(int) error {
	return nil
}

func (h *MockClientConfig) StartDataRedistribution() error {
	return nil
}

func (h *MockClientConfig) GetStoragePartitions() (*[]humioapi.StoragePartition, error) {
	return h.apiClient.StoragePartitions, nil
}

func (h *MockClientConfig) GetIngestPartitions() (*[]humioapi.IngestPartition, error) {
	return h.apiClient.IngestPartitions, nil
}

func (h *MockClientConfig) ApiToken() (string, error) {
	return "mocktoken", nil
}

func (h *MockClientConfig) GetBaseURL(hc *corev1alpha1.HumioCluster) string {
	return h.url
}
