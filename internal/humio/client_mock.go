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
	"sync"
	"time"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	humioapi "github.com/humio/humio-operator/internal/api"
	"github.com/humio/humio-operator/internal/api/humiographql"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
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
	LicenseUID      map[resourceKey]string
	Repository      map[resourceKey]humiographql.RepositoryDetails
	View            map[resourceKey]humiographql.GetSearchDomainSearchDomainView
	IngestToken     map[resourceKey]humiographql.IngestTokenDetails
	Parser          map[resourceKey]humiographql.ParserDetails
	Action          map[resourceKey]humiographql.ActionDetails
	Alert           map[resourceKey]humiographql.AlertDetails
	FilterAlert     map[resourceKey]humiographql.FilterAlertDetails
	AggregateAlert  map[resourceKey]humiographql.AggregateAlertDetails
	ScheduledSearch map[resourceKey]humiographql.ScheduledSearchDetails
	UserID          map[resourceKey]string
}

type MockClientConfig struct {
	apiClient *ClientMock
}

func NewMockClient() *MockClientConfig {
	mockClientConfig := &MockClientConfig{
		apiClient: &ClientMock{
			LicenseUID:      make(map[resourceKey]string),
			Repository:      make(map[resourceKey]humiographql.RepositoryDetails),
			View:            make(map[resourceKey]humiographql.GetSearchDomainSearchDomainView),
			IngestToken:     make(map[resourceKey]humiographql.IngestTokenDetails),
			Parser:          make(map[resourceKey]humiographql.ParserDetails),
			Action:          make(map[resourceKey]humiographql.ActionDetails),
			Alert:           make(map[resourceKey]humiographql.AlertDetails),
			FilterAlert:     make(map[resourceKey]humiographql.FilterAlertDetails),
			AggregateAlert:  make(map[resourceKey]humiographql.AggregateAlertDetails),
			ScheduledSearch: make(map[resourceKey]humiographql.ScheduledSearchDetails),
			UserID:          make(map[resourceKey]string),
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
	h.apiClient.IngestToken = make(map[resourceKey]humiographql.IngestTokenDetails)
	h.apiClient.Parser = make(map[resourceKey]humiographql.ParserDetails)
	h.apiClient.Action = make(map[resourceKey]humiographql.ActionDetails)
	h.apiClient.Alert = make(map[resourceKey]humiographql.AlertDetails)
	h.apiClient.FilterAlert = make(map[resourceKey]humiographql.FilterAlertDetails)
	h.apiClient.AggregateAlert = make(map[resourceKey]humiographql.AggregateAlertDetails)
	h.apiClient.ScheduledSearch = make(map[resourceKey]humiographql.ScheduledSearchDetails)
	h.apiClient.UserID = make(map[resourceKey]string)
}

func (h *MockClientConfig) Status(_ context.Context, _ *humioapi.Client, _ reconcile.Request) (*humioapi.StatusResponse, error) {
	return &humioapi.StatusResponse{
		Version: "x.y.z",
	}, nil
}

func (h *MockClientConfig) GetClusters(_ context.Context, _ *humioapi.Client, _ reconcile.Request) (*humiographql.GetClusterResponse, error) {
	return nil, nil
}

func (h *MockClientConfig) TestAPIToken(_ context.Context, _ *humioapi.Config, _ reconcile.Request) error {
	return nil
}

func (h *MockClientConfig) AddIngestToken(_ context.Context, _ *humioapi.Client, _ reconcile.Request, hit *humiov1alpha1.HumioIngestToken) error {
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

func (h *MockClientConfig) GetIngestToken(_ context.Context, _ *humioapi.Client, _ reconcile.Request, hit *humiov1alpha1.HumioIngestToken) (*humiographql.IngestTokenDetails, error) {
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

func (h *MockClientConfig) UpdateIngestToken(_ context.Context, _ *humioapi.Client, _ reconcile.Request, hit *humiov1alpha1.HumioIngestToken) error {
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

func (h *MockClientConfig) DeleteIngestToken(_ context.Context, _ *humioapi.Client, _ reconcile.Request, hit *humiov1alpha1.HumioIngestToken) error {
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

func (h *MockClientConfig) AddParser(_ context.Context, _ *humioapi.Client, _ reconcile.Request, hp *humiov1alpha1.HumioParser) error {
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

func (h *MockClientConfig) GetParser(_ context.Context, _ *humioapi.Client, _ reconcile.Request, hp *humiov1alpha1.HumioParser) (*humiographql.ParserDetails, error) {
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

func (h *MockClientConfig) UpdateParser(_ context.Context, _ *humioapi.Client, _ reconcile.Request, hp *humiov1alpha1.HumioParser) error {
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

func (h *MockClientConfig) DeleteParser(_ context.Context, _ *humioapi.Client, _ reconcile.Request, hp *humiov1alpha1.HumioParser) error {
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

func (h *MockClientConfig) AddRepository(_ context.Context, _ *humioapi.Client, _ reconcile.Request, hr *humiov1alpha1.HumioRepository) error {
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

	if _, found := h.apiClient.Repository[key]; found {
		return fmt.Errorf("repository already exists with name %s", hr.Spec.Name)
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

func (h *MockClientConfig) GetRepository(_ context.Context, _ *humioapi.Client, _ reconcile.Request, hr *humiov1alpha1.HumioRepository) (*humiographql.RepositoryDetails, error) {
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

func (h *MockClientConfig) UpdateRepository(_ context.Context, _ *humioapi.Client, _ reconcile.Request, hr *humiov1alpha1.HumioRepository) error {
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

func (h *MockClientConfig) DeleteRepository(_ context.Context, _ *humioapi.Client, _ reconcile.Request, hr *humiov1alpha1.HumioRepository) error {
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

func (h *MockClientConfig) GetView(_ context.Context, _ *humioapi.Client, _ reconcile.Request, hv *humiov1alpha1.HumioView) (*humiographql.GetSearchDomainSearchDomainView, error) {
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

func (h *MockClientConfig) AddView(_ context.Context, _ *humioapi.Client, _ reconcile.Request, hv *humiov1alpha1.HumioView) error {
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

	if _, found := h.apiClient.Repository[key]; found {
		return fmt.Errorf("view already exists with name %s", hv.Spec.Name)
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
		Typename:        helpers.StringPtr("View"),
		Id:              kubernetes.RandomString(),
		Name:            hv.Spec.Name,
		Description:     &hv.Spec.Description,
		AutomaticSearch: helpers.BoolTrue(hv.Spec.AutomaticSearch),
		Connections:     connections,
	}
	h.apiClient.View[key] = *value
	return nil
}

func (h *MockClientConfig) UpdateView(_ context.Context, _ *humioapi.Client, _ reconcile.Request, hv *humiov1alpha1.HumioView) error {
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

func (h *MockClientConfig) DeleteView(_ context.Context, _ *humioapi.Client, _ reconcile.Request, hv *humiov1alpha1.HumioView) error {
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

func (h *MockClientConfig) GetAction(_ context.Context, _ *humioapi.Client, _ reconcile.Request, ha *humiov1alpha1.HumioAction) (humiographql.ActionDetails, error) {
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

func (h *MockClientConfig) AddAction(_ context.Context, _ *humioapi.Client, _ reconcile.Request, ha *humiov1alpha1.HumioAction) error {
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

func (h *MockClientConfig) UpdateAction(_ context.Context, _ *humioapi.Client, _ reconcile.Request, ha *humiov1alpha1.HumioAction) error {
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

func (h *MockClientConfig) DeleteAction(_ context.Context, _ *humioapi.Client, _ reconcile.Request, ha *humiov1alpha1.HumioAction) error {
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

func (h *MockClientConfig) GetAlert(_ context.Context, _ *humioapi.Client, _ reconcile.Request, ha *humiov1alpha1.HumioAlert) (*humiographql.AlertDetails, error) {
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

func (h *MockClientConfig) AddAlert(_ context.Context, _ *humioapi.Client, _ reconcile.Request, ha *humiov1alpha1.HumioAlert) error {
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

func (h *MockClientConfig) UpdateAlert(_ context.Context, _ *humioapi.Client, _ reconcile.Request, ha *humiov1alpha1.HumioAlert) error {
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

func (h *MockClientConfig) DeleteAlert(_ context.Context, _ *humioapi.Client, _ reconcile.Request, ha *humiov1alpha1.HumioAlert) error {
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

func (h *MockClientConfig) GetFilterAlert(_ context.Context, _ *humioapi.Client, _ reconcile.Request, hfa *humiov1alpha1.HumioFilterAlert) (*humiographql.FilterAlertDetails, error) {
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

func (h *MockClientConfig) AddFilterAlert(_ context.Context, _ *humioapi.Client, _ reconcile.Request, hfa *humiov1alpha1.HumioFilterAlert) error {
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

func (h *MockClientConfig) UpdateFilterAlert(_ context.Context, _ *humioapi.Client, _ reconcile.Request, hfa *humiov1alpha1.HumioFilterAlert) error {
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

func (h *MockClientConfig) DeleteFilterAlert(_ context.Context, _ *humioapi.Client, _ reconcile.Request, hfa *humiov1alpha1.HumioFilterAlert) error {
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

func (h *MockClientConfig) ValidateActionsForFilterAlert(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioFilterAlert) error {
	return nil
}

func (h *MockClientConfig) GetAggregateAlert(_ context.Context, _ *humioapi.Client, _ reconcile.Request, haa *humiov1alpha1.HumioAggregateAlert) (*humiographql.AggregateAlertDetails, error) {
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

func (h *MockClientConfig) AddAggregateAlert(ctx context.Context, client *humioapi.Client, req reconcile.Request, haa *humiov1alpha1.HumioAggregateAlert) error {
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
	if err := h.ValidateActionsForAggregateAlert(ctx, client, req, haa); err != nil {
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

func (h *MockClientConfig) UpdateAggregateAlert(_ context.Context, _ *humioapi.Client, _ reconcile.Request, haa *humiov1alpha1.HumioAggregateAlert) error {
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

func (h *MockClientConfig) DeleteAggregateAlert(_ context.Context, _ *humioapi.Client, _ reconcile.Request, haa *humiov1alpha1.HumioAggregateAlert) error {
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

func (h *MockClientConfig) ValidateActionsForAggregateAlert(_ context.Context, _ *humioapi.Client, _ reconcile.Request, _ *humiov1alpha1.HumioAggregateAlert) error {
	return nil
}

func (h *MockClientConfig) AddScheduledSearch(_ context.Context, _ *humioapi.Client, _ reconcile.Request, hss *humiov1alpha1.HumioScheduledSearch) error {
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

func (h *MockClientConfig) GetScheduledSearch(_ context.Context, _ *humioapi.Client, _ reconcile.Request, hss *humiov1alpha1.HumioScheduledSearch) (*humiographql.ScheduledSearchDetails, error) {
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

func (h *MockClientConfig) UpdateScheduledSearch(_ context.Context, _ *humioapi.Client, _ reconcile.Request, hss *humiov1alpha1.HumioScheduledSearch) error {
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

func (h *MockClientConfig) DeleteScheduledSearch(_ context.Context, _ *humioapi.Client, _ reconcile.Request, hss *humiov1alpha1.HumioScheduledSearch) error {
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

func (h *MockClientConfig) ValidateActionsForScheduledSearch(context.Context, *humioapi.Client, reconcile.Request, *humiov1alpha1.HumioScheduledSearch) error {
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

	return false
}

func (h *MockClientConfig) GetUserIDForUsername(_ context.Context, _ *humioapi.Client, req reconcile.Request, _ string) (string, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		resourceName: fmt.Sprintf("%s%s", req.Namespace, req.Name),
	}

	currentUserID, found := h.apiClient.UserID[key]
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

	currentUserID, found := h.apiClient.UserID[key]
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

	h.apiClient.UserID[key] = kubernetes.RandomString()
	return h.apiClient.UserID[key], nil
}
