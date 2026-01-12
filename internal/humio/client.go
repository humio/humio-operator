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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/humio/humio-operator/internal/api/humiographql"
	"github.com/humio/humio-operator/internal/helpers"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	humiov1beta1 "github.com/humio/humio-operator/api/v1beta1"
	humioapi "github.com/humio/humio-operator/internal/api"
)

const (
	PackageResponseTypeAnalysisResult = "PackageAnalysisResultJson"
	PackageResponseTypeErrorReport    = "PackageErrorReport"
	PackagePolicyOverwrite            = "overwrite"
	EventForwardingRuleAnnotation     = "core.humio.com/event-forwarding-rule-id"
)

// Client is the interface that can be mocked
type Client interface {
	ClusterClient
	IngestTokensClient
	ParsersClient
	RepositoriesClient
	ViewsClient
	MultiClusterSearchViewsClient
	GroupsClient
	LicenseClient
	ActionsClient
	AlertsClient
	FilterAlertsClient
	FeatureFlagsClient
	AggregateAlertsClient
	ScheduledSearchClient
	ScheduledSearchClientV2
	SavedQueriesClient
	UsersClient
	OrganizationPermissionRolesClient
	SystemPermissionRolesClient
	ViewPermissionRolesClient
	IPFilterClient
	ViewTokenClient
	SystemTokenClient
	OrganizationTokenClient
	SecurityPoliciesClient
	PackageClient
	TelemetryClient
	EventForwardingRulesClient
	EventForwardersClient
}

type ClusterClient interface {
	GetCluster(context.Context, *humioapi.Client) (*humiographql.GetClusterResponse, error)
	GetHumioHttpClient(*humioapi.Config, reconcile.Request) *humioapi.Client
	ClearHumioClientConnections(string)
	TestAPIToken(context.Context, *humioapi.Config, reconcile.Request) error
	Status(context.Context, *humioapi.Client) (*humioapi.StatusResponse, error)
	GetEvictionStatus(context.Context, *humioapi.Client) (*humiographql.GetEvictionStatusResponse, error)
	SetIsBeingEvicted(context.Context, *humioapi.Client, int, bool) error
	RefreshClusterManagementStats(context.Context, *humioapi.Client, int) (*humiographql.RefreshClusterManagementStatsResponse, error)
	UnregisterClusterNode(context.Context, *humioapi.Client, int, bool) (*humiographql.UnregisterClusterNodeResponse, error)
}

type IngestTokensClient interface {
	AddIngestToken(context.Context, *humioapi.Client, *humiov1alpha1.HumioIngestToken) error
	GetIngestToken(context.Context, *humioapi.Client, *humiov1alpha1.HumioIngestToken) (*humiographql.IngestTokenDetails, error)
	UpdateIngestToken(context.Context, *humioapi.Client, *humiov1alpha1.HumioIngestToken) error
	DeleteIngestToken(context.Context, *humioapi.Client, *humiov1alpha1.HumioIngestToken) error
}

type ParsersClient interface {
	AddParser(context.Context, *humioapi.Client, *humiov1alpha1.HumioParser) error
	GetParser(context.Context, *humioapi.Client, *humiov1alpha1.HumioParser) (*humiographql.ParserDetails, error)
	UpdateParser(context.Context, *humioapi.Client, *humiov1alpha1.HumioParser) error
	DeleteParser(context.Context, *humioapi.Client, *humiov1alpha1.HumioParser) error
}

type RepositoriesClient interface {
	AddRepository(context.Context, *humioapi.Client, *humiov1alpha1.HumioRepository) error
	GetRepository(context.Context, *humioapi.Client, *humiov1alpha1.HumioRepository) (*humiographql.RepositoryDetails, error)
	UpdateRepository(context.Context, *humioapi.Client, *humiov1alpha1.HumioRepository) error
	DeleteRepository(context.Context, *humioapi.Client, *humiov1alpha1.HumioRepository) error
}

type ViewsClient interface {
	AddView(context.Context, *humioapi.Client, *humiov1alpha1.HumioView) error
	GetView(context.Context, *humioapi.Client, *humiov1alpha1.HumioView, bool) (*humiographql.GetSearchDomainSearchDomainView, error)
	UpdateView(context.Context, *humioapi.Client, *humiov1alpha1.HumioView) error
	DeleteView(context.Context, *humioapi.Client, *humiov1alpha1.HumioView) error
}

type MultiClusterSearchViewsClient interface {
	AddMultiClusterSearchView(context.Context, *humioapi.Client, *humiov1alpha1.HumioMultiClusterSearchView, []ConnectionDetailsIncludingAPIToken) error
	GetMultiClusterSearchView(context.Context, *humioapi.Client, *humiov1alpha1.HumioMultiClusterSearchView) (*humiographql.GetMultiClusterSearchViewSearchDomainView, error)
	UpdateMultiClusterSearchView(context.Context, *humioapi.Client, *humiov1alpha1.HumioMultiClusterSearchView, []ConnectionDetailsIncludingAPIToken) error
	DeleteMultiClusterSearchView(context.Context, *humioapi.Client, *humiov1alpha1.HumioMultiClusterSearchView) error
}

type GroupsClient interface {
	AddGroup(context.Context, *humioapi.Client, *humiov1alpha1.HumioGroup) error
	GetGroup(context.Context, *humioapi.Client, *humiov1alpha1.HumioGroup) (*humiographql.GroupDetails, error)
	UpdateGroup(context.Context, *humioapi.Client, *humiov1alpha1.HumioGroup) error
	DeleteGroup(context.Context, *humioapi.Client, *humiov1alpha1.HumioGroup) error
}

type ActionsClient interface {
	AddAction(context.Context, *humioapi.Client, *humiov1alpha1.HumioAction) error
	GetAction(context.Context, *humioapi.Client, *humiov1alpha1.HumioAction) (humiographql.ActionDetails, error)
	UpdateAction(context.Context, *humioapi.Client, *humiov1alpha1.HumioAction) error
	DeleteAction(context.Context, *humioapi.Client, *humiov1alpha1.HumioAction) error
}

type AlertsClient interface {
	AddAlert(context.Context, *humioapi.Client, *humiov1alpha1.HumioAlert) error
	GetAlert(context.Context, *humioapi.Client, *humiov1alpha1.HumioAlert) (*humiographql.AlertDetails, error)
	UpdateAlert(context.Context, *humioapi.Client, *humiov1alpha1.HumioAlert) error
	DeleteAlert(context.Context, *humioapi.Client, *humiov1alpha1.HumioAlert) error
}

type FilterAlertsClient interface {
	AddFilterAlert(context.Context, *humioapi.Client, *humiov1alpha1.HumioFilterAlert) error
	GetFilterAlert(context.Context, *humioapi.Client, *humiov1alpha1.HumioFilterAlert) (*humiographql.FilterAlertDetails, error)
	UpdateFilterAlert(context.Context, *humioapi.Client, *humiov1alpha1.HumioFilterAlert) error
	DeleteFilterAlert(context.Context, *humioapi.Client, *humiov1alpha1.HumioFilterAlert) error
	ValidateActionsForFilterAlert(context.Context, *humioapi.Client, *humiov1alpha1.HumioFilterAlert) error
}

type FeatureFlagsClient interface {
	GetFeatureFlags(context.Context, *humioapi.Client) ([]string, error)
	EnableFeatureFlag(context.Context, *humioapi.Client, *humiov1alpha1.HumioFeatureFlag) error
	IsFeatureFlagEnabled(context.Context, *humioapi.Client, *humiov1alpha1.HumioFeatureFlag) (bool, error)
	DisableFeatureFlag(context.Context, *humioapi.Client, *humiov1alpha1.HumioFeatureFlag) error
}

type AggregateAlertsClient interface {
	AddAggregateAlert(context.Context, *humioapi.Client, *humiov1alpha1.HumioAggregateAlert) error
	GetAggregateAlert(context.Context, *humioapi.Client, *humiov1alpha1.HumioAggregateAlert) (*humiographql.AggregateAlertDetails, error)
	UpdateAggregateAlert(context.Context, *humioapi.Client, *humiov1alpha1.HumioAggregateAlert) error
	DeleteAggregateAlert(context.Context, *humioapi.Client, *humiov1alpha1.HumioAggregateAlert) error
	ValidateActionsForAggregateAlert(context.Context, *humioapi.Client, *humiov1alpha1.HumioAggregateAlert) error
}

type ScheduledSearchClient interface {
	AddScheduledSearch(context.Context, *humioapi.Client, *humiov1alpha1.HumioScheduledSearch) error
	GetScheduledSearch(context.Context, *humioapi.Client, *humiov1alpha1.HumioScheduledSearch) (*humiographql.ScheduledSearchDetails, error)
	UpdateScheduledSearch(context.Context, *humioapi.Client, *humiov1alpha1.HumioScheduledSearch) error
	DeleteScheduledSearch(context.Context, *humioapi.Client, *humiov1alpha1.HumioScheduledSearch) error
	ValidateActionsForScheduledSearch(context.Context, *humioapi.Client, *humiov1alpha1.HumioScheduledSearch) error
}

// ScheduledSearchClientV2 soon to replace ScheduledSearchClient
type ScheduledSearchClientV2 interface {
	AddScheduledSearchV2(context.Context, *humioapi.Client, *humiov1beta1.HumioScheduledSearch) error
	GetScheduledSearchV2(context.Context, *humioapi.Client, *humiov1beta1.HumioScheduledSearch) (*humiographql.ScheduledSearchDetailsV2, error)
	UpdateScheduledSearchV2(context.Context, *humioapi.Client, *humiov1beta1.HumioScheduledSearch) error
	DeleteScheduledSearchV2(context.Context, *humioapi.Client, *humiov1beta1.HumioScheduledSearch) error
	ValidateActionsForScheduledSearchV2(context.Context, *humioapi.Client, *humiov1beta1.HumioScheduledSearch) error
}

type SavedQueriesClient interface {
	AddSavedQuery(context.Context, *humioapi.Client, *humiov1alpha1.HumioSavedQuery, bool) error
	AddSavedQueryV2(context.Context, *humioapi.Client, *humiov1alpha1.HumioSavedQuery) error
	GetSavedQuery(context.Context, *humioapi.Client, *humiov1alpha1.HumioSavedQuery) (*humiographql.SavedQueryDetails, error)
	GetSavedQueryV2(context.Context, *humioapi.Client, *humiov1alpha1.HumioSavedQuery) (*humiographql.SavedQueryDetailsV2, error)
	UpdateSavedQuery(context.Context, *humioapi.Client, *humiov1alpha1.HumioSavedQuery, bool) error
	UpdateSavedQueryV2(context.Context, *humioapi.Client, *humiov1alpha1.HumioSavedQuery) error
	DeleteSavedQuery(context.Context, *humioapi.Client, *humiov1alpha1.HumioSavedQuery) error
}

type LicenseClient interface {
	GetLicenseUIDAndExpiry(context.Context, *humioapi.Client, reconcile.Request) (string, time.Time, error)
	InstallLicense(context.Context, *humioapi.Client, reconcile.Request, string) error
}

type UsersClient interface {
	AddUser(context.Context, *humioapi.Client, *humiov1alpha1.HumioUser) error
	GetUser(context.Context, *humioapi.Client, *humiov1alpha1.HumioUser) (*humiographql.UserDetails, error)
	UpdateUser(context.Context, *humioapi.Client, *humiov1alpha1.HumioUser) error
	DeleteUser(context.Context, *humioapi.Client, *humiov1alpha1.HumioUser) error

	// TODO: Rename the ones below, or perhaps get rid of them entirely?
	AddUserAndGetUserID(context.Context, *humioapi.Client, reconcile.Request, string, bool) (string, error)
	GetUserIDForUsername(context.Context, *humioapi.Client, reconcile.Request, string) (string, error)
	RotateUserApiTokenAndGet(context.Context, *humioapi.Client, reconcile.Request, string) (string, error)
}

type SystemPermissionRolesClient interface {
	AddSystemPermissionRole(context.Context, *humioapi.Client, *humiov1alpha1.HumioSystemPermissionRole) error
	GetSystemPermissionRole(context.Context, *humioapi.Client, *humiov1alpha1.HumioSystemPermissionRole) (*humiographql.RoleDetails, error)
	UpdateSystemPermissionRole(context.Context, *humioapi.Client, *humiov1alpha1.HumioSystemPermissionRole) error
	DeleteSystemPermissionRole(context.Context, *humioapi.Client, *humiov1alpha1.HumioSystemPermissionRole) error
}

type OrganizationPermissionRolesClient interface {
	AddOrganizationPermissionRole(context.Context, *humioapi.Client, *humiov1alpha1.HumioOrganizationPermissionRole) error
	GetOrganizationPermissionRole(context.Context, *humioapi.Client, *humiov1alpha1.HumioOrganizationPermissionRole) (*humiographql.RoleDetails, error)
	UpdateOrganizationPermissionRole(context.Context, *humioapi.Client, *humiov1alpha1.HumioOrganizationPermissionRole) error
	DeleteOrganizationPermissionRole(context.Context, *humioapi.Client, *humiov1alpha1.HumioOrganizationPermissionRole) error
}

type ViewPermissionRolesClient interface {
	AddViewPermissionRole(context.Context, *humioapi.Client, *humiov1alpha1.HumioViewPermissionRole) error
	GetViewPermissionRole(context.Context, *humioapi.Client, *humiov1alpha1.HumioViewPermissionRole) (*humiographql.RoleDetails, error)
	UpdateViewPermissionRole(context.Context, *humioapi.Client, *humiov1alpha1.HumioViewPermissionRole) error
	DeleteViewPermissionRole(context.Context, *humioapi.Client, *humiov1alpha1.HumioViewPermissionRole) error
}

type IPFilterClient interface {
	AddIPFilter(context.Context, *humioapi.Client, *humiov1alpha1.HumioIPFilter) (*humiographql.IPFilterDetails, error)
	GetIPFilter(context.Context, *humioapi.Client, *humiov1alpha1.HumioIPFilter) (*humiographql.IPFilterDetails, error)
	UpdateIPFilter(context.Context, *humioapi.Client, *humiov1alpha1.HumioIPFilter) error
	DeleteIPFilter(context.Context, *humioapi.Client, *humiov1alpha1.HumioIPFilter) error
}

type ViewTokenClient interface {
	CreateViewToken(context.Context, *humioapi.Client, *humiov1alpha1.HumioViewToken, string, []string, []humiographql.Permission) (string, string, error)
	GetViewToken(context.Context, *humioapi.Client, *humiov1alpha1.HumioViewToken) (*humiographql.ViewTokenDetailsViewPermissionsToken, error)
	UpdateViewToken(context.Context, *humioapi.Client, *humiov1alpha1.HumioViewToken, []humiographql.Permission) error
	DeleteViewToken(context.Context, *humioapi.Client, *humiov1alpha1.HumioViewToken) error
	RotateViewToken(context.Context, *humioapi.Client, *humiov1alpha1.HumioViewToken) (string, string, error)
}
type SystemTokenClient interface {
	CreateSystemToken(context.Context, *humioapi.Client, *humiov1alpha1.HumioSystemToken, string, []humiographql.SystemPermission) (string, string, error)
	GetSystemToken(context.Context, *humioapi.Client, *humiov1alpha1.HumioSystemToken) (*humiographql.SystemTokenDetailsSystemPermissionsToken, error)
	UpdateSystemToken(context.Context, *humioapi.Client, *humiov1alpha1.HumioSystemToken, []humiographql.SystemPermission) error
	DeleteSystemToken(context.Context, *humioapi.Client, *humiov1alpha1.HumioSystemToken) error
	RotateSystemToken(context.Context, *humioapi.Client, *humiov1alpha1.HumioSystemToken) (string, string, error)
}
type OrganizationTokenClient interface {
	CreateOrganizationToken(context.Context, *humioapi.Client, *humiov1alpha1.HumioOrganizationToken, string, []humiographql.OrganizationPermission) (string, string, error)
	GetOrganizationToken(context.Context, *humioapi.Client, *humiov1alpha1.HumioOrganizationToken) (*humiographql.OrganizationTokenDetailsOrganizationPermissionsToken, error)
	UpdateOrganizationToken(context.Context, *humioapi.Client, *humiov1alpha1.HumioOrganizationToken, []humiographql.OrganizationPermission) error
	DeleteOrganizationToken(context.Context, *humioapi.Client, *humiov1alpha1.HumioOrganizationToken) error
	RotateOrganizationToken(context.Context, *humioapi.Client, *humiov1alpha1.HumioOrganizationToken) (string, string, error)
}
type SecurityPoliciesClient interface {
	EnableTokenUpdatePermissionsForTests(context.Context, *humioapi.Client) error
}

type PackageClient interface {
	AnalyzePackageFromZip(context.Context, *humioapi.Client, string, string) (any, error)
	InstallPackageFromZip(context.Context, *humioapi.Client, *humiov1alpha1.HumioPackage, string, string) error
	UninstallPackage(context.Context, *humioapi.Client, *humiov1alpha1.HumioPackage, string) (bool, error)
	CheckPackage(context.Context, *humioapi.Client, *humiov1alpha1.HumioPackage, string) (*humiographql.PackageDetails, error)
}

type TelemetryClient interface {
	CollectLicenseData(context.Context, *humioapi.Client, client.Client, *humiov1alpha1.HumioCluster) (*TelemetryLicenseData, error)
	CollectClusterInfo(context.Context, *humioapi.Client) (*TelemetryClusterInfo, error)
	CollectTelemetryData(context.Context, *humioapi.Client, []string, string, bool, client.Client, *humiov1alpha1.HumioCluster) ([]TelemetryPayload, string, error)
}

type EventForwardingRulesClient interface {
	AddEventForwardingRule(context.Context, *humioapi.Client, *humiov1alpha1.HumioEventForwardingRule) error
	GetEventForwardingRule(context.Context, *humioapi.Client, *humiov1alpha1.HumioEventForwardingRule) (*humiographql.EventForwardingRuleDetails, error)
	UpdateEventForwardingRule(context.Context, *humioapi.Client, *humiov1alpha1.HumioEventForwardingRule) error
	DeleteEventForwardingRule(context.Context, *humioapi.Client, *humiov1alpha1.HumioEventForwardingRule) error
}

type EventForwardersClient interface {
	AddEventForwarder(context.Context, *humioapi.Client, *humiov1alpha1.HumioEventForwarder) error
	GetEventForwarder(context.Context, *humioapi.Client, *humiov1alpha1.HumioEventForwarder) (*humiographql.KafkaEventForwarderDetails, error)
	UpdateEventForwarder(context.Context, *humioapi.Client, *humiov1alpha1.HumioEventForwarder) error
	DeleteEventForwarder(context.Context, *humioapi.Client, *humiov1alpha1.HumioEventForwarder) error
}

type ConnectionDetailsIncludingAPIToken struct {
	humiov1alpha1.HumioMultiClusterSearchViewConnection
	APIToken string
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

// PackageErrorReportJson represents the error response when analysis fails
type PackageErrorReportJson struct {
	ParseErrors        []string `json:"parseErrors"`
	InstallationErrors []string `json:"installationErrors"`
	ResponseType       string   `json:"responseType"`
}

// PackageManifestResultJson represents the package manifest information. Incomplete structure
type PackageManifestResultJson struct {
	Name        string  `json:"name"`
	Version     string  `json:"version"`
	LogoURL     *string `json:"logoUrl"`
	Description string  `json:"description"`
	Status      string  `json:"status"` // "New", "Unchanged", "Changed"
}

// PackageAnalysisResultJson represents the successful analysis response. Incomplete structure
type PackageAnalysisResultJson struct {
	ExistingVersion           *string                   `json:"existingVersion"`
	PackageManifestResultJson PackageManifestResultJson `json:"packageManifestResultJson"`
	IgnoredFiles              []string                  `json:"ignoredFiles"`
	ResponseType              string                    `json:"responseType"`
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
func (h *ClientConfig) Status(ctx context.Context, client *humioapi.Client) (*humioapi.StatusResponse, error) {
	return client.Status(ctx)
}

// GetCluster returns a humio cluster and can be mocked via the Client interface
func (h *ClientConfig) GetCluster(ctx context.Context, client *humioapi.Client) (*humiographql.GetClusterResponse, error) {
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
func (h *ClientConfig) GetEvictionStatus(ctx context.Context, client *humioapi.Client) (*humiographql.GetEvictionStatusResponse, error) {
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
func (h *ClientConfig) SetIsBeingEvicted(ctx context.Context, client *humioapi.Client, vhost int, isBeingEvicted bool) error {
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
func (h *ClientConfig) RefreshClusterManagementStats(ctx context.Context, client *humioapi.Client, vhost int) (*humiographql.RefreshClusterManagementStatsResponse, error) {
	response, err := humiographql.RefreshClusterManagementStats(
		ctx,
		client,
		vhost,
	)
	return response, err
}

// UnregisterClusterNode unregisters a humio node from the cluster and can be mocked via the Client interface
func (h *ClientConfig) UnregisterClusterNode(ctx context.Context, client *humioapi.Client, nodeId int, force bool) (*humiographql.UnregisterClusterNodeResponse, error) {
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

func (h *ClientConfig) AddIngestToken(ctx context.Context, client *humioapi.Client, hit *humiov1alpha1.HumioIngestToken) error {
	_, err := humiographql.AddIngestToken(
		ctx,
		client,
		hit.Spec.RepositoryName,
		hit.Spec.Name,
		hit.Spec.ParserName,
	)
	return err
}

func (h *ClientConfig) GetIngestToken(ctx context.Context, client *humioapi.Client, hit *humiov1alpha1.HumioIngestToken) (*humiographql.IngestTokenDetails, error) {
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

func (h *ClientConfig) UpdateIngestToken(ctx context.Context, client *humioapi.Client, hit *humiov1alpha1.HumioIngestToken) error {
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

func (h *ClientConfig) DeleteIngestToken(ctx context.Context, client *humioapi.Client, hit *humiov1alpha1.HumioIngestToken) error {
	_, err := humiographql.RemoveIngestToken(
		ctx,
		client,
		hit.Spec.RepositoryName,
		hit.Spec.Name,
	)
	return err
}

func (h *ClientConfig) AddParser(ctx context.Context, client *humioapi.Client, hp *humiov1alpha1.HumioParser) error {
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

func (h *ClientConfig) GetParser(ctx context.Context, client *humioapi.Client, hp *humiov1alpha1.HumioParser) (*humiographql.ParserDetails, error) {
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

func (h *ClientConfig) UpdateParser(ctx context.Context, client *humioapi.Client, hp *humiov1alpha1.HumioParser) error {
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

func (h *ClientConfig) DeleteParser(ctx context.Context, client *humioapi.Client, hp *humiov1alpha1.HumioParser) error {
	parser, err := h.GetParser(ctx, client, hp)
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

func (h *ClientConfig) AddEventForwardingRule(ctx context.Context, client *humioapi.Client, hefr *humiov1alpha1.HumioEventForwardingRule) error {
	var langVer *humiographql.LanguageVersionEnum
	if hefr.Spec.LanguageVersion != nil {
		val := humiographql.LanguageVersionEnum(*hefr.Spec.LanguageVersion)
		langVer = &val
	}

	// Use the resolved event forwarder ID from status
	forwarderID := hefr.Status.ResolvedEventForwarderID
	if forwarderID == "" {
		return fmt.Errorf("resolved event forwarder ID not found in status")
	}

	resp, err := humiographql.CreateEventForwardingRule(
		ctx,
		client,
		hefr.Spec.RepositoryName,
		hefr.Spec.QueryString,
		forwarderID,
		langVer,
	)
	if err != nil {
		return err
	}

	// Store the rule ID in annotations for future operations
	if hefr.Annotations == nil {
		hefr.Annotations = make(map[string]string)
	}
	hefr.Annotations[EventForwardingRuleAnnotation] = resp.CreateEventForwardingRule.GetId()

	return nil
}

func (h *ClientConfig) GetEventForwardingRule(ctx context.Context, client *humioapi.Client, hefr *humiov1alpha1.HumioEventForwardingRule) (*humiographql.EventForwardingRuleDetails, error) {
	// Get rule ID from annotation
	ruleID := hefr.Annotations[EventForwardingRuleAnnotation]
	if ruleID == "" {
		return nil, humioapi.EventForwardingRuleNotFound("unknown")
	}

	// List all rules and find by ID
	resp, err := humiographql.GetEventForwardingRules(
		ctx,
		client,
		hefr.Spec.RepositoryName,
	)
	if err != nil {
		return nil, err
	}

	searchDomain := resp.GetSearchDomain()
	if repo, ok := searchDomain.(*humiographql.GetEventForwardingRulesSearchDomainRepository); ok {
		rules := repo.GetEventForwardingRules()
		for _, rule := range rules {
			if rule.GetId() == ruleID {
				return &rule.EventForwardingRuleDetails, nil
			}
		}
	}

	return nil, humioapi.EventForwardingRuleNotFound(ruleID)
}

func (h *ClientConfig) UpdateEventForwardingRule(ctx context.Context, client *humioapi.Client, hefr *humiov1alpha1.HumioEventForwardingRule) error {
	// Get rule ID from annotation
	ruleID := hefr.Annotations[EventForwardingRuleAnnotation]
	if ruleID == "" {
		return fmt.Errorf("event forwarding rule ID not found in annotations")
	}

	var langVer *humiographql.LanguageVersionEnum
	if hefr.Spec.LanguageVersion != nil {
		val := humiographql.LanguageVersionEnum(*hefr.Spec.LanguageVersion)
		langVer = &val
	}

	// Use the resolved event forwarder ID from status
	forwarderID := hefr.Status.ResolvedEventForwarderID
	if forwarderID == "" {
		return fmt.Errorf("resolved event forwarder ID not found in status")
	}

	_, err := humiographql.UpdateEventForwardingRule(
		ctx,
		client,
		hefr.Spec.RepositoryName,
		ruleID,
		hefr.Spec.QueryString,
		forwarderID,
		langVer,
	)
	return err
}

func (h *ClientConfig) DeleteEventForwardingRule(ctx context.Context, client *humioapi.Client, hefr *humiov1alpha1.HumioEventForwardingRule) error {
	// Get rule ID from annotation
	ruleID := hefr.Annotations[EventForwardingRuleAnnotation]
	if ruleID == "" {
		// If no ID annotation exists, the rule was never created or already deleted
		return nil
	}

	// Check if rule still exists
	_, err := h.GetEventForwardingRule(ctx, client, hefr)
	if errors.As(err, &humioapi.EntityNotFound{}) {
		return nil
	}
	if err != nil {
		return err
	}

	_, err = humiographql.DeleteEventForwardingRule(
		ctx,
		client,
		hefr.Spec.RepositoryName,
		ruleID,
	)
	return err
}

// EventForwarder methods

func (h *ClientConfig) AddEventForwarder(ctx context.Context, client *humioapi.Client, hef *humiov1alpha1.HumioEventForwarder) error {
	if hef.Spec.ForwarderType != "kafka" {
		return fmt.Errorf("unsupported forwarder type: %s", hef.Spec.ForwarderType)
	}

	if hef.Spec.KafkaConfig == nil {
		return fmt.Errorf("kafkaConfig is required when forwarderType is kafka")
	}

	// Create Kafka event forwarder
	resp, err := humiographql.CreateKafkaEventForwarder(
		ctx,
		client,
		hef.Spec.Name,
		hef.Spec.Description,
		hef.Spec.KafkaConfig.Topic,
		hef.Spec.KafkaConfig.Properties,
		&hef.Spec.Enabled,
	)
	if err != nil {
		return err
	}

	// Store the forwarder ID in status
	hef.Status.EventForwarderID = resp.CreateKafkaEventForwarder.GetId()

	return nil
}

func (h *ClientConfig) GetEventForwarder(ctx context.Context, client *humioapi.Client, hef *humiov1alpha1.HumioEventForwarder) (*humiographql.KafkaEventForwarderDetails, error) {
	// List all forwarders
	resp, err := humiographql.GetEventForwarders(ctx, client)
	if err != nil {
		return nil, err
	}

	// Get forwarder ID from status
	forwarderID := hef.Status.EventForwarderID

	// Case 1: We have an ID in status - look up by ID (normal case)
	if forwarderID != "" {
		for _, forwarder := range resp.GetEventForwarders() {
			if kafkaForwarder, ok := forwarder.(*humiographql.GetEventForwardersEventForwardersKafkaEventForwarder); ok {
				if kafkaForwarder.GetId() == forwarderID {
					return &kafkaForwarder.KafkaEventForwarderDetails, nil
				}
			}
		}
		return nil, humioapi.EventForwarderNotFound(forwarderID)
	}

	// Case 2: No ID in status - try to find existing forwarder by name for potential adoption
	// This handles the scenario where an unmanaged forwarder already exists in LogScale
	// We DON'T set the ID here - the controller will decide whether to adopt after property verification
	for _, forwarder := range resp.GetEventForwarders() {
		if kafkaForwarder, ok := forwarder.(*humiographql.GetEventForwardersEventForwardersKafkaEventForwarder); ok {
			if kafkaForwarder.GetName() == hef.Spec.Name {
				// Found a forwarder with matching name
				// Return details WITHOUT setting ID - controller will verify properties and decide
				// Note: The caller must ensure hef.Spec.KafkaConfig.Properties contains merged properties
				// for accurate comparison during adoption
				return &kafkaForwarder.KafkaEventForwarderDetails, nil
			}
		}
	}

	// No forwarder found by ID or name
	return nil, humioapi.EventForwarderNotFound(hef.Spec.Name)
}

func (h *ClientConfig) UpdateEventForwarder(ctx context.Context, client *humioapi.Client, hef *humiov1alpha1.HumioEventForwarder) error {
	// Get forwarder ID from status
	forwarderID := hef.Status.EventForwarderID
	if forwarderID == "" {
		return fmt.Errorf("event forwarder ID not found in status")
	}

	if hef.Spec.ForwarderType != "kafka" {
		return fmt.Errorf("unsupported forwarder type: %s", hef.Spec.ForwarderType)
	}

	if hef.Spec.KafkaConfig == nil {
		return fmt.Errorf("kafkaConfig is required when forwarderType is kafka")
	}

	_, err := humiographql.UpdateKafkaEventForwarder(
		ctx,
		client,
		forwarderID,
		hef.Spec.Name,
		hef.Spec.Description,
		hef.Spec.KafkaConfig.Topic,
		hef.Spec.KafkaConfig.Properties,
		&hef.Spec.Enabled,
	)
	return err
}

func (h *ClientConfig) DeleteEventForwarder(ctx context.Context, client *humioapi.Client, hef *humiov1alpha1.HumioEventForwarder) error {
	// Get forwarder ID from status
	forwarderID := hef.Status.EventForwarderID
	if forwarderID == "" {
		// If no ID in status, the forwarder was never created or already deleted
		return nil
	}

	// Check if forwarder still exists
	_, err := h.GetEventForwarder(ctx, client, hef)
	if errors.As(err, &humioapi.EntityNotFound{}) {
		return nil
	}
	if err != nil {
		return err
	}

	_, err = humiographql.DeleteEventForwarder(ctx, client, forwarderID)
	return err
}

func (h *ClientConfig) AddRepository(ctx context.Context, client *humioapi.Client, hr *humiov1alpha1.HumioRepository) error {
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

func (h *ClientConfig) GetRepository(ctx context.Context, client *humioapi.Client, hr *humiov1alpha1.HumioRepository) (*humiographql.RepositoryDetails, error) {
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

func (h *ClientConfig) UpdateRepository(ctx context.Context, client *humioapi.Client, hr *humiov1alpha1.HumioRepository) error {
	curRepository, err := h.GetRepository(ctx, client, hr)
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

func (h *ClientConfig) DeleteRepository(ctx context.Context, client *humioapi.Client, hr *humiov1alpha1.HumioRepository) error {
	_, err := h.GetRepository(ctx, client, hr)
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

func (h *ClientConfig) GetView(ctx context.Context, client *humioapi.Client, hv *humiov1alpha1.HumioView, includeFederated bool) (*humiographql.GetSearchDomainSearchDomainView, error) {
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
		if !includeFederated {
			if v.GetIsFederated() {
				return nil, fmt.Errorf("view %q is a multi cluster search view", v.GetName())
			}
		}
		return v, nil
	default:
		return nil, humioapi.ViewNotFound(hv.Spec.Name)
	}
}

func (h *ClientConfig) AddView(ctx context.Context, client *humioapi.Client, hv *humiov1alpha1.HumioView) error {
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

func (h *ClientConfig) UpdateView(ctx context.Context, client *humioapi.Client, hv *humiov1alpha1.HumioView) error {
	curView, err := h.GetView(ctx, client, hv, false)
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

func (h *ClientConfig) DeleteView(ctx context.Context, client *humioapi.Client, hv *humiov1alpha1.HumioView) error {
	_, err := h.GetView(ctx, client, hv, false)
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

func (h *ClientConfig) GetMultiClusterSearchView(ctx context.Context, client *humioapi.Client, hv *humiov1alpha1.HumioMultiClusterSearchView) (*humiographql.GetMultiClusterSearchViewSearchDomainView, error) {
	resp, err := humiographql.GetMultiClusterSearchView(
		ctx,
		client,
		hv.Spec.Name,
	)
	if err != nil {
		return nil, humioapi.ViewNotFound(hv.Spec.Name)
	}

	searchDomain := resp.GetSearchDomain()
	switch v := searchDomain.(type) {
	case *humiographql.GetMultiClusterSearchViewSearchDomainView:
		if v.GetIsFederated() {
			return v, nil
		}
		return nil, fmt.Errorf("view %q is not a multi cluster search view", v.GetName())
	default:
		return nil, humioapi.ViewNotFound(hv.Spec.Name)
	}
}

func (h *ClientConfig) AddMultiClusterSearchView(ctx context.Context, client *humioapi.Client, hv *humiov1alpha1.HumioMultiClusterSearchView, connectionDetails []ConnectionDetailsIncludingAPIToken) error {
	// create empty view
	if _, err := humiographql.CreateMultiClusterSearchView(
		ctx,
		client,
		hv.Spec.Name,
		&hv.Spec.Description,
	); err != nil {
		return err
	}

	// set desired automatic search behavior
	if _, err := humiographql.SetAutomaticSearching(
		ctx,
		client,
		hv.Spec.Name,
		helpers.BoolTrue(hv.Spec.AutomaticSearch),
	); err != nil {
		return err
	}

	// add connections
	for _, connection := range connectionDetails {
		if connection.Type == humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeLocal {
			tags := make([]humiographql.ClusterConnectionInputTag, len(connection.Tags)+1)
			tags[0] = humiographql.ClusterConnectionInputTag{
				Key:   "clusteridentity",
				Value: connection.ClusterIdentity,
			}
			for tagIdx, tag := range connection.Tags {
				tags[tagIdx+1] = humiographql.ClusterConnectionInputTag(tag)
			}

			_, createErr := humiographql.CreateLocalMultiClusterSearchViewConnection(
				ctx,
				client,
				hv.Spec.Name,
				connection.ViewOrRepoName,
				tags,
				&connection.Filter,
			)
			if createErr != nil {
				return createErr
			}
		}

		if connection.Type == humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeRemote {
			tags := make([]humiographql.ClusterConnectionInputTag, len(connection.Tags)+2)
			tags[0] = humiographql.ClusterConnectionInputTag{
				Key:   "clusteridentity",
				Value: connection.ClusterIdentity,
			}
			tags[1] = humiographql.ClusterConnectionInputTag{
				Key:   "clusteridentityhash",
				Value: helpers.AsSHA256(fmt.Sprintf("%s|%s", connection.Url, connection.APIToken)),
			}
			for tagIdx, tag := range connection.Tags {
				tags[tagIdx+2] = humiographql.ClusterConnectionInputTag(tag)
			}

			_, createErr := humiographql.CreateRemoteMultiClusterSearchViewConnection(
				ctx,
				client,
				hv.Spec.Name,
				connection.Url,
				connection.APIToken,
				tags,
				&connection.Filter,
			)
			if createErr != nil {
				return createErr
			}
		}
	}

	return nil
}

func (h *ClientConfig) UpdateMultiClusterSearchView(ctx context.Context, client *humioapi.Client, hv *humiov1alpha1.HumioMultiClusterSearchView, connectionDetails []ConnectionDetailsIncludingAPIToken) error {
	curView, err := h.GetMultiClusterSearchView(ctx, client, hv)
	if err != nil {
		return err
	}

	if err := h.updateViewDescription(ctx, client, hv, curView); err != nil {
		return err
	}

	if err := h.updateAutomaticSearch(ctx, client, hv, curView); err != nil {
		return err
	}

	if err := h.syncClusterConnections(ctx, client, hv, curView, connectionDetails); err != nil {
		return err
	}

	return nil
}

func (h *ClientConfig) updateViewDescription(ctx context.Context, client *humioapi.Client, hv *humiov1alpha1.HumioMultiClusterSearchView, curView *humiographql.GetMultiClusterSearchViewSearchDomainView) error {
	if cmp.Diff(curView.Description, &hv.Spec.Description) != "" {
		_, err := humiographql.UpdateDescriptionForSearchDomain(
			ctx,
			client,
			hv.Spec.Name,
			hv.Spec.Description,
		)
		return err
	}
	return nil
}

func (h *ClientConfig) updateAutomaticSearch(ctx context.Context, client *humioapi.Client, hv *humiov1alpha1.HumioMultiClusterSearchView, curView *humiographql.GetMultiClusterSearchViewSearchDomainView) error {
	if curView.AutomaticSearch != helpers.BoolTrue(hv.Spec.AutomaticSearch) {
		_, err := humiographql.SetAutomaticSearching(
			ctx,
			client,
			hv.Spec.Name,
			helpers.BoolTrue(hv.Spec.AutomaticSearch),
		)
		return err
	}
	return nil
}

func (h *ClientConfig) syncClusterConnections(ctx context.Context, client *humioapi.Client, hv *humiov1alpha1.HumioMultiClusterSearchView, curView *humiographql.GetMultiClusterSearchViewSearchDomainView, connectionDetails []ConnectionDetailsIncludingAPIToken) error {
	expectedClusterIdentityNames := h.extractExpectedClusterIdentities(connectionDetails)
	currentClusterIdentityNames, err := h.extractCurrentClusterIdentities(curView)
	if err != nil {
		return err
	}

	if err := h.addMissingConnections(ctx, client, hv, connectionDetails, currentClusterIdentityNames); err != nil {
		return err
	}

	if err := h.removeUnexpectedConnections(ctx, client, hv, curView, expectedClusterIdentityNames); err != nil {
		return err
	}

	if err := h.updateExistingConnections(ctx, client, hv, curView, connectionDetails); err != nil {
		return err
	}

	return nil
}

func (h *ClientConfig) extractExpectedClusterIdentities(connectionDetails []ConnectionDetailsIncludingAPIToken) []string {
	expectedClusterIdentityNames := make([]string, len(connectionDetails))
	for idx, expectedConnection := range connectionDetails {
		expectedClusterIdentityNames[idx] = expectedConnection.ClusterIdentity
	}
	return expectedClusterIdentityNames
}

func (h *ClientConfig) extractCurrentClusterIdentities(curView *humiographql.GetMultiClusterSearchViewSearchDomainView) ([]string, error) {
	currentClusterIdentityNames := make([]string, len(curView.GetClusterConnections()))
	for idx, currentConnection := range curView.GetClusterConnections() {
		switch v := currentConnection.(type) {
		case *humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsLocalClusterConnection:
			currentClusterIdentityNames[idx] = v.GetClusterId()
		case *humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsRemoteClusterConnection:
			currentClusterIdentityNames[idx] = v.GetClusterId()
		default:
			return nil, fmt.Errorf("unknown cluster connection type: %T", v)
		}
	}
	return currentClusterIdentityNames, nil
}

func (h *ClientConfig) addMissingConnections(ctx context.Context, client *humioapi.Client, hv *humiov1alpha1.HumioMultiClusterSearchView, connectionDetails []ConnectionDetailsIncludingAPIToken, currentClusterIdentityNames []string) error {
	for _, expectedConnection := range connectionDetails {
		if !slices.Contains(currentClusterIdentityNames, expectedConnection.ClusterIdentity) {
			if err := h.createConnection(ctx, client, hv, expectedConnection); err != nil {
				return err
			}
		}
	}
	return nil
}

func (h *ClientConfig) createConnection(ctx context.Context, client *humioapi.Client, hv *humiov1alpha1.HumioMultiClusterSearchView, expectedConnection ConnectionDetailsIncludingAPIToken) error {
	switch expectedConnection.Type {
	case humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeLocal:
		return h.createLocalConnection(ctx, client, hv, expectedConnection)
	case humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeRemote:
		return h.createRemoteConnection(ctx, client, hv, expectedConnection)
	default:
		return fmt.Errorf("unknown connection type: %v", expectedConnection.Type)
	}
}

func (h *ClientConfig) createLocalConnection(ctx context.Context, client *humioapi.Client, hv *humiov1alpha1.HumioMultiClusterSearchView, expectedConnection ConnectionDetailsIncludingAPIToken) error {
	tags := h.buildLocalConnectionTags(expectedConnection)
	_, err := humiographql.CreateLocalMultiClusterSearchViewConnection(
		ctx,
		client,
		hv.Spec.Name,
		expectedConnection.ViewOrRepoName,
		tags,
		&expectedConnection.Filter,
	)
	return err
}

func (h *ClientConfig) createRemoteConnection(ctx context.Context, client *humioapi.Client, hv *humiov1alpha1.HumioMultiClusterSearchView, expectedConnection ConnectionDetailsIncludingAPIToken) error {
	tags := h.buildRemoteConnectionTags(expectedConnection)
	_, err := humiographql.CreateRemoteMultiClusterSearchViewConnection(
		ctx,
		client,
		hv.Spec.Name,
		expectedConnection.Url,
		expectedConnection.APIToken,
		tags,
		&expectedConnection.Filter,
	)
	return err
}

func (h *ClientConfig) buildLocalConnectionTags(expectedConnection ConnectionDetailsIncludingAPIToken) []humiographql.ClusterConnectionInputTag {
	tags := make([]humiographql.ClusterConnectionInputTag, len(expectedConnection.Tags)+1)
	tags[0] = humiographql.ClusterConnectionInputTag{
		Key:   "clusteridentity",
		Value: expectedConnection.ClusterIdentity,
	}
	for tagIdx, tag := range expectedConnection.Tags {
		tags[tagIdx+1] = humiographql.ClusterConnectionInputTag(tag)
	}
	return tags
}

func (h *ClientConfig) buildRemoteConnectionTags(expectedConnection ConnectionDetailsIncludingAPIToken) []humiographql.ClusterConnectionInputTag {
	tags := make([]humiographql.ClusterConnectionInputTag, len(expectedConnection.Tags)+2)
	tags[0] = humiographql.ClusterConnectionInputTag{
		Key:   "clusteridentityhash",
		Value: helpers.AsSHA256(fmt.Sprintf("%s|%s", expectedConnection.Url, expectedConnection.APIToken)),
	}
	tags[1] = humiographql.ClusterConnectionInputTag{
		Key:   "clusteridentity",
		Value: expectedConnection.ClusterIdentity,
	}
	for tagIdx, tag := range expectedConnection.Tags {
		tags[tagIdx+2] = humiographql.ClusterConnectionInputTag(tag)
	}
	return tags
}

func (h *ClientConfig) removeUnexpectedConnections(ctx context.Context, client *humioapi.Client, hv *humiov1alpha1.HumioMultiClusterSearchView, curView *humiographql.GetMultiClusterSearchViewSearchDomainView, expectedClusterIdentityNames []string) error {
	for _, currentConnection := range curView.GetClusterConnections() {
		if !slices.Contains(expectedClusterIdentityNames, currentConnection.GetClusterId()) {
			if err := h.deleteConnection(ctx, client, hv, currentConnection); err != nil {
				return err
			}
		}
	}
	return nil
}

func (h *ClientConfig) deleteConnection(ctx context.Context, client *humioapi.Client, hv *humiov1alpha1.HumioMultiClusterSearchView, currentConnection humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsClusterConnection) error {
	switch currentConnection.(type) {
	case *humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsLocalClusterConnection,
		*humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsRemoteClusterConnection:
		_, err := humiographql.DeleteMultiClusterSearchViewConnection(
			ctx,
			client,
			hv.Spec.Name,
			currentConnection.GetId(),
		)
		return err
	default:
		return fmt.Errorf("unknown cluster connection type: %T", currentConnection)
	}
}

func (h *ClientConfig) updateExistingConnections(ctx context.Context, client *humioapi.Client, hv *humiov1alpha1.HumioMultiClusterSearchView, curView *humiographql.GetMultiClusterSearchViewSearchDomainView, connectionDetails []ConnectionDetailsIncludingAPIToken) error {
	for _, currentConnection := range curView.GetClusterConnections() {
		expectedConnection := h.findExpectedConnection(currentConnection.GetClusterId(), connectionDetails)
		if expectedConnection == nil {
			continue
		}

		if err := h.updateConnectionIfNeeded(ctx, client, hv, currentConnection, *expectedConnection); err != nil {
			return err
		}
	}
	return nil
}

func (h *ClientConfig) findExpectedConnection(clusterId string, connectionDetails []ConnectionDetailsIncludingAPIToken) *ConnectionDetailsIncludingAPIToken {
	for _, expectedConnection := range connectionDetails {
		if expectedConnection.ClusterIdentity == clusterId {
			return &expectedConnection
		}
	}
	return nil
}

func (h *ClientConfig) updateConnectionIfNeeded(ctx context.Context, client *humioapi.Client, hv *humiov1alpha1.HumioMultiClusterSearchView, currentConnection humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsClusterConnection, expectedConnection ConnectionDetailsIncludingAPIToken) error {
	currentConnectionTags := h.extractCurrentConnectionTags(currentConnection)

	if h.connectionNeedsUpdate(currentConnection, currentConnectionTags, expectedConnection) {
		return h.updateConnection(ctx, client, hv, currentConnection, expectedConnection)
	}
	return nil
}

func (h *ClientConfig) extractCurrentConnectionTags(currentConnection humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsClusterConnection) []humiov1alpha1.HumioMultiClusterSearchViewConnectionTag {
	currentConnectionTags := make([]humiov1alpha1.HumioMultiClusterSearchViewConnectionTag, len(currentConnection.GetTags()))
	for idx, currentConnectionTag := range currentConnection.GetTags() {
		currentConnectionTags[idx] = humiov1alpha1.HumioMultiClusterSearchViewConnectionTag{
			Key:   currentConnectionTag.GetKey(),
			Value: currentConnectionTag.GetValue(),
		}
	}
	return currentConnectionTags
}

func (h *ClientConfig) connectionNeedsUpdate(currentConnection humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsClusterConnection, currentConnectionTags []humiov1alpha1.HumioMultiClusterSearchViewConnectionTag, expectedConnection ConnectionDetailsIncludingAPIToken) bool {
	return !cmp.Equal(currentConnectionTags, expectedConnection.Tags) ||
		currentConnection.GetQueryPrefix() != expectedConnection.Filter
}

func (h *ClientConfig) updateConnection(ctx context.Context, client *humioapi.Client, hv *humiov1alpha1.HumioMultiClusterSearchView, currentConnection humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsClusterConnection, expectedConnection ConnectionDetailsIncludingAPIToken) error {
	switch v := currentConnection.(type) {
	case *humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsLocalClusterConnection:
		return h.updateLocalConnection(ctx, client, hv, v, expectedConnection)
	case *humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsRemoteClusterConnection:
		return h.updateRemoteConnection(ctx, client, hv, v, expectedConnection)
	default:
		return fmt.Errorf("unknown cluster connection type: %T", v)
	}
}

func (h *ClientConfig) updateLocalConnection(ctx context.Context, client *humioapi.Client, hv *humiov1alpha1.HumioMultiClusterSearchView, currentConnection *humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsLocalClusterConnection, expectedConnection ConnectionDetailsIncludingAPIToken) error {
	tags := h.buildLocalConnectionTags(expectedConnection)
	_, err := humiographql.UpdateLocalMultiClusterSearchViewConnection(
		ctx,
		client,
		hv.Spec.Name,
		currentConnection.GetId(),
		&expectedConnection.ViewOrRepoName,
		tags,
		&expectedConnection.Filter,
	)
	return err
}

func (h *ClientConfig) updateRemoteConnection(ctx context.Context, client *humioapi.Client, hv *humiov1alpha1.HumioMultiClusterSearchView, currentConnection *humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsRemoteClusterConnection, expectedConnection ConnectionDetailsIncludingAPIToken) error {
	tags := h.buildRemoteConnectionTags(expectedConnection)
	_, err := humiographql.UpdateRemoteMultiClusterSearchViewConnection(
		ctx,
		client,
		hv.Spec.Name,
		currentConnection.GetId(),
		&expectedConnection.Url,
		&expectedConnection.APIToken,
		tags,
		&expectedConnection.Filter,
	)
	return err
}

func (h *ClientConfig) DeleteMultiClusterSearchView(ctx context.Context, client *humioapi.Client, hv *humiov1alpha1.HumioMultiClusterSearchView) error {
	_, err := h.GetMultiClusterSearchView(ctx, client, hv)
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

func (h *ClientConfig) AddGroup(ctx context.Context, client *humioapi.Client, hg *humiov1alpha1.HumioGroup) error {
	_, err := humiographql.CreateGroup(
		ctx,
		client,
		hg.Spec.Name,
		hg.Spec.ExternalMappingName,
	)
	return err
}

func (h *ClientConfig) GetGroup(ctx context.Context, client *humioapi.Client, hg *humiov1alpha1.HumioGroup) (*humiographql.GroupDetails, error) {
	getGroupResp, err := humiographql.GetGroupByDisplayName(
		ctx,
		client,
		hg.Spec.Name,
	)
	if err != nil {
		return nil, humioapi.GroupNotFound(hg.Spec.Name)
	}

	group := getGroupResp.GetGroupByDisplayName()
	return &humiographql.GroupDetails{
		Id:          group.GetId(),
		DisplayName: group.GetDisplayName(),
		LookupName:  group.GetLookupName(),
	}, nil
}

func (h *ClientConfig) UpdateGroup(ctx context.Context, client *humioapi.Client, hg *humiov1alpha1.HumioGroup) error {
	curGroup, err := h.GetGroup(ctx, client, hg)
	if err != nil {
		return err
	}

	newLookupName := hg.Spec.ExternalMappingName
	if hg.Spec.ExternalMappingName == nil {
		// LogScale returns null from graphql when lookup name is updated to empty string
		newLookupName = helpers.StringPtr("")
	}

	_, err = humiographql.UpdateGroup(
		ctx,
		client,
		curGroup.GetId(),
		&hg.Spec.Name,
		newLookupName,
	)
	return err
}

func (h *ClientConfig) DeleteGroup(ctx context.Context, client *humioapi.Client, hg *humiov1alpha1.HumioGroup) error {
	group, err := h.GetGroup(ctx, client, hg)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			return nil
		}
		return err
	}

	_, err = humiographql.DeleteGroup(
		ctx,
		client,
		group.Id,
	)
	return err
}

func (h *ClientConfig) GetAction(ctx context.Context, client *humioapi.Client, ha *humiov1alpha1.HumioAction) (humiographql.ActionDetails, error) {
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

func (h *ClientConfig) AddAction(ctx context.Context, client *humioapi.Client, ha *humiov1alpha1.HumioAction) error {
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

func (h *ClientConfig) UpdateAction(ctx context.Context, client *humioapi.Client, ha *humiov1alpha1.HumioAction) error {
	err := validateSearchDomain(ctx, client, ha.Spec.ViewName)
	if err != nil {
		return fmt.Errorf("problem getting view for action %s: %w", ha.Spec.Name, err)
	}

	newActionWithResolvedSecrets, err := ActionFromActionCR(ha)
	if err != nil {
		return err
	}

	currentAction, err := h.GetAction(ctx, client, ha)
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

func (h *ClientConfig) DeleteAction(ctx context.Context, client *humioapi.Client, ha *humiov1alpha1.HumioAction) error {
	action, err := h.GetAction(ctx, client, ha)
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

func (h *ClientConfig) GetAlert(ctx context.Context, client *humioapi.Client, ha *humiov1alpha1.HumioAlert) (*humiographql.AlertDetails, error) {
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

func (h *ClientConfig) AddAlert(ctx context.Context, client *humioapi.Client, ha *humiov1alpha1.HumioAlert) error {
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
		helpers.EmptySliceIfNil(ha.Spec.Labels),
		&queryOwnershipType,
		ha.Spec.ThrottleField,
	)
	return err
}

func (h *ClientConfig) UpdateAlert(ctx context.Context, client *humioapi.Client, ha *humiov1alpha1.HumioAlert) error {
	err := validateSearchDomain(ctx, client, ha.Spec.ViewName)
	if err != nil {
		return fmt.Errorf("problem getting view for action: %w", err)
	}

	currentAlert, err := h.GetAlert(ctx, client, ha)
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
		helpers.EmptySliceIfNil(ha.Spec.Labels),
		&queryOwnershipType,
		ha.Spec.ThrottleField,
	)
	return err
}

func (h *ClientConfig) DeleteAlert(ctx context.Context, client *humioapi.Client, ha *humiov1alpha1.HumioAlert) error {
	alert, err := h.GetAlert(ctx, client, ha)
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

func (h *ClientConfig) GetFilterAlert(ctx context.Context, client *humioapi.Client, hfa *humiov1alpha1.HumioFilterAlert) (*humiographql.FilterAlertDetails, error) {
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

func (h *ClientConfig) AddFilterAlert(ctx context.Context, client *humioapi.Client, hfa *humiov1alpha1.HumioFilterAlert) error {
	err := validateSearchDomain(ctx, client, hfa.Spec.ViewName)
	if err != nil {
		return fmt.Errorf("problem getting view for filter alert: %w", err)
	}
	if err = h.ValidateActionsForFilterAlert(ctx, client, hfa); err != nil {
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
		helpers.EmptySliceIfNil(hfa.Spec.Labels),
		hfa.Spec.Enabled,
		hfa.Spec.ThrottleField,
		int64(hfa.Spec.ThrottleTimeSeconds),
		humiographql.QueryOwnershipTypeOrganization,
	)
	return err
}

func (h *ClientConfig) UpdateFilterAlert(ctx context.Context, client *humioapi.Client, hfa *humiov1alpha1.HumioFilterAlert) error {
	err := validateSearchDomain(ctx, client, hfa.Spec.ViewName)
	if err != nil {
		return fmt.Errorf("problem getting view for action: %w", err)
	}
	if err = h.ValidateActionsForFilterAlert(ctx, client, hfa); err != nil {
		return fmt.Errorf("could not get action id mapping: %w", err)
	}

	currentAlert, err := h.GetFilterAlert(ctx, client, hfa)
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
		helpers.EmptySliceIfNil(hfa.Spec.Labels),
		hfa.Spec.Enabled,
		hfa.Spec.ThrottleField,
		int64(hfa.Spec.ThrottleTimeSeconds),
		humiographql.QueryOwnershipTypeOrganization,
	)
	return err
}

func (h *ClientConfig) DeleteFilterAlert(ctx context.Context, client *humioapi.Client, hfa *humiov1alpha1.HumioFilterAlert) error {
	currentFilterAlert, err := h.GetFilterAlert(ctx, client, hfa)
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

func (h *ClientConfig) AddScheduledSearch(ctx context.Context, client *humioapi.Client, hss *humiov1alpha1.HumioScheduledSearch) error {
	err := validateSearchDomain(ctx, client, hss.Spec.ViewName)
	if err != nil {
		return fmt.Errorf("problem getting view for scheduled search: %w", err)
	}
	if err = h.ValidateActionsForScheduledSearch(ctx, client, hss); err != nil {
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
		helpers.EmptySliceIfNil(hss.Spec.Labels),
		&queryOwnershipType,
	)
	return err
}

func (h *ClientConfig) AddScheduledSearchV2(ctx context.Context, client *humioapi.Client, hss *humiov1beta1.HumioScheduledSearch) error {
	err := validateSearchDomain(ctx, client, hss.Spec.ViewName)
	if err != nil {
		return fmt.Errorf("problem getting view for scheduled search: %w", err)
	}
	if err = h.ValidateActionsForScheduledSearchV2(ctx, client, hss); err != nil {
		return fmt.Errorf("could not get action id mapping: %w", err)
	}
	queryOwnershipType := humiographql.QueryOwnershipTypeOrganization

	var maxWaitTimeSeconds *int64
	if hss.Spec.QueryTimestampType != humiographql.QueryTimestampTypeEventtimestamp {
		maxWaitTimeSeconds = &hss.Spec.MaxWaitTimeSeconds
	}

	_, err = humiographql.CreateScheduledSearchV2(
		ctx,
		client,
		hss.Spec.ViewName,
		hss.Spec.Name,
		&hss.Spec.Description,
		hss.Spec.QueryString,
		hss.Spec.SearchIntervalSeconds,
		hss.Spec.SearchIntervalOffsetSeconds,
		maxWaitTimeSeconds,
		hss.Spec.QueryTimestampType,
		hss.Spec.Schedule,
		hss.Spec.TimeZone,
		hss.Spec.BackfillLimit,
		hss.Spec.Enabled,
		hss.Spec.Actions,
		helpers.EmptySliceIfNil(hss.Spec.Labels),
		queryOwnershipType,
	)
	return err
}

func (h *ClientConfig) GetScheduledSearch(ctx context.Context, client *humioapi.Client, hss *humiov1alpha1.HumioScheduledSearch) (*humiographql.ScheduledSearchDetails, error) {
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

func (h *ClientConfig) GetScheduledSearchV2(ctx context.Context, client *humioapi.Client, hss *humiov1beta1.HumioScheduledSearch) (*humiographql.ScheduledSearchDetailsV2, error) {
	err := validateSearchDomain(ctx, client, hss.Spec.ViewName)
	if err != nil {
		return nil, fmt.Errorf("problem getting view for scheduled search %s: %w", hss.Spec.Name, err)
	}

	var scheduledSearchId string
	respList, err := humiographql.ListScheduledSearchesV2(
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

	respGet, err := humiographql.GetScheduledSearchByIDV2(
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
	return &respGetScheduledSearch.ScheduledSearchDetailsV2, nil
}

func (h *ClientConfig) UpdateScheduledSearch(ctx context.Context, client *humioapi.Client, hss *humiov1alpha1.HumioScheduledSearch) error {
	err := validateSearchDomain(ctx, client, hss.Spec.ViewName)
	if err != nil {
		return fmt.Errorf("problem getting view for scheduled search: %w", err)
	}
	if err = h.ValidateActionsForScheduledSearch(ctx, client, hss); err != nil {
		return fmt.Errorf("could not get action id mapping: %w", err)
	}
	currentScheduledSearch, err := h.GetScheduledSearch(ctx, client, hss)
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
		helpers.EmptySliceIfNil(hss.Spec.Labels),
		&queryOwnershipType,
	)
	return err
}

func (h *ClientConfig) UpdateScheduledSearchV2(ctx context.Context, client *humioapi.Client, hss *humiov1beta1.HumioScheduledSearch) error {
	err := validateSearchDomain(ctx, client, hss.Spec.ViewName)
	if err != nil {
		return fmt.Errorf("problem getting view for scheduled search: %w", err)
	}
	if err = h.ValidateActionsForScheduledSearchV2(ctx, client, hss); err != nil {
		return fmt.Errorf("could not get action id mapping: %w", err)
	}
	currentScheduledSearch, err := h.GetScheduledSearchV2(ctx, client, hss)
	if err != nil {
		return fmt.Errorf("could not find scheduled search with name: %q", hss.Spec.Name)
	}

	var maxWaitTimeSeconds *int64
	if hss.Spec.QueryTimestampType != humiographql.QueryTimestampTypeEventtimestamp {
		maxWaitTimeSeconds = &hss.Spec.MaxWaitTimeSeconds
	}
	queryOwnershipType := humiographql.QueryOwnershipTypeOrganization
	_, err = humiographql.UpdateScheduledSearchV2(
		ctx,
		client,
		hss.Spec.ViewName,
		currentScheduledSearch.GetId(),
		hss.Spec.Name,
		&hss.Spec.Description,
		hss.Spec.QueryString,
		hss.Spec.SearchIntervalSeconds,
		hss.Spec.SearchIntervalOffsetSeconds,
		maxWaitTimeSeconds,
		hss.Spec.QueryTimestampType,
		hss.Spec.Schedule,
		hss.Spec.TimeZone,
		hss.Spec.BackfillLimit,
		hss.Spec.Enabled,
		hss.Spec.Actions,
		helpers.EmptySliceIfNil(hss.Spec.Labels),
		queryOwnershipType,
	)
	return err
}

func (h *ClientConfig) DeleteScheduledSearch(ctx context.Context, client *humioapi.Client, hss *humiov1alpha1.HumioScheduledSearch) error {
	currentScheduledSearch, err := h.GetScheduledSearch(ctx, client, hss)
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

func (h *ClientConfig) DeleteScheduledSearchV2(ctx context.Context, client *humioapi.Client, hss *humiov1beta1.HumioScheduledSearch) error {
	currentScheduledSearch, err := h.GetScheduledSearchV2(ctx, client, hss)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			return nil
		}
		return err
	}

	_, err = humiographql.DeleteScheduledSearchByIDV2(
		ctx,
		client,
		hss.Spec.ViewName,
		currentScheduledSearch.GetId(),
	)
	return err
}

func (h *ClientConfig) AddSavedQuery(ctx context.Context, client *humioapi.Client, hsq *humiov1alpha1.HumioSavedQuery, includeDescriptionAndLabels bool) error {
	// Note: includeDescriptionAndLabels parameter is kept for backward compatibility
	// but is not used since GraphQL schema doesn't support description/labels in versions < 1.200
	_, err := humiographql.CreateSavedQuery(
		ctx,
		client,
		hsq.Spec.ViewName,
		hsq.Spec.Name,
		hsq.Spec.QueryString,
	)
	return err
}

func (h *ClientConfig) GetSavedQuery(ctx context.Context, client *humioapi.Client, hsq *humiov1alpha1.HumioSavedQuery) (*humiographql.SavedQueryDetails, error) {
	resp, err := humiographql.ListSavedQueries(
		ctx,
		client,
		hsq.Spec.ViewName,
	)
	if err != nil {
		// If the view/repository doesn't exist, return a specific SearchDomainNotFound error
		// This allows the controller to handle non-existent views with better error messages
		if strings.Contains(err.Error(), "Entity Not Found") {
			return nil, humioapi.SearchDomainNotFound(hsq.Spec.ViewName)
		}
		return nil, err
	}

	// Find the saved query by name
	for _, sq := range resp.GetSearchDomain().GetSavedQueries() {
		if sq.GetName() == hsq.Spec.Name {
			return &sq.SavedQueryDetails, nil
		}
	}

	return nil, humioapi.SavedQueryNotFound(hsq.Spec.Name)
}

func (h *ClientConfig) UpdateSavedQuery(ctx context.Context, client *humioapi.Client, hsq *humiov1alpha1.HumioSavedQuery, includeDescriptionAndLabels bool) error {
	currentSavedQuery, err := h.GetSavedQuery(ctx, client, hsq)
	if err != nil {
		return err
	}

	// Note: includeDescriptionAndLabels parameter is kept for backward compatibility
	// but is not used since GraphQL schema doesn't support description/labels in versions < 1.200
	_, err = humiographql.UpdateSavedQuery(
		ctx,
		client,
		hsq.Spec.ViewName,
		currentSavedQuery.GetId(),
		helpers.StringPtr(hsq.Spec.Name),
		helpers.StringPtr(hsq.Spec.QueryString),
	)
	return err
}

func (h *ClientConfig) DeleteSavedQuery(ctx context.Context, client *humioapi.Client, hsq *humiov1alpha1.HumioSavedQuery) error {
	currentSavedQuery, err := h.GetSavedQuery(ctx, client, hsq)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			return nil
		}
		return err
	}

	_, err = humiographql.DeleteSavedQuery(
		ctx,
		client,
		hsq.Spec.ViewName,
		currentSavedQuery.GetId(),
	)
	return err
}

// V2 API methods with description and labels support (LogScale 1.200+)

func (h *ClientConfig) AddSavedQueryV2(ctx context.Context, client *humioapi.Client, hsq *humiov1alpha1.HumioSavedQuery) error {
	var description *string
	var labels []string

	if hsq.Spec.Description != "" {
		description = &hsq.Spec.Description
	}
	if len(hsq.Spec.Labels) > 0 {
		labels = hsq.Spec.Labels
	}

	_, err := humiographql.CreateSavedQueryV2(
		ctx,
		client,
		hsq.Spec.ViewName,
		hsq.Spec.Name,
		hsq.Spec.QueryString,
		description,
		labels,
	)
	return err
}

func (h *ClientConfig) GetSavedQueryV2(ctx context.Context, client *humioapi.Client, hsq *humiov1alpha1.HumioSavedQuery) (*humiographql.SavedQueryDetailsV2, error) {
	resp, err := humiographql.ListSavedQueriesV2(
		ctx,
		client,
		hsq.Spec.ViewName,
	)
	if err != nil {
		// If the view/repository doesn't exist, return a specific SearchDomainNotFound error
		if strings.Contains(err.Error(), "Entity Not Found") {
			return nil, humioapi.SearchDomainNotFound(hsq.Spec.ViewName)
		}
		return nil, err
	}

	// Find the saved query by name
	for _, sq := range resp.GetSearchDomain().GetSavedQueries() {
		if sq.GetName() == hsq.Spec.Name {
			return &sq.SavedQueryDetailsV2, nil
		}
	}

	return nil, humioapi.SavedQueryNotFound(hsq.Spec.Name)
}

func (h *ClientConfig) UpdateSavedQueryV2(ctx context.Context, client *humioapi.Client, hsq *humiov1alpha1.HumioSavedQuery) error {
	currentSavedQuery, err := h.GetSavedQueryV2(ctx, client, hsq)
	if err != nil {
		return err
	}

	var description *string
	var labels []string

	if hsq.Spec.Description != "" {
		description = &hsq.Spec.Description
	}
	if len(hsq.Spec.Labels) > 0 {
		labels = hsq.Spec.Labels
	}

	_, err = humiographql.UpdateSavedQueryV2(
		ctx,
		client,
		hsq.Spec.ViewName,
		currentSavedQuery.GetId(),
		helpers.StringPtr(hsq.Spec.Name),
		helpers.StringPtr(hsq.Spec.QueryString),
		description,
		labels,
	)
	return err
}

func (h *ClientConfig) getAndValidateAction(ctx context.Context, client *humioapi.Client, actionName string, viewName string) error {
	action := &humiov1alpha1.HumioAction{
		Spec: humiov1alpha1.HumioActionSpec{
			Name:     actionName,
			ViewName: viewName,
		},
	}

	_, err := h.GetAction(ctx, client, action)
	return err
}

func (h *ClientConfig) ValidateActionsForFilterAlert(ctx context.Context, client *humioapi.Client, hfa *humiov1alpha1.HumioFilterAlert) error {
	for _, actionNameForAlert := range hfa.Spec.Actions {
		if err := h.getAndValidateAction(ctx, client, actionNameForAlert, hfa.Spec.ViewName); err != nil {
			return fmt.Errorf("problem getting action for filter alert %s: %w", hfa.Spec.Name, err)
		}
	}
	return nil
}

func (h *ClientConfig) ValidateActionsForScheduledSearch(ctx context.Context, client *humioapi.Client, hss *humiov1alpha1.HumioScheduledSearch) error {
	for _, actionNameForScheduledSearch := range hss.Spec.Actions {
		if err := h.getAndValidateAction(ctx, client, actionNameForScheduledSearch, hss.Spec.ViewName); err != nil {
			return fmt.Errorf("problem getting action for scheduled search %s: %w", hss.Spec.Name, err)
		}
	}
	return nil
}

func (h *ClientConfig) ValidateActionsForScheduledSearchV2(ctx context.Context, client *humioapi.Client, hss *humiov1beta1.HumioScheduledSearch) error {
	for _, actionNameForScheduledSearch := range hss.Spec.Actions {
		if err := h.getAndValidateAction(ctx, client, actionNameForScheduledSearch, hss.Spec.ViewName); err != nil {
			return fmt.Errorf("problem getting action for scheduled search %s: %w", hss.Spec.Name, err)
		}
	}
	return nil
}

func (h *ClientConfig) AddAggregateAlert(ctx context.Context, client *humioapi.Client, haa *humiov1alpha1.HumioAggregateAlert) error {
	err := validateSearchDomain(ctx, client, haa.Spec.ViewName)
	if err != nil {
		return fmt.Errorf("problem getting view for action: %w", err)
	}
	if err = h.ValidateActionsForAggregateAlert(ctx, client, haa); err != nil {
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
		helpers.EmptySliceIfNil(haa.Spec.Labels),
		haa.Spec.Enabled,
		haa.Spec.ThrottleField,
		int64(haa.Spec.ThrottleTimeSeconds),
		humiographql.TriggerMode(haa.Spec.TriggerMode),
		humiographql.QueryTimestampType(haa.Spec.QueryTimestampType),
		humiographql.QueryOwnershipTypeOrganization,
	)
	return err
}

func (h *ClientConfig) GetAggregateAlert(ctx context.Context, client *humioapi.Client, haa *humiov1alpha1.HumioAggregateAlert) (*humiographql.AggregateAlertDetails, error) {
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

func (h *ClientConfig) UpdateAggregateAlert(ctx context.Context, client *humioapi.Client, haa *humiov1alpha1.HumioAggregateAlert) error {
	err := validateSearchDomain(ctx, client, haa.Spec.ViewName)
	if err != nil {
		return fmt.Errorf("problem getting view for action %s: %w", haa.Spec.Name, err)
	}
	if err = h.ValidateActionsForAggregateAlert(ctx, client, haa); err != nil {
		return fmt.Errorf("could not get action id mapping: %w", err)
	}
	currentAggregateAlert, err := h.GetAggregateAlert(ctx, client, haa)
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
		helpers.EmptySliceIfNil(haa.Spec.Labels),
		haa.Spec.Enabled,
		haa.Spec.ThrottleField,
		int64(haa.Spec.ThrottleTimeSeconds),
		humiographql.TriggerMode(haa.Spec.TriggerMode),
		humiographql.QueryTimestampType(haa.Spec.QueryTimestampType),
		humiographql.QueryOwnershipTypeOrganization,
	)
	return err
}

func (h *ClientConfig) DeleteAggregateAlert(ctx context.Context, client *humioapi.Client, haa *humiov1alpha1.HumioAggregateAlert) error {
	currentAggregateAlert, err := h.GetAggregateAlert(ctx, client, haa)
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

func (h *ClientConfig) ValidateActionsForAggregateAlert(ctx context.Context, client *humioapi.Client, haa *humiov1alpha1.HumioAggregateAlert) error {
	// validate action
	for _, actionNameForAlert := range haa.Spec.Actions {
		if err := h.getAndValidateAction(ctx, client, actionNameForAlert, haa.Spec.ViewName); err != nil {
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

func (h *ClientConfig) AddSystemPermissionRole(ctx context.Context, client *humioapi.Client, role *humiov1alpha1.HumioSystemPermissionRole) error {
	// convert strings to graphql types and call update
	systemPermissions := make([]humiographql.SystemPermission, len(role.Spec.Permissions))
	for idx := range role.Spec.Permissions {
		systemPermissions[idx] = humiographql.SystemPermission(role.Spec.Permissions[idx])
	}
	_, err := humiographql.CreateRole(ctx, client, role.Spec.Name, []humiographql.Permission{}, nil, systemPermissions)
	return err
}

func (h *ClientConfig) GetSystemPermissionRole(ctx context.Context, client *humioapi.Client, role *humiov1alpha1.HumioSystemPermissionRole) (*humiographql.RoleDetails, error) {
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

func (h *ClientConfig) UpdateSystemPermissionRole(ctx context.Context, client *humioapi.Client, role *humiov1alpha1.HumioSystemPermissionRole) error {
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

			if !equalSlices(respRole.GetSystemPermissions(), systemPermissions) {
				if _, err := humiographql.UpdateRole(ctx, client, respRole.GetId(), respRole.GetDisplayName(), []humiographql.Permission{}, nil, systemPermissions); err != nil {
					return err
				}
			}

			// Fetch list of groups that should have the role
			expectedGroupNames := role.Spec.RoleAssignmentGroupNames

			// Unassign role from groups that should not have it
			currentGroupNames, unassignErr := h.getCurrentSystemPermissionGroupNamesAndUnassignRoleFromUndesiredGroups(ctx, client, respRole, expectedGroupNames)
			if unassignErr != nil {
				return unassignErr
			}

			// Assign the role to groups that should have it
			if assignErr := h.assignSystemPermissionRoleToGroups(ctx, client, respRole.GetId(), currentGroupNames, expectedGroupNames); assignErr != nil {
				return assignErr
			}

			return nil
		}
	}

	return humioapi.SystemPermissionRoleNotFound(role.Spec.Name)
}

func (h *ClientConfig) getCurrentSystemPermissionGroupNamesAndUnassignRoleFromUndesiredGroups(ctx context.Context, client *humioapi.Client, respRole humiographql.ListRolesRolesRole, expectedGroupNames []string) ([]string, error) {
	if len(respRole.GetSystemPermissions()) == 0 {
		return nil, fmt.Errorf("role name=%q id=%q is not a system permission role", respRole.GetDisplayName(), respRole.GetId())
	}

	currentGroupNames := []string{}
	for _, currentGroup := range respRole.GetGroups() {
		if slices.Contains(expectedGroupNames, currentGroup.GetDisplayName()) {
			// Nothing to do, group has the role and should have it
			currentGroupNames = append(currentGroupNames, currentGroup.GetDisplayName())
			continue
		}

		// Unassign role from groups that should not have it
		if _, err := humiographql.UnassignSystemPermissionRoleFromGroup(ctx, client, respRole.GetId(), currentGroup.GetId()); err != nil {
			return nil, err
		}
	}

	return currentGroupNames, nil
}

func (h *ClientConfig) assignSystemPermissionRoleToGroups(ctx context.Context, client *humioapi.Client, roleId string, currentGroupNames, expectedGroupNames []string) error {
	for _, expectedGroup := range expectedGroupNames {
		if slices.Contains(currentGroupNames, expectedGroup) {
			// Nothing to do, group already has the role
			continue
		}
		// Look up group ID
		currentGroup, getGroupErr := humiographql.GetGroupByDisplayName(ctx, client, expectedGroup)
		if getGroupErr != nil {
			return getGroupErr
		}
		if currentGroup == nil {
			return fmt.Errorf("unable to fetch group details for group %q when updating role assignment", expectedGroup)
		}
		respCurrentGroup := currentGroup.GetGroupByDisplayName()

		// Assign
		if _, err := humiographql.AssignSystemPermissionRoleToGroup(ctx, client, roleId, respCurrentGroup.GetId()); err != nil {
			return err
		}
	}

	return nil
}

func (h *ClientConfig) DeleteSystemPermissionRole(ctx context.Context, client *humioapi.Client, role *humiov1alpha1.HumioSystemPermissionRole) error {
	resp, listErr := humiographql.ListRoles(ctx, client)
	if listErr != nil {
		return listErr
	}
	if resp == nil {
		return fmt.Errorf("unable to fetch list of roles")
	}

	respListRolesGetRoles := resp.GetRoles()
	for i := range respListRolesGetRoles {
		roleDetails := respListRolesGetRoles[i]
		if roleDetails.GetDisplayName() == role.Spec.Name && len(roleDetails.GetSystemPermissions()) > 0 {
			listGroups := roleDetails.GetGroups()
			for idx := range listGroups {
				if _, unassignErr := humiographql.UnassignSystemPermissionRoleFromGroup(ctx, client, roleDetails.GetId(), listGroups[idx].GetId()); unassignErr != nil {
					return fmt.Errorf("got error unassigning role from group: %w", unassignErr)
				}
			}

			_, err := humiographql.DeleteRoleByID(ctx, client, roleDetails.GetId())
			return err
		}
	}

	return nil
}

func (h *ClientConfig) AddUser(ctx context.Context, client *humioapi.Client, hu *humiov1alpha1.HumioUser) error {
	_, err := humiographql.AddUser(
		ctx,
		client,
		hu.Spec.UserName,
		hu.Spec.IsRoot,
	)
	return err
}

func (h *ClientConfig) GetUser(ctx context.Context, client *humioapi.Client, hu *humiov1alpha1.HumioUser) (*humiographql.UserDetails, error) {
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

func (h *ClientConfig) UpdateUser(ctx context.Context, client *humioapi.Client, hu *humiov1alpha1.HumioUser) error {
	_, err := humiographql.UpdateUser(
		ctx,
		client,
		hu.Spec.UserName,
		hu.Spec.IsRoot,
	)
	return err
}

func (h *ClientConfig) DeleteUser(ctx context.Context, client *humioapi.Client, hu *humiov1alpha1.HumioUser) error {
	_, err := humiographql.RemoveUser(
		ctx,
		client,
		hu.Spec.UserName,
	)
	return err
}

func (h *ClientConfig) AddOrganizationPermissionRole(ctx context.Context, client *humioapi.Client, role *humiov1alpha1.HumioOrganizationPermissionRole) error {
	// convert strings to graphql types and call update
	organizationPermissions := make([]humiographql.OrganizationPermission, len(role.Spec.Permissions))
	for idx := range role.Spec.Permissions {
		organizationPermissions[idx] = humiographql.OrganizationPermission(role.Spec.Permissions[idx])
	}
	_, err := humiographql.CreateRole(ctx, client, role.Spec.Name, []humiographql.Permission{}, organizationPermissions, nil)
	return err
}

func (h *ClientConfig) GetOrganizationPermissionRole(ctx context.Context, client *humioapi.Client, role *humiov1alpha1.HumioOrganizationPermissionRole) (*humiographql.RoleDetails, error) {
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

func (h *ClientConfig) UpdateOrganizationPermissionRole(ctx context.Context, client *humioapi.Client, role *humiov1alpha1.HumioOrganizationPermissionRole) error {
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

			if !equalSlices(respRole.GetOrganizationPermissions(), organizationPermissions) {
				if _, err := humiographql.UpdateRole(ctx, client, respRole.GetId(), respRole.GetDisplayName(), []humiographql.Permission{}, organizationPermissions, nil); err != nil {
					return err
				}
			}

			// Fetch list of groups that should have the role
			expectedGroupNames := role.Spec.RoleAssignmentGroupNames

			// Unassign role from groups that should not have it
			currentGroupNames, unassignErr := h.getCurrentOrganizationPermissionGroupNamesAndUnassignRoleFromUndesiredGroups(ctx, client, respRole, expectedGroupNames)
			if unassignErr != nil {
				return unassignErr
			}

			// Assign the role to groups that should have it
			if err := h.assignOrganizationPermissionRoleToGroups(ctx, client, respRole.GetId(), currentGroupNames, expectedGroupNames); err != nil {
				return err
			}

			return nil
		}
	}
	return humioapi.OrganizationPermissionRoleNotFound(role.Spec.Name)
}

func (h *ClientConfig) getCurrentOrganizationPermissionGroupNamesAndUnassignRoleFromUndesiredGroups(ctx context.Context, client *humioapi.Client, respRole humiographql.ListRolesRolesRole, expectedGroupNames []string) ([]string, error) {
	if len(respRole.GetOrganizationPermissions()) == 0 {
		return nil, fmt.Errorf("role name=%q id=%q is not an organization permission role", respRole.GetDisplayName(), respRole.GetId())
	}

	currentGroupNames := []string{}
	for _, currentGroup := range respRole.GetGroups() {
		if slices.Contains(expectedGroupNames, currentGroup.GetDisplayName()) {
			// Nothing to do, group has the role and should have it
			currentGroupNames = append(currentGroupNames, currentGroup.GetDisplayName())
			continue
		}

		// Unassign role from groups that should not have it
		if _, err := humiographql.UnassignOrganizationPermissionRoleFromGroup(ctx, client, respRole.GetId(), currentGroup.GetId()); err != nil {
			return nil, err
		}
	}

	return currentGroupNames, nil
}

func (h *ClientConfig) assignOrganizationPermissionRoleToGroups(ctx context.Context, client *humioapi.Client, roleId string, currentGroupNames, expectedGroupNames []string) error {
	for _, expectedGroup := range expectedGroupNames {
		if slices.Contains(currentGroupNames, expectedGroup) {
			// Nothing to do, group already has the role
			continue
		}
		// Look up group ID
		currentGroup, getGroupErr := humiographql.GetGroupByDisplayName(ctx, client, expectedGroup)
		if getGroupErr != nil {
			return getGroupErr
		}
		if currentGroup == nil {
			return fmt.Errorf("unable to fetch group details for group %q when updating role assignment", expectedGroup)
		}
		respCurrentGroup := currentGroup.GetGroupByDisplayName()

		// Assign
		if _, err := humiographql.AssignOrganizationPermissionRoleToGroup(ctx, client, roleId, respCurrentGroup.GetId()); err != nil {
			return err
		}
	}

	return nil
}

func (h *ClientConfig) DeleteOrganizationPermissionRole(ctx context.Context, client *humioapi.Client, role *humiov1alpha1.HumioOrganizationPermissionRole) error {
	resp, listErr := humiographql.ListRoles(ctx, client)
	if listErr != nil {
		return listErr
	}
	if resp == nil {
		return fmt.Errorf("unable to fetch list of roles")
	}
	respListRolesGetRoles := resp.GetRoles()
	for i := range respListRolesGetRoles {
		roleDetails := respListRolesGetRoles[i]
		if roleDetails.GetDisplayName() == role.Spec.Name && len(roleDetails.GetOrganizationPermissions()) > 0 {
			listGroups := roleDetails.GetGroups()
			for idx := range listGroups {
				if _, unassignErr := humiographql.UnassignOrganizationPermissionRoleFromGroup(ctx, client, roleDetails.GetId(), listGroups[idx].GetId()); unassignErr != nil {
					return fmt.Errorf("got error unassigning role from group: %w", unassignErr)
				}
			}

			_, err := humiographql.DeleteRoleByID(ctx, client, roleDetails.GetId())
			return err
		}
	}
	return nil
}

func (h *ClientConfig) AddViewPermissionRole(ctx context.Context, client *humioapi.Client, role *humiov1alpha1.HumioViewPermissionRole) error {
	// convert strings to graphql types and call update
	viewPermissions := make([]humiographql.Permission, len(role.Spec.Permissions))
	for idx := range role.Spec.Permissions {
		viewPermissions[idx] = humiographql.Permission(role.Spec.Permissions[idx])
	}
	_, err := humiographql.CreateRole(ctx, client, role.Spec.Name, viewPermissions, nil, nil)
	return err
}

func (h *ClientConfig) GetViewPermissionRole(ctx context.Context, client *humioapi.Client, role *humiov1alpha1.HumioViewPermissionRole) (*humiographql.RoleDetails, error) {
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
		if respRole.GetDisplayName() == role.Spec.Name && len(respRole.GetViewPermissions()) > 0 {
			return &respGetRoles[i].RoleDetails, err
		}
	}

	return nil, humioapi.ViewPermissionRoleNotFound(role.Spec.Name)
}

func (h *ClientConfig) UpdateViewPermissionRole(ctx context.Context, client *humioapi.Client, role *humiov1alpha1.HumioViewPermissionRole) error {
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

		// pick the role with the correct name and which is a role with view permissions
		if respRole.GetDisplayName() == role.Spec.Name && len(respRole.GetViewPermissions()) > 0 {

			// convert strings to graphql types and call update
			viewPermissions := make([]humiographql.Permission, len(role.Spec.Permissions))
			for idx := range role.Spec.Permissions {
				viewPermissions[idx] = humiographql.Permission(role.Spec.Permissions[idx])
			}

			currentAssignedRole := respGetRoles[i]

			if !equalSlices(respRole.GetViewPermissions(), viewPermissions) {
				if _, err := humiographql.UpdateRole(ctx, client, currentAssignedRole.GetId(), currentAssignedRole.GetDisplayName(), viewPermissions, nil, nil); err != nil {
					return err
				}
			}

			// Fetch list of desired/expected role assignments
			expectedRoleAssignments := role.Spec.RoleAssignments

			// Fetch list of groups that have the role and unassign any that should not have it
			currentGroupRoleAssignments := []humiov1alpha1.HumioViewPermissionRoleAssignment{}
			for _, currentGroupAssignmentInfo := range respRole.GetGroups() {
				for _, currentRoleAssignmentForGroup := range currentGroupAssignmentInfo.GetRoles() {
					respSearchDomain := currentRoleAssignmentForGroup.GetSearchDomain()
					if respSearchDomain == nil {
						continue
					}
					currentGroupRoleAssignments = append(currentGroupRoleAssignments,
						humiov1alpha1.HumioViewPermissionRoleAssignment{
							RepoOrViewName: respSearchDomain.GetName(),
							GroupName:      currentGroupAssignmentInfo.GetDisplayName(),
						},
					)

					currentRoleAssignment := humiov1alpha1.HumioViewPermissionRoleAssignment{
						RepoOrViewName: respSearchDomain.GetName(),
						GroupName:      currentGroupAssignmentInfo.GetDisplayName(),
					}
					if slices.Contains(expectedRoleAssignments, currentRoleAssignment) {
						// Nothing to do, group already has the role
						continue
					}

					// Unassign
					if _, unassignErr := humiographql.UnassignViewPermissionRoleFromGroupForView(ctx, client, currentAssignedRole.GetId(), currentGroupAssignmentInfo.GetId(), respSearchDomain.GetId()); unassignErr != nil {
						return unassignErr
					}
				}
			}

			// Assign the role to the groups that should have it
			for _, expectedRoleAssignment := range expectedRoleAssignments {
				if slices.Contains(currentGroupRoleAssignments, expectedRoleAssignment) {
					// Nothing to do, group has the role and should have it
					continue
				}

				// Look up group ID
				currentGroup, getGroupErr := humiographql.GetGroupByDisplayName(ctx, client, expectedRoleAssignment.GroupName)
				if getGroupErr != nil {
					return getGroupErr
				}
				if currentGroup == nil {
					return fmt.Errorf("unable to fetch group details for group %q when updating role assignment", expectedRoleAssignment.GroupName)
				}
				respCurrentGroup := currentGroup.GetGroupByDisplayName()

				// Look up view id
				currentSearchDomain, getSearchDomainErr := humiographql.GetSearchDomain(ctx, client, expectedRoleAssignment.RepoOrViewName)
				if getSearchDomainErr != nil {
					return getSearchDomainErr
				}
				if currentSearchDomain == nil {
					return fmt.Errorf("unable to fetch search domain details for search domain %q when updating role assignment", expectedRoleAssignment.RepoOrViewName)
				}
				respCurrentSearchDomain := currentSearchDomain.GetSearchDomain()

				// Assign
				if _, assignErr := humiographql.AssignViewPermissionRoleToGroupForView(ctx, client, currentAssignedRole.GetId(), respCurrentGroup.GetId(), respCurrentSearchDomain.GetId()); assignErr != nil {
					return assignErr
				}
			}

			return nil
		}
	}
	return humioapi.ViewPermissionRoleNotFound(role.Spec.Name)
}

func (h *ClientConfig) DeleteViewPermissionRole(ctx context.Context, client *humioapi.Client, role *humiov1alpha1.HumioViewPermissionRole) error {
	resp, listErr := humiographql.ListRoles(ctx, client)
	if listErr != nil {
		return listErr
	}
	if resp == nil {
		return fmt.Errorf("unable to fetch list of roles")
	}
	respListRolesGetRoles := resp.GetRoles()
	for i := range respListRolesGetRoles {
		respListRolesRoleDetails := respListRolesGetRoles[i]
		if respListRolesRoleDetails.GetDisplayName() == role.Spec.Name && len(respListRolesRoleDetails.GetViewPermissions()) > 0 {
			if err := h.unassignViewPermissionRoleFromAllGroups(ctx, client, respListRolesRoleDetails.RoleDetails); err != nil {
				return err
			}

			_, err := humiographql.DeleteRoleByID(ctx, client, respListRolesRoleDetails.GetId())
			return err
		}
	}
	return nil
}

func (h *ClientConfig) unassignViewPermissionRoleFromAllGroups(ctx context.Context, client *humioapi.Client, roleDetails humiographql.RoleDetails) error {
	listGroups := roleDetails.GetGroups()
	for idx := range listGroups {
		groupDetails := listGroups[idx]
		for jdx := range groupDetails.GetRoles() {
			viewRoleDetails := groupDetails.GetRoles()[jdx]
			viewRoleDetailsSearchDomain := viewRoleDetails.GetSearchDomain()
			if viewRoleDetailsSearchDomain == nil {
				return fmt.Errorf("unable to fetch details when updating role assignment")
			}
			if _, unassignErr := humiographql.UnassignViewPermissionRoleFromGroupForView(ctx, client, roleDetails.GetId(), groupDetails.GetId(), viewRoleDetailsSearchDomain.GetId()); unassignErr != nil {
				return fmt.Errorf("got error unassigning role from group: %w", unassignErr)
			}
		}
	}
	return nil
}

func (h *ClientConfig) AddIPFilter(ctx context.Context, client *humioapi.Client, ipFilter *humiov1alpha1.HumioIPFilter) (*humiographql.IPFilterDetails, error) {
	// ipFilter.Spec.IPFilter is a list of FirewallRule structs so we need to convert to string for graphql
	filter := helpers.FirewallRulesToString(ipFilter.Spec.IPFilter, "\n")
	ipFilterResp, err := humiographql.CreateIPFilter(
		ctx,
		client,
		ipFilter.Spec.Name,
		filter,
	)
	if err != nil {
		return nil, err
	}
	value := ipFilterResp.GetCreateIPFilter().IPFilterDetails
	return &value, err
}

func (h *ClientConfig) GetIPFilter(ctx context.Context, client *humioapi.Client, ipFilter *humiov1alpha1.HumioIPFilter) (*humiographql.IPFilterDetails, error) {
	// there is no graphql method to get a single IPFilter so we fetch all
	ipFiltersResp, err := humiographql.GetIPFilters(ctx, client)
	if err != nil {
		return nil, err
	}

	for _, filter := range ipFiltersResp.GetIpFilters() {
		// if we have a ipFilter.Status.ID set we do the match on that first
		if ipFilter.Status.ID != "" {
			if filter.GetId() == ipFilter.Status.ID {
				return &filter.IPFilterDetails, nil
			}
		} else {
			// name is not unique for ipFilters so we use it as a fallback
			if filter.GetName() == ipFilter.Spec.Name {
				return &filter.IPFilterDetails, nil
			}
		}
	}
	// if not match we return a not found error
	return nil, humioapi.IPFilterNotFound(ipFilter.Spec.Name)
}

func (h *ClientConfig) UpdateIPFilter(ctx context.Context, client *humioapi.Client, ipFilter *humiov1alpha1.HumioIPFilter) error {
	filter := helpers.FirewallRulesToString(ipFilter.Spec.IPFilter, "\n")
	_, err := humiographql.UpdateIPFilter(
		ctx,
		client,
		ipFilter.Status.ID,
		&ipFilter.Spec.Name,
		&filter,
	)
	return err
}

func (h *ClientConfig) DeleteIPFilter(ctx context.Context, client *humioapi.Client, ipFilter *humiov1alpha1.HumioIPFilter) error {
	_, err := humiographql.DeleteIPFilter(
		ctx,
		client,
		ipFilter.Status.ID,
	)
	return err
}

func (h *ClientConfig) CreateViewToken(ctx context.Context, client *humioapi.Client, viewToken *humiov1alpha1.HumioViewToken, ipFilterId string, viewIds []string, permissions []humiographql.Permission) (string, string, error) {
	var expireAtPtr *int64
	var ipFilterPtr *string
	// cleanup expireAt
	if viewToken.Spec.ExpiresAt != nil {
		timestamp := viewToken.Spec.ExpiresAt.UnixMilli()
		expireAtPtr = &timestamp
	}
	// cleanup ipFilter
	if ipFilterId != "" {
		ipFilterPtr = &ipFilterId
	}

	viewTokenCreateResp, err := humiographql.CreateViewToken(
		ctx,
		client,
		viewToken.Spec.Name,
		ipFilterPtr,
		expireAtPtr,
		viewIds,
		permissions,
	)
	if err != nil {
		return "", "", err
	}
	token := viewTokenCreateResp.CreateViewPermissionsToken
	tokenId, secret := parseTokenRotateResult(token)
	return tokenId, secret, nil
}

func (h *ClientConfig) GetViewToken(ctx context.Context, client *humioapi.Client, viewToken *humiov1alpha1.HumioViewToken) (*humiographql.ViewTokenDetailsViewPermissionsToken, error) {
	// we return early if the id is not set on the viewToken, it means it wasn't created / doesn't exists / we plan to delete it
	if viewToken.Status.HumioID == "" {
		h.logger.Info("missing ID for ViewToken.Status.ID", "id", viewToken.Status.HumioID)
		return nil, humioapi.ViewTokenNotFound(viewToken.Spec.Name)
	}
	viewTokenResp, err := humiographql.GetViewToken(ctx, client, viewToken.Status.HumioID)
	if err != nil {
		return nil, err
	}
	if len(viewTokenResp.Tokens.Results) == 0 {
		h.logger.Info("unexpected scenario, query return 0 results for ViewToken ID", "id", viewToken.Status.HumioID)
		return nil, humioapi.ViewTokenNotFound(viewToken.Spec.Name)
	}
	data := viewTokenResp.Tokens.Results[0].(*humiographql.GetViewTokenTokensTokenQueryResultSetResultsViewPermissionsToken)
	token := data.ViewTokenDetailsViewPermissionsToken

	return &token, nil
}

func (h *ClientConfig) DeleteViewToken(ctx context.Context, client *humioapi.Client, viewToken *humiov1alpha1.HumioViewToken) error {
	_, err := humiographql.DeleteToken(
		ctx,
		client,
		viewToken.Status.HumioID,
	)
	return err
}

func (h *ClientConfig) RotateViewToken(ctx context.Context, client *humioapi.Client, viewToken *humiov1alpha1.HumioViewToken) (string, string, error) {
	result, err := humiographql.RotateToken(
		ctx,
		client,
		viewToken.Status.HumioID,
	)
	tokenId, secret := parseTokenRotateResult(result.RotateToken)
	return tokenId, secret, err
}

func (h *ClientConfig) UpdateViewToken(ctx context.Context, client *humioapi.Client, hvt *humiov1alpha1.HumioViewToken, permissions []humiographql.Permission) error {
	_, err := humiographql.UpdateViewToken(
		ctx,
		client,
		hvt.Status.HumioID,
		permissions,
	)
	return err
}

// EnableTokenUpdatePermissions turns ON the ability to update token permissions (disabled by default)
func (h *ClientConfig) EnableTokenUpdatePermissionsForTests(ctx context.Context, client *humioapi.Client) error {
	_, err := humiographql.UpdateTokenSecurityPolicies(ctx, client, true, true, true, true, true, true, true)
	return err
}

func (h *ClientConfig) CreateSystemToken(ctx context.Context, client *humioapi.Client, systemToken *humiov1alpha1.HumioSystemToken, ipFilterId string, permissions []humiographql.SystemPermission) (string, string, error) {
	var expireAtPtr *int64
	var ipFilterPtr *string
	// cleanup expireAt
	if systemToken.Spec.ExpiresAt != nil {
		timestamp := systemToken.Spec.ExpiresAt.UnixMilli()
		expireAtPtr = &timestamp
	}
	// cleanup ipFilter
	if ipFilterId != "" {
		ipFilterPtr = &ipFilterId
	}

	systemTokenCreateResp, err := humiographql.CreateSystemToken(
		ctx,
		client,
		systemToken.Spec.Name,
		ipFilterPtr,
		expireAtPtr,
		permissions,
	)
	if err != nil {
		return "", "", err
	}
	token := systemTokenCreateResp.CreateSystemPermissionsToken
	tokenId, secret := parseTokenRotateResult(token)
	return tokenId, secret, nil
}

func (h *ClientConfig) GetSystemToken(ctx context.Context, client *humioapi.Client, systemToken *humiov1alpha1.HumioSystemToken) (*humiographql.SystemTokenDetailsSystemPermissionsToken, error) {
	// we return early if the id is not set on the viewToken, it means it wasn't created / doesn't exists / we plan to delete it
	if systemToken.Status.HumioID == "" {
		h.logger.Info("missing ID for SystemToken.Status.ID", "id", systemToken.Status.HumioID)
		return nil, humioapi.SystemTokenNotFound(systemToken.Spec.Name)
	}
	systemTokenResp, err := humiographql.GetSystemToken(ctx, client, systemToken.Status.HumioID)
	if err != nil {
		return nil, err
	}
	if len(systemTokenResp.Tokens.Results) == 0 {
		h.logger.Info("unexpected scenario, query return 0 results for SystemToken ID", "id", systemToken.Status.HumioID)
		return nil, humioapi.SystemTokenNotFound(systemToken.Spec.Name)
	}
	data := systemTokenResp.Tokens.Results[0].(*humiographql.GetSystemTokenTokensTokenQueryResultSetResultsSystemPermissionsToken)
	token := data.SystemTokenDetailsSystemPermissionsToken

	return &token, nil
}

func (h *ClientConfig) DeleteSystemToken(ctx context.Context, client *humioapi.Client, systemToken *humiov1alpha1.HumioSystemToken) error {
	_, err := humiographql.DeleteToken(
		ctx,
		client,
		systemToken.Status.HumioID,
	)
	return err
}

func (h *ClientConfig) RotateSystemToken(ctx context.Context, client *humioapi.Client, systemToken *humiov1alpha1.HumioSystemToken) (string, string, error) {
	result, err := humiographql.RotateToken(
		ctx,
		client,
		systemToken.Status.HumioID,
	)
	tokenId, secret := parseTokenRotateResult(result.RotateToken)
	return tokenId, secret, err
}

func (h *ClientConfig) UpdateSystemToken(ctx context.Context, client *humioapi.Client, hvt *humiov1alpha1.HumioSystemToken, permissions []humiographql.SystemPermission) error {
	_, err := humiographql.UpdateSystemToken(
		ctx,
		client,
		hvt.Status.HumioID,
		permissions,
	)
	return err
}

func (h *ClientConfig) CreateOrganizationToken(ctx context.Context, client *humioapi.Client, orgToken *humiov1alpha1.HumioOrganizationToken, ipFilterId string, permissions []humiographql.OrganizationPermission) (string, string, error) {
	var expireAtPtr *int64
	var ipFilterPtr *string
	// cleanup expireAt
	if orgToken.Spec.ExpiresAt != nil {
		timestamp := orgToken.Spec.ExpiresAt.UnixMilli()
		expireAtPtr = &timestamp
	}
	// cleanup ipFilter
	if ipFilterId != "" {
		ipFilterPtr = &ipFilterId
	}

	orgTokenCreateResp, err := humiographql.CreateOrganizationToken(
		ctx,
		client,
		orgToken.Spec.Name,
		ipFilterPtr,
		expireAtPtr,
		permissions,
	)
	if err != nil {
		return "", "", err
	}
	token := orgTokenCreateResp.CreateOrganizationPermissionsToken
	tokenId, secret := parseTokenRotateResult(token)
	return tokenId, secret, nil
}

func (h *ClientConfig) GetOrganizationToken(ctx context.Context, client *humioapi.Client, orgToken *humiov1alpha1.HumioOrganizationToken) (*humiographql.OrganizationTokenDetailsOrganizationPermissionsToken, error) {
	// we return early if the id is not set on the OrganizationToken, it means it wasn't created / doesn't exists / we plan to delete it
	if orgToken.Status.HumioID == "" {
		h.logger.Info("unexpected scenario, missing ID for OrganizationToken.Status.ID", "id", orgToken.Status.HumioID)
		return nil, humioapi.OrganizationTokenNotFound(orgToken.Spec.Name)
	}
	orgTokenResp, err := humiographql.GetOrganizationToken(ctx, client, orgToken.Status.HumioID)
	if err != nil {
		return nil, err
	}
	if len(orgTokenResp.Tokens.Results) == 0 {
		h.logger.Info("unexpected scenario, query return 0 results for OrganizationToken ID", "id", orgToken.Status.HumioID)
		return nil, humioapi.OrganizationTokenNotFound(orgToken.Spec.Name)
	}
	data := orgTokenResp.Tokens.Results[0].(*humiographql.GetOrganizationTokenTokensTokenQueryResultSetResultsOrganizationPermissionsToken)
	token := data.OrganizationTokenDetailsOrganizationPermissionsToken

	return &token, nil
}

func (h *ClientConfig) DeleteOrganizationToken(ctx context.Context, client *humioapi.Client, orgToken *humiov1alpha1.HumioOrganizationToken) error {
	_, err := humiographql.DeleteToken(
		ctx,
		client,
		orgToken.Status.HumioID,
	)
	return err
}

func (h *ClientConfig) RotateOrganizationToken(ctx context.Context, client *humioapi.Client, orgToken *humiov1alpha1.HumioOrganizationToken) (string, string, error) {
	result, err := humiographql.RotateToken(
		ctx,
		client,
		orgToken.Status.HumioID,
	)
	tokenId, secret := parseTokenRotateResult(result.RotateToken)
	return tokenId, secret, err
}

func (h *ClientConfig) UpdateOrganizationToken(ctx context.Context, client *humioapi.Client, hot *humiov1alpha1.HumioOrganizationToken, permissions []humiographql.OrganizationPermission) error {
	_, err := humiographql.UpdateOrganizationToken(
		ctx,
		client,
		hot.Status.HumioID,
		permissions,
	)
	return err
}

// uploadPackageZip handles the common logic for uploading a zip file to Humio REST API endpoints
func (h *ClientConfig) uploadPackageZip(ctx context.Context, client *humioapi.Client, zipFilePath string, path string) (*http.Response, error) {
	// Open the file
	file, err := os.Open(zipFilePath) // #nosec G304 - zipFilePath is validated and constructed internally by package download process
	if err != nil {
		return nil, fmt.Errorf("failed to open zip file: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			h.logger.Error(err, "could not close upload", "filePath", zipFilePath)
		}
	}()

	// Create a pipe for streaming multipart data
	pipeReader, pipeWriter := io.Pipe()
	writer := multipart.NewWriter(pipeWriter)

	// Start a goroutine to write the multipart data
	go func() {
		defer func() {
			if err := pipeWriter.Close(); err != nil {
				h.logger.Error(err, "could not close pipe writer")
			}
		}()
		defer func() {
			if err := writer.Close(); err != nil {
				h.logger.Error(err, "could not close multipart writer")
			}
		}()

		// Add the file field
		fileWriter, err := writer.CreateFormFile("file", filepath.Base(zipFilePath))
		if err != nil {
			pipeWriter.CloseWithError(fmt.Errorf("failed to create form file: %w", err))
			return
		}
		// Copy file content
		_, err = io.Copy(fileWriter, file)
		if err != nil {
			pipeWriter.CloseWithError(fmt.Errorf("failed to copy file content: %w", err))
			return
		}
	}()

	// Set the correct content type with the boundary from the writer
	contentType := writer.FormDataContentType()
	// Use the API client's HTTPRequestContext method to get proper Auth/TLS configuration
	resp, err := client.HTTPRequestContext(ctx, "POST", path, pipeReader, contentType)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}

	return resp, nil
}

func (h *ClientConfig) InstallPackageFromZip(ctx context.Context, client *humioapi.Client, hp *humiov1alpha1.HumioPackage, zipFilePath string, viewName string) error {
	overwrite := "false"
	if hp.Spec.ConflictPolicy == PackagePolicyOverwrite {
		overwrite = "true"
	}
	path := fmt.Sprintf("/api/v1/packages/install?view=%s&overwrite=%s&queryOwnershipType=%s",
		viewName, overwrite, humiographql.QueryOwnershipTypeOrganization)

	resp, err := h.uploadPackageZip(ctx, client, zipFilePath, path)
	if err != nil {
		return err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			h.logger.Error(err, "could not close response body")
		}
	}()

	// Check response status code
	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == 409 { // same version conflict
			return fmt.Errorf("conflict detected: %s", string(bodyBytes))
		}
		return fmt.Errorf("HTTP error %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}

func (h *ClientConfig) AnalyzePackageFromZip(ctx context.Context, client *humioapi.Client, zipFilePath string, viewName string) (any, error) {
	path := fmt.Sprintf("/api/v1/packages/analyze?view=%s", viewName)

	resp, err := h.uploadPackageZip(ctx, client, zipFilePath, path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			h.logger.Error(err, "could not close response body")
		}
	}()

	jsonData, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP error %d: %s", resp.StatusCode, string(jsonData))
	}

	var responseType struct {
		ResponseType string `json:"responseType"`
	}
	if err := json.Unmarshal(jsonData, &responseType); err != nil {
		return nil, err
	}

	switch responseType.ResponseType {
	case PackageResponseTypeAnalysisResult:
		var result PackageAnalysisResultJson
		err := json.Unmarshal(jsonData, &result)
		return result, err
	case PackageResponseTypeErrorReport:
		var result PackageErrorReportJson
		err := json.Unmarshal(jsonData, &result)
		return result, err
	default:
		return nil, fmt.Errorf("unexpected response from analyze endpoint")
	}
}

func (h *ClientConfig) UninstallPackage(ctx context.Context, client *humioapi.Client, hp *humiov1alpha1.HumioPackage, viewName string) (bool, error) {
	// to uninstall we need a valid HumioPackageName in Status
	if hp.Status.HumioPackageName == "" {
		return false, fmt.Errorf("unexpected scenario, missing HumioPackageName, received: %s", hp.Status.HumioPackageName)
	}
	removed, err := humiographql.UninstallPackage(ctx, client, hp.Status.HumioPackageName, viewName)
	if err != nil {
		return false, err
	}
	response := removed.GetUninstallPackage()

	return response.Result, nil
}

func (h *ClientConfig) CheckPackage(ctx context.Context, client *humioapi.Client, hp *humiov1alpha1.HumioPackage, viewName string) (*humiographql.PackageDetails, error) {
	packageId := fmt.Sprintf("%s@%s", hp.Spec.PackageName, hp.Spec.PackageVersion)
	packageData, err := humiographql.PackageInstallation(ctx, client, packageId, viewName)
	if err != nil {
		return nil, fmt.Errorf("error while checking package installation: %s", err)
	}
	if packageData.InstalledPackage == nil {
		return nil, nil
	}
	return &packageData.InstalledPackage.Package.PackageDetails, nil
}

func equalSlices[T comparable](a, b []T) bool {
	if len(a) != len(b) {
		return false
	}

	// Use a single map for comparing occurrences of each element in the two slices.
	freq := make(map[T]int)

	// Counts occurrences in slice a (positive)
	for _, val := range a {
		freq[val]++
	}

	// Subtracts occurrences in slice b
	for _, val := range b {
		freq[val]--
		// If the count goes negative, slices aren't equal, fails fast
		if freq[val] < 0 {
			return false
		}
	}

	// Checks if all frequencies are zero
	for _, count := range freq {
		if count != 0 {
			return false
		}
	}

	return true
}

// This is a manually maintained map of permissions
// Used in controllers and tests, might need to look for a better location
var EquivalentSpecificPermissions = map[string][]string{
	"ChangeFiles": {
		"CreateFiles",
		"UpdateFiles",
		"DeleteFiles",
	},
	"ChangeDashboards": {
		"CreateDashboards",
		"UpdateDashboards",
		"DeleteDashboards",
	},
	"ChangeSavedQueries": {
		"CreateSavedQueries",
		"UpdateSavedQueries",
		"DeleteSavedQueries",
	},
	"ChangeScheduledReports": {
		"CreateScheduledReports",
		"UpdateScheduledReports",
		"DeleteScheduledReports",
	},
	"ChangeTriggers": {
		"CreateTriggers",
		"UpdateTriggers",
		"DeleteTriggers",
	},
	"ChangeActions": {
		"CreateActions",
		"UpdateActions",
		"DeleteActions",
	},
}

// We need to fix permissions as these are not directly mapped, at least not all
// OrganizationOwnedQueries permission gets added when the view token is created
// EquivalentSpecificPermissions translate specific permissions to others
func FixPermissions(permissions []string) []string {
	permSet := make(map[string]bool)
	for _, perm := range permissions {
		permSet[perm] = true
	}
	// this one just gets added when Token is created
	permSet[string(humiographql.PermissionOrganizationownedqueries)] = true

	for perm := range permSet {
		if extPerms, found := EquivalentSpecificPermissions[perm]; found {
			for _, extPerm := range extPerms {
				permSet[extPerm] = true
			}
			delete(permSet, perm)
		}
	}

	result := make([]string, 0, len(permSet))
	for perm := range permSet {
		result = append(result, perm)
	}
	return result
}

func parseTokenRotateResult(tokenResponse string) (string, string) {
	parts := strings.Split(tokenResponse, "~")
	return parts[0], tokenResponse
}
