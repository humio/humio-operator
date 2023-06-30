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
	"net/http"
	"net/url"
	"reflect"
	"sync"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"

	"github.com/humio/cli/api"
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
	UsersClient
	ViewsClient
	LicenseClient
	ActionsClient
	AlertsClient
}

type ClusterClient interface {
	GetClusters(*humioapi.Config, reconcile.Request) (humioapi.Cluster, error)
	UpdateStoragePartitionScheme(*humioapi.Config, reconcile.Request, []humioapi.StoragePartitionInput) error
	UpdateIngestPartitionScheme(*humioapi.Config, reconcile.Request, []humioapi.IngestPartitionInput) error
	SuggestedStoragePartitions(*humioapi.Config, reconcile.Request) ([]humioapi.StoragePartitionInput, error)
	SuggestedIngestPartitions(*humioapi.Config, reconcile.Request) ([]humioapi.IngestPartitionInput, error)
	GetHumioClient(*humioapi.Config, reconcile.Request) *humioapi.Client
	ClearHumioClientConnections()
	GetBaseURL(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioCluster) *url.URL
	TestAPIToken(*humioapi.Config, reconcile.Request) error
	Status(*humioapi.Config, reconcile.Request) (humioapi.StatusResponse, error)
}

type IngestTokensClient interface {
	AddIngestToken(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioIngestToken) (*humioapi.IngestToken, error)
	GetIngestToken(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioIngestToken) (*humioapi.IngestToken, error)
	UpdateIngestToken(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioIngestToken) (*humioapi.IngestToken, error)
	DeleteIngestToken(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioIngestToken) error
}

type ParsersClient interface {
	AddParser(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioParser) (*humioapi.Parser, error)
	GetParser(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioParser) (*humioapi.Parser, error)
	UpdateParser(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioParser) (*humioapi.Parser, error)
	DeleteParser(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioParser) error
}

type RepositoriesClient interface {
	AddRepository(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioRepository) (*humioapi.Repository, error)
	GetRepository(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioRepository) (*humioapi.Repository, error)
	UpdateRepository(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioRepository) (*humioapi.Repository, error)
	DeleteRepository(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioRepository) error
}

type UsersClient interface {
	AddUser(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioUser) (*humioapi.User, error)
	GetUser(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioUser) (*humioapi.User, error)
	UpdateUser(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioUser) (*humioapi.User, error)
	DeleteUser(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioUser) error
}

type ViewsClient interface {
	AddView(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioView) (*humioapi.View, error)
	GetView(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioView) (*humioapi.View, error)
	UpdateView(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioView) (*humioapi.View, error)
	DeleteView(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioView) error
}

type ActionsClient interface {
	AddAction(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioAction) (*humioapi.Action, error)
	GetAction(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioAction) (*humioapi.Action, error)
	UpdateAction(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioAction) (*humioapi.Action, error)
	DeleteAction(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioAction) error
}

type AlertsClient interface {
	AddAlert(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioAlert) (*humioapi.Alert, error)
	GetAlert(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioAlert) (*humioapi.Alert, error)
	UpdateAlert(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioAlert) (*humioapi.Alert, error)
	DeleteAlert(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioAlert) error
	GetActionIDsMapForAlerts(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioAlert) (map[string]string, error)
}

type LicenseClient interface {
	GetLicense(*humioapi.Config, reconcile.Request) (humioapi.License, error)
	InstallLicense(*humioapi.Config, reconcile.Request, string) error
}

// ClientConfig stores our Humio api client
type ClientConfig struct {
	humioClients      map[humioClientKey]*humioClientConnection
	humioClientsMutex sync.Mutex
	logger            logr.Logger
	userAgent         string
}

type humioClientKey struct {
	namespace, name string
	authenticated   bool
}

type humioClientConnection struct {
	client    *humioapi.Client
	transport *http.Transport
}

// NewClient returns a ClientConfig
func NewClient(logger logr.Logger, config *humioapi.Config, userAgent string) *ClientConfig {
	transport := humioapi.NewHttpTransport(*config)
	return NewClientWithTransport(logger, config, userAgent, transport)
}

// NewClientWithTransport returns a ClientConfig using an existing http.Transport
func NewClientWithTransport(logger logr.Logger, config *humioapi.Config, userAgent string, transport *http.Transport) *ClientConfig {
	return &ClientConfig{
		logger:       logger,
		userAgent:    userAgent,
		humioClients: map[humioClientKey]*humioClientConnection{},
	}
}

// GetHumioClient takes a Humio API config as input and returns an API client that uses this config
func (h *ClientConfig) GetHumioClient(config *humioapi.Config, req ctrl.Request) *humioapi.Client {
	h.humioClientsMutex.Lock()
	defer h.humioClientsMutex.Unlock()

	config.UserAgent = h.userAgent
	key := humioClientKey{
		namespace:     req.Namespace,
		name:          req.Name,
		authenticated: config.Token != "",
	}

	c := h.humioClients[key]
	if c == nil {
		transport := humioapi.NewHttpTransport(*config)
		c = &humioClientConnection{
			client:    humioapi.NewClientWithTransport(*config, transport),
			transport: transport,
		}
	} else {
		existingConfig := c.client.Config()
		equal := existingConfig.Token == config.Token &&
			existingConfig.Insecure == config.Insecure &&
			existingConfig.CACertificatePEM == config.CACertificatePEM &&
			existingConfig.ProxyOrganization == config.ProxyOrganization &&
			existingConfig.Address.String() == config.Address.String()

		// If the cluster address or SSL configuration has changed, we must create a new transport
		if !equal {
			transport := humioapi.NewHttpTransport(*config)
			c = &humioClientConnection{
				client:    humioapi.NewClientWithTransport(*config, transport),
				transport: transport,
			}

		}
		if c.transport == nil {
			c.transport = humioapi.NewHttpTransport(*config)
		}
		// Always create a new client and use the existing transport. Since we're using the same transport, connections
		// will be cached.
		c.client = humioapi.NewClientWithTransport(*config, c.transport)
	}

	h.humioClients[key] = c

	return c.client
}

func (h *ClientConfig) ClearHumioClientConnections() {
	h.humioClientsMutex.Lock()
	defer h.humioClientsMutex.Unlock()

	h.humioClients = make(map[humioClientKey]*humioClientConnection)
}

// Status returns the status of the humio cluster
func (h *ClientConfig) Status(config *humioapi.Config, req reconcile.Request) (humioapi.StatusResponse, error) {
	status, err := h.GetHumioClient(config, req).Status()
	if err != nil {
		h.logger.Error(err, "could not get status")
		return humioapi.StatusResponse{}, err
	}
	return *status, err
}

// GetClusters returns a humio cluster and can be mocked via the Client interface
func (h *ClientConfig) GetClusters(config *humioapi.Config, req reconcile.Request) (humioapi.Cluster, error) {
	clusters, err := h.GetHumioClient(config, req).Clusters().Get()
	if err != nil {
		h.logger.Error(err, "could not get cluster information")
	}
	return clusters, err
}

// UpdateStoragePartitionScheme updates the storage partition scheme and can be mocked via the Client interface
func (h *ClientConfig) UpdateStoragePartitionScheme(config *humioapi.Config, req reconcile.Request, spi []humioapi.StoragePartitionInput) error {
	err := h.GetHumioClient(config, req).Clusters().UpdateStoragePartitionScheme(spi)
	if err != nil {
		h.logger.Error(err, "could not update storage partition scheme cluster information")
	}
	return err
}

// UpdateIngestPartitionScheme updates the ingest partition scheme and can be mocked via the Client interface
func (h *ClientConfig) UpdateIngestPartitionScheme(config *humioapi.Config, req reconcile.Request, ipi []humioapi.IngestPartitionInput) error {
	err := h.GetHumioClient(config, req).Clusters().UpdateIngestPartitionScheme(ipi)
	if err != nil {
		h.logger.Error(err, "could not update ingest partition scheme cluster information")
	}
	return err
}

// SuggestedStoragePartitions gets the suggested storage partition layout
func (h *ClientConfig) SuggestedStoragePartitions(config *humioapi.Config, req reconcile.Request) ([]humioapi.StoragePartitionInput, error) {
	return h.GetHumioClient(config, req).Clusters().SuggestedStoragePartitions()
}

// SuggestedIngestPartitions gets the suggested ingest partition layout
func (h *ClientConfig) SuggestedIngestPartitions(config *humioapi.Config, req reconcile.Request) ([]humioapi.IngestPartitionInput, error) {
	return h.GetHumioClient(config, req).Clusters().SuggestedIngestPartitions()
}

// GetBaseURL returns the base URL for given HumioCluster
func (h *ClientConfig) GetBaseURL(config *humioapi.Config, req reconcile.Request, hc *humiov1alpha1.HumioCluster) *url.URL {
	protocol := "https"
	if !helpers.TLSEnabled(hc) {
		protocol = "http"
	}
	baseURL, _ := url.Parse(fmt.Sprintf("%s://%s-headless.%s:%d/", protocol, hc.Name, hc.Namespace, 8080))
	return baseURL

}

// TestAPIToken tests if an API token is valid by fetching the username that the API token belongs to
func (h *ClientConfig) TestAPIToken(config *humioapi.Config, req reconcile.Request) error {
	_, err := h.GetHumioClient(config, req).Viewer().Username()
	return err
}

func (h *ClientConfig) AddIngestToken(config *humioapi.Config, req reconcile.Request, hit *humiov1alpha1.HumioIngestToken) (*humioapi.IngestToken, error) {
	return h.GetHumioClient(config, req).IngestTokens().Add(hit.Spec.RepositoryName, hit.Spec.Name, hit.Spec.ParserName)
}

func (h *ClientConfig) GetIngestToken(config *humioapi.Config, req reconcile.Request, hit *humiov1alpha1.HumioIngestToken) (*humioapi.IngestToken, error) {
	tokens, err := h.GetHumioClient(config, req).IngestTokens().List(hit.Spec.RepositoryName)
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

func (h *ClientConfig) UpdateIngestToken(config *humioapi.Config, req reconcile.Request, hit *humiov1alpha1.HumioIngestToken) (*humioapi.IngestToken, error) {
	return h.GetHumioClient(config, req).IngestTokens().Update(hit.Spec.RepositoryName, hit.Spec.Name, hit.Spec.ParserName)
}

func (h *ClientConfig) DeleteIngestToken(config *humioapi.Config, req reconcile.Request, hit *humiov1alpha1.HumioIngestToken) error {
	return h.GetHumioClient(config, req).IngestTokens().Remove(hit.Spec.RepositoryName, hit.Spec.Name)
}

func (h *ClientConfig) AddParser(config *humioapi.Config, req reconcile.Request, hp *humiov1alpha1.HumioParser) (*humioapi.Parser, error) {
	parser := humioapi.Parser{
		Name:      hp.Spec.Name,
		Script:    hp.Spec.ParserScript,
		TagFields: hp.Spec.TagFields,
		Tests:     hp.Spec.TestData,
	}
	err := h.GetHumioClient(config, req).Parsers().Add(
		hp.Spec.RepositoryName,
		&parser,
		false,
	)
	return &parser, err
}

func (h *ClientConfig) GetParser(config *humioapi.Config, req reconcile.Request, hp *humiov1alpha1.HumioParser) (*humioapi.Parser, error) {
	return h.GetHumioClient(config, req).Parsers().Get(hp.Spec.RepositoryName, hp.Spec.Name)
}

func (h *ClientConfig) UpdateParser(config *humioapi.Config, req reconcile.Request, hp *humiov1alpha1.HumioParser) (*humioapi.Parser, error) {
	parser := humioapi.Parser{
		Name:      hp.Spec.Name,
		Script:    hp.Spec.ParserScript,
		TagFields: hp.Spec.TagFields,
		Tests:     hp.Spec.TestData,
	}
	err := h.GetHumioClient(config, req).Parsers().Add(
		hp.Spec.RepositoryName,
		&parser,
		true,
	)
	return &parser, err
}

func (h *ClientConfig) DeleteParser(config *humioapi.Config, req reconcile.Request, hp *humiov1alpha1.HumioParser) error {
	return h.GetHumioClient(config, req).Parsers().Remove(hp.Spec.RepositoryName, hp.Spec.Name)
}

func (h *ClientConfig) AddRepository(config *humioapi.Config, req reconcile.Request, hr *humiov1alpha1.HumioRepository) (*humioapi.Repository, error) {
	repository := humioapi.Repository{Name: hr.Spec.Name}
	err := h.GetHumioClient(config, req).Repositories().Create(hr.Spec.Name)
	return &repository, err
}

func (h *ClientConfig) GetRepository(config *humioapi.Config, req reconcile.Request, hr *humiov1alpha1.HumioRepository) (*humioapi.Repository, error) {
	repoList, err := h.GetHumioClient(config, req).Repositories().List()
	if err != nil {
		return &humioapi.Repository{}, fmt.Errorf("could not list repositories: %w", err)
	}
	for _, repo := range repoList {
		if repo.Name == hr.Spec.Name {
			// we now know the repository exists
			repository, err := h.GetHumioClient(config, req).Repositories().Get(hr.Spec.Name)
			return &repository, err
		}
	}
	return &humioapi.Repository{}, nil
}

func (h *ClientConfig) UpdateRepository(config *humioapi.Config, req reconcile.Request, hr *humiov1alpha1.HumioRepository) (*humioapi.Repository, error) {
	curRepository, err := h.GetRepository(config, req, hr)
	if err != nil {
		return &humioapi.Repository{}, err
	}

	if curRepository.Description != hr.Spec.Description {
		err = h.GetHumioClient(config, req).Repositories().UpdateDescription(
			hr.Spec.Name,
			hr.Spec.Description,
		)
		if err != nil {
			return &humioapi.Repository{}, err
		}
	}

	if curRepository.RetentionDays != float64(hr.Spec.Retention.TimeInDays) {
		err = h.GetHumioClient(config, req).Repositories().UpdateTimeBasedRetention(
			hr.Spec.Name,
			float64(hr.Spec.Retention.TimeInDays),
			hr.Spec.AllowDataDeletion,
		)
		if err != nil {
			return &humioapi.Repository{}, err
		}
	}

	if curRepository.StorageRetentionSizeGB != float64(hr.Spec.Retention.StorageSizeInGB) {
		err = h.GetHumioClient(config, req).Repositories().UpdateStorageBasedRetention(
			hr.Spec.Name,
			float64(hr.Spec.Retention.StorageSizeInGB),
			hr.Spec.AllowDataDeletion,
		)
		if err != nil {
			return &humioapi.Repository{}, err
		}
	}

	if curRepository.IngestRetentionSizeGB != float64(hr.Spec.Retention.IngestSizeInGB) {
		err = h.GetHumioClient(config, req).Repositories().UpdateIngestBasedRetention(
			hr.Spec.Name,
			float64(hr.Spec.Retention.IngestSizeInGB),
			hr.Spec.AllowDataDeletion,
		)
		if err != nil {
			return &humioapi.Repository{}, err
		}
	}

	return h.GetRepository(config, req, hr)
}

func (h *ClientConfig) DeleteRepository(config *humioapi.Config, req reconcile.Request, hr *humiov1alpha1.HumioRepository) error {
	// TODO: perhaps we should allow calls to DeleteRepository() to include the reason instead of hardcoding it
	return h.GetHumioClient(config, req).Repositories().Delete(
		hr.Spec.Name,
		"deleted by humio-operator",
		hr.Spec.AllowDataDeletion,
	)
}

func (h *ClientConfig) GetView(config *humioapi.Config, req reconcile.Request, hv *humiov1alpha1.HumioView) (*humioapi.View, error) {
	viewList, err := h.GetHumioClient(config, req).Views().List()
	if err != nil {
		return &humioapi.View{}, fmt.Errorf("could not list views: %w", err)
	}
	for _, v := range viewList {
		if v.Name == hv.Spec.Name {
			// we now know the view exists
			view, err := h.GetHumioClient(config, req).Views().Get(hv.Spec.Name)
			return view, err
		}
	}
	return &humioapi.View{}, nil
}

func (h *ClientConfig) AddView(config *humioapi.Config, req reconcile.Request, hv *humiov1alpha1.HumioView) (*humioapi.View, error) {
	viewConnections := hv.GetViewConnections()

	view := humioapi.View{
		Name:        hv.Spec.Name,
		Connections: viewConnections,
	}

	description := ""
	connectionMap := getConnectionMap(viewConnections)

	err := h.GetHumioClient(config, req).Views().Create(hv.Spec.Name, description, connectionMap)
	return &view, err
}

func (h *ClientConfig) UpdateView(config *humioapi.Config, req reconcile.Request, hv *humiov1alpha1.HumioView) (*humioapi.View, error) {
	curView, err := h.GetView(config, req, hv)
	if err != nil {
		return &humioapi.View{}, err
	}

	connections := hv.GetViewConnections()
	if reflect.DeepEqual(curView.Connections, connections) {
		return h.GetView(config, req, hv)
	}

	err = h.GetHumioClient(config, req).Views().UpdateConnections(
		hv.Spec.Name,
		getConnectionMap(connections),
	)
	if err != nil {
		return &humioapi.View{}, err
	}

	return h.GetView(config, req, hv)
}

func (h *ClientConfig) DeleteView(config *humioapi.Config, req reconcile.Request, hv *humiov1alpha1.HumioView) error {
	return h.GetHumioClient(config, req).Views().Delete(hv.Spec.Name, "Deleted by humio-operator")
}

func (h *ClientConfig) validateView(config *humioapi.Config, req reconcile.Request, viewName string) error {
	view := &humiov1alpha1.HumioView{
		Spec: humiov1alpha1.HumioViewSpec{
			Name: viewName,
		},
	}

	viewResult, err := h.GetView(config, req, view)
	if err != nil {
		return fmt.Errorf("failed to verify view %s exists. error: %w", viewName, err)
	}

	emptyView := &humioapi.View{}
	if reflect.DeepEqual(emptyView, viewResult) {
		return fmt.Errorf("view %s does not exist", viewName)
	}

	return nil
}

func (h *ClientConfig) GetAction(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAction) (*humioapi.Action, error) {
	err := h.validateView(config, req, ha.Spec.ViewName)
	if err != nil {
		return nil, fmt.Errorf("problem getting view for action %s: %w", ha.Spec.Name, err)
	}

	action, err := h.GetHumioClient(config, req).Actions().Get(ha.Spec.ViewName, ha.Spec.Name)
	if err != nil {
		return action, fmt.Errorf("error when trying to get action %+v, name=%s, view=%s: %w", action, ha.Spec.Name, ha.Spec.ViewName, err)
	}

	if action == nil || action.Name == "" {
		return nil, nil
	}

	return action, nil
}

func (h *ClientConfig) AddAction(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAction) (*humioapi.Action, error) {
	err := h.validateView(config, req, ha.Spec.ViewName)
	if err != nil {
		return nil, fmt.Errorf("problem getting view for action %s: %w", ha.Spec.Name, err)
	}

	action, err := ActionFromActionCR(ha)
	if err != nil {
		return action, err
	}

	createdAction, err := h.GetHumioClient(config, req).Actions().Add(ha.Spec.ViewName, action)
	if err != nil {
		return createdAction, fmt.Errorf("got error when attempting to add action: %w", err)
	}
	return createdAction, nil
}

func (h *ClientConfig) UpdateAction(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAction) (*humioapi.Action, error) {
	err := h.validateView(config, req, ha.Spec.ViewName)
	if err != nil {
		return nil, fmt.Errorf("problem getting view for action %s: %w", ha.Spec.Name, err)
	}

	action, err := ActionFromActionCR(ha)
	if err != nil {
		return action, err
	}

	return h.GetHumioClient(config, req).Actions().Update(ha.Spec.ViewName, action)
}

func (h *ClientConfig) DeleteAction(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAction) error {
	return h.GetHumioClient(config, req).Actions().Delete(ha.Spec.ViewName, ha.Spec.Name)
}

func getConnectionMap(viewConnections []humioapi.ViewConnection) map[string]string {
	connectionMap := make(map[string]string)
	for _, connection := range viewConnections {
		connectionMap[connection.RepoName] = connection.Filter
	}
	return connectionMap
}

func (h *ClientConfig) GetLicense(config *humioapi.Config, req reconcile.Request) (humioapi.License, error) {
	return h.GetHumioClient(config, req).Licenses().Get()
}

func (h *ClientConfig) InstallLicense(config *humioapi.Config, req reconcile.Request, license string) error {
	return h.GetHumioClient(config, req).Licenses().Install(license)
}

func (h *ClientConfig) GetAlert(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAlert) (*humioapi.Alert, error) {
	err := h.validateView(config, req, ha.Spec.ViewName)
	if err != nil {
		return &humioapi.Alert{}, fmt.Errorf("problem getting view for action %s: %w", ha.Spec.Name, err)
	}

	alert, err := h.GetHumioClient(config, req).Alerts().Get(ha.Spec.ViewName, ha.Spec.Name)
	if err != nil {
		return alert, fmt.Errorf("error when trying to get alert %+v, name=%s, view=%s: %w", alert, ha.Spec.Name, ha.Spec.ViewName, err)
	}

	if alert == nil || alert.Name == "" {
		return nil, nil
	}

	return alert, nil
}

func (h *ClientConfig) AddAlert(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAlert) (*humioapi.Alert, error) {
	err := h.validateView(config, req, ha.Spec.ViewName)
	if err != nil {
		return &humioapi.Alert{}, fmt.Errorf("problem getting view for action: %w", err)
	}

	actionIdMap, err := h.GetActionIDsMapForAlerts(config, req, ha)
	if err != nil {
		return &humioapi.Alert{}, fmt.Errorf("could not get action id mapping: %w", err)
	}
	alert, err := AlertTransform(ha, actionIdMap)
	if err != nil {
		return alert, err
	}

	createdAlert, err := h.GetHumioClient(config, req).Alerts().Add(ha.Spec.ViewName, alert)
	if err != nil {
		return createdAlert, fmt.Errorf("got error when attempting to add alert: %w, alert: %#v", err, *alert)
	}
	return createdAlert, nil
}

func (h *ClientConfig) UpdateAlert(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAlert) (*humioapi.Alert, error) {
	err := h.validateView(config, req, ha.Spec.ViewName)
	if err != nil {
		return &humioapi.Alert{}, fmt.Errorf("problem getting view for action: %w", err)
	}

	actionIdMap, err := h.GetActionIDsMapForAlerts(config, req, ha)
	if err != nil {
		return &humioapi.Alert{}, fmt.Errorf("could not get action id mapping: %w", err)
	}
	alert, err := AlertTransform(ha, actionIdMap)
	if err != nil {
		return alert, err
	}

	currentAlert, err := h.GetAlert(config, req, ha)
	if err != nil {
		return &humioapi.Alert{}, fmt.Errorf("could not find alert with name: %q", alert.Name)
	}
	alert.ID = currentAlert.ID

	return h.GetHumioClient(config, req).Alerts().Update(ha.Spec.ViewName, alert)
}

func (h *ClientConfig) DeleteAlert(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAlert) error {
	return h.GetHumioClient(config, req).Alerts().Delete(ha.Spec.ViewName, ha.Spec.Name)
}

func (h *ClientConfig) getAndValidateAction(config *humioapi.Config, req reconcile.Request, actionName string, viewName string) (*humioapi.Action, error) {
	action := &humiov1alpha1.HumioAction{
		Spec: humiov1alpha1.HumioActionSpec{
			Name:     actionName,
			ViewName: viewName,
		},
	}

	actionResult, err := h.GetAction(config, req, action)
	if err != nil {
		return actionResult, fmt.Errorf("failed to verify action %s exists. error: %w", actionName, err)
	}

	emptyAction := &humioapi.Action{}
	if reflect.DeepEqual(emptyAction, actionResult) {
		return actionResult, fmt.Errorf("action %s does not exist", actionName)
	}

	return actionResult, nil
}

func (h *ClientConfig) GetActionIDsMapForAlerts(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAlert) (map[string]string, error) {
	actionIdMap := make(map[string]string)
	for _, actionNameForAlert := range ha.Spec.Actions {
		action, err := h.getAndValidateAction(config, req, actionNameForAlert, ha.Spec.ViewName)
		if err != nil {
			return actionIdMap, fmt.Errorf("problem getting action for alert %s: %w", ha.Spec.Name, err)
		}
		actionIdMap[actionNameForAlert] = action.ID

	}
	return actionIdMap, nil
}

func (h *ClientConfig) AddUser(config *humioapi.Config, req reconcile.Request, hu *humiov1alpha1.HumioUser) (*humioapi.User, error) {
	user := humioapi.User{Username: hu.Spec.Username}
	_, err := h.GetHumioClient(config, req).Users().Add(hu.Spec.Username, api.UserChangeSet{
		IsRoot:      &hu.Spec.IsRoot,
		FullName:    &hu.Spec.FullName,
		Company:     &hu.Spec.Company,
		CountryCode: &hu.Spec.CountryCode,
		Email:       &hu.Spec.Email,
		Picture:     &hu.Spec.Picture,
	})
	return &user, err
}

func (h *ClientConfig) GetUser(config *humioapi.Config, req reconcile.Request, hu *humiov1alpha1.HumioUser) (*humioapi.User, error) {
	// userList, err := h.GetHumioClient(config, req).Users().List()
	// if err != nil {
	// 	return &humioapi.User{}, fmt.Errorf("could not list users: %w", err)
	// }
	// for _, user := range userList {
	// 	if user.Username == hu.Spec.Username {
	// 		// we now know the user exists
	// 		user, err := h.GetHumioClient(config, req).Users().Get(hu.Spec.Username)
	// 		return &user, err
	// 	}
	// }
	user, err := h.GetHumioClient(config, req).Users().Get(hu.Spec.Username)
	return &user, err
}

func (h *ClientConfig) UpdateUser(config *humioapi.Config, req reconcile.Request, hu *humiov1alpha1.HumioUser) (*humioapi.User, error) {
	curUser, err := h.GetUser(config, req, hu)
	if err != nil {
		return &humioapi.User{}, err
	}

	if curUser.Email != hu.Spec.Email ||
		curUser.FullName != hu.Spec.FullName ||
		curUser.Company != hu.Spec.Company ||
		curUser.CountryCode != hu.Spec.CountryCode ||
		curUser.Picture != hu.Spec.Picture ||
		curUser.IsRoot != hu.Spec.IsRoot {
		_, err = h.GetHumioClient(config, req).Users().Update(
			hu.Spec.Username,
			api.UserChangeSet{
				Email:       &hu.Spec.Email,
				FullName:    &hu.Spec.FullName,
				Company:     &hu.Spec.Company,
				CountryCode: &hu.Spec.CountryCode,
				Picture:     &hu.Spec.Picture,
				IsRoot:      &hu.Spec.IsRoot,
			},
		)
		if err != nil {
			return &humioapi.User{}, err
		}
	}

	return h.GetUser(config, req, hu)
}

func (h *ClientConfig) DeleteUser(config *humioapi.Config, req reconcile.Request, hu *humiov1alpha1.HumioUser) error {
	_, err := h.GetHumioClient(config, req).Users().Remove(hu.Spec.Username)
	if err != nil {
		return fmt.Errorf("could not delete user: %w", err)
	}
	return err
}
