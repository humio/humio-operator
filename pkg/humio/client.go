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
	"github.com/go-logr/logr"
	"net/url"
	"reflect"

	humioapi "github.com/humio/cli/api"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/helpers"
)

// Client is the interface that can be mocked
type Client interface {
	ClusterClient
	IngestTokensClient
	ParsersClient
	RepositoriesClient
	ViewsClient
}

type ClusterClient interface {
	GetClusters() (humioapi.Cluster, error)
	UpdateStoragePartitionScheme([]humioapi.StoragePartitionInput) error
	UpdateIngestPartitionScheme([]humioapi.IngestPartitionInput) error
	StartDataRedistribution() error
	ClusterMoveStorageRouteAwayFromNode(int) error
	ClusterMoveIngestRoutesAwayFromNode(int) error
	Unregister(int) error
	GetStoragePartitions() (*[]humioapi.StoragePartition, error)
	GetIngestPartitions() (*[]humioapi.IngestPartition, error)
	Authenticate(*humioapi.Config) error
	GetBaseURL(*humiov1alpha1.HumioCluster) *url.URL
	TestAPIToken() error
	Status() (humioapi.StatusResponse, error)
}

type IngestTokensClient interface {
	AddIngestToken(*humiov1alpha1.HumioIngestToken) (*humioapi.IngestToken, error)
	GetIngestToken(*humiov1alpha1.HumioIngestToken) (*humioapi.IngestToken, error)
	UpdateIngestToken(*humiov1alpha1.HumioIngestToken) (*humioapi.IngestToken, error)
	DeleteIngestToken(*humiov1alpha1.HumioIngestToken) error
}

type ParsersClient interface {
	AddParser(*humiov1alpha1.HumioParser) (*humioapi.Parser, error)
	GetParser(*humiov1alpha1.HumioParser) (*humioapi.Parser, error)
	UpdateParser(*humiov1alpha1.HumioParser) (*humioapi.Parser, error)
	DeleteParser(*humiov1alpha1.HumioParser) error
}

type RepositoriesClient interface {
	AddRepository(*humiov1alpha1.HumioRepository) (*humioapi.Repository, error)
	GetRepository(*humiov1alpha1.HumioRepository) (*humioapi.Repository, error)
	UpdateRepository(*humiov1alpha1.HumioRepository) (*humioapi.Repository, error)
	DeleteRepository(*humiov1alpha1.HumioRepository) error
}

type ViewsClient interface {
	AddView(view *humiov1alpha1.HumioView) (*humioapi.View, error)
	GetView(view *humiov1alpha1.HumioView) (*humioapi.View, error)
	UpdateView(view *humiov1alpha1.HumioView) (*humioapi.View, error)
}

// ClientConfig stores our Humio api client
type ClientConfig struct {
	apiClient *humioapi.Client
	logger    logr.Logger
}

// NewClient returns a ClientConfig
func NewClient(logger logr.Logger, config *humioapi.Config) *ClientConfig {
	client := humioapi.NewClient(*config)
	return &ClientConfig{
		apiClient: client,
		logger:    logger,
	}
}

func (h *ClientConfig) Authenticate(config *humioapi.Config) error {
	if config.Token == "" {
		config.Token = h.apiClient.Token()
	}
	if config.Address == nil {
		config.Address = h.apiClient.Address()
	}
	if config.CACertificatePEM == "" {
		config.CACertificatePEM = h.apiClient.CACertificate()
	}
	newClient := humioapi.NewClient(*config)

	h.apiClient = newClient
	return nil
}

// Status returns the status of the humio cluster
func (h *ClientConfig) Status() (humioapi.StatusResponse, error) {
	status, err := h.apiClient.Status()
	if err != nil {
		h.logger.Error(err, "could not get status")
		return humioapi.StatusResponse{}, err
	}
	return *status, err
}

// GetClusters returns a humio cluster and can be mocked via the Client interface
func (h *ClientConfig) GetClusters() (humioapi.Cluster, error) {
	clusters, err := h.apiClient.Clusters().Get()
	if err != nil {
		h.logger.Error(err, "could not get cluster information")
	}
	return clusters, err
}

// UpdateStoragePartitionScheme updates the storage partition scheme and can be mocked via the Client interface
func (h *ClientConfig) UpdateStoragePartitionScheme(spi []humioapi.StoragePartitionInput) error {
	err := h.apiClient.Clusters().UpdateStoragePartitionScheme(spi)
	if err != nil {
		h.logger.Error(err, "could not update storage partition scheme cluster information")
	}
	return err
}

// UpdateIngestPartitionScheme updates the ingest partition scheme and can be mocked via the Client interface
func (h *ClientConfig) UpdateIngestPartitionScheme(ipi []humioapi.IngestPartitionInput) error {
	err := h.apiClient.Clusters().UpdateIngestPartitionScheme(ipi)
	if err != nil {
		h.logger.Error(err, "could not update ingest partition scheme cluster information")
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

// GetBaseURL returns the base URL for given HumioCluster
func (h *ClientConfig) GetBaseURL(hc *humiov1alpha1.HumioCluster) *url.URL {
	protocol := "https"
	if !helpers.TLSEnabled(hc) {
		protocol = "http"
	}
	baseURL, _ := url.Parse(fmt.Sprintf("%s://%s.%s:%d/", protocol, hc.Name, hc.Namespace, 8080))
	return baseURL

}

// TestAPIToken tests if an API token is valid by fetching the username that the API token belongs to
func (h *ClientConfig) TestAPIToken() error {
	if h.apiClient == nil {
		return fmt.Errorf("api client not set yet")
	}
	_, err := h.apiClient.Viewer().Username()
	return err
}

func (h *ClientConfig) AddIngestToken(hit *humiov1alpha1.HumioIngestToken) (*humioapi.IngestToken, error) {
	return h.apiClient.IngestTokens().Add(hit.Spec.RepositoryName, hit.Spec.Name, hit.Spec.ParserName)
}

func (h *ClientConfig) GetIngestToken(hit *humiov1alpha1.HumioIngestToken) (*humioapi.IngestToken, error) {
	tokens, err := h.apiClient.IngestTokens().List(hit.Spec.RepositoryName)
	if err != nil {
		return &humioapi.IngestToken{}, err
	}
	for _, token := range tokens {
		if token.Name == hit.Spec.Name {
			return &token, nil
		}
	}
	return &humioapi.IngestToken{}, nil
}

func (h *ClientConfig) UpdateIngestToken(hit *humiov1alpha1.HumioIngestToken) (*humioapi.IngestToken, error) {
	return h.apiClient.IngestTokens().Update(hit.Spec.RepositoryName, hit.Spec.Name, hit.Spec.ParserName)
}

func (h *ClientConfig) DeleteIngestToken(hit *humiov1alpha1.HumioIngestToken) error {
	return h.apiClient.IngestTokens().Remove(hit.Spec.RepositoryName, hit.Spec.Name)
}

func (h *ClientConfig) AddParser(hp *humiov1alpha1.HumioParser) (*humioapi.Parser, error) {
	parser := humioapi.Parser{
		Name:      hp.Spec.Name,
		Script:    hp.Spec.ParserScript,
		TagFields: hp.Spec.TagFields,
		Tests:     helpers.MapTests(hp.Spec.TestData, helpers.ToTestCase),
	}
	err := h.apiClient.Parsers().Add(
		hp.Spec.RepositoryName,
		&parser,
		false,
	)
	return &parser, err
}

func (h *ClientConfig) GetParser(hp *humiov1alpha1.HumioParser) (*humioapi.Parser, error) {
	return h.apiClient.Parsers().Get(hp.Spec.RepositoryName, hp.Spec.Name)
}

func (h *ClientConfig) UpdateParser(hp *humiov1alpha1.HumioParser) (*humioapi.Parser, error) {
	parser := humioapi.Parser{
		Name:      hp.Spec.Name,
		Script:    hp.Spec.ParserScript,
		TagFields: hp.Spec.TagFields,
		Tests:     helpers.MapTests(hp.Spec.TestData, helpers.ToTestCase),
	}
	err := h.apiClient.Parsers().Add(
		hp.Spec.RepositoryName,
		&parser,
		true,
	)
	return &parser, err
}

func (h *ClientConfig) DeleteParser(hp *humiov1alpha1.HumioParser) error {
	return h.apiClient.Parsers().Remove(hp.Spec.RepositoryName, hp.Spec.Name)
}

func (h *ClientConfig) AddRepository(hr *humiov1alpha1.HumioRepository) (*humioapi.Repository, error) {
	repository := humioapi.Repository{Name: hr.Spec.Name}
	err := h.apiClient.Repositories().Create(hr.Spec.Name)
	return &repository, err
}

func (h *ClientConfig) GetRepository(hr *humiov1alpha1.HumioRepository) (*humioapi.Repository, error) {
	repoList, err := h.apiClient.Repositories().List()
	if err != nil {
		return &humioapi.Repository{}, fmt.Errorf("could not list repositories: %s", err)
	}
	for _, repo := range repoList {
		if repo.Name == hr.Spec.Name {
			// we now know the repository exists
			repository, err := h.apiClient.Repositories().Get(hr.Spec.Name)
			return &repository, err
		}
	}
	return &humioapi.Repository{}, nil
}

func (h *ClientConfig) UpdateRepository(hr *humiov1alpha1.HumioRepository) (*humioapi.Repository, error) {
	curRepository, err := h.GetRepository(hr)
	if err != nil {
		return &humioapi.Repository{}, err
	}

	if curRepository.Description != hr.Spec.Description {
		err = h.apiClient.Repositories().UpdateDescription(
			hr.Spec.Name,
			hr.Spec.Description,
		)
		if err != nil {
			return &humioapi.Repository{}, err
		}
	}

	if curRepository.RetentionDays != float64(hr.Spec.Retention.TimeInDays) {
		err = h.apiClient.Repositories().UpdateTimeBasedRetention(
			hr.Spec.Name,
			float64(hr.Spec.Retention.TimeInDays),
			hr.Spec.AllowDataDeletion,
		)
		if err != nil {
			return &humioapi.Repository{}, err
		}
	}

	if curRepository.StorageRetentionSizeGB != float64(hr.Spec.Retention.StorageSizeInGB) {
		err = h.apiClient.Repositories().UpdateStorageBasedRetention(
			hr.Spec.Name,
			float64(hr.Spec.Retention.StorageSizeInGB),
			hr.Spec.AllowDataDeletion,
		)
		if err != nil {
			return &humioapi.Repository{}, err
		}
	}

	if curRepository.IngestRetentionSizeGB != float64(hr.Spec.Retention.IngestSizeInGB) {
		err = h.apiClient.Repositories().UpdateIngestBasedRetention(
			hr.Spec.Name,
			float64(hr.Spec.Retention.IngestSizeInGB),
			hr.Spec.AllowDataDeletion,
		)
		if err != nil {
			return &humioapi.Repository{}, err
		}
	}

	return h.GetRepository(hr)
}

func (h *ClientConfig) DeleteRepository(hr *humiov1alpha1.HumioRepository) error {
	// perhaps we should allow calls to DeleteRepository() to include the reason instead of hardcoding it
	return h.apiClient.Repositories().Delete(
		hr.Spec.Name,
		"deleted by humio-operator",
		hr.Spec.AllowDataDeletion,
	)
}

func (h *ClientConfig) GetView(hv *humiov1alpha1.HumioView) (*humioapi.View, error) {
	viewList, err := h.apiClient.Views().List()
	if err != nil {
		return &humioapi.View{}, fmt.Errorf("could not list views: %s", err)
	}
	for _, v := range viewList {
		if v.Name == hv.Spec.Name {
			// we now know the view exists
			view, err := h.apiClient.Views().Get(hv.Spec.Name)
			return view, err
		}
	}
	return &humioapi.View{}, nil
}

func (h *ClientConfig) AddView(hv *humiov1alpha1.HumioView) (*humioapi.View, error) {
	viewConnections := hv.GetViewConnections()

	view := humioapi.View{
		Name:        hv.Spec.Name,
		Connections: viewConnections,
	}

	connectionMap := GetConnectionMap(viewConnections)
	err := h.apiClient.Views().Create(hv.Spec.Name, hv.Spec.Description, connectionMap)
	return &view, err
}

func (h *ClientConfig) UpdateView(hv *humiov1alpha1.HumioView) (*humioapi.View, error) {
	curView, err := h.GetView(hv)
	if err != nil {
		return &humioapi.View{}, err
	}
	connections := hv.GetViewConnections()
	if reflect.DeepEqual(curView.Connections, connections) == false {
		err = h.apiClient.Views().UpdateConnections(
			hv.Spec.Name,
			GetConnectionMap(connections),
		)
		if err != nil {
			return &humioapi.View{}, err
		}
	}

	return h.GetView(hv)
}

func GetConnectionMap(viewConnections []humioapi.ViewConnection) map[string]string {
	connectionMap := make(map[string]string)
	for _, connection := range viewConnections {
		connectionMap[connection.RepoName] = connection.Filter
	}
	return connectionMap
}
