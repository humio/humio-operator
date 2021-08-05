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
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/go-logr/logr"

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
	LicenseClient
	NotifiersClient
	AlertsClient
}

type ClusterClient interface {
	GetClusters() (humioapi.Cluster, error)
	UpdateStoragePartitionScheme([]humioapi.StoragePartitionInput) error
	UpdateIngestPartitionScheme([]humioapi.IngestPartitionInput) error
	StartDataRedistribution() error
	ClusterMoveStorageRouteAwayFromNode(int) error
	ClusterMoveIngestRoutesAwayFromNode(int) error
	Unregister(int) error
	SuggestedStoragePartitions() ([]humioapi.StoragePartitionInput, error)
	SuggestedIngestPartitions() ([]humioapi.IngestPartitionInput, error)
	SetHumioClientConfig(*humioapi.Config, ctrl.Request)
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
	DeleteView(view *humiov1alpha1.HumioView) error
}

type NotifiersClient interface {
	AddNotifier(*humiov1alpha1.HumioAction) (*humioapi.Notifier, error)
	GetNotifier(*humiov1alpha1.HumioAction) (*humioapi.Notifier, error)
	UpdateNotifier(*humiov1alpha1.HumioAction) (*humioapi.Notifier, error)
	DeleteNotifier(*humiov1alpha1.HumioAction) error
}

type AlertsClient interface {
	AddAlert(alert *humiov1alpha1.HumioAlert) (*humioapi.Alert, error)
	GetAlert(alert *humiov1alpha1.HumioAlert) (*humioapi.Alert, error)
	UpdateAlert(alert *humiov1alpha1.HumioAlert) (*humioapi.Alert, error)
	DeleteAlert(alert *humiov1alpha1.HumioAlert) error
	GetActionIDsMapForAlerts(*humiov1alpha1.HumioAlert) (map[string]string, error)
}

type LicenseClient interface {
	GetLicense() (humioapi.License, error)
	InstallLicense(string) error
}

// ClientConfig stores our Humio api client
type ClientConfig struct {
	apiClient    *humioapi.Client
	logger       logr.Logger
	userAgent    string
	humioClients map[humioClientKey]*humioapi.Client
}

type humioClientKey struct {
	namespace, name string
	authenticated   bool
}

// NewClient returns a ClientConfig
func NewClient(logger logr.Logger, config *humioapi.Config, userAgent string) *ClientConfig {
	client := humioapi.NewClient(*config)
	return &ClientConfig{
		apiClient:    client,
		logger:       logger,
		userAgent:    userAgent,
		humioClients: map[humioClientKey]*humioapi.Client{},
	}
}

// SetHumioClientConfig takes a Humio API config as input and ensures to create a new API client that uses this config
func (h *ClientConfig) SetHumioClientConfig(config *humioapi.Config, req ctrl.Request) {
	config.UserAgent = h.userAgent
	key := humioClientKey{
		namespace:     req.Namespace,
		name:          req.Name,
		authenticated: config.Token != "",
	}
	c := h.humioClients[key]
	if c == nil {
		c = humioapi.NewClient(*config)
	} else {
		existingConfig := c.Config()
		equal := existingConfig.Token == config.Token &&
			existingConfig.Insecure == config.Insecure &&
			existingConfig.CACertificatePEM == config.CACertificatePEM &&
			existingConfig.ProxyOrganization == config.ProxyOrganization &&
			existingConfig.Address.String() == config.Address.String()
		if !equal {
			c = humioapi.NewClient(*config)
		}
	}
	h.humioClients[key] = c
	h.apiClient = c
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

// SuggestedStoragePartitions gets the suggested storage partition layout
func (h *ClientConfig) SuggestedStoragePartitions() ([]humioapi.StoragePartitionInput, error) {
	return h.apiClient.Clusters().SuggestedStoragePartitions()
}

// SuggestedIngestPartitions gets the suggested ingest partition layout
func (h *ClientConfig) SuggestedIngestPartitions() ([]humioapi.IngestPartitionInput, error) {
	return h.apiClient.Clusters().SuggestedIngestPartitions()
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
		Tests:     hp.Spec.TestData,
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
		Tests:     hp.Spec.TestData,
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

	description := ""
	connectionMap := getConnectionMap(viewConnections)

	err := h.apiClient.Views().Create(hv.Spec.Name, description, connectionMap)
	return &view, err
}

func (h *ClientConfig) UpdateView(hv *humiov1alpha1.HumioView) (*humioapi.View, error) {
	curView, err := h.GetView(hv)
	if err != nil {
		return &humioapi.View{}, err
	}

	connections := hv.GetViewConnections()
	if reflect.DeepEqual(curView.Connections, connections) {
		return h.GetView(hv)
	}

	err = h.apiClient.Views().UpdateConnections(
		hv.Spec.Name,
		getConnectionMap(connections),
	)
	if err != nil {
		return &humioapi.View{}, err
	}

	return h.GetView(hv)
}

func (h *ClientConfig) DeleteView(hv *humiov1alpha1.HumioView) error {
	return h.apiClient.Views().Delete(hv.Spec.Name, "Deleted by humio-operator")
}

func (h *ClientConfig) validateView(viewName string) error {
	view := &humiov1alpha1.HumioView{
		Spec: humiov1alpha1.HumioViewSpec{
			Name: viewName,
		},
	}

	viewResult, err := h.GetView(view)
	if err != nil {
		return fmt.Errorf("failed to verify view %s exists. error: %s", viewName, err)
	}

	emptyView := &humioapi.View{}
	if reflect.DeepEqual(emptyView, viewResult) {
		return fmt.Errorf("view %s does not exist", viewName)
	}

	return nil
}

func (h *ClientConfig) GetNotifier(ha *humiov1alpha1.HumioAction) (*humioapi.Notifier, error) {
	err := h.validateView(ha.Spec.ViewName)
	if err != nil {
		return &humioapi.Notifier{}, fmt.Errorf("problem getting view for action %s: %s", ha.Spec.Name, err)
	}

	notifier, err := h.apiClient.Notifiers().Get(ha.Spec.ViewName, ha.Spec.Name)
	if err != nil {
		return notifier, fmt.Errorf("error when trying to get notifier %+v, name=%s, view=%s: %s", notifier, ha.Spec.Name, ha.Spec.ViewName, err)
	}

	if notifier == nil || notifier.Name == "" {
		return nil, nil
	}

	return notifier, nil
}

func (h *ClientConfig) AddNotifier(ha *humiov1alpha1.HumioAction) (*humioapi.Notifier, error) {
	err := h.validateView(ha.Spec.ViewName)
	if err != nil {
		return &humioapi.Notifier{}, fmt.Errorf("problem getting view for action %s: %s", ha.Spec.Name, err)
	}

	notifier, err := NotifierFromAction(ha)
	if err != nil {
		return notifier, err
	}

	createdNotifier, err := h.apiClient.Notifiers().Add(ha.Spec.ViewName, notifier, false)
	if err != nil {
		return createdNotifier, fmt.Errorf("got error when attempting to add notifier: %s", err)
	}
	return createdNotifier, nil
}

func (h *ClientConfig) UpdateNotifier(ha *humiov1alpha1.HumioAction) (*humioapi.Notifier, error) {
	err := h.validateView(ha.Spec.ViewName)
	if err != nil {
		return &humioapi.Notifier{}, fmt.Errorf("problem getting view for action %s: %s", ha.Spec.Name, err)
	}

	notifier, err := NotifierFromAction(ha)
	if err != nil {
		return notifier, err
	}

	return h.apiClient.Notifiers().Update(ha.Spec.ViewName, notifier)
}

func (h *ClientConfig) DeleteNotifier(ha *humiov1alpha1.HumioAction) error {
	return h.apiClient.Notifiers().Delete(ha.Spec.ViewName, ha.Spec.Name)
}

func getConnectionMap(viewConnections []humioapi.ViewConnection) map[string]string {
	connectionMap := make(map[string]string)
	for _, connection := range viewConnections {
		connectionMap[connection.RepoName] = connection.Filter
	}
	return connectionMap
}

func (h *ClientConfig) GetLicense() (humioapi.License, error) {
	licensesClient := h.apiClient.Licenses()
	emptyConfig := humioapi.Config{}
	if !reflect.DeepEqual(h.apiClient.Config(), emptyConfig) && h.apiClient.Config().Address != nil {
		return licensesClient.Get()
	}
	return nil, fmt.Errorf("no api client configured yet")
}

func (h *ClientConfig) InstallLicense(license string) error {
	licensesClient := h.apiClient.Licenses()
	return licensesClient.Install(license)
}

func (h *ClientConfig) GetAlert(ha *humiov1alpha1.HumioAlert) (*humioapi.Alert, error) {
	err := h.validateView(ha.Spec.ViewName)
	if err != nil {
		return &humioapi.Alert{}, fmt.Errorf("problem getting view for action %s: %s", ha.Spec.Name, err)
	}

	alert, err := h.apiClient.Alerts().Get(ha.Spec.ViewName, ha.Spec.Name)
	if err != nil {
		return alert, fmt.Errorf("error when trying to get alert %+v, name=%s, view=%s: %s", alert, ha.Spec.Name, ha.Spec.ViewName, err)
	}

	if alert == nil || alert.Name == "" {
		return nil, nil
	}

	return alert, nil
}

func (h *ClientConfig) AddAlert(ha *humiov1alpha1.HumioAlert) (*humioapi.Alert, error) {
	err := h.validateView(ha.Spec.ViewName)
	if err != nil {
		return &humioapi.Alert{}, fmt.Errorf("problem getting view for action: %s", err)
	}

	actionIdMap, err := h.GetActionIDsMapForAlerts(ha)
	if err != nil {
		return &humioapi.Alert{}, fmt.Errorf("could not get action id mapping: %s", err)
	}
	alert, err := AlertTransform(ha, actionIdMap)
	if err != nil {
		return alert, err
	}

	createdAlert, err := h.apiClient.Alerts().Add(ha.Spec.ViewName, alert, false)
	if err != nil {
		return createdAlert, fmt.Errorf("got error when attempting to add alert: %s, alert: %#v", err, *alert)
	}
	return createdAlert, nil
}

func (h *ClientConfig) UpdateAlert(ha *humiov1alpha1.HumioAlert) (*humioapi.Alert, error) {
	err := h.validateView(ha.Spec.ViewName)
	if err != nil {
		return &humioapi.Alert{}, fmt.Errorf("problem getting view for action: %s", err)
	}

	actionIdMap, err := h.GetActionIDsMapForAlerts(ha)
	if err != nil {
		return &humioapi.Alert{}, fmt.Errorf("could not get action id mapping: %s", err)
	}
	alert, err := AlertTransform(ha, actionIdMap)
	if err != nil {
		return alert, err
	}

	return h.apiClient.Alerts().Update(ha.Spec.ViewName, alert)
}

func (h *ClientConfig) DeleteAlert(ha *humiov1alpha1.HumioAlert) error {
	return h.apiClient.Alerts().Delete(ha.Spec.ViewName, ha.Spec.Name)
}

func (h *ClientConfig) getAndValidateAction(notifierName string, viewName string) (*humioapi.Notifier, error) {
	action := &humiov1alpha1.HumioAction{
		Spec: humiov1alpha1.HumioActionSpec{
			Name:     notifierName,
			ViewName: viewName,
		},
	}

	notifierResult, err := h.GetNotifier(action)
	if err != nil {
		return notifierResult, fmt.Errorf("failed to verify notifier %s exists. error: %s", notifierName, err)
	}

	emptyNotifier := &humioapi.Notifier{}
	if reflect.DeepEqual(emptyNotifier, notifierResult) {
		return notifierResult, fmt.Errorf("notifier %s does not exist", notifierName)
	}

	return notifierResult, nil
}

func (h *ClientConfig) GetActionIDsMapForAlerts(ha *humiov1alpha1.HumioAlert) (map[string]string, error) {
	actionIdMap := make(map[string]string)
	for _, action := range ha.Spec.Actions {
		notifier, err := h.getAndValidateAction(action, ha.Spec.ViewName)
		if err != nil {
			return actionIdMap, fmt.Errorf("problem getting action for alert %s: %s", ha.Spec.Name, err)
		}
		actionIdMap[action] = notifier.ID

	}
	return actionIdMap, nil
}
