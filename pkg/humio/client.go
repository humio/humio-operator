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
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"sync"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	graphql "github.com/cli/shurcooL-graphql"
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
	ActionsClient
	AlertsClient
	FilterAlertsClient
	AggregateAlertsClient
	ScheduledSearchClient
	UsersClient
}

type ClusterClient interface {
	GetClusters(*humioapi.Config, reconcile.Request) (humioapi.Cluster, error)
	GetHumioClient(*humioapi.Config, reconcile.Request) *humioapi.Client
	ClearHumioClientConnections(string)
	GetBaseURL(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioCluster) *url.URL
	TestAPIToken(*humioapi.Config, reconcile.Request) error
	Status(*humioapi.Config, reconcile.Request) (*humioapi.StatusResponse, error)
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

type FilterAlertsClient interface {
	AddFilterAlert(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioFilterAlert) (*humioapi.FilterAlert, error)
	GetFilterAlert(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioFilterAlert) (*humioapi.FilterAlert, error)
	UpdateFilterAlert(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioFilterAlert) (*humioapi.FilterAlert, error)
	DeleteFilterAlert(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioFilterAlert) error
	ValidateActionsForFilterAlert(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioFilterAlert) error
}

type AggregateAlertsClient interface {
	AddAggregateAlert(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioAggregateAlert) (*humioapi.AggregateAlert, error)
	GetAggregateAlert(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioAggregateAlert) (*humioapi.AggregateAlert, error)
	UpdateAggregateAlert(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioAggregateAlert) (*humioapi.AggregateAlert, error)
	DeleteAggregateAlert(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioAggregateAlert) error
	ValidateActionsForAggregateAlert(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioAggregateAlert) error
}

type ScheduledSearchClient interface {
	AddScheduledSearch(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioScheduledSearch) (*humioapi.ScheduledSearch, error)
	GetScheduledSearch(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioScheduledSearch) (*humioapi.ScheduledSearch, error)
	UpdateScheduledSearch(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioScheduledSearch) (*humioapi.ScheduledSearch, error)
	DeleteScheduledSearch(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioScheduledSearch) error
	ValidateActionsForScheduledSearch(*humioapi.Config, reconcile.Request, *humiov1alpha1.HumioScheduledSearch) error
}

type LicenseClient interface {
	GetLicense(*humioapi.Config, reconcile.Request) (humioapi.License, error)
	InstallLicense(*humioapi.Config, reconcile.Request, string) error
}

type UsersClient interface {
	AddUser(*humioapi.Config, reconcile.Request, string, bool) (*humioapi.User, error)
	ListAllHumioUsersInCurrentOrganization(*humioapi.Config, reconcile.Request) ([]user, error)
	RotateUserApiTokenAndGet(*humioapi.Config, reconcile.Request, string) (string, error)
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
func NewClient(logger logr.Logger, userAgent string) *ClientConfig {
	return NewClientWithTransport(logger, userAgent)
}

// NewClientWithTransport returns a ClientConfig using an existing http.Transport
func NewClientWithTransport(logger logr.Logger, userAgent string) *ClientConfig {
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

func (h *ClientConfig) ClearHumioClientConnections(string) {
	h.humioClientsMutex.Lock()
	defer h.humioClientsMutex.Unlock()

	h.humioClients = make(map[humioClientKey]*humioClientConnection)
}

// Status returns the status of the humio cluster
func (h *ClientConfig) Status(config *humioapi.Config, req reconcile.Request) (*humioapi.StatusResponse, error) {
	return h.GetHumioClient(config, req).Status()
}

// GetClusters returns a humio cluster and can be mocked via the Client interface
func (h *ClientConfig) GetClusters(config *humioapi.Config, req reconcile.Request) (humioapi.Cluster, error) {
	return h.GetHumioClient(config, req).Clusters().Get()
}

// GetBaseURL returns the base URL for given HumioCluster
func (h *ClientConfig) GetBaseURL(config *humioapi.Config, req reconcile.Request, hc *humiov1alpha1.HumioCluster) *url.URL {
	protocol := "https"
	if !helpers.TLSEnabled(hc) {
		protocol = "http"
	}
	baseURL, _ := url.Parse(fmt.Sprintf("%s://%s-internal.%s:%d/", protocol, hc.Name, hc.Namespace, 8080))
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
	return h.GetHumioClient(config, req).IngestTokens().Get(hit.Spec.RepositoryName, hit.Spec.Name)
}

func (h *ClientConfig) UpdateIngestToken(config *humioapi.Config, req reconcile.Request, hit *humiov1alpha1.HumioIngestToken) (*humioapi.IngestToken, error) {
	return h.GetHumioClient(config, req).IngestTokens().Update(hit.Spec.RepositoryName, hit.Spec.Name, hit.Spec.ParserName)
}

func (h *ClientConfig) DeleteIngestToken(config *humioapi.Config, req reconcile.Request, hit *humiov1alpha1.HumioIngestToken) error {
	return h.GetHumioClient(config, req).IngestTokens().Remove(hit.Spec.RepositoryName, hit.Spec.Name)
}

func (h *ClientConfig) AddParser(config *humioapi.Config, req reconcile.Request, hp *humiov1alpha1.HumioParser) (*humioapi.Parser, error) {
	parser := humioapi.Parser{
		Name:        hp.Spec.Name,
		Script:      hp.Spec.ParserScript,
		FieldsToTag: hp.Spec.TagFields,
	}

	testCasesGQL := make([]humioapi.ParserTestCase, len(hp.Spec.TestData))
	for i := range hp.Spec.TestData {
		testCasesGQL[i] = humioapi.ParserTestCase{
			Event: humioapi.ParserTestEvent{
				RawString: hp.Spec.TestData[i],
			},
		}
	}
	parser.TestCases = testCasesGQL

	return h.GetHumioClient(config, req).Parsers().Add(
		hp.Spec.RepositoryName,
		&parser,
		false,
	)
}

func (h *ClientConfig) GetParser(config *humioapi.Config, req reconcile.Request, hp *humiov1alpha1.HumioParser) (*humioapi.Parser, error) {
	return h.GetHumioClient(config, req).Parsers().Get(hp.Spec.RepositoryName, hp.Spec.Name)
}

func (h *ClientConfig) UpdateParser(config *humioapi.Config, req reconcile.Request, hp *humiov1alpha1.HumioParser) (*humioapi.Parser, error) {
	parser := humioapi.Parser{
		Name:        hp.Spec.Name,
		Script:      hp.Spec.ParserScript,
		FieldsToTag: hp.Spec.TagFields,
	}

	testCasesGQL := make([]humioapi.ParserTestCase, len(hp.Spec.TestData))
	for i := range hp.Spec.TestData {
		testCasesGQL[i] = humioapi.ParserTestCase{
			Event: humioapi.ParserTestEvent{RawString: hp.Spec.TestData[i]},
		}
	}
	parser.TestCases = testCasesGQL

	return h.GetHumioClient(config, req).Parsers().Add(
		hp.Spec.RepositoryName,
		&parser,
		true,
	)
}

func (h *ClientConfig) DeleteParser(config *humioapi.Config, req reconcile.Request, hp *humiov1alpha1.HumioParser) error {
	_, err := h.GetParser(config, req, hp)
	if errors.As(err, &humioapi.EntityNotFound{}) {
		return nil
	}
	return h.GetHumioClient(config, req).Parsers().Delete(hp.Spec.RepositoryName, hp.Spec.Name)
}

func (h *ClientConfig) AddRepository(config *humioapi.Config, req reconcile.Request, hr *humiov1alpha1.HumioRepository) (*humioapi.Repository, error) {
	repository := humioapi.Repository{Name: hr.Spec.Name}
	err := h.GetHumioClient(config, req).Repositories().Create(hr.Spec.Name)
	return &repository, err
}

func (h *ClientConfig) GetRepository(config *humioapi.Config, req reconcile.Request, hr *humiov1alpha1.HumioRepository) (*humioapi.Repository, error) {
	repo, err := h.GetHumioClient(config, req).Repositories().Get(hr.Spec.Name)
	if err != nil {
		return nil, err
	}
	return &repo, nil
}

func (h *ClientConfig) UpdateRepository(config *humioapi.Config, req reconcile.Request, hr *humiov1alpha1.HumioRepository) (*humioapi.Repository, error) {
	curRepository, err := h.GetRepository(config, req, hr)
	if err != nil {
		return nil, err
	}

	if curRepository.Description != hr.Spec.Description {
		err = h.GetHumioClient(config, req).Repositories().UpdateDescription(
			hr.Spec.Name,
			hr.Spec.Description,
		)
		if err != nil {
			return nil, err
		}
	}

	if curRepository.RetentionDays != float64(hr.Spec.Retention.TimeInDays) {
		err = h.GetHumioClient(config, req).Repositories().UpdateTimeBasedRetention(
			hr.Spec.Name,
			float64(hr.Spec.Retention.TimeInDays),
			hr.Spec.AllowDataDeletion,
		)
		if err != nil {
			return nil, err
		}
	}

	if curRepository.StorageRetentionSizeGB != float64(hr.Spec.Retention.StorageSizeInGB) {
		err = h.GetHumioClient(config, req).Repositories().UpdateStorageBasedRetention(
			hr.Spec.Name,
			float64(hr.Spec.Retention.StorageSizeInGB),
			hr.Spec.AllowDataDeletion,
		)
		if err != nil {
			return nil, err
		}
	}

	if curRepository.IngestRetentionSizeGB != float64(hr.Spec.Retention.IngestSizeInGB) {
		err = h.GetHumioClient(config, req).Repositories().UpdateIngestBasedRetention(
			hr.Spec.Name,
			float64(hr.Spec.Retention.IngestSizeInGB),
			hr.Spec.AllowDataDeletion,
		)
		if err != nil {
			return nil, err
		}
	}

	if curRepository.AutomaticSearch != helpers.BoolTrue(hr.Spec.AutomaticSearch) {
		err = h.GetHumioClient(config, req).Repositories().UpdateAutomaticSearch(
			hr.Spec.Name,
			helpers.BoolTrue(hr.Spec.AutomaticSearch),
		)
		if err != nil {
			return nil, err
		}
	}

	return h.GetRepository(config, req, hr)
}

func (h *ClientConfig) DeleteRepository(config *humioapi.Config, req reconcile.Request, hr *humiov1alpha1.HumioRepository) error {
	_, err := h.GetRepository(config, req, hr)
	if errors.As(err, &humioapi.EntityNotFound{}) {
		return nil
	}
	// TODO: perhaps we should allow calls to DeleteRepository() to include the reason instead of hardcoding it
	return h.GetHumioClient(config, req).Repositories().Delete(
		hr.Spec.Name,
		"deleted by humio-operator",
		hr.Spec.AllowDataDeletion,
	)
}

func (h *ClientConfig) GetView(config *humioapi.Config, req reconcile.Request, hv *humiov1alpha1.HumioView) (*humioapi.View, error) {
	return h.GetHumioClient(config, req).Views().Get(hv.Spec.Name)
}

func (h *ClientConfig) AddView(config *humioapi.Config, req reconcile.Request, hv *humiov1alpha1.HumioView) (*humioapi.View, error) {
	viewConnections := hv.GetViewConnections()

	view := humioapi.View{
		Name:        hv.Spec.Name,
		Connections: viewConnections,
	}

	description := ""

	err := h.GetHumioClient(config, req).Views().Create(hv.Spec.Name, description, getConnectionMap(viewConnections))
	return &view, err
}

func (h *ClientConfig) UpdateView(config *humioapi.Config, req reconcile.Request, hv *humiov1alpha1.HumioView) (*humioapi.View, error) {
	curView, err := h.GetView(config, req, hv)
	if err != nil {
		return nil, err
	}

	if curView.Description != hv.Spec.Description {
		err = h.GetHumioClient(config, req).Views().UpdateDescription(
			hv.Spec.Name,
			hv.Spec.Description,
		)
		if err != nil {
			return nil, err
		}
	}

	if curView.AutomaticSearch != helpers.BoolTrue(hv.Spec.AutomaticSearch) {
		err = h.GetHumioClient(config, req).Views().UpdateAutomaticSearch(
			hv.Spec.Name,
			helpers.BoolTrue(hv.Spec.AutomaticSearch),
		)
		if err != nil {
			return nil, err
		}
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
		return nil, err
	}

	return h.GetView(config, req, hv)
}

func (h *ClientConfig) DeleteView(config *humioapi.Config, req reconcile.Request, hv *humiov1alpha1.HumioView) error {
	_, err := h.GetView(config, req, hv)
	if errors.As(err, &humioapi.EntityNotFound{}) {
		return nil
	}
	return h.GetHumioClient(config, req).Views().Delete(hv.Spec.Name, "Deleted by humio-operator")
}

func (h *ClientConfig) validateSearchDomain(config *humioapi.Config, req reconcile.Request, searchDomainName string) error {
	_, err := h.GetHumioClient(config, req).SearchDomains().Get(searchDomainName)
	return err
}

func (h *ClientConfig) GetAction(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAction) (*humioapi.Action, error) {
	err := h.validateSearchDomain(config, req, ha.Spec.ViewName)
	if err != nil {
		return nil, fmt.Errorf("problem getting view for action %s: %w", ha.Spec.Name, err)
	}

	return h.GetHumioClient(config, req).Actions().Get(ha.Spec.ViewName, ha.Spec.Name)
}

func (h *ClientConfig) AddAction(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAction) (*humioapi.Action, error) {
	err := h.validateSearchDomain(config, req, ha.Spec.ViewName)
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
	err := h.validateSearchDomain(config, req, ha.Spec.ViewName)
	if err != nil {
		return nil, fmt.Errorf("problem getting view for action %s: %w", ha.Spec.Name, err)
	}

	action, err := ActionFromActionCR(ha)
	if err != nil {
		return action, err
	}

	currentAction, err := h.GetAction(config, req, ha)
	if err != nil {
		return nil, fmt.Errorf("could not find action with name: %q", ha.Spec.Name)
	}
	action.ID = currentAction.ID

	return h.GetHumioClient(config, req).Actions().Update(ha.Spec.ViewName, action)
}

func (h *ClientConfig) DeleteAction(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAction) error {
	_, err := h.GetAction(config, req, ha)
	if errors.As(err, &humioapi.EntityNotFound{}) {
		return nil
	}
	return h.GetHumioClient(config, req).Actions().Delete(ha.Spec.ViewName, ha.Spec.Name)
}

func getConnectionMap(viewConnections []humioapi.ViewConnection) []humioapi.ViewConnectionInput {
	connectionMap := make([]humioapi.ViewConnectionInput, 0)
	for _, connection := range viewConnections {
		connectionMap = append(connectionMap, humioapi.ViewConnectionInput{
			RepositoryName: graphql.String(connection.RepoName),
			Filter:         graphql.String(connection.Filter),
		})
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
	err := h.validateSearchDomain(config, req, ha.Spec.ViewName)
	if err != nil {
		return &humioapi.Alert{}, fmt.Errorf("problem getting view for alert %s: %w", ha.Spec.Name, err)
	}

	return h.GetHumioClient(config, req).Alerts().Get(ha.Spec.ViewName, ha.Spec.Name)
}

func (h *ClientConfig) AddAlert(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAlert) (*humioapi.Alert, error) {
	err := h.validateSearchDomain(config, req, ha.Spec.ViewName)
	if err != nil {
		return &humioapi.Alert{}, fmt.Errorf("problem getting view for alert: %w", err)
	}

	actionIdMap, err := h.GetActionIDsMapForAlerts(config, req, ha)
	if err != nil {
		return &humioapi.Alert{}, fmt.Errorf("could not get action id mapping: %w", err)
	}

	alert := AlertTransform(ha, actionIdMap)
	createdAlert, err := h.GetHumioClient(config, req).Alerts().Add(ha.Spec.ViewName, alert)
	if err != nil {
		return createdAlert, fmt.Errorf("got error when attempting to add alert: %w, alert: %#v", err, *alert)
	}
	return createdAlert, nil
}

func (h *ClientConfig) UpdateAlert(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAlert) (*humioapi.Alert, error) {
	err := h.validateSearchDomain(config, req, ha.Spec.ViewName)
	if err != nil {
		return &humioapi.Alert{}, fmt.Errorf("problem getting view for action: %w", err)
	}

	actionIdMap, err := h.GetActionIDsMapForAlerts(config, req, ha)
	if err != nil {
		return &humioapi.Alert{}, fmt.Errorf("could not get action id mapping: %w", err)
	}

	alert := AlertTransform(ha, actionIdMap)
	currentAlert, err := h.GetAlert(config, req, ha)
	if err != nil {
		return &humioapi.Alert{}, fmt.Errorf("could not find alert with name: %q", alert.Name)
	}
	alert.ID = currentAlert.ID

	return h.GetHumioClient(config, req).Alerts().Update(ha.Spec.ViewName, alert)
}

func (h *ClientConfig) DeleteAlert(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAlert) error {
	_, err := h.GetAlert(config, req, ha)
	if errors.As(err, &humioapi.EntityNotFound{}) {
		return nil
	}
	return h.GetHumioClient(config, req).Alerts().Delete(ha.Spec.ViewName, ha.Spec.Name)
}

func (h *ClientConfig) GetFilterAlert(config *humioapi.Config, req reconcile.Request, hfa *humiov1alpha1.HumioFilterAlert) (*humioapi.FilterAlert, error) {
	err := h.validateSearchDomain(config, req, hfa.Spec.ViewName)
	if err != nil {
		return &humioapi.FilterAlert{}, fmt.Errorf("problem getting view for filter alert %s: %w", hfa.Spec.Name, err)
	}

	var filterAlertId string
	filterAlertsList, err := h.GetHumioClient(config, req).FilterAlerts().List(hfa.Spec.ViewName)
	if err != nil {
		return nil, fmt.Errorf("unable to list filter alerts: %w", err)
	}
	for _, filterAlert := range filterAlertsList {
		if filterAlert.Name == hfa.Spec.Name {
			filterAlertId = filterAlert.ID
		}
	}
	if filterAlertId == "" {
		return nil, humioapi.FilterAlertNotFound(hfa.Spec.Name)
	}
	filterAlert, err := h.GetHumioClient(config, req).FilterAlerts().Get(hfa.Spec.ViewName, filterAlertId)
	if err != nil {
		return filterAlert, fmt.Errorf("error when trying to get filter alert %+v, name=%s, view=%s: %w", filterAlert, hfa.Spec.Name, hfa.Spec.ViewName, err)
	}

	if filterAlert == nil || filterAlert.Name == "" {
		return nil, nil
	}

	return filterAlert, nil
}

func (h *ClientConfig) AddFilterAlert(config *humioapi.Config, req reconcile.Request, hfa *humiov1alpha1.HumioFilterAlert) (*humioapi.FilterAlert, error) {
	err := h.validateSearchDomain(config, req, hfa.Spec.ViewName)
	if err != nil {
		return &humioapi.FilterAlert{}, fmt.Errorf("problem getting view for filter alert: %w", err)
	}
	if err = h.ValidateActionsForFilterAlert(config, req, hfa); err != nil {
		return &humioapi.FilterAlert{}, fmt.Errorf("could not get action id mapping: %w", err)
	}

	filterAlert := FilterAlertTransform(hfa)
	createdAlert, err := h.GetHumioClient(config, req).FilterAlerts().Create(hfa.Spec.ViewName, filterAlert)
	if err != nil {
		return createdAlert, fmt.Errorf("got error when attempting to add filter alert: %w, filteralert: %#v", err, *filterAlert)
	}
	return createdAlert, nil
}

func (h *ClientConfig) UpdateFilterAlert(config *humioapi.Config, req reconcile.Request, hfa *humiov1alpha1.HumioFilterAlert) (*humioapi.FilterAlert, error) {
	err := h.validateSearchDomain(config, req, hfa.Spec.ViewName)
	if err != nil {
		return &humioapi.FilterAlert{}, fmt.Errorf("problem getting view for action: %w", err)
	}
	if err = h.ValidateActionsForFilterAlert(config, req, hfa); err != nil {
		return &humioapi.FilterAlert{}, fmt.Errorf("could not get action id mapping: %w", err)
	}

	filterAlert := FilterAlertTransform(hfa)
	currentAlert, err := h.GetFilterAlert(config, req, hfa)
	if err != nil {
		return &humioapi.FilterAlert{}, fmt.Errorf("could not find filter alert with name: %q", filterAlert.Name)
	}
	filterAlert.ID = currentAlert.ID

	return h.GetHumioClient(config, req).FilterAlerts().Update(hfa.Spec.ViewName, filterAlert)
}

func (h *ClientConfig) DeleteFilterAlert(config *humioapi.Config, req reconcile.Request, hfa *humiov1alpha1.HumioFilterAlert) error {
	currentAlert, err := h.GetFilterAlert(config, req, hfa)
	if errors.As(err, &humioapi.EntityNotFound{}) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("could not find filter alert with name: %q", hfa.Name)
	}
	return h.GetHumioClient(config, req).FilterAlerts().Delete(hfa.Spec.ViewName, currentAlert.ID)
}

func (h *ClientConfig) AddScheduledSearch(config *humioapi.Config, req reconcile.Request, hss *humiov1alpha1.HumioScheduledSearch) (*humioapi.ScheduledSearch, error) {
	err := h.validateSearchDomain(config, req, hss.Spec.ViewName)
	if err != nil {
		return &humioapi.ScheduledSearch{}, fmt.Errorf("problem getting view for scheduled search: %w", err)
	}
	if err = h.ValidateActionsForScheduledSearch(config, req, hss); err != nil {
		return &humioapi.ScheduledSearch{}, fmt.Errorf("could not get action id mapping: %w", err)
	}
	scheduledSearch := ScheduledSearchTransform(hss)

	createdScheduledSearch, err := h.GetHumioClient(config, req).ScheduledSearches().Create(hss.Spec.ViewName, scheduledSearch)
	if err != nil {
		return createdScheduledSearch, fmt.Errorf("got error when attempting to add scheduled search: %w, scheduledsearch: %#v", err, *scheduledSearch)
	}
	return createdScheduledSearch, nil
}

func (h *ClientConfig) GetScheduledSearch(config *humioapi.Config, req reconcile.Request, hss *humiov1alpha1.HumioScheduledSearch) (*humioapi.ScheduledSearch, error) {
	err := h.validateSearchDomain(config, req, hss.Spec.ViewName)
	if err != nil {
		return &humioapi.ScheduledSearch{}, fmt.Errorf("problem getting view for scheduled search %s: %w", hss.Spec.Name, err)
	}

	var scheduledSearchId string
	scheduledSearchList, err := h.GetHumioClient(config, req).ScheduledSearches().List(hss.Spec.ViewName)
	if err != nil {
		return nil, fmt.Errorf("unable to list scheduled searches: %w", err)
	}
	for _, scheduledSearch := range scheduledSearchList {
		if scheduledSearch.Name == hss.Spec.Name {
			scheduledSearchId = scheduledSearch.ID
		}
	}
	if scheduledSearchId == "" {
		return nil, humioapi.ScheduledSearchNotFound(hss.Spec.Name)
	}
	scheduledSearch, err := h.GetHumioClient(config, req).ScheduledSearches().Get(hss.Spec.ViewName, scheduledSearchId)
	if err != nil {
		return scheduledSearch, fmt.Errorf("error when trying to get scheduled search %+v, name=%s, view=%s: %w", scheduledSearch, hss.Spec.Name, hss.Spec.ViewName, err)
	}

	if scheduledSearch == nil || scheduledSearch.Name == "" {
		return nil, nil
	}

	return scheduledSearch, nil
}

func (h *ClientConfig) UpdateScheduledSearch(config *humioapi.Config, req reconcile.Request, hss *humiov1alpha1.HumioScheduledSearch) (*humioapi.ScheduledSearch, error) {
	err := h.validateSearchDomain(config, req, hss.Spec.ViewName)
	if err != nil {
		return &humioapi.ScheduledSearch{}, fmt.Errorf("problem getting view for scheduled search: %w", err)
	}
	if err = h.ValidateActionsForScheduledSearch(config, req, hss); err != nil {
		return &humioapi.ScheduledSearch{}, fmt.Errorf("could not get action id mapping: %w", err)
	}
	scheduledSearch := ScheduledSearchTransform(hss)

	currentScheduledSearch, err := h.GetScheduledSearch(config, req, hss)
	if err != nil {
		return &humioapi.ScheduledSearch{}, fmt.Errorf("could not find scheduled search with name: %q", scheduledSearch.Name)
	}
	scheduledSearch.ID = currentScheduledSearch.ID

	return h.GetHumioClient(config, req).ScheduledSearches().Update(hss.Spec.ViewName, scheduledSearch)
}

func (h *ClientConfig) DeleteScheduledSearch(config *humioapi.Config, req reconcile.Request, hss *humiov1alpha1.HumioScheduledSearch) error {
	currentScheduledSearch, err := h.GetScheduledSearch(config, req, hss)
	if errors.As(err, &humioapi.EntityNotFound{}) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("could not find scheduled search with name: %q", hss.Name)
	}
	return h.GetHumioClient(config, req).ScheduledSearches().Delete(hss.Spec.ViewName, currentScheduledSearch.ID)
}

func (h *ClientConfig) getAndValidateAction(config *humioapi.Config, req reconcile.Request, actionName string, viewName string) (*humioapi.Action, error) {
	action := &humiov1alpha1.HumioAction{
		Spec: humiov1alpha1.HumioActionSpec{
			Name:     actionName,
			ViewName: viewName,
		},
	}

	return h.GetAction(config, req, action)
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

func (h *ClientConfig) ValidateActionsForFilterAlert(config *humioapi.Config, req reconcile.Request, hfa *humiov1alpha1.HumioFilterAlert) error {
	for _, actionNameForAlert := range hfa.Spec.Actions {
		if _, err := h.getAndValidateAction(config, req, actionNameForAlert, hfa.Spec.ViewName); err != nil {
			return fmt.Errorf("problem getting action for filter alert %s: %w", hfa.Spec.Name, err)
		}
	}
	return nil
}

func (h *ClientConfig) ValidateActionsForScheduledSearch(config *humioapi.Config, req reconcile.Request, hss *humiov1alpha1.HumioScheduledSearch) error {
	for _, actionNameForScheduledSearch := range hss.Spec.Actions {
		if _, err := h.getAndValidateAction(config, req, actionNameForScheduledSearch, hss.Spec.ViewName); err != nil {
			return fmt.Errorf("problem getting action for scheduled search %s: %w", hss.Spec.Name, err)
		}
	}
	return nil
}

func (h *ClientConfig) AddAggregateAlert(config *humioapi.Config, req reconcile.Request, haa *humiov1alpha1.HumioAggregateAlert) (*humioapi.AggregateAlert, error) {
	err := h.validateSearchDomain(config, req, haa.Spec.ViewName)
	if err != nil {
		return &humioapi.AggregateAlert{}, fmt.Errorf("problem getting view for action: %w", err)
	}
	if err = h.ValidateActionsForAggregateAlert(config, req, haa); err != nil {
		return &humioapi.AggregateAlert{}, fmt.Errorf("could not get action id mapping: %w", err)
	}

	aggregateAlert := AggregateAlertTransform(haa)
	createdAggregateAlert, err := h.GetHumioClient(config, req).AggregateAlerts().Create(haa.Spec.ViewName, aggregateAlert)
	if err != nil {
		return createdAggregateAlert, fmt.Errorf("got error when attempting to add aggregate alert: %w, aggregatealert: %#v", err, *aggregateAlert)
	}
	return createdAggregateAlert, nil
}

func (h *ClientConfig) GetAggregateAlert(config *humioapi.Config, req reconcile.Request, haa *humiov1alpha1.HumioAggregateAlert) (*humioapi.AggregateAlert, error) {
	err := h.validateSearchDomain(config, req, haa.Spec.ViewName)
	if err != nil {
		return &humioapi.AggregateAlert{}, fmt.Errorf("problem getting view for action %s: %w", haa.Spec.Name, err)
	}

	var aggregateAlertId string
	aggregateAlertsList, err := h.GetHumioClient(config, req).AggregateAlerts().List(haa.Spec.ViewName)
	if err != nil {
		return nil, fmt.Errorf("unable to list aggregate alerts: %w", err)
	}
	for _, aggregateAlert := range aggregateAlertsList {
		if aggregateAlert.Name == haa.Spec.Name {
			aggregateAlertId = aggregateAlert.ID
		}
	}
	if aggregateAlertId == "" {
		return nil, humioapi.AggregateAlertNotFound(haa.Spec.Name)
	}
	aggregateAlert, err := h.GetHumioClient(config, req).AggregateAlerts().Get(haa.Spec.ViewName, aggregateAlertId)
	if err != nil {
		return aggregateAlert, fmt.Errorf("error when trying to get aggregate alert %+v, name=%s, view=%s: %w", aggregateAlert, haa.Spec.Name, haa.Spec.ViewName, err)
	}

	if aggregateAlert == nil || aggregateAlert.Name == "" {
		return nil, nil
	}

	return aggregateAlert, nil
}

func (h *ClientConfig) UpdateAggregateAlert(config *humioapi.Config, req reconcile.Request, haa *humiov1alpha1.HumioAggregateAlert) (*humioapi.AggregateAlert, error) {
	err := h.validateSearchDomain(config, req, haa.Spec.ViewName)
	if err != nil {
		return &humioapi.AggregateAlert{}, fmt.Errorf("problem getting view for action %s: %w", haa.Spec.Name, err)
	}
	if err = h.ValidateActionsForAggregateAlert(config, req, haa); err != nil {
		return &humioapi.AggregateAlert{}, fmt.Errorf("could not get action id mapping: %w", err)
	}
	aggregateAlert := AggregateAlertTransform(haa)
	currentAggregateAlert, err := h.GetAggregateAlert(config, req, haa)
	if err != nil {
		return &humioapi.AggregateAlert{}, fmt.Errorf("could not find aggregate alert with namer: %q", aggregateAlert.Name)
	}
	aggregateAlert.ID = currentAggregateAlert.ID

	return h.GetHumioClient(config, req).AggregateAlerts().Update(haa.Spec.ViewName, aggregateAlert)
}

func (h *ClientConfig) DeleteAggregateAlert(config *humioapi.Config, req reconcile.Request, haa *humiov1alpha1.HumioAggregateAlert) error {
	currentAggregateAlert, err := h.GetAggregateAlert(config, req, haa)
	if errors.As(err, &humioapi.EntityNotFound{}) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("could not find aggregate alert with name: %q", haa.Name)
	}
	return h.GetHumioClient(config, req).AggregateAlerts().Delete(haa.Spec.ViewName, currentAggregateAlert.ID)
}

func (h *ClientConfig) ValidateActionsForAggregateAlert(config *humioapi.Config, req reconcile.Request, haa *humiov1alpha1.HumioAggregateAlert) error {
	// validate action
	for _, actionNameForAlert := range haa.Spec.Actions {
		if _, err := h.getAndValidateAction(config, req, actionNameForAlert, haa.Spec.ViewName); err != nil {
			return fmt.Errorf("problem getting action for aggregate alert %s: %w", haa.Spec.Name, err)
		}
	}
	return nil
}

type user struct {
	Id       string
	Username string
}

type OrganizationSearchResultEntry struct {
	EntityId         string `graphql:"entityId"`
	SearchMatch      string `graphql:"searchMatch"`
	OrganizationName string `graphql:"organizationName"`
}

func (h *ClientConfig) ListAllHumioUsersInCurrentOrganization(config *humioapi.Config, req reconcile.Request) ([]user, error) {
	var q struct {
		Users []user `graphql:"users"`
	}
	err := h.GetHumioClient(config, req).Query(&q, nil)
	return q.Users, err
}

func (h *ClientConfig) RotateUserApiTokenAndGet(config *humioapi.Config, req reconcile.Request, userID string) (string, error) {
	token, err := h.GetHumioClient(config, req).Users().RotateToken(userID)
	if err != nil {
		return "", fmt.Errorf("could not rotate apiToken for userID %s, err: %w", userID, err)
	}
	return token, nil
}

func (h *ClientConfig) AddUser(config *humioapi.Config, req reconcile.Request, username string, isRoot bool) (*humioapi.User, error) {
	user, err := h.GetHumioClient(config, req).Users().Add(username, humioapi.UserChangeSet{
		IsRoot: &isRoot,
	})
	if err != nil {
		return &humioapi.User{}, err
	}
	return &user, nil
}
