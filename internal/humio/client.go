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
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/humio/humio-operator/internal/api/humiographql"
	"github.com/humio/humio-operator/internal/helpers"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	humioapi "github.com/humio/humio-operator/internal/api"
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
	FeatureFlagsClient
	AggregateAlertsClient
	ScheduledSearchClient
	UsersClient
	OrganizationPermissionRolesClient
	SystemPermissionRolesClient
}

type ClusterClient interface {
	GetCluster(context.Context, *humioapi.Client, reconcile.Request) (*humiographql.GetClusterResponse, error)
	GetHumioHttpClient(*humioapi.Config, reconcile.Request) *humioapi.Client
	ClearHumioClientConnections(string)
	TestAPIToken(context.Context, *humioapi.Config, reconcile.Request) error
	Status(context.Context, *humioapi.Client, reconcile.Request) (*humioapi.StatusResponse, error)
	GetEvictionStatus(context.Context, *humioapi.Client, reconcile.Request) (*humiographql.GetEvictionStatusResponse, error)
	SetIsBeingEvicted(context.Context, *humioapi.Client, reconcile.Request, int, bool) error
	RefreshClusterManagementStats(context.Context, *humioapi.Client, reconcile.Request, int) (*humiographql.RefreshClusterManagementStatsResponse, error)
	UnregisterClusterNode(context.Context, *humioapi.Client, reconcile.Request, int, bool) (*humiographql.UnregisterClusterNodeResponse, error)
}

type IngestTokensClient interface {
	AddIngestToken(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioIngestToken) error
	GetIngestToken(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioIngestToken) (*humiographql.IngestTokenDetails, error)
	UpdateIngestToken(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioIngestToken) error
	DeleteIngestToken(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioIngestToken) error
}

type ParsersClient interface {
	AddParser(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioParser) error
	GetParser(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioParser) (*humiographql.ParserDetails, error)
	UpdateParser(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioParser) error
	DeleteParser(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioParser) error
}

type RepositoriesClient interface {
	AddRepository(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioRepository) error
	GetRepository(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioRepository) (*humiographql.RepositoryDetails, error)
	UpdateRepository(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioRepository) error
	DeleteRepository(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioRepository) error
}

type ViewsClient interface {
	AddView(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioView) error
	GetView(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioView) (*humiographql.GetSearchDomainSearchDomainView, error)
	UpdateView(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioView) error
	DeleteView(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioView) error
}

type ActionsClient interface {
	AddAction(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioAction) error
	GetAction(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioAction) (humiographql.ActionDetails, error)
	UpdateAction(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioAction) error
	DeleteAction(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioAction) error
}

type AlertsClient interface {
	AddAlert(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioAlert) error
	GetAlert(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioAlert) (*humiographql.AlertDetails, error)
	UpdateAlert(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioAlert) error
	DeleteAlert(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioAlert) error
}

type FilterAlertsClient interface {
	AddFilterAlert(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioFilterAlert) error
	GetFilterAlert(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioFilterAlert) (*humiographql.FilterAlertDetails, error)
	UpdateFilterAlert(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioFilterAlert) error
	DeleteFilterAlert(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioFilterAlert) error
	ValidateActionsForFilterAlert(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioFilterAlert) error
}

type FeatureFlagsClient interface {
	GetFeatureFlags(context.Context, *humioapi.Client) ([]string, error)
	EnableFeatureFlag(context.Context, *humioapi.Client, *humiov1alpha1.HumioFeatureFlag) error
	IsFeatureFlagEnabled(context.Context, *humioapi.Client, *humiov1alpha1.HumioFeatureFlag) (bool, error)
	DisableFeatureFlag(context.Context, *humioapi.Client, *humiov1alpha1.HumioFeatureFlag) error
}

type AggregateAlertsClient interface {
	AddAggregateAlert(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioAggregateAlert) error
	GetAggregateAlert(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioAggregateAlert) (*humiographql.AggregateAlertDetails, error)
	UpdateAggregateAlert(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioAggregateAlert) error
	DeleteAggregateAlert(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioAggregateAlert) error
	ValidateActionsForAggregateAlert(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioAggregateAlert) error
}

type ScheduledSearchClient interface {
	AddScheduledSearch(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioScheduledSearch) error
	GetScheduledSearch(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioScheduledSearch) (*humiographql.ScheduledSearchDetails, error)
	UpdateScheduledSearch(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioScheduledSearch) error
	DeleteScheduledSearch(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioScheduledSearch) error
	ValidateActionsForScheduledSearch(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioScheduledSearch) error
}

type LicenseClient interface {
	GetLicenseUIDAndExpiry(context.Context, *humioapi.Client, reconcile.Request) (string, time.Time, error)
	InstallLicense(context.Context, *humioapi.Client, reconcile.Request, string) error
}

type UsersClient interface {
	AddUser(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioUser) error
	GetUser(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioUser) (*humiographql.UserDetails, error)
	UpdateUser(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioUser) error
	DeleteUser(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioUser) error

	// TODO: Rename the ones below, or perhaps get rid of them entirely?
	AddUserAndGetUserID(context.Context, *humioapi.Client, reconcile.Request, string, bool) (string, error)
	GetUserIDForUsername(context.Context, *humioapi.Client, reconcile.Request, string) (string, error)
	RotateUserApiTokenAndGet(context.Context, *humioapi.Client, reconcile.Request, string) (string, error)
}

type SystemPermissionRolesClient interface {
	AddSystemPermissionRole(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioSystemPermissionRole) error
	GetSystemPermissionRole(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioSystemPermissionRole) (*humiographql.RoleDetails, error)
	UpdateSystemPermissionRole(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioSystemPermissionRole) error
	DeleteSystemPermissionRole(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioSystemPermissionRole) error
}

type OrganizationPermissionRolesClient interface {
	AddOrganizationPermissionRole(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioOrganizationPermissionRole) error
	GetOrganizationPermissionRole(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioOrganizationPermissionRole) (*humiographql.RoleDetails, error)
	UpdateOrganizationPermissionRole(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioOrganizationPermissionRole) error
	DeleteOrganizationPermissionRole(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioOrganizationPermissionRole) error
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

// GetHumioHttpClient takes a Humio API config as input and returns an API client that uses this config
func (h *ClientConfig) GetHumioHttpClient(config *humioapi.Config, req ctrl.Request) *humioapi.Client {
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

func (h *ClientConfig) ClearHumioClientConnections(_ string) {
	h.humioClientsMutex.Lock()
	defer h.humioClientsMutex.Unlock()

	h.humioClients = make(map[humioClientKey]*humioClientConnection)
}

// Status returns the status of the humio cluster
func (h *ClientConfig) Status(ctx context.Context, client *humioapi.Client, _ reconcile.Request) (*humioapi.StatusResponse, error) {
	return client.Status(ctx)
}

// GetCluster returns a humio cluster and can be mocked via the Client interface
func (h *ClientConfig) GetCluster(ctx context.Context, client *humioapi.Client, _ reconcile.Request) (*humiographql.GetClusterResponse, error) {
	resp, err := humiographql.GetCluster(
		ctx,
		client,
	)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

// GetEvictionStatus returns the EvictionStatus of the humio cluster nodes and can be mocked via the Client interface
func (h *ClientConfig) GetEvictionStatus(ctx context.Context, client *humioapi.Client, _ reconcile.Request) (*humiographql.GetEvictionStatusResponse, error) {
	resp, err := humiographql.GetEvictionStatus(
		ctx,
		client,
	)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

// SetIsBeingEvicted sets the EvictionStatus of a humio cluster node and can be mocked via the Client interface
func (h *ClientConfig) SetIsBeingEvicted(ctx context.Context, client *humioapi.Client, _ reconcile.Request, vhost int, isBeingEvicted bool) error {
	_, err := humiographql.SetIsBeingEvicted(
		ctx,
		client,
		vhost,
		isBeingEvicted,
	)
	return err
}

// RefreshClusterManagementStats invalidates the cache and refreshes the stats related to the cluster management. This is useful for checking various cluster details,
// such as whether a node can be safely unregistered.
func (h *ClientConfig) RefreshClusterManagementStats(ctx context.Context, client *humioapi.Client, _ reconcile.Request, vhost int) (*humiographql.RefreshClusterManagementStatsResponse, error) {
	response, err := humiographql.RefreshClusterManagementStats(
		ctx,
		client,
		vhost,
	)
	return response, err
}

// UnregisterClusterNode unregisters a humio node from the cluster and can be mocked via the Client interface
func (h *ClientConfig) UnregisterClusterNode(ctx context.Context, client *humioapi.Client, _ reconcile.Request, nodeId int, force bool) (*humiographql.UnregisterClusterNodeResponse, error) {
	resp, err := humiographql.UnregisterClusterNode(
		ctx,
		client,
		nodeId,
		force,
	)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

// TestAPIToken tests if an API token is valid by fetching the username that the API token belongs to
func (h *ClientConfig) TestAPIToken(ctx context.Context, config *humioapi.Config, req reconcile.Request) error {
	humioHttpClient := h.GetHumioHttpClient(config, req)
	_, err := humiographql.GetUsername(ctx, humioHttpClient)
	return err
}

func (h *ClientConfig) AddIngestToken(ctx context.Context, client *humioapi.Client, _ reconcile.Request, hit *humiov1alpha1.HumioIngestToken) error {
	_, err := humiographql.AddIngestToken(
		ctx,
		client,
		hit.Spec.RepositoryName,
		hit.Spec.Name,
		hit.Spec.ParserName,
	)
	return err
}

func (h *ClientConfig) GetIngestToken(ctx context.Context, client *humioapi.Client, _ reconcile.Request, hit *humiov1alpha1.HumioIngestToken) (*humiographql.IngestTokenDetails, error) {
	resp, err := humiographql.ListIngestTokens(
		ctx,
		client,
		hit.Spec.RepositoryName,
	)
	if err != nil {
		return nil, err
	}
	respRepo := resp.GetRepository()
	respRepoTokens := respRepo.GetIngestTokens()
	tokensInRepo := make([]humiographql.IngestTokenDetails, len(respRepoTokens))
	for idx, token := range respRepoTokens {
		tokensInRepo[idx] = humiographql.IngestTokenDetails{
			Name:   token.GetName(),
			Token:  token.GetToken(),
			Parser: token.GetParser(),
		}
	}

	for _, token := range tokensInRepo {
		if token.Name == hit.Spec.Name {
			return &token, nil
		}
	}

	return nil, humioapi.IngestTokenNotFound(hit.Spec.Name)
}

func (h *ClientConfig) UpdateIngestToken(ctx context.Context, client *humioapi.Client, _ reconcile.Request, hit *humiov1alpha1.HumioIngestToken) error {
	if hit.Spec.ParserName != nil {
		_, err := humiographql.AssignParserToIngestToken(
			ctx,
			client,
			hit.Spec.RepositoryName,
			hit.Spec.Name,
			*hit.Spec.ParserName,
		)
		return err
	}

	_, err := humiographql.UnassignParserToIngestToken(
		ctx,
		client,
		hit.Spec.RepositoryName,
		hit.Spec.Name,
	)
	return err
}

func (h *ClientConfig) DeleteIngestToken(ctx context.Context, client *humioapi.Client, _ reconcile.Request, hit *humiov1alpha1.HumioIngestToken) error {
	_, err := humiographql.RemoveIngestToken(
		ctx,
		client,
		hit.Spec.RepositoryName,
		hit.Spec.Name,
	)
	return err
}

func (h *ClientConfig) AddParser(ctx context.Context, client *humioapi.Client, _ reconcile.Request, hp *humiov1alpha1.HumioParser) error {
	tagFields := []string{}
	if hp.Spec.TagFields != nil {
		tagFields = hp.Spec.TagFields
	}
	_, err := humiographql.CreateParserOrUpdate(
		ctx,
		client,
		hp.Spec.RepositoryName,
		hp.Spec.Name,
		hp.Spec.ParserScript,
		humioapi.TestDataToParserTestCaseInput(hp.Spec.TestData),
		tagFields,
		[]string{},
		false,
	)
	return err
}

func (h *ClientConfig) GetParser(ctx context.Context, client *humioapi.Client, _ reconcile.Request, hp *humiov1alpha1.HumioParser) (*humiographql.ParserDetails, error) {
	// list parsers to get the parser ID
	resp, err := humiographql.ListParsers(
		ctx,
		client,
		hp.Spec.RepositoryName,
	)
	if err != nil {
		return nil, err
	}
	respRepoForParserList := resp.GetRepository()
	parserList := respRepoForParserList.GetParsers()
	parserID := ""
	for i := range parserList {
		if parserList[i].Name == hp.Spec.Name {
			parserID = parserList[i].GetId()
			break
		}
	}
	if parserID == "" {
		return nil, humioapi.ParserNotFound(hp.Spec.Name)
	}

	// lookup details for the parser id
	respDetails, err := humiographql.GetParserByID(
		ctx,
		client,
		hp.Spec.RepositoryName,
		parserID,
	)
	if err != nil {
		return nil, err
	}

	respRepoForParser := respDetails.GetRepository()
	respParser := respRepoForParser.GetParser()
	if respParser != nil {
		return &respParser.ParserDetails, nil
	}

	return nil, humioapi.ParserNotFound(hp.Spec.Name)
}

func (h *ClientConfig) UpdateParser(ctx context.Context, client *humioapi.Client, _ reconcile.Request, hp *humiov1alpha1.HumioParser) error {
	_, err := humiographql.CreateParserOrUpdate(
		ctx,
		client,
		hp.Spec.RepositoryName,
		hp.Spec.Name,
		hp.Spec.ParserScript,
		humioapi.TestDataToParserTestCaseInput(hp.Spec.TestData),
		hp.Spec.TagFields,
		[]string{},
		true,
	)
	return err
}

func (h *ClientConfig) DeleteParser(ctx context.Context, client *humioapi.Client, req reconcile.Request, hp *humiov1alpha1.HumioParser) error {
	parser, err := h.GetParser(ctx, client, req, hp)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			return nil
		}
		return err
	}

	_, err = humiographql.DeleteParserByID(
		ctx,
		client,
		hp.Spec.RepositoryName,
		parser.Id,
	)
	return err
}

func (h *ClientConfig) AddRepository(ctx context.Context, client *humioapi.Client, _ reconcile.Request, hr *humiov1alpha1.HumioRepository) error {
	retentionSpec := hr.Spec.Retention
	if retentionSpec.TimeInDays != nil || retentionSpec.IngestSizeInGB != nil || retentionSpec.StorageSizeInGB != nil {
		// use CreateRepositoryWithRetention() if any retention parameters are set
		var retentionInMillis *int64
		if retentionSpec.TimeInDays != nil {
			duration := time.Duration(*retentionSpec.TimeInDays) * time.Hour * 24
			retentionInMillis = helpers.Int64Ptr(duration.Milliseconds())
		}
		var retentionInIngestSizeBytes *int64
		if retentionSpec.IngestSizeInGB != nil {
			retentionInIngestSizeBytes = helpers.Int64Ptr(int64(*retentionSpec.IngestSizeInGB) * 1024 * 1024 * 1024)
		}
		var retentionInStorageSizeBytes *int64
		if retentionSpec.StorageSizeInGB != nil {
			retentionInStorageSizeBytes = helpers.Int64Ptr(int64(*retentionSpec.StorageSizeInGB) * 1024 * 1024 * 1024)
		}
		_, err := humiographql.CreateRepositoryWithRetention(
			ctx,
			client,
			hr.Spec.Name,
			retentionInMillis,
			retentionInIngestSizeBytes,
			retentionInStorageSizeBytes,
		)
		return err
	} else {
		// use the basic CreateRepository() if no retention parameters are set
		_, err := humiographql.CreateRepository(
			ctx,
			client,
			hr.Spec.Name,
		)
		return err
	}
}

func (h *ClientConfig) GetRepository(ctx context.Context, client *humioapi.Client, _ reconcile.Request, hr *humiov1alpha1.HumioRepository) (*humiographql.RepositoryDetails, error) {
	getRepositoryResp, err := humiographql.GetRepository(
		ctx,
		client,
		hr.Spec.Name,
	)
	if err != nil {
		return nil, humioapi.RepositoryNotFound(hr.Spec.Name)
	}

	repository := getRepositoryResp.GetRepository()
	return &humiographql.RepositoryDetails{
		Id:                        repository.GetId(),
		Name:                      repository.GetName(),
		Description:               repository.GetDescription(),
		TimeBasedRetention:        repository.GetTimeBasedRetention(),
		IngestSizeBasedRetention:  repository.GetIngestSizeBasedRetention(),
		StorageSizeBasedRetention: repository.GetStorageSizeBasedRetention(),
		CompressedByteSize:        repository.GetCompressedByteSize(),
		AutomaticSearch:           repository.GetAutomaticSearch(),
	}, nil
}

func (h *ClientConfig) UpdateRepository(ctx context.Context, client *humioapi.Client, req reconcile.Request, hr *humiov1alpha1.HumioRepository) error {
	curRepository, err := h.GetRepository(ctx, client, req, hr)
	if err != nil {
		return err
	}

	if cmp.Diff(curRepository.GetDescription(), &hr.Spec.Description) != "" {
		_, err = humiographql.UpdateDescriptionForSearchDomain(
			ctx,
			client,
			hr.Spec.Name,
			hr.Spec.Description,
		)
		if err != nil {
			return err
		}
	}

	var desiredRetentionTimeInDays *float64
	if hr.Spec.Retention.TimeInDays != nil {
		desiredRetentionTimeInDaysFloat := float64(*hr.Spec.Retention.TimeInDays)
		desiredRetentionTimeInDays = &desiredRetentionTimeInDaysFloat
	}
	if cmp.Diff(curRepository.GetTimeBasedRetention(), desiredRetentionTimeInDays) != "" {
		if desiredRetentionTimeInDays != nil && *desiredRetentionTimeInDays > 0 {
			if curRepository.GetTimeBasedRetention() == nil || *desiredRetentionTimeInDays < *curRepository.GetTimeBasedRetention() {
				if !hr.Spec.AllowDataDeletion {
					return fmt.Errorf("repository may contain data and data deletion not enabled")
				}
			}
		}

		_, err = humiographql.UpdateTimeBasedRetention(
			ctx,
			client,
			hr.Spec.Name,
			desiredRetentionTimeInDays,
		)
		if err != nil {
			return err
		}
	}

	var desiredRetentionStorageSizeInGB *float64
	if hr.Spec.Retention.StorageSizeInGB != nil {
		desiredRetentionStorageSizeInGBFloat := float64(*hr.Spec.Retention.StorageSizeInGB)
		desiredRetentionStorageSizeInGB = &desiredRetentionStorageSizeInGBFloat
	}
	if cmp.Diff(curRepository.GetStorageSizeBasedRetention(), desiredRetentionStorageSizeInGB) != "" {
		if desiredRetentionStorageSizeInGB != nil && *desiredRetentionStorageSizeInGB > 0 {
			if curRepository.GetStorageSizeBasedRetention() == nil || *desiredRetentionStorageSizeInGB < *curRepository.GetStorageSizeBasedRetention() {
				if !hr.Spec.AllowDataDeletion {
					return fmt.Errorf("repository may contain data and data deletion not enabled")
				}
			}
		}

		_, err = humiographql.UpdateStorageBasedRetention(
			ctx,
			client,
			hr.Spec.Name,
			desiredRetentionStorageSizeInGB,
		)
		if err != nil {
			return err
		}
	}

	var desiredRetentionIngestSizeInGB *float64
	if hr.Spec.Retention.IngestSizeInGB != nil {
		desiredRetentionIngestSizeInGBFloat := float64(*hr.Spec.Retention.IngestSizeInGB)
		desiredRetentionIngestSizeInGB = &desiredRetentionIngestSizeInGBFloat
	}
	if cmp.Diff(curRepository.GetIngestSizeBasedRetention(), desiredRetentionIngestSizeInGB) != "" {
		if desiredRetentionIngestSizeInGB != nil && *desiredRetentionIngestSizeInGB > 0 {
			if curRepository.GetIngestSizeBasedRetention() == nil || *desiredRetentionIngestSizeInGB < *curRepository.GetIngestSizeBasedRetention() {
				if !hr.Spec.AllowDataDeletion {
					return fmt.Errorf("repository may contain data and data deletion not enabled")
				}
			}
		}

		_, err = humiographql.UpdateIngestBasedRetention(
			ctx,
			client,
			hr.Spec.Name,
			desiredRetentionIngestSizeInGB,
		)

		if err != nil {
			return err
		}
	}

	if curRepository.AutomaticSearch != helpers.BoolTrue(hr.Spec.AutomaticSearch) {
		_, err = humiographql.SetAutomaticSearching(
			ctx,
			client,
			hr.Spec.Name,
			helpers.BoolTrue(hr.Spec.AutomaticSearch),
		)
		if err != nil {
			return err
		}
	}

	return nil
}

func (h *ClientConfig) DeleteRepository(ctx context.Context, client *humioapi.Client, req reconcile.Request, hr *humiov1alpha1.HumioRepository) error {
	_, err := h.GetRepository(ctx, client, req, hr)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			return nil
		}
		return err
	}

	if !hr.Spec.AllowDataDeletion {
		return fmt.Errorf("repository may contain data and data deletion not enabled")
	}

	_, err = humiographql.DeleteSearchDomain(
		ctx,
		client,
		hr.Spec.Name,
		"deleted by humio-operator",
	)
	return err
}

func (h *ClientConfig) GetView(ctx context.Context, client *humioapi.Client, _ reconcile.Request, hv *humiov1alpha1.HumioView) (*humiographql.GetSearchDomainSearchDomainView, error) {
	resp, err := humiographql.GetSearchDomain(
		ctx,
		client,
		hv.Spec.Name,
	)
	if err != nil {
		return nil, humioapi.ViewNotFound(hv.Spec.Name)
	}

	searchDomain := resp.GetSearchDomain()
	switch v := searchDomain.(type) {
	case *humiographql.GetSearchDomainSearchDomainView:
		return v, nil
	default:
		return nil, humioapi.ViewNotFound(hv.Spec.Name)
	}
}

func (h *ClientConfig) AddView(ctx context.Context, client *humioapi.Client, _ reconcile.Request, hv *humiov1alpha1.HumioView) error {
	viewConnections := hv.GetViewConnections()
	internalConnType := make([]humiographql.ViewConnectionInput, len(viewConnections))
	for i := range viewConnections {
		internalConnType[i] = humiographql.ViewConnectionInput{
			RepositoryName: viewConnections[i].Repository.Name,
			Filter:         viewConnections[i].Filter,
		}
	}
	_, err := humiographql.CreateView(
		ctx,
		client,
		hv.Spec.Name,
		&hv.Spec.Description,
		internalConnType,
	)
	return err
}

func (h *ClientConfig) UpdateView(ctx context.Context, client *humioapi.Client, req reconcile.Request, hv *humiov1alpha1.HumioView) error {
	curView, err := h.GetView(ctx, client, req, hv)
	if err != nil {
		return err
	}

	if cmp.Diff(curView.Description, &hv.Spec.Description) != "" {
		_, err = humiographql.UpdateDescriptionForSearchDomain(
			ctx,
			client,
			hv.Spec.Name,
			hv.Spec.Description,
		)
		if err != nil {
			return err
		}
	}

	if curView.AutomaticSearch != helpers.BoolTrue(hv.Spec.AutomaticSearch) {
		_, err = humiographql.SetAutomaticSearching(
			ctx,
			client,
			hv.Spec.Name,
			helpers.BoolTrue(hv.Spec.AutomaticSearch),
		)
		if err != nil {
			return err
		}
	}

	connections := hv.GetViewConnections()
	if cmp.Diff(curView.Connections, connections) != "" {
		internalConnType := make([]humiographql.ViewConnectionInput, len(connections))
		for i := range connections {
			internalConnType[i] = humiographql.ViewConnectionInput{
				RepositoryName: connections[i].Repository.Name,
				Filter:         connections[i].Filter,
			}
		}
		_, err = humiographql.UpdateViewConnections(
			ctx,
			client,
			hv.Spec.Name,
			internalConnType,
		)
		if err != nil {
			return err
		}
	}

	return nil
}

func (h *ClientConfig) DeleteView(ctx context.Context, client *humioapi.Client, req reconcile.Request, hv *humiov1alpha1.HumioView) error {
	_, err := h.GetView(ctx, client, req, hv)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			return nil
		}
		return err
	}

	_, err = humiographql.DeleteSearchDomain(
		ctx,
		client,
		hv.Spec.Name,
		"Deleted by humio-operator",
	)
	return err
}

func validateSearchDomain(ctx context.Context, client *humioapi.Client, searchDomainName string) error {
	resp, err := humiographql.GetSearchDomain(
		ctx,
		client,
		searchDomainName,
	)
	if err != nil {
		return fmt.Errorf("got error fetching searchdomain: %w", err)
	}
	if resp != nil {
		return nil
	}

	return humioapi.SearchDomainNotFound(searchDomainName)
}

func (h *ClientConfig) GetAction(ctx context.Context, client *humioapi.Client, _ reconcile.Request, ha *humiov1alpha1.HumioAction) (humiographql.ActionDetails, error) {
	err := validateSearchDomain(ctx, client, ha.Spec.ViewName)
	if err != nil {
		return nil, fmt.Errorf("problem getting view for action %s: %w", ha.Spec.Name, err)
	}

	resp, err := humiographql.ListActions(
		ctx,
		client,
		ha.Spec.ViewName,
	)
	if err != nil {
		return nil, err
	}
	respSearchDomain := resp.GetSearchDomain()
	respSearchDomainActions := respSearchDomain.GetActions()
	for idx := range respSearchDomainActions {
		if respSearchDomainActions[idx].GetName() == ha.Spec.Name {
			switch v := respSearchDomainActions[idx].(type) {
			case *humiographql.ListActionsSearchDomainActionsEmailAction:
				return &humiographql.ActionDetailsEmailAction{
					Id:                v.GetId(),
					Name:              v.GetName(),
					Recipients:        v.GetRecipients(),
					SubjectTemplate:   v.GetSubjectTemplate(),
					EmailBodyTemplate: v.GetEmailBodyTemplate(),
					UseProxy:          v.GetUseProxy(),
				}, nil
			case *humiographql.ListActionsSearchDomainActionsHumioRepoAction:
				return &humiographql.ActionDetailsHumioRepoAction{
					Id:          v.GetId(),
					Name:        v.GetName(),
					IngestToken: v.GetIngestToken(),
				}, nil
			case *humiographql.ListActionsSearchDomainActionsOpsGenieAction:
				return &humiographql.ActionDetailsOpsGenieAction{
					Id:       v.GetId(),
					Name:     v.GetName(),
					ApiUrl:   v.GetApiUrl(),
					GenieKey: v.GetGenieKey(),
					UseProxy: v.GetUseProxy(),
				}, nil
			case *humiographql.ListActionsSearchDomainActionsPagerDutyAction:
				return &humiographql.ActionDetailsPagerDutyAction{
					Id:         v.GetId(),
					Name:       v.GetName(),
					Severity:   v.GetSeverity(),
					RoutingKey: v.GetRoutingKey(),
					UseProxy:   v.GetUseProxy(),
				}, nil
			case *humiographql.ListActionsSearchDomainActionsSlackAction:
				return &humiographql.ActionDetailsSlackAction{
					Id:       v.GetId(),
					Name:     v.GetName(),
					Url:      v.GetUrl(),
					Fields:   v.GetFields(),
					UseProxy: v.GetUseProxy(),
				}, nil
			case *humiographql.ListActionsSearchDomainActionsSlackPostMessageAction:
				return &humiographql.ActionDetailsSlackPostMessageAction{
					Id:       v.GetId(),
					Name:     v.GetName(),
					ApiToken: v.GetApiToken(),
					Channels: v.GetChannels(),
					Fields:   v.GetFields(),
					UseProxy: v.GetUseProxy(),
				}, nil
			case *humiographql.ListActionsSearchDomainActionsVictorOpsAction:
				return &humiographql.ActionDetailsVictorOpsAction{
					Id:          v.GetId(),
					Name:        v.GetName(),
					MessageType: v.GetMessageType(),
					NotifyUrl:   v.GetNotifyUrl(),
					UseProxy:    v.GetUseProxy(),
				}, nil
			case *humiographql.ListActionsSearchDomainActionsWebhookAction:
				return &humiographql.ActionDetailsWebhookAction{
					Id:                  v.GetId(),
					Name:                v.GetName(),
					Method:              v.GetMethod(),
					Url:                 v.GetUrl(),
					Headers:             v.GetHeaders(),
					WebhookBodyTemplate: v.GetWebhookBodyTemplate(),
					IgnoreSSL:           v.GetIgnoreSSL(),
					UseProxy:            v.GetUseProxy(),
				}, nil
			}
		}
	}

	return nil, humioapi.ActionNotFound(ha.Spec.Name)
}

func (h *ClientConfig) AddAction(ctx context.Context, client *humioapi.Client, _ reconcile.Request, ha *humiov1alpha1.HumioAction) error {
	err := validateSearchDomain(ctx, client, ha.Spec.ViewName)
	if err != nil {
		return fmt.Errorf("problem getting view for action %s: %w", ha.Spec.Name, err)
	}

	newActionWithResolvedSecrets, err := ActionFromActionCR(ha)
	if err != nil {
		return err
	}

	switch v := (newActionWithResolvedSecrets).(type) {
	case *humiographql.ActionDetailsEmailAction:
		_, err = humiographql.CreateEmailAction(
			ctx,
			client,
			ha.Spec.ViewName,
			v.GetName(),
			v.GetRecipients(),
			v.GetSubjectTemplate(),
			v.GetEmailBodyTemplate(),
			v.GetUseProxy(),
		)
		return err
	case *humiographql.ActionDetailsHumioRepoAction:
		_, err = humiographql.CreateHumioRepoAction(
			ctx,
			client,
			ha.Spec.ViewName,
			v.GetName(),
			v.GetIngestToken(),
		)
		return err
	case *humiographql.ActionDetailsOpsGenieAction:
		_, err = humiographql.CreateOpsGenieAction(
			ctx,
			client,
			ha.Spec.ViewName,
			v.GetName(),
			v.GetApiUrl(),
			v.GetGenieKey(),
			v.GetUseProxy(),
		)
		return err
	case *humiographql.ActionDetailsPagerDutyAction:
		_, err = humiographql.CreatePagerDutyAction(
			ctx,
			client,
			ha.Spec.ViewName,
			v.GetName(),
			v.GetSeverity(),
			v.GetRoutingKey(),
			v.GetUseProxy(),
		)
		return err
	case *humiographql.ActionDetailsSlackAction:
		resolvedFields := v.GetFields()
		fields := make([]humiographql.SlackFieldEntryInput, len(resolvedFields))
		for idx := range resolvedFields {
			fields[idx] = humiographql.SlackFieldEntryInput{
				FieldName: resolvedFields[idx].GetFieldName(),
				Value:     resolvedFields[idx].GetValue(),
			}
		}
		_, err = humiographql.CreateSlackAction(
			ctx,
			client,
			ha.Spec.ViewName,
			v.GetName(),
			fields,
			v.GetUrl(),
			v.GetUseProxy(),
		)
		return err
	case *humiographql.ActionDetailsSlackPostMessageAction:
		resolvedFields := v.GetFields()
		fields := make([]humiographql.SlackFieldEntryInput, len(resolvedFields))
		for idx := range resolvedFields {
			fields[idx] = humiographql.SlackFieldEntryInput{
				FieldName: resolvedFields[idx].GetFieldName(),
				Value:     resolvedFields[idx].GetValue(),
			}
		}
		_, err = humiographql.CreateSlackPostMessageAction(
			ctx,
			client,
			ha.Spec.ViewName,
			v.GetName(),
			v.GetApiToken(),
			v.GetChannels(),
			fields,
			v.GetUseProxy(),
		)
		return err
	case *humiographql.ActionDetailsVictorOpsAction:
		_, err = humiographql.CreateVictorOpsAction(
			ctx,
			client,
			ha.Spec.ViewName,
			v.GetName(),
			v.GetMessageType(),
			v.GetNotifyUrl(),
			v.GetUseProxy(),
		)
		return err
	case *humiographql.ActionDetailsWebhookAction:
		resolvedHeaders := v.GetHeaders()
		headers := make([]humiographql.HttpHeaderEntryInput, len(resolvedHeaders))
		for idx := range resolvedHeaders {
			headers[idx] = humiographql.HttpHeaderEntryInput{
				Header: resolvedHeaders[idx].GetHeader(),
				Value:  resolvedHeaders[idx].GetValue(),
			}
		}
		_, err = humiographql.CreateWebhookAction(
			ctx,
			client,
			ha.Spec.ViewName,
			v.GetName(),
			v.GetUrl(),
			v.GetMethod(),
			headers,
			v.GetWebhookBodyTemplate(),
			v.GetIgnoreSSL(),
			v.GetUseProxy(),
		)
		return err
	}

	return fmt.Errorf("no action details specified or unsupported action type used")
}

func (h *ClientConfig) UpdateAction(ctx context.Context, client *humioapi.Client, req reconcile.Request, ha *humiov1alpha1.HumioAction) error {
	err := validateSearchDomain(ctx, client, ha.Spec.ViewName)
	if err != nil {
		return fmt.Errorf("problem getting view for action %s: %w", ha.Spec.Name, err)
	}

	newActionWithResolvedSecrets, err := ActionFromActionCR(ha)
	if err != nil {
		return err
	}

	currentAction, err := h.GetAction(ctx, client, req, ha)
	if err != nil {
		return fmt.Errorf("could not find action with name: %q", ha.Spec.Name)
	}

	switch v := (newActionWithResolvedSecrets).(type) {
	case *humiographql.ActionDetailsEmailAction:
		_, err = humiographql.UpdateEmailAction(
			ctx,
			client,
			ha.Spec.ViewName,
			currentAction.GetId(),
			v.GetName(),
			v.GetRecipients(),
			v.GetSubjectTemplate(),
			v.GetEmailBodyTemplate(),
			v.GetUseProxy(),
		)
		return err
	case *humiographql.ActionDetailsHumioRepoAction:
		_, err = humiographql.UpdateHumioRepoAction(
			ctx,
			client,
			ha.Spec.ViewName,
			currentAction.GetId(),
			v.GetName(),
			v.GetIngestToken(),
		)
		return err
	case *humiographql.ActionDetailsOpsGenieAction:
		_, err = humiographql.UpdateOpsGenieAction(
			ctx,
			client,
			ha.Spec.ViewName,
			currentAction.GetId(),
			v.GetName(),
			v.GetApiUrl(),
			v.GetGenieKey(),
			v.GetUseProxy(),
		)
		return err
	case *humiographql.ActionDetailsPagerDutyAction:
		_, err = humiographql.UpdatePagerDutyAction(
			ctx,
			client,
			ha.Spec.ViewName,
			currentAction.GetId(),
			v.GetName(),
			v.GetSeverity(),
			v.GetRoutingKey(),
			v.GetUseProxy(),
		)
		return err
	case *humiographql.ActionDetailsSlackAction:
		resolvedFields := v.GetFields()
		fields := make([]humiographql.SlackFieldEntryInput, len(resolvedFields))
		for idx := range resolvedFields {
			fields[idx] = humiographql.SlackFieldEntryInput{
				FieldName: resolvedFields[idx].GetFieldName(),
				Value:     resolvedFields[idx].GetValue(),
			}
		}
		_, err = humiographql.UpdateSlackAction(
			ctx,
			client,
			ha.Spec.ViewName,
			currentAction.GetId(),
			v.GetName(),
			fields,
			v.GetUrl(),
			v.GetUseProxy(),
		)
		return err
	case *humiographql.ActionDetailsSlackPostMessageAction:
		resolvedFields := v.GetFields()
		fields := make([]humiographql.SlackFieldEntryInput, len(resolvedFields))
		for idx := range resolvedFields {
			fields[idx] = humiographql.SlackFieldEntryInput{
				FieldName: resolvedFields[idx].GetFieldName(),
				Value:     resolvedFields[idx].GetValue(),
			}
		}
		_, err = humiographql.UpdateSlackPostMessageAction(
			ctx,
			client,
			ha.Spec.ViewName,
			currentAction.GetId(),
			v.GetName(),
			v.GetApiToken(),
			v.GetChannels(),
			fields,
			v.GetUseProxy(),
		)
		return err
	case *humiographql.ActionDetailsVictorOpsAction:
		_, err = humiographql.UpdateVictorOpsAction(
			ctx,
			client,
			ha.Spec.ViewName,
			currentAction.GetId(),
			v.GetName(),
			v.GetMessageType(),
			v.GetNotifyUrl(),
			v.GetUseProxy(),
		)
		return err
	case *humiographql.ActionDetailsWebhookAction:
		resolvedHeaders := v.GetHeaders()
		headers := make([]humiographql.HttpHeaderEntryInput, len(resolvedHeaders))
		for idx := range resolvedHeaders {
			headers[idx] = humiographql.HttpHeaderEntryInput{
				Header: resolvedHeaders[idx].GetHeader(),
				Value:  resolvedHeaders[idx].GetValue(),
			}
		}
		_, err = humiographql.UpdateWebhookAction(
			ctx,
			client,
			ha.Spec.ViewName,
			currentAction.GetId(),
			v.GetName(),
			v.GetUrl(),
			v.GetMethod(),
			headers,
			v.GetWebhookBodyTemplate(),
			v.GetIgnoreSSL(),
			v.GetUseProxy(),
		)
		return err
	}

	return fmt.Errorf("no action details specified or unsupported action type used")
}

func (h *ClientConfig) DeleteAction(ctx context.Context, client *humioapi.Client, req reconcile.Request, ha *humiov1alpha1.HumioAction) error {
	action, err := h.GetAction(ctx, client, req, ha)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			return nil
		}
		return err
	}
	if action.GetId() == "" {
		return humioapi.ActionNotFound(action.GetId())
	}

	_, err = humiographql.DeleteActionByID(
		ctx,
		client,
		ha.Spec.ViewName,
		action.GetId(),
	)
	return err
}

func (h *ClientConfig) GetLicenseUIDAndExpiry(ctx context.Context, client *humioapi.Client, _ reconcile.Request) (string, time.Time, error) {
	resp, err := humiographql.GetLicense(
		ctx,
		client,
	)
	if err != nil {
		return "", time.Time{}, err
	}

	installedLicense := resp.GetInstalledLicense()
	if installedLicense == nil {
		return "", time.Time{}, humioapi.EntityNotFound{}
	}

	switch v := (*installedLicense).(type) {
	case *humiographql.GetLicenseInstalledLicenseOnPremLicense:
		return v.GetUid(), v.GetExpiresAt(), nil
	default:
		return "", time.Time{}, fmt.Errorf("unknown license type %T", v)
	}
}

func (h *ClientConfig) InstallLicense(ctx context.Context, client *humioapi.Client, _ reconcile.Request, license string) error {
	_, err := humiographql.UpdateLicenseKey(
		ctx,
		client,
		license,
	)
	return err

}

func (h *ClientConfig) GetAlert(ctx context.Context, client *humioapi.Client, _ reconcile.Request, ha *humiov1alpha1.HumioAlert) (*humiographql.AlertDetails, error) {
	err := validateSearchDomain(ctx, client, ha.Spec.ViewName)
	if err != nil {
		if !errors.As(err, &humioapi.EntityNotFound{}) {
			return nil, fmt.Errorf("problem getting view for alert %s: %w", ha.Spec.Name, err)
		}
	}

	resp, err := humiographql.ListAlerts(
		ctx,
		client,
		ha.Spec.ViewName,
	)
	if err != nil {
		return nil, err
	}
	respSearchDomain := resp.GetSearchDomain()
	respAlerts := respSearchDomain.GetAlerts()
	for idx := range respAlerts {
		if respAlerts[idx].Name == ha.Spec.Name {
			return &humiographql.AlertDetails{
				Id:                 respAlerts[idx].GetId(),
				Name:               respAlerts[idx].GetName(),
				QueryString:        respAlerts[idx].GetQueryString(),
				QueryStart:         respAlerts[idx].GetQueryStart(),
				ThrottleField:      respAlerts[idx].GetThrottleField(),
				Description:        respAlerts[idx].GetDescription(),
				ThrottleTimeMillis: respAlerts[idx].GetThrottleTimeMillis(),
				Enabled:            respAlerts[idx].GetEnabled(),
				ActionsV2:          respAlerts[idx].GetActionsV2(),
				Labels:             respAlerts[idx].GetLabels(),
				QueryOwnership:     respAlerts[idx].GetQueryOwnership(),
			}, nil
		}
	}

	return nil, humioapi.AlertNotFound(ha.Spec.Name)
}

func (h *ClientConfig) AddAlert(ctx context.Context, client *humioapi.Client, _ reconcile.Request, ha *humiov1alpha1.HumioAlert) error {
	err := validateSearchDomain(ctx, client, ha.Spec.ViewName)
	if err != nil {
		return fmt.Errorf("problem getting view for alert: %w", err)
	}

	isEnabled := !ha.Spec.Silenced
	queryOwnershipType := humiographql.QueryOwnershipTypeOrganization
	_, err = humiographql.CreateAlert(
		ctx,
		client,
		ha.Spec.ViewName,
		ha.Spec.Name,
		&ha.Spec.Description,
		ha.Spec.Query.QueryString,
		ha.Spec.Query.Start,
		int64(ha.Spec.ThrottleTimeMillis),
		&isEnabled,
		ha.Spec.Actions,
		ha.Spec.Labels,
		&queryOwnershipType,
		ha.Spec.ThrottleField,
	)
	return err
}

func (h *ClientConfig) UpdateAlert(ctx context.Context, client *humioapi.Client, req reconcile.Request, ha *humiov1alpha1.HumioAlert) error {
	err := validateSearchDomain(ctx, client, ha.Spec.ViewName)
	if err != nil {
		return fmt.Errorf("problem getting view for action: %w", err)
	}

	currentAlert, err := h.GetAlert(ctx, client, req, ha)
	if err != nil {
		return fmt.Errorf("could not find alert with name: %q", ha.Spec.Name)
	}

	queryOwnershipType := humiographql.QueryOwnershipTypeOrganization
	_, err = humiographql.UpdateAlert(
		ctx,
		client,
		ha.Spec.ViewName,
		currentAlert.GetId(),
		ha.Spec.Name,
		&ha.Spec.Description,
		ha.Spec.Query.QueryString,
		ha.Spec.Query.Start,
		int64(ha.Spec.ThrottleTimeMillis),
		!ha.Spec.Silenced,
		ha.Spec.Actions,
		ha.Spec.Labels,
		&queryOwnershipType,
		ha.Spec.ThrottleField,
	)
	return err
}

func (h *ClientConfig) DeleteAlert(ctx context.Context, client *humioapi.Client, req reconcile.Request, ha *humiov1alpha1.HumioAlert) error {
	alert, err := h.GetAlert(ctx, client, req, ha)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			return nil
		}
		return err
	}

	_, err = humiographql.DeleteAlertByID(
		ctx,
		client,
		ha.Spec.ViewName,
		alert.GetId(),
	)
	return err
}

func (h *ClientConfig) GetFilterAlert(ctx context.Context, client *humioapi.Client, _ reconcile.Request, hfa *humiov1alpha1.HumioFilterAlert) (*humiographql.FilterAlertDetails, error) {
	err := validateSearchDomain(ctx, client, hfa.Spec.ViewName)
	if err != nil {
		return nil, fmt.Errorf("problem getting view for filter alert %s: %w", hfa.Spec.Name, err)
	}

	respList, err := humiographql.ListFilterAlerts(
		ctx,
		client,
		hfa.Spec.ViewName,
	)
	if err != nil {
		return nil, err
	}
	respSearchDomain := respList.GetSearchDomain()
	respFilterAlerts := respSearchDomain.GetFilterAlerts()

	var filterAlertId string
	for _, filterAlert := range respFilterAlerts {
		if filterAlert.Name == hfa.Spec.Name {
			filterAlertId = filterAlert.GetId()
		}
	}
	if filterAlertId == "" {
		return nil, humioapi.FilterAlertNotFound(hfa.Spec.Name)
	}

	respGet, err := humiographql.GetFilterAlertByID(
		ctx,
		client,
		hfa.Spec.ViewName,
		filterAlertId,
	)
	if err != nil {
		return nil, err
	}
	respFilterAlert := respGet.GetSearchDomain().GetFilterAlert()
	return &respFilterAlert.FilterAlertDetails, nil
}

func (h *ClientConfig) AddFilterAlert(ctx context.Context, client *humioapi.Client, req reconcile.Request, hfa *humiov1alpha1.HumioFilterAlert) error {
	err := validateSearchDomain(ctx, client, hfa.Spec.ViewName)
	if err != nil {
		return fmt.Errorf("problem getting view for filter alert: %w", err)
	}
	if err = h.ValidateActionsForFilterAlert(ctx, client, req, hfa); err != nil {
		return fmt.Errorf("could not get action id mapping: %w", err)
	}

	_, err = humiographql.CreateFilterAlert(
		ctx,
		client,
		hfa.Spec.ViewName,
		hfa.Spec.Name,
		&hfa.Spec.Description,
		hfa.Spec.QueryString,
		hfa.Spec.Actions,
		hfa.Spec.Labels,
		hfa.Spec.Enabled,
		hfa.Spec.ThrottleField,
		int64(hfa.Spec.ThrottleTimeSeconds),
		humiographql.QueryOwnershipTypeOrganization,
	)
	return err
}

func (h *ClientConfig) UpdateFilterAlert(ctx context.Context, client *humioapi.Client, req reconcile.Request, hfa *humiov1alpha1.HumioFilterAlert) error {
	err := validateSearchDomain(ctx, client, hfa.Spec.ViewName)
	if err != nil {
		return fmt.Errorf("problem getting view for action: %w", err)
	}
	if err = h.ValidateActionsForFilterAlert(ctx, client, req, hfa); err != nil {
		return fmt.Errorf("could not get action id mapping: %w", err)
	}

	currentAlert, err := h.GetFilterAlert(ctx, client, req, hfa)
	if err != nil {
		return fmt.Errorf("could not find filter alert with name: %q", hfa.Spec.Name)
	}

	_, err = humiographql.UpdateFilterAlert(
		ctx,
		client,
		hfa.Spec.ViewName,
		currentAlert.GetId(),
		hfa.Spec.Name,
		&hfa.Spec.Description,
		hfa.Spec.QueryString,
		hfa.Spec.Actions,
		hfa.Spec.Labels,
		hfa.Spec.Enabled,
		hfa.Spec.ThrottleField,
		int64(hfa.Spec.ThrottleTimeSeconds),
		humiographql.QueryOwnershipTypeOrganization,
	)
	return err
}

func (h *ClientConfig) DeleteFilterAlert(ctx context.Context, client *humioapi.Client, req reconcile.Request, hfa *humiov1alpha1.HumioFilterAlert) error {
	currentFilterAlert, err := h.GetFilterAlert(ctx, client, req, hfa)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			return nil
		}
		return err
	}

	_, err = humiographql.DeleteFilterAlert(
		ctx,
		client,
		hfa.Spec.ViewName,
		currentFilterAlert.GetId(),
	)
	return err
}

func (h *ClientConfig) GetFeatureFlags(ctx context.Context, client *humioapi.Client) ([]string, error) {
	resp, err := humiographql.GetFeatureFlags(ctx, client)
	if err != nil {
		return nil, err
	}
	featureFlagNames := make([]string, len(resp.GetFeatureFlags()))
	for _, featureFlag := range resp.GetFeatureFlags() {
		featureFlagNames = append(featureFlagNames, string(featureFlag.GetFlag()))
	}
	return featureFlagNames, nil
}

func (h *ClientConfig) EnableFeatureFlag(ctx context.Context, client *humioapi.Client, featureFlag *humiov1alpha1.HumioFeatureFlag) error {
	_, err := humiographql.EnableGlobalFeatureFlag(
		ctx,
		client,
		humiographql.FeatureFlag(featureFlag.Spec.Name),
	)

	return err
}

func (h *ClientConfig) IsFeatureFlagEnabled(ctx context.Context, client *humioapi.Client, featureFlag *humiov1alpha1.HumioFeatureFlag) (bool, error) {
	response, err := humiographql.IsFeatureGloballyEnabled(
		ctx,
		client,
		humiographql.FeatureFlag(featureFlag.Spec.Name),
	)
	if response == nil {
		return false, humioapi.FeatureFlagNotFound(featureFlag.Spec.Name)
	}
	responseMeta := response.GetMeta()
	return responseMeta.GetIsFeatureFlagEnabled(), err
}

func (h *ClientConfig) DisableFeatureFlag(ctx context.Context, client *humioapi.Client, featureFlag *humiov1alpha1.HumioFeatureFlag) error {
	_, err := humiographql.DisableGlobalFeatureFlag(
		ctx,
		client,
		humiographql.FeatureFlag(featureFlag.Spec.Name),
	)
	return err
}

func (h *ClientConfig) AddScheduledSearch(ctx context.Context, client *humioapi.Client, req reconcile.Request, hss *humiov1alpha1.HumioScheduledSearch) error {
	err := validateSearchDomain(ctx, client, hss.Spec.ViewName)
	if err != nil {
		return fmt.Errorf("problem getting view for scheduled search: %w", err)
	}
	if err = h.ValidateActionsForScheduledSearch(ctx, client, req, hss); err != nil {
		return fmt.Errorf("could not get action id mapping: %w", err)
	}
	queryOwnershipType := humiographql.QueryOwnershipTypeOrganization
	_, err = humiographql.CreateScheduledSearch(
		ctx,
		client,
		hss.Spec.ViewName,
		hss.Spec.Name,
		&hss.Spec.Description,
		hss.Spec.QueryString,
		hss.Spec.QueryStart,
		hss.Spec.QueryEnd,
		hss.Spec.Schedule,
		hss.Spec.TimeZone,
		hss.Spec.BackfillLimit,
		hss.Spec.Enabled,
		hss.Spec.Actions,
		hss.Spec.Labels,
		&queryOwnershipType,
	)
	return err
}

func (h *ClientConfig) GetScheduledSearch(ctx context.Context, client *humioapi.Client, _ reconcile.Request, hss *humiov1alpha1.HumioScheduledSearch) (*humiographql.ScheduledSearchDetails, error) {
	err := validateSearchDomain(ctx, client, hss.Spec.ViewName)
	if err != nil {
		return nil, fmt.Errorf("problem getting view for scheduled search %s: %w", hss.Spec.Name, err)
	}

	var scheduledSearchId string
	respList, err := humiographql.ListScheduledSearches(
		ctx,
		client,
		hss.Spec.ViewName,
	)
	if err != nil {
		return nil, err
	}
	respListSearchDomain := respList.GetSearchDomain()
	for _, scheduledSearch := range respListSearchDomain.GetScheduledSearches() {
		if scheduledSearch.Name == hss.Spec.Name {
			scheduledSearchId = scheduledSearch.GetId()
		}
	}
	if scheduledSearchId == "" {
		return nil, humioapi.ScheduledSearchNotFound(hss.Spec.Name)
	}

	respGet, err := humiographql.GetScheduledSearchByID(
		ctx,
		client,
		hss.Spec.ViewName,
		scheduledSearchId,
	)
	if err != nil {
		return nil, err
	}
	respGetSearchDomain := respGet.GetSearchDomain()
	respGetScheduledSearch := respGetSearchDomain.GetScheduledSearch()
	return &respGetScheduledSearch.ScheduledSearchDetails, nil
}

func (h *ClientConfig) UpdateScheduledSearch(ctx context.Context, client *humioapi.Client, req reconcile.Request, hss *humiov1alpha1.HumioScheduledSearch) error {
	err := validateSearchDomain(ctx, client, hss.Spec.ViewName)
	if err != nil {
		return fmt.Errorf("problem getting view for scheduled search: %w", err)
	}
	if err = h.ValidateActionsForScheduledSearch(ctx, client, req, hss); err != nil {
		return fmt.Errorf("could not get action id mapping: %w", err)
	}
	currentScheduledSearch, err := h.GetScheduledSearch(ctx, client, req, hss)
	if err != nil {
		return fmt.Errorf("could not find scheduled search with name: %q", hss.Spec.Name)
	}

	queryOwnershipType := humiographql.QueryOwnershipTypeOrganization
	_, err = humiographql.UpdateScheduledSearch(
		ctx,
		client,
		hss.Spec.ViewName,
		currentScheduledSearch.GetId(),
		hss.Spec.Name,
		&hss.Spec.Description,
		hss.Spec.QueryString,
		hss.Spec.QueryStart,
		hss.Spec.QueryEnd,
		hss.Spec.Schedule,
		hss.Spec.TimeZone,
		hss.Spec.BackfillLimit,
		hss.Spec.Enabled,
		hss.Spec.Actions,
		hss.Spec.Labels,
		&queryOwnershipType,
	)
	return err
}

func (h *ClientConfig) DeleteScheduledSearch(ctx context.Context, client *humioapi.Client, req reconcile.Request, hss *humiov1alpha1.HumioScheduledSearch) error {
	currentScheduledSearch, err := h.GetScheduledSearch(ctx, client, req, hss)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			return nil
		}
		return err
	}

	_, err = humiographql.DeleteScheduledSearchByID(
		ctx,
		client,
		hss.Spec.ViewName,
		currentScheduledSearch.GetId(),
	)
	return err
}

func (h *ClientConfig) getAndValidateAction(ctx context.Context, client *humioapi.Client, req reconcile.Request, actionName string, viewName string) error {
	action := &humiov1alpha1.HumioAction{
		Spec: humiov1alpha1.HumioActionSpec{
			Name:     actionName,
			ViewName: viewName,
		},
	}

	_, err := h.GetAction(ctx, client, req, action)
	return err
}

func (h *ClientConfig) ValidateActionsForFilterAlert(ctx context.Context, client *humioapi.Client, req reconcile.Request, hfa *humiov1alpha1.HumioFilterAlert) error {
	for _, actionNameForAlert := range hfa.Spec.Actions {
		if err := h.getAndValidateAction(ctx, client, req, actionNameForAlert, hfa.Spec.ViewName); err != nil {
			return fmt.Errorf("problem getting action for filter alert %s: %w", hfa.Spec.Name, err)
		}
	}
	return nil
}

func (h *ClientConfig) ValidateActionsForScheduledSearch(ctx context.Context, client *humioapi.Client, req reconcile.Request, hss *humiov1alpha1.HumioScheduledSearch) error {
	for _, actionNameForScheduledSearch := range hss.Spec.Actions {
		if err := h.getAndValidateAction(ctx, client, req, actionNameForScheduledSearch, hss.Spec.ViewName); err != nil {
			return fmt.Errorf("problem getting action for scheduled search %s: %w", hss.Spec.Name, err)
		}
	}
	return nil
}

func (h *ClientConfig) AddAggregateAlert(ctx context.Context, client *humioapi.Client, req reconcile.Request, haa *humiov1alpha1.HumioAggregateAlert) error {
	err := validateSearchDomain(ctx, client, haa.Spec.ViewName)
	if err != nil {
		return fmt.Errorf("problem getting view for action: %w", err)
	}
	if err = h.ValidateActionsForAggregateAlert(ctx, client, req, haa); err != nil {
		return fmt.Errorf("could not get action id mapping: %w", err)
	}

	_, err = humiographql.CreateAggregateAlert(
		ctx,
		client,
		haa.Spec.ViewName,
		haa.Spec.Name,
		&haa.Spec.Description,
		haa.Spec.QueryString,
		int64(haa.Spec.SearchIntervalSeconds),
		haa.Spec.Actions,
		haa.Spec.Labels,
		haa.Spec.Enabled,
		haa.Spec.ThrottleField,
		int64(haa.Spec.ThrottleTimeSeconds),
		humiographql.TriggerMode(haa.Spec.TriggerMode),
		humiographql.QueryTimestampType(haa.Spec.QueryTimestampType),
		humiographql.QueryOwnershipTypeOrganization,
	)
	return err
}

func (h *ClientConfig) GetAggregateAlert(ctx context.Context, client *humioapi.Client, _ reconcile.Request, haa *humiov1alpha1.HumioAggregateAlert) (*humiographql.AggregateAlertDetails, error) {
	err := validateSearchDomain(ctx, client, haa.Spec.ViewName)
	if err != nil {
		return nil, fmt.Errorf("problem getting view for action %s: %w", haa.Spec.Name, err)
	}

	var aggregateAlertId string
	respList, err := humiographql.ListAggregateAlerts(
		ctx,
		client,
		haa.Spec.ViewName,
	)
	if err != nil {
		return nil, err
	}
	respSearchDomain := respList.GetSearchDomain()
	respAggregateAlerts := respSearchDomain.GetAggregateAlerts()
	for _, aggregateAlert := range respAggregateAlerts {
		if aggregateAlert.Name == haa.Spec.Name {
			aggregateAlertId = aggregateAlert.GetId()
		}
	}
	if aggregateAlertId == "" {
		return nil, humioapi.AggregateAlertNotFound(haa.Spec.Name)
	}
	respGet, err := humiographql.GetAggregateAlertByID(
		ctx,
		client,
		haa.Spec.ViewName,
		aggregateAlertId,
	)
	if err != nil {
		return nil, err
	}
	respAggregateAlert := respGet.GetSearchDomain().GetAggregateAlert()
	return &respAggregateAlert.AggregateAlertDetails, nil
}

func (h *ClientConfig) UpdateAggregateAlert(ctx context.Context, client *humioapi.Client, req reconcile.Request, haa *humiov1alpha1.HumioAggregateAlert) error {
	err := validateSearchDomain(ctx, client, haa.Spec.ViewName)
	if err != nil {
		return fmt.Errorf("problem getting view for action %s: %w", haa.Spec.Name, err)
	}
	if err = h.ValidateActionsForAggregateAlert(ctx, client, req, haa); err != nil {
		return fmt.Errorf("could not get action id mapping: %w", err)
	}
	currentAggregateAlert, err := h.GetAggregateAlert(ctx, client, req, haa)
	if err != nil {
		return fmt.Errorf("could not find aggregate alert with name: %q", haa.Spec.Name)
	}

	_, err = humiographql.UpdateAggregateAlert(
		ctx,
		client,
		haa.Spec.ViewName,
		currentAggregateAlert.GetId(),
		haa.Spec.Name,
		&haa.Spec.Description,
		haa.Spec.QueryString,
		int64(haa.Spec.SearchIntervalSeconds),
		haa.Spec.Actions,
		haa.Spec.Labels,
		haa.Spec.Enabled,
		haa.Spec.ThrottleField,
		int64(haa.Spec.ThrottleTimeSeconds),
		humiographql.TriggerMode(haa.Spec.TriggerMode),
		humiographql.QueryTimestampType(haa.Spec.QueryTimestampType),
		humiographql.QueryOwnershipTypeOrganization,
	)
	return err
}

func (h *ClientConfig) DeleteAggregateAlert(ctx context.Context, client *humioapi.Client, req reconcile.Request, haa *humiov1alpha1.HumioAggregateAlert) error {
	currentAggregateAlert, err := h.GetAggregateAlert(ctx, client, req, haa)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			return nil
		}
		return err
	}

	_, err = humiographql.DeleteAggregateAlert(
		ctx,
		client,
		haa.Spec.ViewName,
		currentAggregateAlert.GetId(),
	)
	return err
}

func (h *ClientConfig) ValidateActionsForAggregateAlert(ctx context.Context, client *humioapi.Client, req reconcile.Request, haa *humiov1alpha1.HumioAggregateAlert) error {
	// validate action
	for _, actionNameForAlert := range haa.Spec.Actions {
		if err := h.getAndValidateAction(ctx, client, req, actionNameForAlert, haa.Spec.ViewName); err != nil {
			return fmt.Errorf("problem getting action for aggregate alert %s: %w", haa.Spec.Name, err)
		}
	}
	return nil
}

func (h *ClientConfig) GetUserIDForUsername(ctx context.Context, client *humioapi.Client, _ reconcile.Request, username string) (string, error) {
	resp, err := humiographql.GetUsersByUsername(
		ctx,
		client,
		username,
	)
	if err != nil {
		return "", err
	}

	respUsers := resp.GetUsers()
	for _, user := range respUsers {
		if user.Username == username {
			return user.GetId(), nil
		}
	}

	return "", humioapi.UserNotFound(username)
}

func (h *ClientConfig) RotateUserApiTokenAndGet(ctx context.Context, client *humioapi.Client, _ reconcile.Request, userID string) (string, error) {
	if userID == "" {
		return "", fmt.Errorf("userID must not be empty")
	}
	resp, err := humiographql.RotateTokenByID(
		ctx,
		client,
		userID,
	)
	if err != nil {
		return "", err
	}

	return resp.GetRotateToken(), nil
}

func (h *ClientConfig) AddUserAndGetUserID(ctx context.Context, client *humioapi.Client, _ reconcile.Request, username string, isRoot bool) (string, error) {
	resp, err := humiographql.AddUser(
		ctx,
		client,
		username,
		&isRoot,
	)
	if err != nil {
		return "", err
	}

	createdUser := resp.GetAddUserV2()
	switch v := createdUser.(type) {
	case *humiographql.AddUserAddUserV2User:
		return v.GetId(), nil
	default:
		return "", fmt.Errorf("got unknown user type=%v", v)
	}
}

func (h *ClientConfig) AddSystemPermissionRole(ctx context.Context, client *humioapi.Client, request reconcile.Request, role *humiov1alpha1.HumioSystemPermissionRole) error {
	// convert strings to graphql types and call update
	systemPermissions := make([]humiographql.SystemPermission, len(role.Spec.Permissions))
	for idx := range role.Spec.Permissions {
		systemPermissions[idx] = humiographql.SystemPermission(role.Spec.Permissions[idx])
	}

	_, err := humiographql.CreateRole(ctx, client, role.Spec.Name, systemPermissions)
	return err
}

func (h *ClientConfig) GetSystemPermissionRole(ctx context.Context, client *humioapi.Client, request reconcile.Request, role *humiov1alpha1.HumioSystemPermissionRole) (*humiographql.RoleDetails, error) {
	resp, err := humiographql.ListRoles(
		ctx,
		client,
	)
	if err != nil {
		return nil, err
	}
	respGetRoles := resp.GetRoles()
	for i := range respGetRoles {
		respRole := respGetRoles[i]
		if respRole.GetDisplayName() == role.Spec.Name && len(respRole.GetSystemPermissions()) > 0 {
			return &respGetRoles[i].RoleDetails, err
		}
	}

	return nil, humioapi.SystemPermissionRoleNotFound(role.Spec.Name)
}

func (h *ClientConfig) UpdateSystemPermissionRole(ctx context.Context, client *humioapi.Client, request reconcile.Request, role *humiov1alpha1.HumioSystemPermissionRole) error {
	resp, listErr := humiographql.ListRoles(
		ctx,
		client,
	)
	if listErr != nil {
		return listErr
	}
	if resp == nil {
		return fmt.Errorf("unable to fetch list of roles")
	}

	// list all roles
	respGetRoles := resp.GetRoles()
	for i := range respGetRoles {
		respRole := respGetRoles[i]

		// pick the role with the correct name and which is a role with system permissions
		if respRole.GetDisplayName() == role.Spec.Name && len(respRole.GetSystemPermissions()) > 0 {

			// convert strings to graphql types and call update
			systemPermissions := make([]humiographql.SystemPermission, len(role.Spec.Permissions))
			for idx := range role.Spec.Permissions {
				systemPermissions[idx] = humiographql.SystemPermission(role.Spec.Permissions[idx])
			}
			_, err := humiographql.UpdateRole(ctx, client, respGetRoles[i].GetId(), respGetRoles[i].GetDisplayName(), []humiographql.Permission{}, nil, systemPermissions)
			return err
		}
	}
	return humioapi.SystemPermissionRoleNotFound(role.Spec.Name)
}

func (h *ClientConfig) DeleteSystemPermissionRole(ctx context.Context, client *humioapi.Client, request reconcile.Request, role *humiov1alpha1.HumioSystemPermissionRole) error {
	resp, listErr := humiographql.ListRoles(ctx, client)
	if listErr != nil {
		return listErr
	}
	if resp == nil {
		return fmt.Errorf("unable to fetch list of roles")
	}
	respListRolesGetRoles := resp.GetRoles()
	for i := range respListRolesGetRoles {
		if respListRolesGetRoles[i].GetDisplayName() == role.Spec.Name && len(respListRolesGetRoles[i].GetSystemPermissions()) > 0 {
			_, err := humiographql.DeleteRoleByID(ctx, client, respListRolesGetRoles[i].GetId())
			return err
		}
	}
	return nil
}

func (h *ClientConfig) AddUser(ctx context.Context, client *humioapi.Client, _ reconcile.Request, hu *humiov1alpha1.HumioUser) error {
	_, err := humiographql.AddUser(
		ctx,
		client,
		hu.Spec.UserName,
		hu.Spec.IsRoot,
	)
	return err
}

func (h *ClientConfig) GetUser(ctx context.Context, client *humioapi.Client, _ reconcile.Request, hu *humiov1alpha1.HumioUser) (*humiographql.UserDetails, error) {
	resp, err := humiographql.GetUsersByUsername(
		ctx,
		client,
		hu.Spec.UserName,
	)
	if err != nil {
		return nil, err
	}

	respUsers := resp.GetUsers()
	for _, user := range respUsers {
		if user.Username == hu.Spec.UserName {
			return &user.UserDetails, nil
		}
	}

	return nil, humioapi.UserNotFound(hu.Spec.UserName)
}

func (h *ClientConfig) UpdateUser(ctx context.Context, client *humioapi.Client, _ reconcile.Request, hu *humiov1alpha1.HumioUser) error {
	_, err := humiographql.UpdateUser(
		ctx,
		client,
		hu.Spec.UserName,
		hu.Spec.IsRoot,
	)
	return err
}

func (h *ClientConfig) DeleteUser(ctx context.Context, client *humioapi.Client, _ reconcile.Request, hu *humiov1alpha1.HumioUser) error {
	_, err := humiographql.RemoveUser(
		ctx,
		client,
		hu.Spec.UserName,
	)
	return err
}

func (h *ClientConfig) AddOrganizationPermissionRole(ctx context.Context, client *humioapi.Client, request reconcile.Request, role *humiov1alpha1.HumioOrganizationPermissionRole) error {
	// convert strings to graphql types and call update
	organizationPermissions := make([]humiographql.OrganizationPermission, len(role.Spec.Permissions))
	for idx := range role.Spec.Permissions {
		organizationPermissions[idx] = humiographql.OrganizationPermission(role.Spec.Permissions[idx])
	}
	_, err := humiographql.CreateRole(ctx, client, role.Spec.Name, []humiographql.Permission{}, organizationPermissions, nil)
	return err
}

func (h *ClientConfig) GetOrganizationPermissionRole(ctx context.Context, client *humioapi.Client, request reconcile.Request, role *humiov1alpha1.HumioOrganizationPermissionRole) (*humiographql.RoleDetails, error) {
	resp, err := humiographql.ListRoles(
		ctx,
		client,
	)
	if err != nil {
		return nil, err
	}
	respGetRoles := resp.GetRoles()
	for i := range respGetRoles {
		respRole := respGetRoles[i]
		if respRole.GetDisplayName() == role.Spec.Name && len(respRole.GetOrganizationPermissions()) > 0 {
			return &respGetRoles[i].RoleDetails, err
		}
	}

	return nil, humioapi.OrganizationPermissionRoleNotFound(role.Spec.Name)
}

func (h *ClientConfig) UpdateOrganizationPermissionRole(ctx context.Context, client *humioapi.Client, request reconcile.Request, role *humiov1alpha1.HumioOrganizationPermissionRole) error {
	resp, listErr := humiographql.ListRoles(
		ctx,
		client,
	)
	if listErr != nil {
		return listErr
	}
	if resp == nil {
		return fmt.Errorf("unable to fetch list of roles")
	}

	// list all roles
	respGetRoles := resp.GetRoles()
	for i := range respGetRoles {
		respRole := respGetRoles[i]

		// pick the role with the correct name and which is a role with organization permissions
		if respRole.GetDisplayName() == role.Spec.Name && len(respRole.GetOrganizationPermissions()) > 0 {

			// convert strings to graphql types and call update
			organizationPermissions := make([]humiographql.OrganizationPermission, len(role.Spec.Permissions))
			for idx := range role.Spec.Permissions {
				organizationPermissions[idx] = humiographql.OrganizationPermission(role.Spec.Permissions[idx])
			}
			_, err := humiographql.UpdateRole(ctx, client, respGetRoles[i].GetId(), respGetRoles[i].GetDisplayName(), []humiographql.Permission{}, organizationPermissions, nil)
			return err
		}
	}
	return humioapi.OrganizationPermissionRoleNotFound(role.Spec.Name)
}

func (h *ClientConfig) DeleteOrganizationPermissionRole(ctx context.Context, client *humioapi.Client, request reconcile.Request, role *humiov1alpha1.HumioOrganizationPermissionRole) error {
	resp, listErr := humiographql.ListRoles(ctx, client)
	if listErr != nil {
		return listErr
	}
	if resp == nil {
		return fmt.Errorf("unable to fetch list of roles")
	}
	respListRolesGetRoles := resp.GetRoles()
	for i := range respListRolesGetRoles {
		if respListRolesGetRoles[i].GetDisplayName() == role.Spec.Name && len(respListRolesGetRoles[i].GetOrganizationPermissions()) > 0 {
			_, err := humiographql.DeleteRoleByID(ctx, client, respListRolesGetRoles[i].GetId())
			return err
		}
	}
	return nil
}
