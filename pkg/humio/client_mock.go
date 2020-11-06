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

package humio

import (
	"fmt"
	"net/url"

	humioapi "github.com/humio/cli/api"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/helpers"
)

type ClientMock struct {
	Cluster                           humioapi.Cluster
	ClusterError                      error
	UpdateStoragePartitionSchemeError error
	UpdateIngestPartitionSchemeError  error
	IngestToken                       humioapi.IngestToken
	Parser                            humioapi.Parser
	Repository                        humioapi.Repository
	View                              humioapi.View
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
			Parser:                            humioapi.Parser{Tests: []humioapi.ParserTestCase{}},
			Repository:                        humioapi.Repository{},
			View:                              humioapi.View{},
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

func (h *MockClientConfig) GetBaseURL(hc *humiov1alpha1.HumioCluster) *url.URL {
	baseURL, _ := url.Parse(fmt.Sprintf("http://%s.%s:%d/", hc.Name, hc.Namespace, 8080))
	return baseURL
}

func (h *MockClientConfig) TestAPIToken() error {
	return nil
}

func (h *MockClientConfig) AddIngestToken(hit *humiov1alpha1.HumioIngestToken) (*humioapi.IngestToken, error) {
	updatedApiClient := h.apiClient
	updatedApiClient.IngestToken = humioapi.IngestToken{
		Name:           hit.Spec.Name,
		AssignedParser: hit.Spec.ParserName,
		Token:          "mocktoken",
	}
	return &h.apiClient.IngestToken, nil
}

func (h *MockClientConfig) GetIngestToken(hit *humiov1alpha1.HumioIngestToken) (*humioapi.IngestToken, error) {
	return &h.apiClient.IngestToken, nil
}

func (h *MockClientConfig) UpdateIngestToken(hit *humiov1alpha1.HumioIngestToken) (*humioapi.IngestToken, error) {
	return h.AddIngestToken(hit)
}

func (h *MockClientConfig) DeleteIngestToken(hit *humiov1alpha1.HumioIngestToken) error {
	updatedApiClient := h.apiClient
	updatedApiClient.IngestToken = humioapi.IngestToken{}
	return nil
}

func (h *MockClientConfig) AddParser(hp *humiov1alpha1.HumioParser) (*humioapi.Parser, error) {
	updatedApiClient := h.apiClient
	updatedApiClient.Parser = humioapi.Parser{
		Name:      hp.Spec.Name,
		Script:    hp.Spec.ParserScript,
		TagFields: hp.Spec.TagFields,
		Tests:     helpers.MapTests(hp.Spec.TestData, helpers.ToTestCase),
	}
	return &h.apiClient.Parser, nil
}

func (h *MockClientConfig) GetParser(hp *humiov1alpha1.HumioParser) (*humioapi.Parser, error) {
	return &h.apiClient.Parser, nil
}

func (h *MockClientConfig) UpdateParser(hp *humiov1alpha1.HumioParser) (*humioapi.Parser, error) {
	return h.AddParser(hp)
}

func (h *MockClientConfig) DeleteParser(hp *humiov1alpha1.HumioParser) error {
	updatedApiClient := h.apiClient
	updatedApiClient.Parser = humioapi.Parser{Tests: []humioapi.ParserTestCase{}}
	return nil
}

func (h *MockClientConfig) AddRepository(hr *humiov1alpha1.HumioRepository) (*humioapi.Repository, error) {
	updatedApiClient := h.apiClient
	updatedApiClient.Repository = humioapi.Repository{
		Name:                   hr.Spec.Name,
		Description:            hr.Spec.Description,
		RetentionDays:          float64(hr.Spec.Retention.TimeInDays),
		IngestRetentionSizeGB:  float64(hr.Spec.Retention.IngestSizeInGB),
		StorageRetentionSizeGB: float64(hr.Spec.Retention.StorageSizeInGB),
	}
	return &h.apiClient.Repository, nil
}

func (h *MockClientConfig) GetRepository(hr *humiov1alpha1.HumioRepository) (*humioapi.Repository, error) {
	return &h.apiClient.Repository, nil
}

func (h *MockClientConfig) UpdateRepository(hr *humiov1alpha1.HumioRepository) (*humioapi.Repository, error) {
	return h.AddRepository(hr)
}

func (h *MockClientConfig) DeleteRepository(hr *humiov1alpha1.HumioRepository) error {
	updatedApiClient := h.apiClient
	updatedApiClient.Repository = humioapi.Repository{}
	return nil
}

func (h *MockClientConfig) GetView(hv *humiov1alpha1.HumioView) (*humioapi.View, error) {
	return &h.apiClient.View, nil
}

func (h *MockClientConfig) AddView(hv *humiov1alpha1.HumioView) (*humioapi.View, error) {
	updatedApiClient := h.apiClient

	connections := make([]humioapi.ViewConnection, 0)
	for _, connection := range hv.Spec.Connections {
		connections = append(connections, humioapi.ViewConnection{
			RepoName: connection.RepositoryName,
			Filter:   connection.Filter,
		})
	}

	updatedApiClient.View = humioapi.View{
		Name:        hv.Spec.Name,
		Connections: connections,
	}
	return &h.apiClient.View, nil
}
