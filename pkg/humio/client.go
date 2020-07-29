package humio

import (
	"fmt"

	humioapi "github.com/humio/cli/api"
	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	"github.com/humio/humio-operator/pkg/helpers"
	"go.uber.org/zap"
)

// Client is the interface that can be mocked
type Client interface {
	ClusterClient
	IngestTokensClient
	ParsersClient
	RepositoriesClient
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
	GetBaseURL(*corev1alpha1.HumioCluster) string
	TestAPIToken() error
	Status() (humioapi.StatusResponse, error)
}

type IngestTokensClient interface {
	AddIngestToken(*corev1alpha1.HumioIngestToken) (*humioapi.IngestToken, error)
	GetIngestToken(*corev1alpha1.HumioIngestToken) (*humioapi.IngestToken, error)
	UpdateIngestToken(*corev1alpha1.HumioIngestToken) (*humioapi.IngestToken, error)
	DeleteIngestToken(*corev1alpha1.HumioIngestToken) error
}

type ParsersClient interface {
	AddParser(*corev1alpha1.HumioParser) (*humioapi.Parser, error)
	GetParser(*corev1alpha1.HumioParser) (*humioapi.Parser, error)
	UpdateParser(*corev1alpha1.HumioParser) (*humioapi.Parser, error)
	DeleteParser(*corev1alpha1.HumioParser) error
}

type RepositoriesClient interface {
	AddRepository(*corev1alpha1.HumioRepository) (*humioapi.Repository, error)
	GetRepository(*corev1alpha1.HumioRepository) (*humioapi.Repository, error)
	UpdateRepository(*corev1alpha1.HumioRepository) (*humioapi.Repository, error)
	DeleteRepository(*corev1alpha1.HumioRepository) error
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
	if len(config.CACertificate) == 0 {
		config.CACertificate = h.apiClient.CACertificate()
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

// GetBaseURL returns the base URL for given HumioCluster
func (h *ClientConfig) GetBaseURL(hc *corev1alpha1.HumioCluster) string {
	protocol := "https"
	if !helpers.TLSEnabled(hc) {
		protocol = "http"
	}
	return fmt.Sprintf("%s://%s.%s:%d/", protocol, hc.Name, hc.Namespace, 8080)

}

// GetBaseURL returns the base URL for given HumioCluster
func (h *ClientConfig) TestAPIToken() error {
	if h.apiClient == nil {
		return fmt.Errorf("api client not set yet")
	}
	_, err := h.apiClient.Viewer().Username()
	return err
}

func (h *ClientConfig) AddIngestToken(hit *corev1alpha1.HumioIngestToken) (*humioapi.IngestToken, error) {
	return h.apiClient.IngestTokens().Add(hit.Spec.RepositoryName, hit.Spec.Name, hit.Spec.ParserName)
}

func (h *ClientConfig) GetIngestToken(hit *corev1alpha1.HumioIngestToken) (*humioapi.IngestToken, error) {
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

func (h *ClientConfig) UpdateIngestToken(hit *corev1alpha1.HumioIngestToken) (*humioapi.IngestToken, error) {
	return h.apiClient.IngestTokens().Update(hit.Spec.RepositoryName, hit.Spec.Name, hit.Spec.ParserName)
}

func (h *ClientConfig) DeleteIngestToken(hit *corev1alpha1.HumioIngestToken) error {
	return h.apiClient.IngestTokens().Remove(hit.Spec.RepositoryName, hit.Spec.Name)
}

func (h *ClientConfig) AddParser(hp *corev1alpha1.HumioParser) (*humioapi.Parser, error) {
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

func (h *ClientConfig) GetParser(hp *corev1alpha1.HumioParser) (*humioapi.Parser, error) {
	return h.apiClient.Parsers().Get(hp.Spec.RepositoryName, hp.Spec.Name)
}

func (h *ClientConfig) UpdateParser(hp *corev1alpha1.HumioParser) (*humioapi.Parser, error) {
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

func (h *ClientConfig) DeleteParser(hp *corev1alpha1.HumioParser) error {
	return h.apiClient.Parsers().Remove(hp.Spec.RepositoryName, hp.Spec.Name)
}

func (h *ClientConfig) AddRepository(hr *corev1alpha1.HumioRepository) (*humioapi.Repository, error) {
	repository := humioapi.Repository{Name: hr.Spec.Name}
	err := h.apiClient.Repositories().Create(hr.Spec.Name)
	return &repository, err
}

func (h *ClientConfig) GetRepository(hr *corev1alpha1.HumioRepository) (*humioapi.Repository, error) {
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

func (h *ClientConfig) UpdateRepository(hr *corev1alpha1.HumioRepository) (*humioapi.Repository, error) {
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

func (h *ClientConfig) DeleteRepository(hr *corev1alpha1.HumioRepository) error {
	// perhaps we should allow calls to DeleteRepository() to include the reason instead of hardcoding it
	return h.apiClient.Repositories().Delete(
		hr.Spec.Name,
		"deleted by humio-operator",
		hr.Spec.AllowDataDeletion,
	)
}
