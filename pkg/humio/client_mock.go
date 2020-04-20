package humio

import (
	"fmt"

	humioapi "github.com/humio/cli/api"
	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
)

type ClientMock struct {
	Cluster                           humioapi.Cluster
	ClusterError                      error
	UpdateStoragePartitionSchemeError error
	UpdateIngestPartitionSchemeError  error
	IngestToken                       humioapi.IngestToken
}

type MockClientConfig struct {
	apiClient *ClientMock
	Url       string
	Version   string
}

func NewMocklient(cluster humioapi.Cluster, clusterError error, updateStoragePartitionSchemeError error, updateIngestPartitionSchemeError error, version string) *MockClientConfig {
	storagePartition := humioapi.StoragePartition{}
	ingestPartition := humioapi.IngestPartition{}

	mockClientConfig := &MockClientConfig{
		apiClient: &ClientMock{
			Cluster:                           cluster,
			ClusterError:                      clusterError,
			UpdateStoragePartitionSchemeError: updateStoragePartitionSchemeError,
			UpdateIngestPartitionSchemeError:  updateIngestPartitionSchemeError,
			IngestToken:                       humioapi.IngestToken{},
		},
		Version: version,
	}

	cluster.StoragePartitions = []humioapi.StoragePartition{storagePartition}
	cluster.IngestPartitions = []humioapi.IngestPartition{ingestPartition}

	return mockClientConfig
}

func (h *MockClientConfig) Authenticate(config *humioapi.Config) error {
	return nil
}

func (h *MockClientConfig) Status() (humioapi.StatusResponse, error) {
	return humioapi.StatusResponse{
		Status:  "OK",
		Version: h.Version,
	}, nil
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
	h.apiClient.Cluster.StoragePartitions = storagePartitions

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
	h.apiClient.Cluster.IngestPartitions = ingestPartitions

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
	return &h.apiClient.Cluster.StoragePartitions, nil
}

func (h *MockClientConfig) GetIngestPartitions() (*[]humioapi.IngestPartition, error) {
	return &h.apiClient.Cluster.IngestPartitions, nil
}

func (h *MockClientConfig) GetBaseURL(hc *corev1alpha1.HumioCluster) string {
	return fmt.Sprintf("http://%s.%s:%d/", hc.Name, hc.Namespace, 8080)
}

func (h *MockClientConfig) AddIngestToken(hit *corev1alpha1.HumioIngestToken) (*humioapi.IngestToken, error) {
	updatedApiClient := h.apiClient
	updatedApiClient.IngestToken = humioapi.IngestToken{
		Name:           hit.Spec.Name,
		AssignedParser: hit.Spec.ParserName,
		Token:          "mocktoken",
	}
	return &h.apiClient.IngestToken, nil
}

func (h *MockClientConfig) GetIngestToken(hit *corev1alpha1.HumioIngestToken) (*humioapi.IngestToken, error) {
	return &h.apiClient.IngestToken, nil
}

func (h *MockClientConfig) UpdateIngestToken(hit *corev1alpha1.HumioIngestToken) (*humioapi.IngestToken, error) {
	return h.AddIngestToken(hit)
}

func (h *MockClientConfig) DeleteIngestToken(hit *corev1alpha1.HumioIngestToken) error {
	updatedApiClient := h.apiClient
	updatedApiClient.IngestToken = humioapi.IngestToken{}
	return nil
}
