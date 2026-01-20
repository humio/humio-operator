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
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	humiov1beta1 "github.com/humio/humio-operator/api/v1beta1"
	humioapi "github.com/humio/humio-operator/internal/api"
	"github.com/humio/humio-operator/internal/api/humiographql"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/kubernetes"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	WebhookHumioVersion string = "1.180.0"
)

var (
	humioClientMu sync.Mutex
)

type resourceKey struct {
	// clusterName holds the value of the cluster
	clusterName string
	// searchDomainName is the name of the repository or view
	searchDomainName string
	// resourceName is the name of resource, like IngestToken, Parser, etc.
	resourceName string
}

type ClientMock struct {
	LicenseUID             map[resourceKey]string
	Repository             map[resourceKey]humiographql.RepositoryDetails
	View                   map[resourceKey]humiographql.GetSearchDomainSearchDomainView
	MultiClusterSearchView map[resourceKey]humiographql.GetMultiClusterSearchViewSearchDomainView
	Group                  map[resourceKey]humiographql.GroupDetails
	IngestToken            map[resourceKey]humiographql.IngestTokenDetails
	Parser                 map[resourceKey]humiographql.ParserDetails
	Action                 map[resourceKey]humiographql.ActionDetails
	Alert                  map[resourceKey]humiographql.AlertDetails
	FilterAlert            map[resourceKey]humiographql.FilterAlertDetails
	FeatureFlag            map[resourceKey]bool
	AggregateAlert         map[resourceKey]humiographql.AggregateAlertDetails
	ScheduledSearch        map[resourceKey]humiographql.ScheduledSearchDetails
	ScheduledSearchV2      map[resourceKey]humiographql.ScheduledSearchDetailsV2
	SavedQuery             map[resourceKey]humiographql.SavedQueryDetails
	SavedQueryV2           map[resourceKey]humiographql.SavedQueryDetailsV2
	User                   map[resourceKey]humiographql.UserDetails
	AdminUserID            map[resourceKey]string
	Role                   map[resourceKey]humiographql.RoleDetails
	IPFilter               map[resourceKey]humiographql.IPFilterDetails
	ViewToken              map[resourceKey]humiographql.ViewTokenDetailsViewPermissionsToken
	SystemToken            map[resourceKey]humiographql.SystemTokenDetailsSystemPermissionsToken
	OrganizationToken      map[resourceKey]humiographql.OrganizationTokenDetailsOrganizationPermissionsToken
	Package                map[resourceKey]humiographql.PackageDetails
	EventForwardingRule    map[resourceKey]humiographql.EventForwardingRuleDetails
	EventForwarder         map[resourceKey]humiographql.KafkaEventForwarderDetails
}

type MockClientConfig struct {
	apiClient *ClientMock
}

func NewMockClient() *MockClientConfig {
	mockClientConfig := &MockClientConfig{
		apiClient: &ClientMock{
			LicenseUID:             make(map[resourceKey]string),
			Repository:             make(map[resourceKey]humiographql.RepositoryDetails),
			View:                   make(map[resourceKey]humiographql.GetSearchDomainSearchDomainView),
			MultiClusterSearchView: make(map[resourceKey]humiographql.GetMultiClusterSearchViewSearchDomainView),
			Group:                  make(map[resourceKey]humiographql.GroupDetails),
			IngestToken:            make(map[resourceKey]humiographql.IngestTokenDetails),
			Parser:                 make(map[resourceKey]humiographql.ParserDetails),
			Action:                 make(map[resourceKey]humiographql.ActionDetails),
			Alert:                  make(map[resourceKey]humiographql.AlertDetails),
			FilterAlert:            make(map[resourceKey]humiographql.FilterAlertDetails),
			FeatureFlag:            make(map[resourceKey]bool),
			AggregateAlert:         make(map[resourceKey]humiographql.AggregateAlertDetails),
			ScheduledSearch:        make(map[resourceKey]humiographql.ScheduledSearchDetails),
			ScheduledSearchV2:      make(map[resourceKey]humiographql.ScheduledSearchDetailsV2),
			SavedQuery:             make(map[resourceKey]humiographql.SavedQueryDetails),
			User:                   make(map[resourceKey]humiographql.UserDetails),
			AdminUserID:            make(map[resourceKey]string),
			Role:                   make(map[resourceKey]humiographql.RoleDetails),
			IPFilter:               make(map[resourceKey]humiographql.IPFilterDetails),
			ViewToken:              make(map[resourceKey]humiographql.ViewTokenDetailsViewPermissionsToken),
			SystemToken:            make(map[resourceKey]humiographql.SystemTokenDetailsSystemPermissionsToken),
			OrganizationToken:      make(map[resourceKey]humiographql.OrganizationTokenDetailsOrganizationPermissionsToken),
			Package:                make(map[resourceKey]humiographql.PackageDetails),
			EventForwardingRule:    make(map[resourceKey]humiographql.EventForwardingRuleDetails),
			EventForwarder:         make(map[resourceKey]humiographql.KafkaEventForwarderDetails),
		},
	}

	return mockClientConfig
}

func (h *MockClientConfig) ClearHumioClientConnections(repoNameToKeep string) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	for k := range h.apiClient.Repository {
		if k.resourceName != repoNameToKeep {
			delete(h.apiClient.Repository, k)
		}
	}
	h.apiClient.View = make(map[resourceKey]humiographql.GetSearchDomainSearchDomainView)
	h.apiClient.MultiClusterSearchView = make(map[resourceKey]humiographql.GetMultiClusterSearchViewSearchDomainView)
	h.apiClient.Group = make(map[resourceKey]humiographql.GroupDetails)
	h.apiClient.Role = make(map[resourceKey]humiographql.RoleDetails)
	h.apiClient.IngestToken = make(map[resourceKey]humiographql.IngestTokenDetails)
	h.apiClient.Parser = make(map[resourceKey]humiographql.ParserDetails)
	h.apiClient.Action = make(map[resourceKey]humiographql.ActionDetails)
	h.apiClient.Alert = make(map[resourceKey]humiographql.AlertDetails)
	h.apiClient.FilterAlert = make(map[resourceKey]humiographql.FilterAlertDetails)
	h.apiClient.FeatureFlag = make(map[resourceKey]bool)
	h.apiClient.AggregateAlert = make(map[resourceKey]humiographql.AggregateAlertDetails)
	h.apiClient.ScheduledSearch = make(map[resourceKey]humiographql.ScheduledSearchDetails)
	h.apiClient.ScheduledSearchV2 = make(map[resourceKey]humiographql.ScheduledSearchDetailsV2)
	h.apiClient.User = make(map[resourceKey]humiographql.UserDetails)
	h.apiClient.AdminUserID = make(map[resourceKey]string)
	h.apiClient.IPFilter = make(map[resourceKey]humiographql.IPFilterDetails)
	h.apiClient.ViewToken = make(map[resourceKey]humiographql.ViewTokenDetailsViewPermissionsToken)
	h.apiClient.SystemToken = make(map[resourceKey]humiographql.SystemTokenDetailsSystemPermissionsToken)
	h.apiClient.Package = make(map[resourceKey]humiographql.PackageDetails)
	h.apiClient.EventForwardingRule = make(map[resourceKey]humiographql.EventForwardingRuleDetails)
}

func (h *MockClientConfig) Status(_ context.Context, _ *humioapi.Client) (*humioapi.StatusResponse, error) {
	return &humioapi.StatusResponse{
		Version: WebhookHumioVersion,
	}, nil
}

func (h *MockClientConfig) GetCluster(_ context.Context, _ *humioapi.Client) (*humiographql.GetClusterResponse, error) {
	return nil, nil
}

func (h *MockClientConfig) GetEvictionStatus(_ context.Context, _ *humioapi.Client) (*humiographql.GetEvictionStatusResponse, error) {
	return nil, nil
}

func (h *MockClientConfig) SetIsBeingEvicted(_ context.Context, _ *humioapi.Client, vhost int, isBeingEvicted bool) error {
	return nil
}

func (h *MockClientConfig) RefreshClusterManagementStats(_ context.Context, _ *humioapi.Client, vhost int) (*humiographql.RefreshClusterManagementStatsResponse, error) {
	return nil, nil
}

func (h *MockClientConfig) UnregisterClusterNode(ctx context.Context, client *humioapi.Client, i int, b bool) (*humiographql.UnregisterClusterNodeResponse, error) {
	return &humiographql.UnregisterClusterNodeResponse{}, nil
}

func (h *MockClientConfig) TestAPIToken(_ context.Context, _ *humioapi.Config, _ reconcile.Request) error {
	return nil
}

func (h *MockClientConfig) AddIngestToken(_ context.Context, _ *humioapi.Client, hit *humiov1alpha1.HumioIngestToken) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", hit.Spec.ManagedClusterName, hit.Spec.ExternalClusterName)
	if !h.searchDomainNameExists(clusterName, hit.Spec.RepositoryName) {
		return fmt.Errorf("search domain name does not exist")
	}

	key := resourceKey{
		clusterName:      clusterName,
		searchDomainName: hit.Spec.RepositoryName,
		resourceName:     hit.Spec.Name,
	}

	if _, found := h.apiClient.IngestToken[key]; found {
		return fmt.Errorf("ingest token already exists with name %s", hit.Spec.Name)
	}

	var parser *humiographql.IngestTokenDetailsParser
	if hit.Spec.ParserName != nil {
		parser = &humiographql.IngestTokenDetailsParser{Name: *hit.Spec.ParserName}
	}
	h.apiClient.IngestToken[key] = humiographql.IngestTokenDetails{
		Name:   hit.Spec.Name,
		Parser: parser,
		Token:  kubernetes.RandomString(),
	}
	return nil
}

func (h *MockClientConfig) GetIngestToken(_ context.Context, _ *humioapi.Client, hit *humiov1alpha1.HumioIngestToken) (*humiographql.IngestTokenDetails, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", hit.Spec.ManagedClusterName, hit.Spec.ExternalClusterName),
		searchDomainName: hit.Spec.RepositoryName,
		resourceName:     hit.Spec.Name,
	}
	if value, found := h.apiClient.IngestToken[key]; found {
		return &value, nil

	}
	return nil, fmt.Errorf("could not find ingest token in repository %s with name %s, err=%w", hit.Spec.RepositoryName, hit.Spec.Name, humioapi.EntityNotFound{})
}

func (h *MockClientConfig) UpdateIngestToken(_ context.Context, _ *humioapi.Client, hit *humiov1alpha1.HumioIngestToken) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", hit.Spec.ManagedClusterName, hit.Spec.ExternalClusterName),
		searchDomainName: hit.Spec.RepositoryName,
		resourceName:     hit.Spec.Name,
	}

	currentIngestToken, found := h.apiClient.IngestToken[key]

	if !found {
		return fmt.Errorf("ingest token not found with name %s, err=%w", hit.Spec.Name, humioapi.EntityNotFound{})
	}

	var parser *humiographql.IngestTokenDetailsParser
	if hit.Spec.ParserName != nil {
		parser = &humiographql.IngestTokenDetailsParser{Name: *hit.Spec.ParserName}
	}
	h.apiClient.IngestToken[key] = humiographql.IngestTokenDetails{
		Name:   hit.Spec.Name,
		Parser: parser,
		Token:  currentIngestToken.GetToken(),
	}

	return nil
}

func (h *MockClientConfig) DeleteIngestToken(_ context.Context, _ *humioapi.Client, hit *humiov1alpha1.HumioIngestToken) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", hit.Spec.ManagedClusterName, hit.Spec.ExternalClusterName),
		searchDomainName: hit.Spec.RepositoryName,
		resourceName:     hit.Spec.Name,
	}

	delete(h.apiClient.IngestToken, key)
	return nil
}

func (h *MockClientConfig) AddParser(_ context.Context, _ *humioapi.Client, hp *humiov1alpha1.HumioParser) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", hp.Spec.ManagedClusterName, hp.Spec.ExternalClusterName)
	if !h.searchDomainNameExists(clusterName, hp.Spec.RepositoryName) {
		return fmt.Errorf("search domain name does not exist")
	}

	key := resourceKey{
		clusterName:      clusterName,
		searchDomainName: hp.Spec.RepositoryName,
		resourceName:     hp.Spec.Name,
	}

	if _, found := h.apiClient.Parser[key]; found {
		return fmt.Errorf("parser already exists with name %s", hp.Spec.Name)
	}

	h.apiClient.Parser[key] = humiographql.ParserDetails{
		Id:          kubernetes.RandomString(),
		Name:        hp.Spec.Name,
		Script:      hp.Spec.ParserScript,
		FieldsToTag: hp.Spec.TagFields,
		TestCases:   humioapi.TestDataToParserDetailsTestCasesParserTestCase(hp.Spec.TestData),
	}
	return nil
}

func (h *MockClientConfig) GetParser(_ context.Context, _ *humioapi.Client, hp *humiov1alpha1.HumioParser) (*humiographql.ParserDetails, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", hp.Spec.ManagedClusterName, hp.Spec.ExternalClusterName),
		searchDomainName: hp.Spec.RepositoryName,
		resourceName:     hp.Spec.Name,
	}
	if value, found := h.apiClient.Parser[key]; found {
		return &value, nil

	}
	return nil, fmt.Errorf("could not find parser in repository %s with name %s, err=%w", hp.Spec.RepositoryName, hp.Spec.Name, humioapi.EntityNotFound{})
}

func (h *MockClientConfig) UpdateParser(_ context.Context, _ *humioapi.Client, hp *humiov1alpha1.HumioParser) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", hp.Spec.ManagedClusterName, hp.Spec.ExternalClusterName),
		searchDomainName: hp.Spec.RepositoryName,
		resourceName:     hp.Spec.Name,
	}

	currentParser, found := h.apiClient.Parser[key]

	if !found {
		return fmt.Errorf("parser not found with name %s, err=%w", hp.Spec.Name, humioapi.EntityNotFound{})
	}

	h.apiClient.Parser[key] = humiographql.ParserDetails{
		Id:          currentParser.GetId(),
		Name:        hp.Spec.Name,
		Script:      hp.Spec.ParserScript,
		FieldsToTag: hp.Spec.TagFields,
		TestCases:   humioapi.TestDataToParserDetailsTestCasesParserTestCase(hp.Spec.TestData),
	}
	return nil
}

func (h *MockClientConfig) DeleteParser(_ context.Context, _ *humioapi.Client, hp *humiov1alpha1.HumioParser) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", hp.Spec.ManagedClusterName, hp.Spec.ExternalClusterName),
		searchDomainName: hp.Spec.RepositoryName,
		resourceName:     hp.Spec.Name,
	}

	delete(h.apiClient.Parser, key)
	return nil
}

func (h *MockClientConfig) AddRepository(_ context.Context, _ *humioapi.Client, hr *humiov1alpha1.HumioRepository) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", hr.Spec.ManagedClusterName, hr.Spec.ExternalClusterName)
	if h.searchDomainNameExists(clusterName, hr.Spec.Name) {
		return fmt.Errorf("search domain name already in use")
	}

	key := resourceKey{
		clusterName:  clusterName,
		resourceName: hr.Spec.Name,
	}

	var retentionInDays, ingestSizeInGB, storageSizeInGB float64
	if hr.Spec.Retention.TimeInDays != nil {
		retentionInDays = float64(*hr.Spec.Retention.TimeInDays)
	}
	if hr.Spec.Retention.IngestSizeInGB != nil {
		ingestSizeInGB = float64(*hr.Spec.Retention.IngestSizeInGB)
	}
	if hr.Spec.Retention.StorageSizeInGB != nil {
		storageSizeInGB = float64(*hr.Spec.Retention.StorageSizeInGB)
	}

	value := &humiographql.RepositoryDetails{
		Id:                        kubernetes.RandomString(),
		Name:                      hr.Spec.Name,
		Description:               &hr.Spec.Description,
		TimeBasedRetention:        &retentionInDays,
		IngestSizeBasedRetention:  &ingestSizeInGB,
		StorageSizeBasedRetention: &storageSizeInGB,
		AutomaticSearch:           helpers.BoolTrue(hr.Spec.AutomaticSearch),
	}

	h.apiClient.Repository[key] = *value
	return nil
}

func (h *MockClientConfig) GetRepository(_ context.Context, _ *humioapi.Client, hr *humiov1alpha1.HumioRepository) (*humiographql.RepositoryDetails, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:  fmt.Sprintf("%s%s", hr.Spec.ManagedClusterName, hr.Spec.ExternalClusterName),
		resourceName: hr.Spec.Name,
	}
	if value, found := h.apiClient.Repository[key]; found {
		return &value, nil

	}
	return nil, fmt.Errorf("could not find repository with name %s, err=%w", hr.Spec.Name, humioapi.EntityNotFound{})

}

func (h *MockClientConfig) UpdateRepository(_ context.Context, _ *humioapi.Client, hr *humiov1alpha1.HumioRepository) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:  fmt.Sprintf("%s%s", hr.Spec.ManagedClusterName, hr.Spec.ExternalClusterName),
		resourceName: hr.Spec.Name,
	}

	currentRepository, found := h.apiClient.Repository[key]

	if !found {
		return fmt.Errorf("repository not found with name %s, err=%w", hr.Spec.Name, humioapi.EntityNotFound{})
	}

	var retentionInDays, ingestSizeInGB, storageSizeInGB float64
	if hr.Spec.Retention.TimeInDays != nil {
		retentionInDays = float64(*hr.Spec.Retention.TimeInDays)
	}
	if hr.Spec.Retention.IngestSizeInGB != nil {
		ingestSizeInGB = float64(*hr.Spec.Retention.IngestSizeInGB)
	}
	if hr.Spec.Retention.StorageSizeInGB != nil {
		storageSizeInGB = float64(*hr.Spec.Retention.StorageSizeInGB)
	}
	value := &humiographql.RepositoryDetails{
		Id:                        currentRepository.GetId(),
		Name:                      hr.Spec.Name,
		Description:               &hr.Spec.Description,
		TimeBasedRetention:        &retentionInDays,
		IngestSizeBasedRetention:  &ingestSizeInGB,
		StorageSizeBasedRetention: &storageSizeInGB,
		AutomaticSearch:           helpers.BoolTrue(hr.Spec.AutomaticSearch),
	}

	h.apiClient.Repository[key] = *value
	return nil
}

func (h *MockClientConfig) DeleteRepository(_ context.Context, _ *humioapi.Client, hr *humiov1alpha1.HumioRepository) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	// TODO: consider finding all entities referring to this searchDomainName and remove them as well

	key := resourceKey{
		clusterName:  fmt.Sprintf("%s%s", hr.Spec.ManagedClusterName, hr.Spec.ExternalClusterName),
		resourceName: hr.Spec.Name,
	}

	delete(h.apiClient.Repository, key)
	return nil
}

func (h *MockClientConfig) GetView(_ context.Context, _ *humioapi.Client, hv *humiov1alpha1.HumioView, includeFederated bool) (*humiographql.GetSearchDomainSearchDomainView, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:  fmt.Sprintf("%s%s", hv.Spec.ManagedClusterName, hv.Spec.ExternalClusterName),
		resourceName: hv.Spec.Name,
	}
	if value, found := h.apiClient.View[key]; found {
		return &value, nil

	}
	return nil, fmt.Errorf("could not find view with name %s, err=%w", hv.Spec.Name, humioapi.EntityNotFound{})
}

func (h *MockClientConfig) AddView(_ context.Context, _ *humioapi.Client, hv *humiov1alpha1.HumioView) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", hv.Spec.ManagedClusterName, hv.Spec.ExternalClusterName)
	if h.searchDomainNameExists(clusterName, hv.Spec.Name) {
		return fmt.Errorf("search domain name already in use")
	}

	key := resourceKey{
		clusterName:  clusterName,
		resourceName: hv.Spec.Name,
	}

	connections := make([]humiographql.GetSearchDomainSearchDomainViewConnectionsViewConnection, 0)
	for _, connection := range hv.Spec.Connections {
		connections = append(connections, humiographql.GetSearchDomainSearchDomainViewConnectionsViewConnection{
			Repository: humiographql.GetSearchDomainSearchDomainViewConnectionsViewConnectionRepository{
				Name: connection.RepositoryName,
			},
			Filter: connection.Filter,
		})
	}

	value := &humiographql.GetSearchDomainSearchDomainView{
		IsFederated:     false,
		Typename:        helpers.StringPtr("View"),
		Id:              hv.Spec.Name,
		Name:            hv.Spec.Name,
		Description:     &hv.Spec.Description,
		AutomaticSearch: helpers.BoolTrue(hv.Spec.AutomaticSearch),
		Connections:     connections,
	}
	h.apiClient.View[key] = *value
	return nil
}

func (h *MockClientConfig) UpdateView(_ context.Context, _ *humioapi.Client, hv *humiov1alpha1.HumioView) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:  fmt.Sprintf("%s%s", hv.Spec.ManagedClusterName, hv.Spec.ExternalClusterName),
		resourceName: hv.Spec.Name,
	}

	currentView, found := h.apiClient.View[key]

	if !found {
		return fmt.Errorf("view not found with name %s, err=%w", hv.Spec.Name, humioapi.EntityNotFound{})
	}

	connections := make([]humiographql.GetSearchDomainSearchDomainViewConnectionsViewConnection, 0)
	for _, connection := range hv.Spec.Connections {
		connections = append(connections, humiographql.GetSearchDomainSearchDomainViewConnectionsViewConnection{
			Repository: humiographql.GetSearchDomainSearchDomainViewConnectionsViewConnectionRepository{
				Name: connection.RepositoryName,
			},
			Filter: connection.Filter,
		})
	}

	value := &humiographql.GetSearchDomainSearchDomainView{
		IsFederated:     currentView.GetIsFederated(),
		Typename:        helpers.StringPtr("View"),
		Id:              currentView.GetId(),
		Name:            hv.Spec.Name,
		Description:     &hv.Spec.Description,
		Connections:     connections,
		AutomaticSearch: helpers.BoolTrue(hv.Spec.AutomaticSearch),
	}
	h.apiClient.View[key] = *value
	return nil
}

func (h *MockClientConfig) DeleteView(_ context.Context, _ *humioapi.Client, hv *humiov1alpha1.HumioView) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	// TODO: consider finding all entities referring to this searchDomainName and remove them as well

	key := resourceKey{
		clusterName:  fmt.Sprintf("%s%s", hv.Spec.ManagedClusterName, hv.Spec.ExternalClusterName),
		resourceName: hv.Spec.Name,
	}

	delete(h.apiClient.View, key)
	return nil
}

func (h *MockClientConfig) AddMultiClusterSearchView(_ context.Context, _ *humioapi.Client, hv *humiov1alpha1.HumioMultiClusterSearchView, connectionDetails []ConnectionDetailsIncludingAPIToken) error {
	clusterName := fmt.Sprintf("%s%s", hv.Spec.ManagedClusterName, hv.Spec.ExternalClusterName)
	if h.searchDomainNameExists(clusterName, hv.Spec.Name) {
		return fmt.Errorf("search domain name already in use")
	}

	key := resourceKey{
		clusterName:  clusterName,
		resourceName: hv.Spec.Name,
	}

	connections := make([]humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsClusterConnection, len(connectionDetails))
	for idx, connection := range connectionDetails {
		if connection.Type == humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeLocal {
			tags := make([]humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsClusterConnectionTagsClusterConnectionTag, len(connection.Tags)+1)
			tags[0] = humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsClusterConnectionTagsClusterConnectionTag{
				Key:   "clusteridentity",
				Value: connection.ClusterIdentity,
			}

			for tagIdx, tag := range connection.Tags {
				tags[tagIdx+1] = humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsClusterConnectionTagsClusterConnectionTag{
					Key:   tag.Key,
					Value: tag.Value,
				}
			}

			connections[idx] = &humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsLocalClusterConnection{
				Typename:       helpers.StringPtr("LocalClusterConnection"),
				ClusterId:      connection.ClusterIdentity,
				Id:             kubernetes.RandomString(),
				QueryPrefix:    connection.Filter,
				Tags:           tags,
				TargetViewName: connection.ViewOrRepoName,
			}
		}
		if connection.Type == humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeRemote {
			tags := make([]humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsClusterConnectionTagsClusterConnectionTag, len(connection.Tags)+1)
			tags[0] = humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsClusterConnectionTagsClusterConnectionTag{
				Key:   "clusteridentity",
				Value: connection.ClusterIdentity,
			}
			tags[1] = humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsClusterConnectionTagsClusterConnectionTag{
				Key:   "clusteridentityhash",
				Value: helpers.AsSHA256(fmt.Sprintf("%s|%s", connection.Url, connection.APIToken)),
			}

			for tagIdx, tag := range connection.Tags {
				tags[tagIdx+2] = humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsClusterConnectionTagsClusterConnectionTag{
					Key:   tag.Key,
					Value: tag.Value,
				}
			}

			connections[idx] = &humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsRemoteClusterConnection{
				Typename:    helpers.StringPtr("RemoteClusterConnection"),
				ClusterId:   connection.ClusterIdentity,
				Id:          kubernetes.RandomString(),
				QueryPrefix: connection.Filter,
				Tags:        tags,
				PublicUrl:   connection.Url,
			}
		}
	}

	value := &humiographql.GetMultiClusterSearchViewSearchDomainView{
		IsFederated:        true,
		Typename:           helpers.StringPtr("View"),
		Id:                 kubernetes.RandomString(),
		Name:               hv.Spec.Name,
		Description:        &hv.Spec.Description,
		AutomaticSearch:    helpers.BoolTrue(hv.Spec.AutomaticSearch),
		ClusterConnections: connections,
	}

	h.apiClient.MultiClusterSearchView[key] = *value
	return nil
}

func (h *MockClientConfig) GetMultiClusterSearchView(_ context.Context, _ *humioapi.Client, hv *humiov1alpha1.HumioMultiClusterSearchView) (*humiographql.GetMultiClusterSearchViewSearchDomainView, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:  fmt.Sprintf("%s%s", hv.Spec.ManagedClusterName, hv.Spec.ExternalClusterName),
		resourceName: hv.Spec.Name,
	}
	if value, found := h.apiClient.MultiClusterSearchView[key]; found {
		return &value, nil

	}
	return nil, fmt.Errorf("could not find view with name %s, err=%w", hv.Spec.Name, humioapi.EntityNotFound{})
}

func (h *MockClientConfig) UpdateMultiClusterSearchView(_ context.Context, _ *humioapi.Client, hv *humiov1alpha1.HumioMultiClusterSearchView, connectionDetails []ConnectionDetailsIncludingAPIToken) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:  fmt.Sprintf("%s%s", hv.Spec.ManagedClusterName, hv.Spec.ExternalClusterName),
		resourceName: hv.Spec.Name,
	}

	currentView, found := h.apiClient.MultiClusterSearchView[key]

	if !found {
		return fmt.Errorf("view not found with name %s, err=%w", hv.Spec.Name, humioapi.EntityNotFound{})
	}

	connections := make([]humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsClusterConnection, len(connectionDetails))
	for idx, connection := range connectionDetails {
		if connection.Type == humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeLocal {
			tags := make([]humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsClusterConnectionTagsClusterConnectionTag, len(connection.Tags)+1)
			tags[0] = humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsClusterConnectionTagsClusterConnectionTag{
				Key:   "clusteridentity",
				Value: connection.ClusterIdentity,
			}

			for tagIdx, tag := range connection.Tags {
				tags[tagIdx+1] = humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsClusterConnectionTagsClusterConnectionTag{
					Key:   tag.Key,
					Value: tag.Value,
				}
			}

			connections[idx] = &humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsLocalClusterConnection{
				Typename:       helpers.StringPtr("LocalClusterConnection"),
				ClusterId:      connection.ClusterIdentity,
				Id:             kubernetes.RandomString(), // Perhaps we should use the same as before
				QueryPrefix:    connection.Filter,
				Tags:           tags,
				TargetViewName: connection.ViewOrRepoName,
			}
		}
		if connection.Type == humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeRemote {
			tags := make([]humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsClusterConnectionTagsClusterConnectionTag, len(connection.Tags)+2)
			tags[0] = humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsClusterConnectionTagsClusterConnectionTag{
				Key:   "clusteridentity",
				Value: connection.ClusterIdentity,
			}
			tags[1] = humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsClusterConnectionTagsClusterConnectionTag{
				Key:   "clusteridentityhash",
				Value: helpers.AsSHA256(fmt.Sprintf("%s|%s", connection.Url, connection.APIToken)),
			}

			for tagIdx, tag := range connection.Tags {
				tags[tagIdx+2] = humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsClusterConnectionTagsClusterConnectionTag{
					Key:   tag.Key,
					Value: tag.Value,
				}
			}

			connections[idx] = &humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsRemoteClusterConnection{
				Typename:    helpers.StringPtr("RemoteClusterConnection"),
				ClusterId:   connection.ClusterIdentity,
				Id:          kubernetes.RandomString(),
				QueryPrefix: connection.Filter,
				Tags:        tags,
				PublicUrl:   connection.Url,
			}
		}
	}

	value := &humiographql.GetMultiClusterSearchViewSearchDomainView{
		IsFederated:        currentView.GetIsFederated(),
		Typename:           helpers.StringPtr("View"),
		Id:                 currentView.GetId(),
		Name:               hv.Spec.Name,
		Description:        &hv.Spec.Description,
		AutomaticSearch:    helpers.BoolTrue(hv.Spec.AutomaticSearch),
		ClusterConnections: connections,
	}

	h.apiClient.MultiClusterSearchView[key] = *value
	return nil
}

func (h *MockClientConfig) DeleteMultiClusterSearchView(_ context.Context, _ *humioapi.Client, hv *humiov1alpha1.HumioMultiClusterSearchView) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:  fmt.Sprintf("%s%s", hv.Spec.ManagedClusterName, hv.Spec.ExternalClusterName),
		resourceName: hv.Spec.Name,
	}

	delete(h.apiClient.MultiClusterSearchView, key)
	return nil
}

func (h *MockClientConfig) AddGroup(_ context.Context, _ *humioapi.Client, group *humiov1alpha1.HumioGroup) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", group.Spec.ManagedClusterName, group.Spec.ExternalClusterName)
	key := resourceKey{
		clusterName:  clusterName,
		resourceName: group.Spec.Name,
	}
	if _, found := h.apiClient.Group[key]; found {
		return fmt.Errorf("group already exists with name %s", group.Spec.Name)
	}

	value := &humiographql.GroupDetails{
		Id:          kubernetes.RandomString(),
		DisplayName: group.Spec.Name,
		LookupName:  group.Spec.ExternalMappingName,
	}

	h.apiClient.Group[key] = *value
	return nil
}

func (h *MockClientConfig) GetGroup(_ context.Context, _ *humioapi.Client, group *humiov1alpha1.HumioGroup) (*humiographql.GroupDetails, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:  fmt.Sprintf("%s%s", group.Spec.ManagedClusterName, group.Spec.ExternalClusterName),
		resourceName: group.Spec.Name,
	}
	if value, found := h.apiClient.Group[key]; found {
		return &value, nil
	}
	return nil, humioapi.GroupNotFound(group.Spec.Name)
}

func (h *MockClientConfig) UpdateGroup(_ context.Context, _ *humioapi.Client, group *humiov1alpha1.HumioGroup) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:  fmt.Sprintf("%s%s", group.Spec.ManagedClusterName, group.Spec.ExternalClusterName),
		resourceName: group.Spec.Name,
	}
	currentGroup, found := h.apiClient.Group[key]

	if !found {
		return humioapi.GroupNotFound(group.Spec.Name)
	}

	newLookupName := group.Spec.ExternalMappingName
	if group.Spec.ExternalMappingName != nil && *group.Spec.ExternalMappingName == "" {
		// LogScale returns null from graphql when lookup name is updated to empty string
		newLookupName = nil
	}

	value := &humiographql.GroupDetails{
		Id:          currentGroup.GetId(),
		DisplayName: group.Spec.Name,
		LookupName:  newLookupName,
	}

	h.apiClient.Group[key] = *value
	return nil
}

func (h *MockClientConfig) DeleteGroup(_ context.Context, _ *humioapi.Client, group *humiov1alpha1.HumioGroup) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:  fmt.Sprintf("%s%s", group.Spec.ManagedClusterName, group.Spec.ExternalClusterName),
		resourceName: group.Spec.Name,
	}
	delete(h.apiClient.Group, key)
	return nil
}

func (h *MockClientConfig) GetLicenseUIDAndExpiry(_ context.Context, _ *humioapi.Client, req reconcile.Request) (string, time.Time, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		resourceName: fmt.Sprintf("%s%s", req.Namespace, req.Name),
	}

	if value, found := h.apiClient.LicenseUID[key]; found {
		return value, time.Now(), nil
	}

	return "", time.Time{}, humioapi.EntityNotFound{}
}

func (h *MockClientConfig) InstallLicense(_ context.Context, _ *humioapi.Client, req reconcile.Request, licenseString string) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		resourceName: fmt.Sprintf("%s%s", req.Namespace, req.Name),
	}

	licenseUID, err := GetLicenseUIDFromLicenseString(licenseString)
	if err != nil {
		return fmt.Errorf("failed to parse license: %w", err)
	}

	h.apiClient.LicenseUID[key] = licenseUID
	return nil
}

func (h *MockClientConfig) GetAction(_ context.Context, _ *humioapi.Client, ha *humiov1alpha1.HumioAction) (humiographql.ActionDetails, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", ha.Spec.ManagedClusterName, ha.Spec.ExternalClusterName),
		searchDomainName: ha.Spec.ViewName,
		resourceName:     ha.Spec.Name,
	}
	if value, found := h.apiClient.Action[key]; found {
		return value, nil

	}
	return nil, fmt.Errorf("could not find action in view %q with name %q, err=%w", ha.Spec.ViewName, ha.Spec.Name, humioapi.EntityNotFound{})
}

func (h *MockClientConfig) AddAction(_ context.Context, _ *humioapi.Client, ha *humiov1alpha1.HumioAction) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", ha.Spec.ManagedClusterName, ha.Spec.ExternalClusterName)
	if !h.searchDomainNameExists(clusterName, ha.Spec.ViewName) {
		return fmt.Errorf("search domain name does not exist")
	}

	key := resourceKey{
		clusterName:      clusterName,
		searchDomainName: ha.Spec.ViewName,
		resourceName:     ha.Spec.Name,
	}

	if _, found := h.apiClient.Action[key]; found {
		return fmt.Errorf("action already exists with name %s", ha.Spec.Name)
	}

	newActionWithResolvedSecrets, err := ActionFromActionCR(ha)
	if err != nil {
		return err
	}

	switch v := (newActionWithResolvedSecrets).(type) {
	case *humiographql.ActionDetailsEmailAction:
		h.apiClient.Action[key] = &humiographql.ActionDetailsEmailAction{
			Id:                kubernetes.RandomString(),
			Name:              v.GetName(),
			Recipients:        v.GetRecipients(),
			SubjectTemplate:   v.GetSubjectTemplate(),
			EmailBodyTemplate: v.GetEmailBodyTemplate(),
			UseProxy:          v.GetUseProxy(),
		}
	case *humiographql.ActionDetailsHumioRepoAction:
		h.apiClient.Action[key] = &humiographql.ActionDetailsHumioRepoAction{
			Id:          kubernetes.RandomString(),
			Name:        v.GetName(),
			IngestToken: v.GetIngestToken(),
		}
	case *humiographql.ActionDetailsOpsGenieAction:
		h.apiClient.Action[key] = &humiographql.ActionDetailsOpsGenieAction{
			Id:       kubernetes.RandomString(),
			Name:     v.GetName(),
			ApiUrl:   v.GetApiUrl(),
			GenieKey: v.GetGenieKey(),
			UseProxy: v.GetUseProxy(),
		}
	case *humiographql.ActionDetailsPagerDutyAction:
		h.apiClient.Action[key] = &humiographql.ActionDetailsPagerDutyAction{
			Id:         kubernetes.RandomString(),
			Name:       v.GetName(),
			Severity:   v.GetSeverity(),
			RoutingKey: v.GetRoutingKey(),
			UseProxy:   v.GetUseProxy(),
		}
	case *humiographql.ActionDetailsSlackAction:
		h.apiClient.Action[key] = &humiographql.ActionDetailsSlackAction{
			Id:       kubernetes.RandomString(),
			Name:     v.GetName(),
			Url:      v.GetUrl(),
			Fields:   v.GetFields(),
			UseProxy: v.GetUseProxy(),
		}
	case *humiographql.ActionDetailsSlackPostMessageAction:
		h.apiClient.Action[key] = &humiographql.ActionDetailsSlackPostMessageAction{
			Id:       kubernetes.RandomString(),
			Name:     v.GetName(),
			ApiToken: v.GetApiToken(),
			Channels: v.GetChannels(),
			Fields:   v.GetFields(),
			UseProxy: v.GetUseProxy(),
		}
	case *humiographql.ActionDetailsVictorOpsAction:
		h.apiClient.Action[key] = &humiographql.ActionDetailsVictorOpsAction{
			Id:          kubernetes.RandomString(),
			Name:        v.GetName(),
			MessageType: v.GetMessageType(),
			NotifyUrl:   v.GetNotifyUrl(),
			UseProxy:    v.GetUseProxy(),
		}
	case *humiographql.ActionDetailsWebhookAction:
		h.apiClient.Action[key] = &humiographql.ActionDetailsWebhookAction{
			Id:                  kubernetes.RandomString(),
			Name:                v.GetName(),
			Method:              v.GetMethod(),
			Url:                 v.GetUrl(),
			Headers:             v.GetHeaders(),
			WebhookBodyTemplate: v.GetWebhookBodyTemplate(),
			IgnoreSSL:           v.GetIgnoreSSL(),
			UseProxy:            v.GetUseProxy(),
		}
	}

	return nil
}

func (h *MockClientConfig) UpdateAction(_ context.Context, _ *humioapi.Client, ha *humiov1alpha1.HumioAction) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", ha.Spec.ManagedClusterName, ha.Spec.ExternalClusterName),
		searchDomainName: ha.Spec.ViewName,
		resourceName:     ha.Spec.Name,
	}

	currentAction, found := h.apiClient.Action[key]

	if !found {
		return fmt.Errorf("could not find action in view %q with name %q, err=%w", ha.Spec.ViewName, ha.Spec.Name, humioapi.EntityNotFound{})
	}

	newActionWithResolvedSecrets, err := ActionFromActionCR(ha)
	if err != nil {
		return err
	}

	switch v := (newActionWithResolvedSecrets).(type) {
	case *humiographql.ActionDetailsEmailAction:
		h.apiClient.Action[key] = &humiographql.ActionDetailsEmailAction{
			Id:                currentAction.GetId(),
			Name:              v.GetName(),
			Recipients:        v.GetRecipients(),
			SubjectTemplate:   v.GetSubjectTemplate(),
			EmailBodyTemplate: v.GetEmailBodyTemplate(),
			UseProxy:          v.GetUseProxy(),
		}
	case *humiographql.ActionDetailsHumioRepoAction:
		h.apiClient.Action[key] = &humiographql.ActionDetailsHumioRepoAction{
			Id:          currentAction.GetId(),
			Name:        v.GetName(),
			IngestToken: v.GetIngestToken(),
		}
	case *humiographql.ActionDetailsOpsGenieAction:
		h.apiClient.Action[key] = &humiographql.ActionDetailsOpsGenieAction{
			Id:       currentAction.GetId(),
			Name:     v.GetName(),
			ApiUrl:   v.GetApiUrl(),
			GenieKey: v.GetGenieKey(),
			UseProxy: v.GetUseProxy(),
		}
	case *humiographql.ActionDetailsPagerDutyAction:
		h.apiClient.Action[key] = &humiographql.ActionDetailsPagerDutyAction{
			Id:         currentAction.GetId(),
			Name:       v.GetName(),
			Severity:   v.GetSeverity(),
			RoutingKey: v.GetRoutingKey(),
			UseProxy:   v.GetUseProxy(),
		}
	case *humiographql.ActionDetailsSlackAction:
		h.apiClient.Action[key] = &humiographql.ActionDetailsSlackAction{
			Id:       currentAction.GetId(),
			Name:     v.GetName(),
			Url:      v.GetUrl(),
			Fields:   v.GetFields(),
			UseProxy: v.GetUseProxy(),
		}
	case *humiographql.ActionDetailsSlackPostMessageAction:
		h.apiClient.Action[key] = &humiographql.ActionDetailsSlackPostMessageAction{
			Id:       currentAction.GetId(),
			Name:     v.GetName(),
			ApiToken: v.GetApiToken(),
			Channels: v.GetChannels(),
			Fields:   v.GetFields(),
			UseProxy: v.GetUseProxy(),
		}
	case *humiographql.ActionDetailsVictorOpsAction:
		h.apiClient.Action[key] = &humiographql.ActionDetailsVictorOpsAction{
			Id:          currentAction.GetId(),
			Name:        v.GetName(),
			MessageType: v.GetMessageType(),
			NotifyUrl:   v.GetNotifyUrl(),
			UseProxy:    v.GetUseProxy(),
		}
	case *humiographql.ActionDetailsWebhookAction:
		h.apiClient.Action[key] = &humiographql.ActionDetailsWebhookAction{
			Id:                  currentAction.GetId(),
			Name:                v.GetName(),
			Method:              v.GetMethod(),
			Url:                 v.GetUrl(),
			Headers:             v.GetHeaders(),
			WebhookBodyTemplate: v.GetWebhookBodyTemplate(),
			IgnoreSSL:           v.GetIgnoreSSL(),
			UseProxy:            v.GetUseProxy(),
		}
	}

	return nil
}

func (h *MockClientConfig) DeleteAction(_ context.Context, _ *humioapi.Client, ha *humiov1alpha1.HumioAction) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", ha.Spec.ManagedClusterName, ha.Spec.ExternalClusterName),
		searchDomainName: ha.Spec.ViewName,
		resourceName:     ha.Spec.Name,
	}

	delete(h.apiClient.Action, key)
	return nil
}

func (h *MockClientConfig) GetAlert(_ context.Context, _ *humioapi.Client, ha *humiov1alpha1.HumioAlert) (*humiographql.AlertDetails, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", ha.Spec.ManagedClusterName, ha.Spec.ExternalClusterName),
		searchDomainName: ha.Spec.ViewName,
		resourceName:     ha.Spec.Name,
	}
	if value, found := h.apiClient.Alert[key]; found {
		return &value, nil

	}
	return nil, fmt.Errorf("could not find alert in view %q with name %q, err=%w", ha.Spec.ViewName, ha.Spec.Name, humioapi.EntityNotFound{})
}

func (h *MockClientConfig) AddAlert(_ context.Context, _ *humioapi.Client, ha *humiov1alpha1.HumioAlert) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", ha.Spec.ManagedClusterName, ha.Spec.ExternalClusterName)
	if !h.searchDomainNameExists(clusterName, ha.Spec.ViewName) {
		return fmt.Errorf("search domain name does not exist")
	}

	key := resourceKey{
		clusterName:      clusterName,
		searchDomainName: ha.Spec.ViewName,
		resourceName:     ha.Spec.Name,
	}

	if _, found := h.apiClient.Alert[key]; found {
		return fmt.Errorf("alert already exists with name %s", ha.Spec.Name)
	}

	h.apiClient.Alert[key] = humiographql.AlertDetails{
		Id:                 kubernetes.RandomString(),
		Name:               ha.Spec.Name,
		QueryString:        ha.Spec.Query.QueryString,
		QueryStart:         ha.Spec.Query.Start,
		ThrottleField:      ha.Spec.ThrottleField,
		Description:        &ha.Spec.Description,
		ThrottleTimeMillis: int64(ha.Spec.ThrottleTimeMillis),
		Enabled:            !ha.Spec.Silenced,
		ActionsV2:          humioapi.ActionNamesToEmailActions(ha.Spec.Actions),
		Labels:             ha.Spec.Labels,
		QueryOwnership: &humiographql.SharedQueryOwnershipTypeOrganizationOwnership{
			Typename: helpers.StringPtr("OrganizationOwnership"),
			QueryOwnershipOrganizationOwnership: humiographql.QueryOwnershipOrganizationOwnership{
				Typename: helpers.StringPtr("OrganizationOwnership"),
			},
		},
	}
	return nil
}

func (h *MockClientConfig) UpdateAlert(_ context.Context, _ *humioapi.Client, ha *humiov1alpha1.HumioAlert) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", ha.Spec.ManagedClusterName, ha.Spec.ExternalClusterName),
		searchDomainName: ha.Spec.ViewName,
		resourceName:     ha.Spec.Name,
	}

	currentAlert, found := h.apiClient.Alert[key]
	if !found {
		return fmt.Errorf("alert not found with name %s, err=%w", ha.Spec.Name, humioapi.EntityNotFound{})
	}

	h.apiClient.Alert[key] = humiographql.AlertDetails{
		Id:                 currentAlert.GetId(),
		Name:               ha.Spec.Name,
		QueryString:        ha.Spec.Query.QueryString,
		QueryStart:         ha.Spec.Query.Start,
		ThrottleField:      ha.Spec.ThrottleField,
		Description:        &ha.Spec.Description,
		ThrottleTimeMillis: int64(ha.Spec.ThrottleTimeMillis),
		Enabled:            !ha.Spec.Silenced,
		ActionsV2:          humioapi.ActionNamesToEmailActions(ha.Spec.Actions),
		Labels:             ha.Spec.Labels,
		QueryOwnership: &humiographql.SharedQueryOwnershipTypeOrganizationOwnership{
			Typename: helpers.StringPtr("OrganizationOwnership"),
			QueryOwnershipOrganizationOwnership: humiographql.QueryOwnershipOrganizationOwnership{
				Typename: helpers.StringPtr("OrganizationOwnership"),
			},
		},
	}
	return nil
}

func (h *MockClientConfig) DeleteAlert(_ context.Context, _ *humioapi.Client, ha *humiov1alpha1.HumioAlert) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", ha.Spec.ManagedClusterName, ha.Spec.ExternalClusterName),
		searchDomainName: ha.Spec.ViewName,
		resourceName:     ha.Spec.Name,
	}

	delete(h.apiClient.Alert, key)
	return nil
}

func (h *MockClientConfig) GetFilterAlert(_ context.Context, _ *humioapi.Client, hfa *humiov1alpha1.HumioFilterAlert) (*humiographql.FilterAlertDetails, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", hfa.Spec.ManagedClusterName, hfa.Spec.ExternalClusterName),
		searchDomainName: hfa.Spec.ViewName,
		resourceName:     hfa.Spec.Name,
	}
	if value, found := h.apiClient.FilterAlert[key]; found {
		return &value, nil

	}
	return nil, fmt.Errorf("could not find alert in view %q with name %q, err=%w", hfa.Spec.ViewName, hfa.Spec.Name, humioapi.EntityNotFound{})
}

func (h *MockClientConfig) AddFilterAlert(_ context.Context, _ *humioapi.Client, hfa *humiov1alpha1.HumioFilterAlert) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", hfa.Spec.ManagedClusterName, hfa.Spec.ExternalClusterName)
	if !h.searchDomainNameExists(clusterName, hfa.Spec.ViewName) {
		return fmt.Errorf("search domain name does not exist")
	}

	key := resourceKey{
		clusterName:      clusterName,
		searchDomainName: hfa.Spec.ViewName,
		resourceName:     hfa.Spec.Name,
	}

	if _, found := h.apiClient.FilterAlert[key]; found {
		return fmt.Errorf("filter alert already exists with name %s", hfa.Spec.Name)
	}

	h.apiClient.FilterAlert[key] = humiographql.FilterAlertDetails{
		Id:                  kubernetes.RandomString(),
		Name:                hfa.Spec.Name,
		Description:         &hfa.Spec.Description,
		QueryString:         hfa.Spec.QueryString,
		ThrottleTimeSeconds: helpers.Int64Ptr(int64(hfa.Spec.ThrottleTimeSeconds)),
		ThrottleField:       hfa.Spec.ThrottleField,
		Labels:              hfa.Spec.Labels,
		Enabled:             hfa.Spec.Enabled,
		Actions:             humioapi.ActionNamesToEmailActions(hfa.Spec.Actions),
		QueryOwnership: &humiographql.SharedQueryOwnershipTypeOrganizationOwnership{
			Typename: helpers.StringPtr("OrganizationOwnership"),
			QueryOwnershipOrganizationOwnership: humiographql.QueryOwnershipOrganizationOwnership{
				Typename: helpers.StringPtr("OrganizationOwnership"),
			},
		},
	}
	return nil
}

func (h *MockClientConfig) UpdateFilterAlert(_ context.Context, _ *humioapi.Client, hfa *humiov1alpha1.HumioFilterAlert) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", hfa.Spec.ManagedClusterName, hfa.Spec.ExternalClusterName),
		searchDomainName: hfa.Spec.ViewName,
		resourceName:     hfa.Spec.Name,
	}

	currentFilterAlert, found := h.apiClient.FilterAlert[key]

	if !found {
		return fmt.Errorf("could not find filter alert in view %q with name %q, err=%w", hfa.Spec.ViewName, hfa.Spec.Name, humioapi.EntityNotFound{})
	}

	h.apiClient.FilterAlert[key] = humiographql.FilterAlertDetails{
		Id:                  currentFilterAlert.GetId(),
		Name:                hfa.Spec.Name,
		Description:         &hfa.Spec.Description,
		QueryString:         hfa.Spec.QueryString,
		ThrottleTimeSeconds: helpers.Int64Ptr(int64(hfa.Spec.ThrottleTimeSeconds)),
		ThrottleField:       hfa.Spec.ThrottleField,
		Labels:              hfa.Spec.Labels,
		Enabled:             hfa.Spec.Enabled,
		Actions:             humioapi.ActionNamesToEmailActions(hfa.Spec.Actions),
		QueryOwnership: &humiographql.SharedQueryOwnershipTypeOrganizationOwnership{
			Typename: helpers.StringPtr("OrganizationOwnership"),
			QueryOwnershipOrganizationOwnership: humiographql.QueryOwnershipOrganizationOwnership{
				Typename: helpers.StringPtr("OrganizationOwnership"),
			},
		},
	}
	return nil
}

func (h *MockClientConfig) DeleteFilterAlert(_ context.Context, _ *humioapi.Client, hfa *humiov1alpha1.HumioFilterAlert) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", hfa.Spec.ManagedClusterName, hfa.Spec.ExternalClusterName),
		searchDomainName: hfa.Spec.ViewName,
		resourceName:     hfa.Spec.Name,
	}

	delete(h.apiClient.FilterAlert, key)
	return nil
}

func (h *MockClientConfig) ValidateActionsForFilterAlert(context.Context, *humioapi.Client, *humiov1alpha1.HumioFilterAlert) error {
	return nil
}

func (h *MockClientConfig) GetFeatureFlags(_ context.Context, _ *humioapi.Client) ([]string, error) {
	return []string{"ArrayFunctions"}, nil
}

func (h *MockClientConfig) EnableFeatureFlag(_ context.Context, _ *humioapi.Client, featureFlag *humiov1alpha1.HumioFeatureFlag) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:  fmt.Sprintf("%s%s", featureFlag.Spec.ManagedClusterName, featureFlag.Spec.ExternalClusterName),
		resourceName: featureFlag.Spec.Name,
	}

	h.apiClient.FeatureFlag[key] = true
	return nil
}

func (h *MockClientConfig) IsFeatureFlagEnabled(_ context.Context, _ *humioapi.Client, featureFlag *humiov1alpha1.HumioFeatureFlag) (bool, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()
	supportedFlag := resourceKey{
		clusterName:  fmt.Sprintf("%s%s", featureFlag.Spec.ManagedClusterName, featureFlag.Spec.ExternalClusterName),
		resourceName: "ArrayFunctions",
	}
	if _, found := h.apiClient.FeatureFlag[supportedFlag]; !found {
		h.apiClient.FeatureFlag[supportedFlag] = false
	}

	key := resourceKey{
		clusterName:  fmt.Sprintf("%s%s", featureFlag.Spec.ManagedClusterName, featureFlag.Spec.ExternalClusterName),
		resourceName: featureFlag.Spec.Name,
	}
	if value, found := h.apiClient.FeatureFlag[key]; found {
		return value, nil
	}
	return false, fmt.Errorf("could not find feature flag with name %q, err=%w", featureFlag.Spec.Name, humioapi.EntityNotFound{})
}

func (h *MockClientConfig) DisableFeatureFlag(_ context.Context, _ *humioapi.Client, featureFlag *humiov1alpha1.HumioFeatureFlag) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:  fmt.Sprintf("%s%s", featureFlag.Spec.ManagedClusterName, featureFlag.Spec.ExternalClusterName),
		resourceName: featureFlag.Spec.Name,
	}

	h.apiClient.FeatureFlag[key] = false
	return nil
}

func (h *MockClientConfig) GetAggregateAlert(_ context.Context, _ *humioapi.Client, haa *humiov1alpha1.HumioAggregateAlert) (*humiographql.AggregateAlertDetails, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", haa.Spec.ManagedClusterName, haa.Spec.ExternalClusterName),
		searchDomainName: haa.Spec.ViewName,
		resourceName:     haa.Spec.Name,
	}
	if value, found := h.apiClient.AggregateAlert[key]; found {
		return &value, nil

	}
	return nil, fmt.Errorf("could not find aggregate alert in view %q with name %q, err=%w", haa.Spec.ViewName, haa.Spec.Name, humioapi.EntityNotFound{})
}

func (h *MockClientConfig) AddAggregateAlert(ctx context.Context, client *humioapi.Client, haa *humiov1alpha1.HumioAggregateAlert) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", haa.Spec.ManagedClusterName, haa.Spec.ExternalClusterName),
		searchDomainName: haa.Spec.ViewName,
		resourceName:     haa.Spec.Name,
	}

	if _, found := h.apiClient.AggregateAlert[key]; found {
		return fmt.Errorf("aggregate alert already exists with name %s", haa.Spec.Name)
	}
	if err := h.ValidateActionsForAggregateAlert(ctx, client, haa); err != nil {
		return fmt.Errorf("could not get action id mapping: %w", err)
	}

	h.apiClient.AggregateAlert[key] = humiographql.AggregateAlertDetails{
		Id:                    kubernetes.RandomString(),
		Name:                  haa.Spec.Name,
		Description:           &haa.Spec.Description,
		QueryString:           haa.Spec.QueryString,
		SearchIntervalSeconds: int64(haa.Spec.SearchIntervalSeconds),
		ThrottleTimeSeconds:   int64(haa.Spec.ThrottleTimeSeconds),
		ThrottleField:         haa.Spec.ThrottleField,
		Labels:                haa.Spec.Labels,
		Enabled:               haa.Spec.Enabled,
		TriggerMode:           humiographql.TriggerMode(haa.Spec.TriggerMode),
		QueryTimestampType:    humiographql.QueryTimestampType(haa.Spec.QueryTimestampType),
		Actions:               humioapi.ActionNamesToEmailActions(haa.Spec.Actions),
		QueryOwnership: &humiographql.SharedQueryOwnershipTypeOrganizationOwnership{
			Typename: helpers.StringPtr("OrganizationOwnership"),
			QueryOwnershipOrganizationOwnership: humiographql.QueryOwnershipOrganizationOwnership{
				Typename: helpers.StringPtr("OrganizationOwnership"),
			},
		},
	}
	return nil
}

func (h *MockClientConfig) UpdateAggregateAlert(_ context.Context, _ *humioapi.Client, haa *humiov1alpha1.HumioAggregateAlert) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", haa.Spec.ManagedClusterName, haa.Spec.ExternalClusterName),
		searchDomainName: haa.Spec.ViewName,
		resourceName:     haa.Spec.Name,
	}

	currentAggregateAlert, found := h.apiClient.AggregateAlert[key]

	if !found {
		return fmt.Errorf("could not find aggregate alert in view %q with name %q, err=%w", haa.Spec.ViewName, haa.Spec.Name, humioapi.EntityNotFound{})
	}

	h.apiClient.AggregateAlert[key] = humiographql.AggregateAlertDetails{
		Id:                    currentAggregateAlert.GetId(),
		Name:                  haa.Spec.Name,
		Description:           &haa.Spec.Description,
		QueryString:           haa.Spec.QueryString,
		SearchIntervalSeconds: int64(haa.Spec.SearchIntervalSeconds),
		ThrottleTimeSeconds:   int64(haa.Spec.ThrottleTimeSeconds),
		ThrottleField:         haa.Spec.ThrottleField,
		Labels:                haa.Spec.Labels,
		Enabled:               haa.Spec.Enabled,
		TriggerMode:           humiographql.TriggerMode(haa.Spec.TriggerMode),
		QueryTimestampType:    humiographql.QueryTimestampType(haa.Spec.QueryTimestampType),
		Actions:               humioapi.ActionNamesToEmailActions(haa.Spec.Actions),
		QueryOwnership: &humiographql.SharedQueryOwnershipTypeOrganizationOwnership{
			Typename: helpers.StringPtr("OrganizationOwnership"),
			QueryOwnershipOrganizationOwnership: humiographql.QueryOwnershipOrganizationOwnership{
				Typename: helpers.StringPtr("OrganizationOwnership"),
			},
		},
	}
	return nil
}

func (h *MockClientConfig) DeleteAggregateAlert(_ context.Context, _ *humioapi.Client, haa *humiov1alpha1.HumioAggregateAlert) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", haa.Spec.ManagedClusterName, haa.Spec.ExternalClusterName),
		searchDomainName: haa.Spec.ViewName,
		resourceName:     haa.Spec.Name,
	}

	delete(h.apiClient.AggregateAlert, key)
	return nil
}

func (h *MockClientConfig) ValidateActionsForAggregateAlert(_ context.Context, _ *humioapi.Client, _ *humiov1alpha1.HumioAggregateAlert) error {
	return nil
}

func (h *MockClientConfig) AddScheduledSearch(_ context.Context, _ *humioapi.Client, hss *humiov1alpha1.HumioScheduledSearch) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", hss.Spec.ManagedClusterName, hss.Spec.ExternalClusterName)
	if !h.searchDomainNameExists(clusterName, hss.Spec.ViewName) {
		return fmt.Errorf("search domain name does not exist")
	}

	key := resourceKey{
		clusterName:      clusterName,
		searchDomainName: hss.Spec.ViewName,
		resourceName:     hss.Spec.Name,
	}

	if _, found := h.apiClient.ScheduledSearch[key]; found {
		return fmt.Errorf("scheduled search already exists with name %s", hss.Spec.Name)
	}

	h.apiClient.ScheduledSearch[key] = humiographql.ScheduledSearchDetails{
		Id:            kubernetes.RandomString(),
		Name:          hss.Spec.Name,
		Description:   &hss.Spec.Description,
		QueryString:   hss.Spec.QueryString,
		Start:         hss.Spec.QueryStart,
		End:           hss.Spec.QueryEnd,
		TimeZone:      hss.Spec.TimeZone,
		Schedule:      hss.Spec.Schedule,
		BackfillLimit: hss.Spec.BackfillLimit,
		Enabled:       hss.Spec.Enabled,
		Labels:        hss.Spec.Labels,
		QueryOwnership: &humiographql.SharedQueryOwnershipTypeOrganizationOwnership{
			Typename: helpers.StringPtr("OrganizationOwnership"),
			QueryOwnershipOrganizationOwnership: humiographql.QueryOwnershipOrganizationOwnership{
				Typename: helpers.StringPtr("OrganizationOwnership"),
			},
		},
		ActionsV2: humioapi.ActionNamesToEmailActions(hss.Spec.Actions),
	}
	return nil
}

func (h *MockClientConfig) AddScheduledSearchV2(_ context.Context, _ *humioapi.Client, hss *humiov1beta1.HumioScheduledSearch) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", hss.Spec.ManagedClusterName, hss.Spec.ExternalClusterName)
	if !h.searchDomainNameExists(clusterName, hss.Spec.ViewName) {
		return fmt.Errorf("search domain name does not exist")
	}

	key := resourceKey{
		clusterName:      clusterName,
		searchDomainName: hss.Spec.ViewName,
		resourceName:     hss.Spec.Name,
	}

	if _, found := h.apiClient.ScheduledSearchV2[key]; found {
		return fmt.Errorf("scheduled search already exists with name %s", hss.Spec.Name)
	}

	h.apiClient.ScheduledSearchV2[key] = humiographql.ScheduledSearchDetailsV2{
		Id:                          kubernetes.RandomString(),
		Name:                        hss.Spec.Name,
		Description:                 &hss.Spec.Description,
		QueryString:                 hss.Spec.QueryString,
		SearchIntervalSeconds:       hss.Spec.SearchIntervalSeconds,
		SearchIntervalOffsetSeconds: hss.Spec.SearchIntervalOffsetSeconds,
		MaxWaitTimeSeconds:          helpers.Int64Ptr(hss.Spec.MaxWaitTimeSeconds),
		TimeZone:                    hss.Spec.TimeZone,
		Schedule:                    hss.Spec.Schedule,
		BackfillLimitV2:             hss.Spec.BackfillLimit,
		Enabled:                     hss.Spec.Enabled,
		Labels:                      hss.Spec.Labels,
		QueryOwnership: &humiographql.SharedQueryOwnershipTypeOrganizationOwnership{
			Typename: helpers.StringPtr("OrganizationOwnership"),
			QueryOwnershipOrganizationOwnership: humiographql.QueryOwnershipOrganizationOwnership{
				Typename: helpers.StringPtr("OrganizationOwnership"),
			},
		},
		ActionsV2:          humioapi.ActionNamesToEmailActions(hss.Spec.Actions),
		QueryTimestampType: hss.Spec.QueryTimestampType,
	}
	return nil
}

func (h *MockClientConfig) GetScheduledSearch(_ context.Context, _ *humioapi.Client, hss *humiov1alpha1.HumioScheduledSearch) (*humiographql.ScheduledSearchDetails, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", hss.Spec.ManagedClusterName, hss.Spec.ExternalClusterName),
		searchDomainName: hss.Spec.ViewName,
		resourceName:     hss.Spec.Name,
	}
	if value, found := h.apiClient.ScheduledSearch[key]; found {
		return &value, nil

	}
	return nil, fmt.Errorf("could not find scheduled search in view %q with name %q, err=%w", hss.Spec.ViewName, hss.Spec.Name, humioapi.EntityNotFound{})
}

func (h *MockClientConfig) GetScheduledSearchV2(_ context.Context, _ *humioapi.Client, hss *humiov1beta1.HumioScheduledSearch) (*humiographql.ScheduledSearchDetailsV2, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", hss.Spec.ManagedClusterName, hss.Spec.ExternalClusterName),
		searchDomainName: hss.Spec.ViewName,
		resourceName:     hss.Spec.Name,
	}
	if value, found := h.apiClient.ScheduledSearchV2[key]; found {
		return &value, nil

	}
	return nil, fmt.Errorf("could not find scheduled search in view %q with name %q, err=%w", hss.Spec.ViewName, hss.Spec.Name, humioapi.EntityNotFound{})
}

func (h *MockClientConfig) UpdateScheduledSearch(_ context.Context, _ *humioapi.Client, hss *humiov1alpha1.HumioScheduledSearch) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", hss.Spec.ManagedClusterName, hss.Spec.ExternalClusterName),
		searchDomainName: hss.Spec.ViewName,
		resourceName:     hss.Spec.Name,
	}

	currentScheduledSearch, found := h.apiClient.ScheduledSearch[key]

	if !found {
		return fmt.Errorf("could not find scheduled search in view %q with name %q, err=%w", hss.Spec.ViewName, hss.Spec.Name, humioapi.EntityNotFound{})
	}

	h.apiClient.ScheduledSearch[key] = humiographql.ScheduledSearchDetails{
		Id:            currentScheduledSearch.GetId(),
		Name:          hss.Spec.Name,
		Description:   &hss.Spec.Description,
		QueryString:   hss.Spec.QueryString,
		Start:         hss.Spec.QueryStart,
		End:           hss.Spec.QueryEnd,
		TimeZone:      hss.Spec.TimeZone,
		Schedule:      hss.Spec.Schedule,
		BackfillLimit: hss.Spec.BackfillLimit,
		Enabled:       hss.Spec.Enabled,
		Labels:        hss.Spec.Labels,
		QueryOwnership: &humiographql.SharedQueryOwnershipTypeOrganizationOwnership{
			Typename: helpers.StringPtr("OrganizationOwnership"),
			QueryOwnershipOrganizationOwnership: humiographql.QueryOwnershipOrganizationOwnership{
				Typename: helpers.StringPtr("OrganizationOwnership"),
			},
		},
		ActionsV2: humioapi.ActionNamesToEmailActions(hss.Spec.Actions),
	}
	return nil
}

func (h *MockClientConfig) UpdateScheduledSearchV2(_ context.Context, _ *humioapi.Client, hss *humiov1beta1.HumioScheduledSearch) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", hss.Spec.ManagedClusterName, hss.Spec.ExternalClusterName),
		searchDomainName: hss.Spec.ViewName,
		resourceName:     hss.Spec.Name,
	}

	currentScheduledSearch, found := h.apiClient.ScheduledSearchV2[key]

	if !found {
		return fmt.Errorf("could not find scheduled search in view %q with name %q, err=%w", hss.Spec.ViewName, hss.Spec.Name, humioapi.EntityNotFound{})
	}

	h.apiClient.ScheduledSearchV2[key] = humiographql.ScheduledSearchDetailsV2{
		Id:                          currentScheduledSearch.GetId(),
		Name:                        hss.Spec.Name,
		Description:                 &hss.Spec.Description,
		QueryString:                 hss.Spec.QueryString,
		SearchIntervalSeconds:       hss.Spec.SearchIntervalSeconds,
		SearchIntervalOffsetSeconds: hss.Spec.SearchIntervalOffsetSeconds,
		MaxWaitTimeSeconds:          helpers.Int64Ptr(hss.Spec.MaxWaitTimeSeconds),
		TimeZone:                    hss.Spec.TimeZone,
		Schedule:                    hss.Spec.Schedule,
		BackfillLimitV2:             hss.Spec.BackfillLimit,
		Enabled:                     hss.Spec.Enabled,
		Labels:                      hss.Spec.Labels,
		QueryOwnership: &humiographql.SharedQueryOwnershipTypeOrganizationOwnership{
			Typename: helpers.StringPtr("OrganizationOwnership"),
			QueryOwnershipOrganizationOwnership: humiographql.QueryOwnershipOrganizationOwnership{
				Typename: helpers.StringPtr("OrganizationOwnership"),
			},
		},
		ActionsV2:          humioapi.ActionNamesToEmailActions(hss.Spec.Actions),
		QueryTimestampType: hss.Spec.QueryTimestampType,
	}
	return nil
}

func (h *MockClientConfig) DeleteScheduledSearch(_ context.Context, _ *humioapi.Client, hss *humiov1alpha1.HumioScheduledSearch) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", hss.Spec.ManagedClusterName, hss.Spec.ExternalClusterName),
		searchDomainName: hss.Spec.ViewName,
		resourceName:     hss.Spec.Name,
	}

	delete(h.apiClient.ScheduledSearch, key)
	return nil
}

func (h *MockClientConfig) DeleteScheduledSearchV2(_ context.Context, _ *humioapi.Client, hss *humiov1beta1.HumioScheduledSearch) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", hss.Spec.ManagedClusterName, hss.Spec.ExternalClusterName),
		searchDomainName: hss.Spec.ViewName,
		resourceName:     hss.Spec.Name,
	}

	delete(h.apiClient.ScheduledSearchV2, key)
	return nil
}

func (h *MockClientConfig) ValidateActionsForScheduledSearch(context.Context, *humioapi.Client, *humiov1alpha1.HumioScheduledSearch) error {
	return nil
}

func (h *MockClientConfig) ValidateActionsForScheduledSearchV2(context.Context, *humioapi.Client, *humiov1beta1.HumioScheduledSearch) error {
	return nil
}

func (h *MockClientConfig) AddSavedQuery(_ context.Context, _ *humioapi.Client, hsq *humiov1alpha1.HumioSavedQuery, includeDescriptionAndLabels bool) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", hsq.Spec.ManagedClusterName, hsq.Spec.ExternalClusterName)
	if !h.searchDomainNameExists(clusterName, hsq.Spec.ViewName) {
		return fmt.Errorf("view or repository %s does not exist", hsq.Spec.ViewName)
	}

	key := resourceKey{
		clusterName:      clusterName,
		searchDomainName: hsq.Spec.ViewName,
		resourceName:     hsq.Spec.Name,
	}

	query := humiographql.SavedQueryDetails{
		Id:          kubernetes.RandomString(),
		Name:        hsq.Spec.Name,
		DisplayName: hsq.Spec.Name,
		Query: humiographql.SavedQueryDetailsQueryHumioQuery{
			QueryString: hsq.Spec.QueryString,
		},
	}

	// Note: includeDescriptionAndLabels parameter is kept for backward compatibility
	// but Description and Labels fields are not stored in mock since GraphQL schema
	// doesn't support them in versions < 1.200

	h.apiClient.SavedQuery[key] = query
	return nil
}

func (h *MockClientConfig) GetSavedQuery(_ context.Context, _ *humioapi.Client, hsq *humiov1alpha1.HumioSavedQuery) (*humiographql.SavedQueryDetails, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", hsq.Spec.ManagedClusterName, hsq.Spec.ExternalClusterName)

	// Check if the view/repository exists first
	viewKey := resourceKey{
		clusterName:      clusterName,
		searchDomainName: "",
		resourceName:     hsq.Spec.ViewName,
	}
	_, viewExists := h.apiClient.Repository[viewKey]
	if !viewExists {
		return nil, humioapi.SearchDomainNotFound(hsq.Spec.ViewName)
	}

	// Now check if the saved query exists
	key := resourceKey{
		clusterName:      clusterName,
		searchDomainName: hsq.Spec.ViewName,
		resourceName:     hsq.Spec.Name,
	}

	savedQuery, found := h.apiClient.SavedQuery[key]
	if !found {
		return nil, humioapi.SavedQueryNotFound(hsq.Spec.Name)
	}

	return &savedQuery, nil
}

func (h *MockClientConfig) UpdateSavedQuery(_ context.Context, _ *humioapi.Client, hsq *humiov1alpha1.HumioSavedQuery, includeDescriptionAndLabels bool) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", hsq.Spec.ManagedClusterName, hsq.Spec.ExternalClusterName),
		searchDomainName: hsq.Spec.ViewName,
		resourceName:     hsq.Spec.Name,
	}

	savedQuery, found := h.apiClient.SavedQuery[key]
	if !found {
		return humioapi.SavedQueryNotFound(hsq.Spec.Name)
	}

	// Update queryString (always)
	savedQuery.Query = humiographql.SavedQueryDetailsQueryHumioQuery{
		QueryString: hsq.Spec.QueryString,
	}

	// Note: includeDescriptionAndLabels parameter is kept for backward compatibility
	// but Description and Labels fields are not updated in mock since GraphQL schema
	// doesn't support them in versions < 1.200

	h.apiClient.SavedQuery[key] = savedQuery
	return nil
}

func (h *MockClientConfig) DeleteSavedQuery(_ context.Context, _ *humioapi.Client, hsq *humiov1alpha1.HumioSavedQuery) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", hsq.Spec.ManagedClusterName, hsq.Spec.ExternalClusterName),
		searchDomainName: hsq.Spec.ViewName,
		resourceName:     hsq.Spec.Name,
	}

	delete(h.apiClient.SavedQuery, key)
	return nil
}

// V2 API methods with description and labels support (LogScale 1.200+)

func (h *MockClientConfig) AddSavedQueryV2(_ context.Context, _ *humioapi.Client, hsq *humiov1alpha1.HumioSavedQuery) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", hsq.Spec.ManagedClusterName, hsq.Spec.ExternalClusterName)
	if !h.searchDomainNameExists(clusterName, hsq.Spec.ViewName) {
		return fmt.Errorf("view or repository %s does not exist", hsq.Spec.ViewName)
	}

	key := resourceKey{
		clusterName:      clusterName,
		searchDomainName: hsq.Spec.ViewName,
		resourceName:     hsq.Spec.Name,
	}

	query := humiographql.SavedQueryDetailsV2{
		Id:          kubernetes.RandomString(),
		Name:        hsq.Spec.Name,
		DisplayName: hsq.Spec.Name,
		Query: humiographql.SavedQueryDetailsV2QueryHumioQuery{
			QueryString: hsq.Spec.QueryString,
		},
	}

	if hsq.Spec.Description != "" {
		query.Description = &hsq.Spec.Description
	}
	if len(hsq.Spec.Labels) > 0 {
		query.Labels = hsq.Spec.Labels
	}

	// Initialize map if needed
	if h.apiClient.SavedQueryV2 == nil {
		h.apiClient.SavedQueryV2 = make(map[resourceKey]humiographql.SavedQueryDetailsV2)
	}
	h.apiClient.SavedQueryV2[key] = query
	return nil
}

func (h *MockClientConfig) GetSavedQueryV2(_ context.Context, _ *humioapi.Client, hsq *humiov1alpha1.HumioSavedQuery) (*humiographql.SavedQueryDetailsV2, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", hsq.Spec.ManagedClusterName, hsq.Spec.ExternalClusterName)

	// Check if the view/repository exists first
	viewKey := resourceKey{
		clusterName:      clusterName,
		searchDomainName: "",
		resourceName:     hsq.Spec.ViewName,
	}
	_, viewExists := h.apiClient.Repository[viewKey]
	if !viewExists {
		return nil, humioapi.SearchDomainNotFound(hsq.Spec.ViewName)
	}

	// Check V2 map first
	key := resourceKey{
		clusterName:      clusterName,
		searchDomainName: hsq.Spec.ViewName,
		resourceName:     hsq.Spec.Name,
	}
	if savedQuery, found := h.apiClient.SavedQueryV2[key]; found {
		return &savedQuery, nil
	}

	return nil, humioapi.SavedQueryNotFound(hsq.Spec.Name)
}

func (h *MockClientConfig) UpdateSavedQueryV2(_ context.Context, _ *humioapi.Client, hsq *humiov1alpha1.HumioSavedQuery) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", hsq.Spec.ManagedClusterName, hsq.Spec.ExternalClusterName),
		searchDomainName: hsq.Spec.ViewName,
		resourceName:     hsq.Spec.Name,
	}

	savedQuery, found := h.apiClient.SavedQueryV2[key]
	if !found {
		return humioapi.SavedQueryNotFound(hsq.Spec.Name)
	}

	// Update queryString (always)
	savedQuery.Query = humiographql.SavedQueryDetailsV2QueryHumioQuery{
		QueryString: hsq.Spec.QueryString,
	}

	// Update description and labels
	if hsq.Spec.Description != "" {
		savedQuery.Description = &hsq.Spec.Description
	} else {
		savedQuery.Description = nil
	}
	if len(hsq.Spec.Labels) > 0 {
		savedQuery.Labels = hsq.Spec.Labels
	} else {
		savedQuery.Labels = []string{}
	}

	h.apiClient.SavedQueryV2[key] = savedQuery
	return nil
}

func (h *MockClientConfig) GetHumioHttpClient(_ *humioapi.Config, _ ctrl.Request) *humioapi.Client {
	clusterURL, _ := url.Parse("http://localhost:8080/")
	return humioapi.NewClient(humioapi.Config{Address: clusterURL})
}

// searchDomainNameExists returns a boolean if either a repository or view exists with the given search domain name.
// It assumes the caller already holds the lock humioClientMu.
func (h *MockClientConfig) searchDomainNameExists(clusterName, searchDomainName string) bool {
	key := resourceKey{
		clusterName:  clusterName,
		resourceName: searchDomainName,
	}

	if _, found := h.apiClient.Repository[key]; found {
		return true
	}

	if _, found := h.apiClient.View[key]; found {
		return true
	}

	if _, found := h.apiClient.MultiClusterSearchView[key]; found {
		return true
	}

	return false
}

func (h *MockClientConfig) GetUserIDForUsername(_ context.Context, _ *humioapi.Client, req reconcile.Request, _ string) (string, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		resourceName: fmt.Sprintf("%s%s", req.Namespace, req.Name),
	}

	currentUserID, found := h.apiClient.AdminUserID[key]
	if !found {
		return "", humioapi.EntityNotFound{}
	}

	return currentUserID, nil
}

func (h *MockClientConfig) RotateUserApiTokenAndGet(_ context.Context, _ *humioapi.Client, req reconcile.Request, _ string) (string, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		resourceName: fmt.Sprintf("%s%s", req.Namespace, req.Name),
	}

	currentUserID, found := h.apiClient.AdminUserID[key]
	if !found {
		return "", fmt.Errorf("could not find user")
	}

	return currentUserID, nil
}

func (h *MockClientConfig) AddUserAndGetUserID(_ context.Context, _ *humioapi.Client, req reconcile.Request, _ string, _ bool) (string, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		resourceName: fmt.Sprintf("%s%s", req.Namespace, req.Name),
	}

	h.apiClient.AdminUserID[key] = kubernetes.RandomString()
	return h.apiClient.AdminUserID[key], nil
}

func (h *MockClientConfig) AddUser(ctx context.Context, client *humioapi.Client, hu *humiov1alpha1.HumioUser) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:  fmt.Sprintf("%s%s", hu.Spec.ManagedClusterName, hu.Spec.ExternalClusterName),
		resourceName: hu.Spec.UserName,
	}

	if _, found := h.apiClient.User[key]; found {
		return fmt.Errorf("user already exists with username %q", hu.Spec.UserName)
	}

	value := &humiographql.UserDetails{
		Id:       kubernetes.RandomString(),
		Username: hu.Spec.UserName,
		IsRoot:   helpers.BoolFalse(hu.Spec.IsRoot),
	}

	h.apiClient.User[key] = *value
	return nil
}

func (h *MockClientConfig) GetUser(ctx context.Context, client *humioapi.Client, hu *humiov1alpha1.HumioUser) (*humiographql.UserDetails, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:  fmt.Sprintf("%s%s", hu.Spec.ManagedClusterName, hu.Spec.ExternalClusterName),
		resourceName: hu.Spec.UserName,
	}
	if value, found := h.apiClient.User[key]; found {
		return &value, nil
	}
	return nil, fmt.Errorf("could not find user with username %q, err=%w", hu.Spec.UserName, humioapi.EntityNotFound{})
}

func (h *MockClientConfig) UpdateUser(ctx context.Context, client *humioapi.Client, hu *humiov1alpha1.HumioUser) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:  fmt.Sprintf("%s%s", hu.Spec.ManagedClusterName, hu.Spec.ExternalClusterName),
		resourceName: hu.Spec.UserName,
	}

	currentUser, found := h.apiClient.User[key]

	if !found {
		return fmt.Errorf("could not find user with username %q, err=%w", hu.Spec.UserName, humioapi.EntityNotFound{})
	}

	value := &humiographql.UserDetails{
		Id:       currentUser.GetId(),
		Username: currentUser.GetUsername(),
		IsRoot:   helpers.BoolFalse(hu.Spec.IsRoot),
	}

	h.apiClient.User[key] = *value
	return nil
}

func (h *MockClientConfig) DeleteUser(ctx context.Context, client *humioapi.Client, hu *humiov1alpha1.HumioUser) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:  fmt.Sprintf("%s%s", hu.Spec.ManagedClusterName, hu.Spec.ExternalClusterName),
		resourceName: hu.Spec.UserName,
	}

	delete(h.apiClient.User, key)
	return nil
}

func (h *MockClientConfig) AddSystemPermissionRole(ctx context.Context, client *humioapi.Client, role *humiov1alpha1.HumioSystemPermissionRole) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:  fmt.Sprintf("%s%s", role.Spec.ManagedClusterName, role.Spec.ExternalClusterName),
		resourceName: role.Spec.Name,
	}

	if _, found := h.apiClient.Role[key]; found {
		return fmt.Errorf("role already exists with name %s", role.Spec.Name)
	}

	for idx := range role.Spec.Permissions {
		if !slices.Contains(humiographql.AllSystemPermission, humiographql.SystemPermission(role.Spec.Permissions[idx])) {
			// nolint:staticcheck // ST1005 - keep the capitalization the same as how LogScale responds
			return fmt.Errorf("Expected type 'SystemPermission!', found '%s'. Enum value '%s' is undefined in enum type 'SystemPermission'", role.Spec.Permissions[idx], role.Spec.Permissions[idx])
		}
	}
	systemPermissions := make([]humiographql.SystemPermission, len(role.Spec.Permissions))
	for idx := range role.Spec.Permissions {
		systemPermissions[idx] = humiographql.SystemPermission(role.Spec.Permissions[idx])
	}

	groups := make([]humiographql.RoleDetailsGroupsGroup, len(role.Spec.RoleAssignmentGroupNames))
	for idx := range role.Spec.RoleAssignmentGroupNames {
		groups[idx] = humiographql.RoleDetailsGroupsGroup{
			Id:          kubernetes.RandomString(),
			DisplayName: role.Spec.RoleAssignmentGroupNames[idx],
		}
	}

	h.apiClient.Role[key] = humiographql.RoleDetails{
		Id:                      kubernetes.RandomString(),
		DisplayName:             role.Spec.Name,
		ViewPermissions:         []humiographql.Permission{},
		OrganizationPermissions: nil,
		SystemPermissions:       systemPermissions,
		Groups:                  groups,
	}
	return nil
}

func (h *MockClientConfig) GetSystemPermissionRole(ctx context.Context, client *humioapi.Client, role *humiov1alpha1.HumioSystemPermissionRole) (*humiographql.RoleDetails, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:  fmt.Sprintf("%s%s", role.Spec.ManagedClusterName, role.Spec.ExternalClusterName),
		resourceName: role.Spec.Name,
	}
	if value, found := h.apiClient.Role[key]; found {
		return &value, nil

	}
	return nil, humioapi.SystemPermissionRoleNotFound(role.Spec.Name)
}

func (h *MockClientConfig) UpdateSystemPermissionRole(ctx context.Context, client *humioapi.Client, role *humiov1alpha1.HumioSystemPermissionRole) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:  fmt.Sprintf("%s%s", role.Spec.ManagedClusterName, role.Spec.ExternalClusterName),
		resourceName: role.Spec.Name,
	}

	currentRole, found := h.apiClient.Role[key]

	if !found {
		return humioapi.SystemPermissionRoleNotFound(role.Spec.Name)
	}

	for idx := range role.Spec.Permissions {
		if !slices.Contains(humiographql.AllSystemPermission, humiographql.SystemPermission(role.Spec.Permissions[idx])) {
			// nolint:staticcheck // ST1005 - keep the capitalization the same as how LogScale responds
			return fmt.Errorf("Expected type 'SystemPermission!', found '%s'. Enum value '%s' is undefined in enum type 'SystemPermission'", role.Spec.Permissions[idx], role.Spec.Permissions[idx])
		}
	}
	systemPermissions := make([]humiographql.SystemPermission, len(role.Spec.Permissions))
	for idx := range role.Spec.Permissions {
		systemPermissions[idx] = humiographql.SystemPermission(role.Spec.Permissions[idx])
	}

	groups := make([]humiographql.RoleDetailsGroupsGroup, len(role.Spec.RoleAssignmentGroupNames))
	for idx := range role.Spec.RoleAssignmentGroupNames {
		groups[idx] = humiographql.RoleDetailsGroupsGroup{
			Id:          kubernetes.RandomString(),
			DisplayName: role.Spec.RoleAssignmentGroupNames[idx],
		}
	}

	h.apiClient.Role[key] = humiographql.RoleDetails{
		Id:                      currentRole.GetId(),
		DisplayName:             role.Spec.Name,
		ViewPermissions:         []humiographql.Permission{},
		OrganizationPermissions: nil,
		SystemPermissions:       systemPermissions,
		Groups:                  groups,
	}
	return nil
}

func (h *MockClientConfig) DeleteSystemPermissionRole(ctx context.Context, client *humioapi.Client, role *humiov1alpha1.HumioSystemPermissionRole) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:  fmt.Sprintf("%s%s", role.Spec.ManagedClusterName, role.Spec.ExternalClusterName),
		resourceName: role.Spec.Name,
	}

	delete(h.apiClient.Role, key)
	return nil
}

func (h *MockClientConfig) AddOrganizationPermissionRole(ctx context.Context, client *humioapi.Client, role *humiov1alpha1.HumioOrganizationPermissionRole) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:  fmt.Sprintf("%s%s", role.Spec.ManagedClusterName, role.Spec.ExternalClusterName),
		resourceName: role.Spec.Name,
	}

	if _, found := h.apiClient.Role[key]; found {
		return fmt.Errorf("role already exists with name %s", role.Spec.Name)
	}

	for idx := range role.Spec.Permissions {
		if !slices.Contains(humiographql.AllOrganizationPermission, humiographql.OrganizationPermission(role.Spec.Permissions[idx])) {
			// nolint:staticcheck // ST1005 - keep the capitalization the same as how LogScale responds
			return fmt.Errorf("Expected type 'OrganizationPermission!', found '%s'. Enum value '%s' is undefined in enum type 'OrganizationPermission'", role.Spec.Permissions[idx], role.Spec.Permissions[idx])
		}
	}
	oraganizationPermissions := make([]humiographql.OrganizationPermission, len(role.Spec.Permissions))
	for idx := range role.Spec.Permissions {
		oraganizationPermissions[idx] = humiographql.OrganizationPermission(role.Spec.Permissions[idx])
	}

	groups := make([]humiographql.RoleDetailsGroupsGroup, len(role.Spec.RoleAssignmentGroupNames))
	for idx := range role.Spec.RoleAssignmentGroupNames {
		groups[idx] = humiographql.RoleDetailsGroupsGroup{
			Id:          kubernetes.RandomString(),
			DisplayName: role.Spec.RoleAssignmentGroupNames[idx],
		}
	}

	h.apiClient.Role[key] = humiographql.RoleDetails{
		Id:                      kubernetes.RandomString(),
		DisplayName:             role.Spec.Name,
		ViewPermissions:         []humiographql.Permission{},
		OrganizationPermissions: oraganizationPermissions,
		SystemPermissions:       nil,
		Groups:                  groups,
	}
	return nil
}

func (h *MockClientConfig) GetOrganizationPermissionRole(ctx context.Context, client *humioapi.Client, role *humiov1alpha1.HumioOrganizationPermissionRole) (*humiographql.RoleDetails, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:  fmt.Sprintf("%s%s", role.Spec.ManagedClusterName, role.Spec.ExternalClusterName),
		resourceName: role.Spec.Name,
	}
	if value, found := h.apiClient.Role[key]; found {
		return &value, nil

	}
	return nil, humioapi.OrganizationPermissionRoleNotFound(role.Spec.Name)
}

func (h *MockClientConfig) UpdateOrganizationPermissionRole(ctx context.Context, client *humioapi.Client, role *humiov1alpha1.HumioOrganizationPermissionRole) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:  fmt.Sprintf("%s%s", role.Spec.ManagedClusterName, role.Spec.ExternalClusterName),
		resourceName: role.Spec.Name,
	}

	currentRole, found := h.apiClient.Role[key]

	if !found {
		return humioapi.OrganizationPermissionRoleNotFound(role.Spec.Name)
	}

	for idx := range role.Spec.Permissions {
		if !slices.Contains(humiographql.AllOrganizationPermission, humiographql.OrganizationPermission(role.Spec.Permissions[idx])) {
			// nolint:staticcheck // ST1005 - keep the capitalization the same as how LogScale responds
			return fmt.Errorf("Expected type 'OrganizationPermission!', found '%s'. Enum value '%s' is undefined in enum type 'OrganizationPermission'", role.Spec.Permissions[idx], role.Spec.Permissions[idx])
		}
	}
	oraganizationPermissions := make([]humiographql.OrganizationPermission, len(role.Spec.Permissions))
	for idx := range role.Spec.Permissions {
		oraganizationPermissions[idx] = humiographql.OrganizationPermission(role.Spec.Permissions[idx])
	}

	groups := make([]humiographql.RoleDetailsGroupsGroup, len(role.Spec.RoleAssignmentGroupNames))
	for idx := range role.Spec.RoleAssignmentGroupNames {
		groups[idx] = humiographql.RoleDetailsGroupsGroup{
			Id:          kubernetes.RandomString(),
			DisplayName: role.Spec.RoleAssignmentGroupNames[idx],
		}
	}

	h.apiClient.Role[key] = humiographql.RoleDetails{
		Id:                      currentRole.GetId(),
		DisplayName:             role.Spec.Name,
		ViewPermissions:         []humiographql.Permission{},
		OrganizationPermissions: oraganizationPermissions,
		SystemPermissions:       nil,
		Groups:                  groups,
	}
	return nil
}

func (h *MockClientConfig) DeleteOrganizationPermissionRole(ctx context.Context, client *humioapi.Client, role *humiov1alpha1.HumioOrganizationPermissionRole) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:  fmt.Sprintf("%s%s", role.Spec.ManagedClusterName, role.Spec.ExternalClusterName),
		resourceName: role.Spec.Name,
	}

	delete(h.apiClient.Role, key)
	return nil
}

func (h *MockClientConfig) AddViewPermissionRole(ctx context.Context, client *humioapi.Client, role *humiov1alpha1.HumioViewPermissionRole) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:  fmt.Sprintf("%s%s", role.Spec.ManagedClusterName, role.Spec.ExternalClusterName),
		resourceName: role.Spec.Name,
	}

	if _, found := h.apiClient.Role[key]; found {
		return fmt.Errorf("role already exists with name %s", role.Spec.Name)
	}

	for idx := range role.Spec.Permissions {
		if !slices.Contains(humiographql.AllPermission, humiographql.Permission(role.Spec.Permissions[idx])) {
			// nolint:staticcheck // ST1005 - keep the capitalization the same as how LogScale responds
			return fmt.Errorf("Expected type 'Permission!', found '%s'. Enum value '%s' is undefined in enum type 'Permission'", role.Spec.Permissions[idx], role.Spec.Permissions[idx])
		}
	}
	viewPermissions := make([]humiographql.Permission, len(role.Spec.Permissions))
	for idx := range role.Spec.Permissions {
		viewPermissions[idx] = humiographql.Permission(role.Spec.Permissions[idx])
	}

	h.apiClient.Role[key] = humiographql.RoleDetails{
		Id:                      kubernetes.RandomString(),
		DisplayName:             role.Spec.Name,
		ViewPermissions:         viewPermissions,
		OrganizationPermissions: nil,
		SystemPermissions:       nil,
	}
	return nil
}

func (h *MockClientConfig) GetViewPermissionRole(ctx context.Context, client *humioapi.Client, role *humiov1alpha1.HumioViewPermissionRole) (*humiographql.RoleDetails, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:  fmt.Sprintf("%s%s", role.Spec.ManagedClusterName, role.Spec.ExternalClusterName),
		resourceName: role.Spec.Name,
	}
	if value, found := h.apiClient.Role[key]; found {
		return &value, nil

	}
	return nil, humioapi.ViewPermissionRoleNotFound(role.Spec.Name)
}

func (h *MockClientConfig) UpdateViewPermissionRole(ctx context.Context, client *humioapi.Client, role *humiov1alpha1.HumioViewPermissionRole) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:  fmt.Sprintf("%s%s", role.Spec.ManagedClusterName, role.Spec.ExternalClusterName),
		resourceName: role.Spec.Name,
	}

	currentRole, found := h.apiClient.Role[key]

	if !found {
		return humioapi.ViewPermissionRoleNotFound(role.Spec.Name)
	}

	for idx := range role.Spec.Permissions {
		if !slices.Contains(humiographql.AllPermission, humiographql.Permission(role.Spec.Permissions[idx])) {
			// nolint:staticcheck // ST1005 - keep the capitalization the same as how LogScale responds
			return fmt.Errorf("Expected type 'Permission!', found '%s'. Enum value '%s' is undefined in enum type 'Permission'", role.Spec.Permissions[idx], role.Spec.Permissions[idx])
		}
	}
	viewPermissions := make([]humiographql.Permission, len(role.Spec.Permissions))
	for idx := range role.Spec.Permissions {
		viewPermissions[idx] = humiographql.Permission(role.Spec.Permissions[idx])
	}

	groups := make([]humiographql.RoleDetailsGroupsGroup, len(role.Spec.RoleAssignments))
	for idx := range role.Spec.RoleAssignments {
		groups[idx] = humiographql.RoleDetailsGroupsGroup{
			Id:          kubernetes.RandomString(),
			DisplayName: role.Spec.RoleAssignments[idx].GroupName,
			Roles: []humiographql.RoleDetailsGroupsGroupRolesSearchDomainRole{ // We can probably get away with just supporting a single role assignment per group in the mock client
				{
					Role: humiographql.RoleDetailsGroupsGroupRolesSearchDomainRoleRole{},
					SearchDomain: &humiographql.RoleDetailsGroupsGroupRolesSearchDomainRoleSearchDomainView{
						Typename: helpers.StringPtr("View"),
						Id:       kubernetes.RandomString(),
						Name:     role.Spec.RoleAssignments[idx].RepoOrViewName,
					},
				},
			},
		}
	}

	h.apiClient.Role[key] = humiographql.RoleDetails{
		Id:                      currentRole.GetId(),
		DisplayName:             role.Spec.Name,
		ViewPermissions:         viewPermissions,
		OrganizationPermissions: nil,
		SystemPermissions:       nil,
		Groups:                  groups,
	}
	return nil
}

func (h *MockClientConfig) DeleteViewPermissionRole(ctx context.Context, client *humioapi.Client, role *humiov1alpha1.HumioViewPermissionRole) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:  fmt.Sprintf("%s%s", role.Spec.ManagedClusterName, role.Spec.ExternalClusterName),
		resourceName: role.Spec.Name,
	}

	delete(h.apiClient.Role, key)
	return nil
}

func (h *MockClientConfig) AddIPFilter(ctx context.Context, client *humioapi.Client, ipFilter *humiov1alpha1.HumioIPFilter) (*humiographql.IPFilterDetails, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", ipFilter.Spec.ManagedClusterName, ipFilter.Spec.ExternalClusterName)

	key := resourceKey{
		clusterName:  clusterName,
		resourceName: ipFilter.Spec.Name,
	}
	if value, found := h.apiClient.IPFilter[key]; found {
		return &value, fmt.Errorf("IPFilter already exists with name %s", ipFilter.Spec.Name)
	}

	value := &humiographql.IPFilterDetails{
		Id:       ipFilter.Spec.Name,
		Name:     ipFilter.Spec.Name,
		IpFilter: helpers.FirewallRulesToString(ipFilter.Spec.IPFilter, "\n"),
	}

	h.apiClient.IPFilter[key] = *value

	return value, nil
}

func (h *MockClientConfig) GetIPFilter(ctx context.Context, _ *humioapi.Client, ipFilter *humiov1alpha1.HumioIPFilter) (*humiographql.IPFilterDetails, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", ipFilter.Spec.ManagedClusterName, ipFilter.Spec.ExternalClusterName)

	key := resourceKey{
		clusterName:  clusterName,
		resourceName: ipFilter.Spec.Name,
	}

	if value, found := h.apiClient.IPFilter[key]; found {
		return &value, nil
	}

	return nil, humioapi.IPFilterNotFound(ipFilter.Spec.Name)
}

func (h *MockClientConfig) UpdateIPFilter(ctx context.Context, _ *humioapi.Client, ipFilter *humiov1alpha1.HumioIPFilter) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", ipFilter.Spec.ManagedClusterName, ipFilter.Spec.ExternalClusterName)

	key := resourceKey{
		clusterName:  clusterName,
		resourceName: ipFilter.Spec.Name,
	}

	currentValue, found := h.apiClient.IPFilter[key]
	if !found {
		return humioapi.IPFilterNotFound(ipFilter.Spec.Name)
	}

	value := &humiographql.IPFilterDetails{
		Id:       currentValue.Id,
		Name:     ipFilter.Spec.Name,
		IpFilter: helpers.FirewallRulesToString(ipFilter.Spec.IPFilter, "\n"),
	}
	h.apiClient.IPFilter[key] = *value
	return nil
}

func (h *MockClientConfig) DeleteIPFilter(ctx context.Context, _ *humioapi.Client, ipFilter *humiov1alpha1.HumioIPFilter) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", ipFilter.Spec.ManagedClusterName, ipFilter.Spec.ExternalClusterName)

	key := resourceKey{
		clusterName:  clusterName,
		resourceName: ipFilter.Spec.Name,
	}
	delete(h.apiClient.IPFilter, key)
	return nil
}

func (h *MockClientConfig) CreateViewToken(ctx context.Context, client *humioapi.Client, viewToken *humiov1alpha1.HumioViewToken, ipFilter string, views []string, permissions []humiographql.Permission) (string, string, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", viewToken.Spec.ManagedClusterName, viewToken.Spec.ExternalClusterName)

	key := resourceKey{
		clusterName:  clusterName,
		resourceName: viewToken.Spec.Name,
	}
	if _, found := h.apiClient.ViewToken[key]; found {
		return "", "", fmt.Errorf("ViewToken already exists with name %s", viewToken.Spec.Name)
	}

	value := fmt.Sprintf("%s~%s", kubernetes.RandomString(), kubernetes.RandomString())
	parts := strings.Split(value, "~")

	var expireAt *int64
	if viewToken.Spec.ExpiresAt != nil {
		temp := viewToken.Spec.ExpiresAt.UnixMilli()
		expireAt = &temp
	} else {
		expireAt = nil
	}

	localViews := make([]humiographql.ViewTokenDetailsViewsSearchDomain, 0, len(views))
	for _, viewName := range views {
		view := &humiographql.ViewTokenDetailsViewsView{
			Typename: helpers.StringPtr("View"),
			Id:       viewName,
			Name:     viewName,
		}
		localViews = append(localViews, view)
	}

	perms := FixPermissions(viewToken.Spec.Permissions)
	response := &humiographql.ViewTokenDetailsViewPermissionsToken{
		TokenDetailsViewPermissionsToken: humiographql.TokenDetailsViewPermissionsToken{
			Id:       parts[0],
			Name:     viewToken.Spec.Name,
			ExpireAt: expireAt,
			IpFilterV2: &humiographql.TokenDetailsIpFilterV2IPFilter{
				Id: ipFilter,
			},
		},
		Permissions: perms,
		Views:       localViews,
	}
	h.apiClient.ViewToken[key] = *response
	return parts[0], value, nil
}

func (h *MockClientConfig) GetViewToken(ctx context.Context, client *humioapi.Client, viewToken *humiov1alpha1.HumioViewToken) (*humiographql.ViewTokenDetailsViewPermissionsToken, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", viewToken.Spec.ManagedClusterName, viewToken.Spec.ExternalClusterName)
	key := resourceKey{
		clusterName:  clusterName,
		resourceName: viewToken.Spec.Name,
	}
	if value, found := h.apiClient.ViewToken[key]; found {
		return &value, nil
	}
	return nil, humioapi.ViewTokenNotFound(viewToken.Spec.Name)
}

func (h *MockClientConfig) UpdateViewToken(ctx context.Context, client *humioapi.Client, viewToken *humiov1alpha1.HumioViewToken, permissions []humiographql.Permission) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", viewToken.Spec.ManagedClusterName, viewToken.Spec.ExternalClusterName)
	key := resourceKey{
		clusterName:  clusterName,
		resourceName: viewToken.Spec.Name,
	}
	currentValue, found := h.apiClient.ViewToken[key]
	if !found {
		return humioapi.ViewTokenNotFound(viewToken.Spec.Name)
	}
	perms := make([]string, 0, len(permissions))
	for _, p := range permissions {
		perms = append(perms, string(p))
	}
	currentValue.Permissions = FixPermissions(perms)
	h.apiClient.ViewToken[key] = currentValue

	return nil
}

func (h *MockClientConfig) DeleteViewToken(ctx context.Context, client *humioapi.Client, viewToken *humiov1alpha1.HumioViewToken) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", viewToken.Spec.ManagedClusterName, viewToken.Spec.ExternalClusterName)

	key := resourceKey{
		clusterName:  clusterName,
		resourceName: viewToken.Spec.Name,
	}
	delete(h.apiClient.ViewToken, key)
	return nil
}

func (h *MockClientConfig) RotateViewToken(ctx context.Context, client *humioapi.Client, viewToken *humiov1alpha1.HumioViewToken) (string, string, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()
	clusterName := fmt.Sprintf("%s%s", viewToken.Spec.ManagedClusterName, viewToken.Spec.ExternalClusterName)
	key := resourceKey{
		clusterName:  clusterName,
		resourceName: viewToken.Spec.Name,
	}
	tokenId := kubernetes.RandomString()
	secret := fmt.Sprintf("%s~%s", tokenId, kubernetes.RandomString())
	// on rotate un change the underlying Humio Token ID field
	value := h.apiClient.ViewToken[key]
	value.Id = tokenId
	h.apiClient.ViewToken[key] = value

	return tokenId, secret, nil
}

func (h *MockClientConfig) CreateSystemToken(ctx context.Context, client *humioapi.Client, systemToken *humiov1alpha1.HumioSystemToken, ipFilter string, permissions []humiographql.SystemPermission) (string, string, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", systemToken.Spec.ManagedClusterName, systemToken.Spec.ExternalClusterName)

	key := resourceKey{
		clusterName:  clusterName,
		resourceName: systemToken.Spec.Name,
	}
	if _, found := h.apiClient.SystemToken[key]; found {
		return "", "", fmt.Errorf("SystemToken already exists with name %s", systemToken.Spec.Name)
	}

	value := fmt.Sprintf("%s~%s", kubernetes.RandomString(), kubernetes.RandomString())
	parts := strings.Split(value, "~")

	var expireAt *int64
	if systemToken.Spec.ExpiresAt != nil {
		temp := systemToken.Spec.ExpiresAt.UnixMilli()
		expireAt = &temp
	} else {
		expireAt = nil
	}

	perms := systemToken.Spec.Permissions
	response := &humiographql.SystemTokenDetailsSystemPermissionsToken{
		TokenDetailsSystemPermissionsToken: humiographql.TokenDetailsSystemPermissionsToken{
			Id:       parts[0],
			Name:     systemToken.Spec.Name,
			ExpireAt: expireAt,
			IpFilterV2: &humiographql.TokenDetailsIpFilterV2IPFilter{
				Id: ipFilter,
			},
		},
		Permissions: perms,
	}
	h.apiClient.SystemToken[key] = *response
	return parts[0], value, nil
}

func (h *MockClientConfig) GetSystemToken(ctx context.Context, client *humioapi.Client, systemToken *humiov1alpha1.HumioSystemToken) (*humiographql.SystemTokenDetailsSystemPermissionsToken, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", systemToken.Spec.ManagedClusterName, systemToken.Spec.ExternalClusterName)
	key := resourceKey{
		clusterName:  clusterName,
		resourceName: systemToken.Spec.Name,
	}
	if value, found := h.apiClient.SystemToken[key]; found {
		return &value, nil
	}
	return nil, humioapi.SystemTokenNotFound(systemToken.Spec.Name)
}

func (h *MockClientConfig) UpdateSystemToken(ctx context.Context, client *humioapi.Client, systemToken *humiov1alpha1.HumioSystemToken, permissions []humiographql.SystemPermission) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", systemToken.Spec.ManagedClusterName, systemToken.Spec.ExternalClusterName)
	key := resourceKey{
		clusterName:  clusterName,
		resourceName: systemToken.Spec.Name,
	}
	currentValue, found := h.apiClient.SystemToken[key]
	if !found {
		return humioapi.SystemTokenNotFound(systemToken.Spec.Name)
	}

	perms := make([]string, 0, len(permissions))
	for _, p := range permissions {
		perms = append(perms, string(p))
	}
	currentValue.Permissions = perms
	h.apiClient.SystemToken[key] = currentValue

	return nil
}

func (h *MockClientConfig) DeleteSystemToken(ctx context.Context, client *humioapi.Client, systemToken *humiov1alpha1.HumioSystemToken) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", systemToken.Spec.ManagedClusterName, systemToken.Spec.ExternalClusterName)

	key := resourceKey{
		clusterName:  clusterName,
		resourceName: systemToken.Spec.Name,
	}
	delete(h.apiClient.SystemToken, key)
	return nil
}

func (h *MockClientConfig) RotateSystemToken(ctx context.Context, client *humioapi.Client, systemToken *humiov1alpha1.HumioSystemToken) (string, string, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()
	clusterName := fmt.Sprintf("%s%s", systemToken.Spec.ManagedClusterName, systemToken.Spec.ExternalClusterName)
	key := resourceKey{
		clusterName:  clusterName,
		resourceName: systemToken.Spec.Name,
	}
	tokenId := kubernetes.RandomString()
	secret := fmt.Sprintf("%s~%s", tokenId, kubernetes.RandomString())
	// on rotate un change the underlying Humio Token ID field
	value := h.apiClient.SystemToken[key]
	value.Id = tokenId
	h.apiClient.SystemToken[key] = value

	return tokenId, secret, nil
}

func (h *MockClientConfig) CreateOrganizationToken(ctx context.Context, client *humioapi.Client, orgToken *humiov1alpha1.HumioOrganizationToken, ipFilter string, permissions []humiographql.OrganizationPermission) (string, string, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", orgToken.Spec.ManagedClusterName, orgToken.Spec.ExternalClusterName)

	key := resourceKey{
		clusterName:  clusterName,
		resourceName: orgToken.Spec.Name,
	}
	if _, found := h.apiClient.OrganizationToken[key]; found {
		return "", "", fmt.Errorf("OrganizationToken already exists with name %s", orgToken.Spec.Name)
	}

	value := fmt.Sprintf("%s~%s", kubernetes.RandomString(), kubernetes.RandomString())
	parts := strings.Split(value, "~")

	var expireAt *int64
	if orgToken.Spec.ExpiresAt != nil {
		temp := orgToken.Spec.ExpiresAt.UnixMilli()
		expireAt = &temp
	} else {
		expireAt = nil
	}

	perms := orgToken.Spec.Permissions
	response := &humiographql.OrganizationTokenDetailsOrganizationPermissionsToken{
		TokenDetailsOrganizationPermissionsToken: humiographql.TokenDetailsOrganizationPermissionsToken{
			Id:       parts[0],
			Name:     orgToken.Spec.Name,
			ExpireAt: expireAt,
			IpFilterV2: &humiographql.TokenDetailsIpFilterV2IPFilter{
				Id: ipFilter,
			},
		},
		Permissions: perms,
	}
	h.apiClient.OrganizationToken[key] = *response
	return parts[0], value, nil
}

func (h *MockClientConfig) GetOrganizationToken(ctx context.Context, client *humioapi.Client, orgToken *humiov1alpha1.HumioOrganizationToken) (*humiographql.OrganizationTokenDetailsOrganizationPermissionsToken, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", orgToken.Spec.ManagedClusterName, orgToken.Spec.ExternalClusterName)
	key := resourceKey{
		clusterName:  clusterName,
		resourceName: orgToken.Spec.Name,
	}
	if value, found := h.apiClient.OrganizationToken[key]; found {
		return &value, nil
	}
	return nil, humioapi.OrganizationTokenNotFound(orgToken.Spec.Name)
}

func (h *MockClientConfig) UpdateOrganizationToken(ctx context.Context, client *humioapi.Client, orgToken *humiov1alpha1.HumioOrganizationToken, permissions []humiographql.OrganizationPermission) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", orgToken.Spec.ManagedClusterName, orgToken.Spec.ExternalClusterName)
	key := resourceKey{
		clusterName:  clusterName,
		resourceName: orgToken.Spec.Name,
	}
	currentValue, found := h.apiClient.OrganizationToken[key]
	if !found {
		return humioapi.OrganizationTokenNotFound(orgToken.Spec.Name)
	}

	perms := make([]string, 0, len(permissions))
	for _, p := range permissions {
		perms = append(perms, string(p))
	}
	currentValue.Permissions = perms
	h.apiClient.OrganizationToken[key] = currentValue

	return nil
}

func (h *MockClientConfig) DeleteOrganizationToken(ctx context.Context, client *humioapi.Client, orgToken *humiov1alpha1.HumioOrganizationToken) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", orgToken.Spec.ManagedClusterName, orgToken.Spec.ExternalClusterName)

	key := resourceKey{
		clusterName:  clusterName,
		resourceName: orgToken.Spec.Name,
	}
	delete(h.apiClient.OrganizationToken, key)
	return nil
}

func (h *MockClientConfig) RotateOrganizationToken(ctx context.Context, client *humioapi.Client, orgToken *humiov1alpha1.HumioOrganizationToken) (string, string, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()
	clusterName := fmt.Sprintf("%s%s", orgToken.Spec.ManagedClusterName, orgToken.Spec.ExternalClusterName)
	key := resourceKey{
		clusterName:  clusterName,
		resourceName: orgToken.Spec.Name,
	}
	tokenId := kubernetes.RandomString()
	secret := fmt.Sprintf("%s~%s", tokenId, kubernetes.RandomString())
	// on rotate un change the underlying Humio Token ID field
	value := h.apiClient.OrganizationToken[key]
	value.Id = tokenId
	h.apiClient.OrganizationToken[key] = value

	return tokenId, secret, nil
}

func (h *MockClientConfig) EnableTokenUpdatePermissionsForTests(ctx context.Context, client *humioapi.Client) error {
	return nil
}

func (h *MockClientConfig) AnalyzePackageFromZip(ctx context.Context, client *humioapi.Client, name, viewName string) (any, error) {
	return nil, nil
}

func (h *MockClientConfig) InstallPackageFromZip(ctx context.Context, client *humioapi.Client, hp *humiov1alpha1.HumioPackage, zipFilePath string, viewName string) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()
	clusterName := fmt.Sprintf("%s%s", hp.Spec.ManagedClusterName, hp.Spec.ExternalClusterName)
	key := resourceKey{
		clusterName:      clusterName,
		resourceName:     hp.Spec.GetPackageName(),
		searchDomainName: viewName,
	}
	packageDetails := humiographql.PackageDetails{
		Name:    hp.Spec.PackageName,
		Version: hp.Spec.PackageVersion,
	}
	h.apiClient.Package[key] = packageDetails
	return nil
}

func (h *MockClientConfig) UninstallPackage(ctx context.Context, client *humioapi.Client, hp *humiov1alpha1.HumioPackage, viewName string) (bool, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()
	clusterName := fmt.Sprintf("%s%s", hp.Spec.ManagedClusterName, hp.Spec.ExternalClusterName)
	key := resourceKey{
		clusterName:      clusterName,
		resourceName:     hp.Spec.GetPackageName(),
		searchDomainName: viewName,
	}
	if _, found := h.apiClient.Package[key]; found {
		delete(h.apiClient.Package, key)
		return true, nil
	}
	return false, fmt.Errorf("package is not installed")
}

func (h *MockClientConfig) CheckPackage(ctx context.Context, client *humioapi.Client, hp *humiov1alpha1.HumioPackage, viewName string) (*humiographql.PackageDetails, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()
	clusterName := fmt.Sprintf("%s%s", hp.Spec.ManagedClusterName, hp.Spec.ExternalClusterName)
	key := resourceKey{
		clusterName:      clusterName,
		resourceName:     hp.Spec.GetPackageName(),
		searchDomainName: viewName,
	}
	packageDetails, exists := h.apiClient.Package[key]
	if !exists {
		return nil, nil
	}
	return &packageDetails, nil
}

// Telemetry methods for mock client
func (h *MockClientConfig) CollectLicenseData(ctx context.Context, client *humioapi.Client, k8sClient client.Client, hc *humiov1alpha1.HumioCluster) (*TelemetryLicenseData, error) {
	// Return mock license data for testing
	mockIngestLimit := 10.0 // 10 GB per day
	mockCores := 4
	mockValidUntil := time.Now().Add(365 * 24 * time.Hour) // 1 year from now

	return &TelemetryLicenseData{
		LicenseUID:     "mock-license-uid-123",
		LicenseType:    "onprem",
		ExpirationDate: time.Now().Add(365 * 24 * time.Hour), // 1 year from now
		IssuedDate:     time.Now().Add(-30 * 24 * time.Hour), // 30 days ago
		Owner:          "Test Organization",
		MaxUsers:       func() *int { i := 100; return &i }(),
		IsSaaS:         helpers.BoolPtr(false),
		IsOem:          helpers.BoolPtr(false),

		// Mock JWT-extracted fields
		MaxIngestGbPerDay:    &mockIngestLimit,
		MaxCores:             &mockCores,
		LicenseValidUntil:    &mockValidUntil,
		LicenseSubject:       "MockOrganization",
		JWTExtractionSuccess: true,

		RawLicenseData: map[string]interface{}{
			"extracted":  false,
			"mock_phase": 1,
			"note":       "Mock license data for testing with JWT fields",
		},
	}, nil
}

func (h *MockClientConfig) CollectClusterInfo(ctx context.Context, client *humioapi.Client) (*TelemetryClusterInfo, error) {
	// Return mock cluster info for testing
	return &TelemetryClusterInfo{
		Version:         "1.122.0--build-12345--sha-abcdef123456",
		NodeCount:       3,
		UserCount:       15,
		RepositoryCount: 8,
	}, nil
}

func (h *MockClientConfig) CollectTelemetryData(ctx context.Context, apiClient *humioapi.Client, dataTypes []string, clusterID string, sendCollectionErrors bool, k8sClient client.Client, hc *humiov1alpha1.HumioCluster) ([]TelemetryPayload, string, error) {
	var payloads []TelemetryPayload
	timestamp := time.Now()

	// Use pod discovery logic to determine source info (similar to real implementation)
	sourceInfo := "from https://test-cluster.test-namespace.svc.cluster.local:8080"
	if hc != nil {
		sourceInfo = fmt.Sprintf("from https://%s.%s.svc.cluster.local:8080", hc.Name, hc.Namespace)
	}
	if k8sClient != nil && hc != nil {
		// Try to discover query-capable pods
		pods := &corev1.PodList{}
		matchingLabels := map[string]string{
			"app.kubernetes.io/name":       "humio",
			"app.kubernetes.io/instance":   hc.Name,
			"app.kubernetes.io/managed-by": "humio-operator",
		}

		listOpts := []client.ListOption{
			client.InNamespace(hc.Namespace),
			client.MatchingLabels(matchingLabels),
		}
		if err := k8sClient.List(ctx, pods, listOpts...); err == nil {
			// Find first query-capable pod
			for _, pod := range pods.Items {
				if pod.Status.Phase != corev1.PodRunning {
					continue
				}

				// Check NODE_ROLES
				isQueryCapable := true // Default to query-capable if no NODE_ROLES
				for _, container := range pod.Spec.Containers {
					for _, env := range container.Env {
						if env.Name == EnvNodeRoles {
							switch env.Value {
							case NodeRoleIngestOnly:
								isQueryCapable = false
							case NodeRoleHTTPOnly, NodeRoleAll:
								isQueryCapable = true
							}
							break
						}
					}
				}

				if isQueryCapable {
					sourceInfo = fmt.Sprintf("from pod %s", pod.Name)
					break
				}
			}
		}
	}

	for _, dataType := range dataTypes {
		switch dataType {
		case "license":
			licenseData, err := h.CollectLicenseData(ctx, apiClient, k8sClient, hc)
			if err != nil {
				return nil, sourceInfo, fmt.Errorf("failed to collect license data: %w", err)
			}
			payloads = append(payloads, TelemetryPayload{
				Timestamp:      timestamp,
				ClusterID:      clusterID,
				CollectionType: "license",
				SourceType:     "json",
				Data:           licenseData,
			})

		case "cluster_info":
			clusterInfo, err := h.CollectClusterInfo(ctx, apiClient)
			if err != nil {
				return nil, sourceInfo, fmt.Errorf("failed to collect cluster info: %w", err)
			}
			payloads = append(payloads, TelemetryPayload{
				Timestamp:      timestamp,
				ClusterID:      clusterID,
				CollectionType: "cluster_info",
				SourceType:     "json",
				Data:           clusterInfo,
			})

		case "user_info":
			// Mock user info
			userInfo := map[string]interface{}{
				"total_users": 15,
				"mock_phase":  1,
				"note":        "Mock user info for testing",
			}
			payloads = append(payloads, TelemetryPayload{
				Timestamp:      timestamp,
				ClusterID:      clusterID,
				CollectionType: "user_info",
				SourceType:     "json",
				Data:           userInfo,
			})

		case "repository_info":
			// Mock repository info
			repoInfo := map[string]interface{}{
				"total_repositories": 8,
				"mock_phase":         1,
				"note":               "Mock repository info for testing",
			}
			payloads = append(payloads, TelemetryPayload{
				Timestamp:      timestamp,
				ClusterID:      clusterID,
				CollectionType: "repository_info",
				SourceType:     "json",
				Data:           repoInfo,
			})
		default:
			return nil, sourceInfo, fmt.Errorf("unsupported data type: %s", dataType)
		}
	}

	return payloads, sourceInfo, nil
}

func (h *MockClientConfig) CollectIngestionMetrics(ctx context.Context, client *humioapi.Client, settings QuerySettings) ([]*TelemetryIngestionMetrics, error) {
	now := time.Now()
	startTime := now.Add(-30 * 24 * time.Hour) // Changed back to 30 days to match actual implementation

	return []*TelemetryIngestionMetrics{{
		TimeRange: struct {
			Start time.Time `json:"start"`
			End   time.Time `json:"end"`
		}{Start: startTime, End: now},
		Daily: struct {
			IngestVolumeGB    float64 `json:"ingest_volume_gb"`
			EventCount        int64   `json:"event_count"`
			AverageEventSizeB int64   `json:"average_event_size_bytes"`
		}{
			IngestVolumeGB:    15.5,
			EventCount:        1500000,
			AverageEventSizeB: 512,
		},
		Weekly: struct {
			IngestVolumeGB    float64 `json:"ingest_volume_gb"`
			EventCount        int64   `json:"event_count"`
			GrowthRatePercent float64 `json:"growth_rate_percent"`
		}{
			IngestVolumeGB:    108.5,
			EventCount:        10500000,
			GrowthRatePercent: 3.2,
		},
		Monthly: struct {
			IngestVolumeGB float64 `json:"ingest_volume_gb"`
			EventCount     int64   `json:"event_count"`
			TrendDirection string  `json:"trend_direction"`
		}{
			IngestVolumeGB: 465.0,
			EventCount:     45000000,
			TrendDirection: "increasing",
		},
	}}, nil
}

func (h *MockClientConfig) CollectRepositoryUsage(ctx context.Context, client *humioapi.Client, settings QuerySettings) (*TelemetryRepositoryUsageMetrics, error) {
	repositories := []RepositoryUsage{
		{
			Name:              "humio",
			IngestVolumeGB24h: 8.5,
			EventCount24h:     850000,
			RetentionDays:     30,
			StorageUsageGB:    250.0,
			LastActivityTime:  time.Now().Add(-1 * time.Hour),
			Dataspace:         "humio",
		},
		{
			Name:              "sandbox",
			IngestVolumeGB24h: 2.1,
			EventCount24h:     210000,
			RetentionDays:     7,
			StorageUsageGB:    15.0,
			LastActivityTime:  time.Now().Add(-30 * time.Minute),
			Dataspace:         "sandbox",
		},
	}

	return &TelemetryRepositoryUsageMetrics{
		TotalRepositories: len(repositories),
		Repositories:      repositories,
		TopRepositories:   repositories, // All are top repositories in mock
	}, nil
}

func (h *MockClientConfig) CollectUserActivity(ctx context.Context, client *humioapi.Client, settings QuerySettings) (*TelemetryUserActivityMetrics, error) {
	now := time.Now()
	startTime := now.Add(-30 * 24 * time.Hour) // Changed back to 30 days

	return &TelemetryUserActivityMetrics{
		TimeRange: struct {
			Start time.Time `json:"start"`
			End   time.Time `json:"end"`
		}{Start: startTime, End: now},
		ActiveUsers: struct {
			Last24h int `json:"last_24h"`
			Last7d  int `json:"last_7d"`
			Last30d int `json:"last_30d"`
		}{
			Last24h: 12,
			Last7d:  28,
			Last30d: 45,
		},
		QueryActivity: struct {
			TotalQueries  int64            `json:"total_queries"`
			AvgQueryTime  float64          `json:"avg_query_time_seconds"`
			TopQueryTypes []QueryTypeUsage `json:"top_query_types"`
		}{
			TotalQueries: 2150,
			AvgQueryTime: 2.8,
			TopQueryTypes: []QueryTypeUsage{
				{Type: "search", Count: 1200, AvgDuration: 2.1},
				{Type: "dashboard", Count: 650, AvgDuration: 3.5},
				{Type: "alert", Count: 300, AvgDuration: 1.8},
			},
		},
		LoginActivity: struct {
			TotalLogins    int64 `json:"total_logins"`
			UniqueUsers    int   `json:"unique_users"`
			FailedAttempts int64 `json:"failed_attempts"`
		}{
			TotalLogins:    580,
			UniqueUsers:    38,
			FailedAttempts: 12,
		},
	}, nil
}

func (h *MockClientConfig) CollectDetailedAnalytics(ctx context.Context, client *humioapi.Client, settings QuerySettings) (*TelemetryDetailedAnalytics, error) {
	now := time.Now()
	startTime := now.Add(-4 * time.Hour)

	return &TelemetryDetailedAnalytics{
		TimeRange: struct {
			Start time.Time `json:"start"`
			End   time.Time `json:"end"`
		}{Start: startTime, End: now},
		PerformanceMetrics: map[string]interface{}{
			"avg_query_response_time":    2.15,
			"peak_concurrent_queries":    18,
			"memory_usage_percent":       65.4,
			"cpu_utilization_percent":    42.1,
			"disk_io_operations_per_sec": 1250,
		},
		UsagePatterns: map[string]interface{}{
			"most_active_hour":       "14:00",
			"query_complexity_trend": "moderate",
			"top_search_keywords":    []string{"error", "warn", "exception", "timeout"},
			"avg_session_duration":   "45m",
			"peak_concurrent_users":  8,
		},
	}, nil
}

func (h *MockClientConfig) AddEventForwardingRule(_ context.Context, _ *humioapi.Client, hefr *humiov1alpha1.HumioEventForwardingRule) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", hefr.Spec.ManagedClusterName, hefr.Spec.ExternalClusterName)
	if !h.searchDomainNameExists(clusterName, hefr.Spec.RepositoryName) {
		return fmt.Errorf("could not find Repository '%s'", hefr.Spec.RepositoryName)
	}

	// Use the resolved event forwarder ID from status
	forwarderID := hefr.Status.ResolvedEventForwarderID
	if forwarderID == "" {
		return fmt.Errorf("resolved event forwarder ID not found in status")
	}

	// Generate a unique ID for the rule
	ruleID := kubernetes.RandomString()

	key := resourceKey{
		clusterName:      clusterName,
		searchDomainName: hefr.Spec.RepositoryName,
		resourceName:     ruleID,
	}

	if _, found := h.apiClient.EventForwardingRule[key]; found {
		return fmt.Errorf("event forwarding rule already exists")
	}

	// Store the rule ID in annotations
	if hefr.Annotations == nil {
		hefr.Annotations = make(map[string]string)
	}
	hefr.Annotations[EventForwardingRuleAnnotation] = ruleID

	langVersion := humiographql.EventForwardingRuleDetailsLanguageVersion{}
	if hefr.Spec.LanguageVersion != nil {
		enumVal := humiographql.LanguageVersionEnum(*hefr.Spec.LanguageVersion)
		langVersion.Name = &enumVal
	}

	h.apiClient.EventForwardingRule[key] = humiographql.EventForwardingRuleDetails{
		Id:               ruleID,
		QueryString:      hefr.Spec.QueryString,
		EventForwarderId: forwarderID,
		LanguageVersion:  langVersion,
	}

	return nil
}

func (h *MockClientConfig) GetEventForwardingRule(_ context.Context, _ *humioapi.Client, hefr *humiov1alpha1.HumioEventForwardingRule) (*humiographql.EventForwardingRuleDetails, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	ruleID := hefr.Annotations[EventForwardingRuleAnnotation]
	if ruleID == "" {
		return nil, humioapi.EventForwardingRuleNotFound("unknown")
	}

	clusterName := fmt.Sprintf("%s%s", hefr.Spec.ManagedClusterName, hefr.Spec.ExternalClusterName)
	key := resourceKey{
		clusterName:      clusterName,
		searchDomainName: hefr.Spec.RepositoryName,
		resourceName:     ruleID,
	}

	if value, found := h.apiClient.EventForwardingRule[key]; found {
		return &value, nil
	}

	return nil, humioapi.EventForwardingRuleNotFound(ruleID)
}

func (h *MockClientConfig) UpdateEventForwardingRule(_ context.Context, _ *humioapi.Client, hefr *humiov1alpha1.HumioEventForwardingRule) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	ruleID := hefr.Annotations[EventForwardingRuleAnnotation]
	if ruleID == "" {
		return fmt.Errorf("event forwarding rule ID not found in annotations")
	}

	// Use the resolved event forwarder ID from status
	forwarderID := hefr.Status.ResolvedEventForwarderID
	if forwarderID == "" {
		return fmt.Errorf("resolved event forwarder ID not found in status")
	}

	clusterName := fmt.Sprintf("%s%s", hefr.Spec.ManagedClusterName, hefr.Spec.ExternalClusterName)
	key := resourceKey{
		clusterName:      clusterName,
		searchDomainName: hefr.Spec.RepositoryName,
		resourceName:     ruleID,
	}

	currentRule, found := h.apiClient.EventForwardingRule[key]
	if !found {
		return humioapi.EventForwardingRuleNotFound(ruleID)
	}

	langVersion := humiographql.EventForwardingRuleDetailsLanguageVersion{}
	if hefr.Spec.LanguageVersion != nil {
		enumVal := humiographql.LanguageVersionEnum(*hefr.Spec.LanguageVersion)
		langVersion.Name = &enumVal
	}

	h.apiClient.EventForwardingRule[key] = humiographql.EventForwardingRuleDetails{
		Id:               currentRule.Id,
		QueryString:      hefr.Spec.QueryString,
		EventForwarderId: forwarderID,
		LanguageVersion:  langVersion,
		CreatedAt:        currentRule.CreatedAt,
	}

	return nil
}

func (h *MockClientConfig) DeleteEventForwardingRule(_ context.Context, _ *humioapi.Client, hefr *humiov1alpha1.HumioEventForwardingRule) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	ruleID := hefr.Annotations[EventForwardingRuleAnnotation]
	if ruleID == "" {
		return nil
	}

	clusterName := fmt.Sprintf("%s%s", hefr.Spec.ManagedClusterName, hefr.Spec.ExternalClusterName)
	key := resourceKey{
		clusterName:      clusterName,
		searchDomainName: hefr.Spec.RepositoryName,
		resourceName:     ruleID,
	}

	delete(h.apiClient.EventForwardingRule, key)
	return nil
}

// EventForwarder mock methods

func (h *MockClientConfig) AddEventForwarder(_ context.Context, _ *humioapi.Client, hef *humiov1alpha1.HumioEventForwarder) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", hef.Spec.ManagedClusterName, hef.Spec.ExternalClusterName)

	// Generate a unique ID for the forwarder
	forwarderID := kubernetes.RandomString()

	key := resourceKey{
		clusterName:      clusterName,
		searchDomainName: "", // Event forwarders are org-level, not scoped to a search domain
		resourceName:     hef.Spec.Name,
	}

	if _, found := h.apiClient.EventForwarder[key]; found {
		return fmt.Errorf("event forwarder already exists")
	}

	// Store the forwarder ID in status
	hef.Status.EventForwarderID = forwarderID

	h.apiClient.EventForwarder[key] = humiographql.KafkaEventForwarderDetails{
		Id:          forwarderID,
		Name:        hef.Spec.Name,
		Description: hef.Spec.Description,
		Enabled:     hef.Spec.Enabled,
		Topic:       hef.Spec.KafkaConfig.Topic,
		Properties:  hef.Spec.KafkaConfig.Properties,
	}

	return nil
}

func (h *MockClientConfig) GetEventForwarder(_ context.Context, _ *humioapi.Client, hef *humiov1alpha1.HumioEventForwarder) (*humiographql.KafkaEventForwarderDetails, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", hef.Spec.ManagedClusterName, hef.Spec.ExternalClusterName)
	forwarderID := hef.Status.EventForwarderID

	// Case 1: We have an ID in status - look up by ID (matches real implementation)
	if forwarderID != "" {
		for key, forwarder := range h.apiClient.EventForwarder {
			if key.clusterName == clusterName && forwarder.Id == forwarderID {
				return &forwarder, nil
			}
		}
		return nil, humioapi.EventForwarderNotFound(forwarderID)
	}

	// Case 2: No ID in status - look up by name (for adoption or first create check)
	key := resourceKey{
		clusterName:      clusterName,
		searchDomainName: "",
		resourceName:     hef.Spec.Name,
	}

	forwarder, found := h.apiClient.EventForwarder[key]
	if !found {
		return nil, humioapi.EventForwarderNotFound(hef.Spec.Name)
	}

	return &forwarder, nil
}

func (h *MockClientConfig) UpdateEventForwarder(_ context.Context, _ *humioapi.Client, hef *humiov1alpha1.HumioEventForwarder) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", hef.Spec.ManagedClusterName, hef.Spec.ExternalClusterName)
	key := resourceKey{
		clusterName:      clusterName,
		searchDomainName: "",
		resourceName:     hef.Spec.Name,
	}

	// Get the existing forwarder to preserve the ID
	existing, found := h.apiClient.EventForwarder[key]
	if !found {
		// If not found by name, return EntityNotFound error
		identifier := hef.Status.EventForwarderID
		if identifier == "" {
			identifier = hef.Spec.Name
		}
		return humioapi.EventForwarderNotFound(identifier)
	}

	// Use existing ID to ensure consistency
	forwarderID := existing.Id

	// Update the forwarder with new values but keep the same ID
	h.apiClient.EventForwarder[key] = humiographql.KafkaEventForwarderDetails{
		Id:          forwarderID,
		Name:        hef.Spec.Name,
		Description: hef.Spec.Description,
		Enabled:     hef.Spec.Enabled,
		Topic:       hef.Spec.KafkaConfig.Topic,
		Properties:  hef.Spec.KafkaConfig.Properties,
	}

	return nil
}

func (h *MockClientConfig) supportsSearchExecution(_ context.Context, _ *humioapi.Client, hc *humiov1alpha1.HumioCluster) (bool, error) {
	// Mock implementation: check node pools for search capability
	if hc == nil {
		// Fall back to legacy check - for mock, return false (ingest-only scenario)
		return false, fmt.Errorf("mock: search not supported without cluster configuration")
	}

	// Check if we have any query-capable node pools
	hasQueryCapable := false
	for _, nodePool := range hc.Spec.NodePools {
		if nodePool.NodeCount == 0 {
			continue // Skip pools with no nodes
		}

		// Check NODE_ROLES environment variable
		isQueryCapable := true // Default to query-capable if no NODE_ROLES is specified
		for _, env := range nodePool.EnvironmentVariables {
			if env.Name == EnvNodeRoles {
				switch env.Value {
				case NodeRoleIngestOnly:
					isQueryCapable = false
				case NodeRoleHTTPOnly, NodeRoleAll:
					isQueryCapable = true
				}
				break
			}
		}

		if isQueryCapable {
			hasQueryCapable = true
			break
		}
	}

	// If no node pools defined, assume query-capable (fallback to main cluster service)
	if len(hc.Spec.NodePools) == 0 {
		hasQueryCapable = true
	}

	if !hasQueryCapable {
		return false, fmt.Errorf("mock: no query-capable services found - all node pools are ingest-only")
	}

	return true, nil
}

func (h *MockClientConfig) DeleteEventForwarder(_ context.Context, _ *humioapi.Client, hef *humiov1alpha1.HumioEventForwarder) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	forwarderID := hef.Status.EventForwarderID
	if forwarderID == "" {
		return nil
	}

	clusterName := fmt.Sprintf("%s%s", hef.Spec.ManagedClusterName, hef.Spec.ExternalClusterName)
	key := resourceKey{
		clusterName:      clusterName,
		searchDomainName: "",
		resourceName:     hef.Spec.Name,
	}

	delete(h.apiClient.EventForwarder, key)
	return nil
}
