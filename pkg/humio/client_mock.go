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
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"net/url"
	"sync"

	"github.com/humio/humio-operator/pkg/helpers"

	humioapi "github.com/humio/cli/api"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/kubernetes"
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
	OnPremLicense   map[resourceKey]humioapi.OnPremLicense
	Repository      map[resourceKey]humioapi.Repository
	View            map[resourceKey]humioapi.View
	IngestToken     map[resourceKey]humioapi.IngestToken
	Parser          map[resourceKey]humioapi.Parser
	Action          map[resourceKey]humioapi.Action
	Alert           map[resourceKey]humioapi.Alert
	FilterAlert     map[resourceKey]humioapi.FilterAlert
	AggregateAlert  map[resourceKey]humioapi.AggregateAlert
	ScheduledSearch map[resourceKey]humioapi.ScheduledSearch
	User            humioapi.User
}

type MockClientConfig struct {
	apiClient *ClientMock
}

func NewMockClient() *MockClientConfig {
	mockClientConfig := &MockClientConfig{
		apiClient: &ClientMock{
			OnPremLicense:   make(map[resourceKey]humioapi.OnPremLicense),
			Repository:      make(map[resourceKey]humioapi.Repository),
			View:            make(map[resourceKey]humioapi.View),
			IngestToken:     make(map[resourceKey]humioapi.IngestToken),
			Parser:          make(map[resourceKey]humioapi.Parser),
			Action:          make(map[resourceKey]humioapi.Action),
			Alert:           make(map[resourceKey]humioapi.Alert),
			FilterAlert:     make(map[resourceKey]humioapi.FilterAlert),
			AggregateAlert:  make(map[resourceKey]humioapi.AggregateAlert),
			ScheduledSearch: make(map[resourceKey]humioapi.ScheduledSearch),
			User:            humioapi.User{},
		},
	}

	return mockClientConfig
}

func (h *MockClientConfig) Status(config *humioapi.Config, req reconcile.Request) (*humioapi.StatusResponse, error) {
	return &humioapi.StatusResponse{
		Status:  "OK",
		Version: "x.y.z",
	}, nil
}

func (h *MockClientConfig) GetClusters(config *humioapi.Config, req reconcile.Request) (humioapi.Cluster, error) {
	return humioapi.Cluster{}, fmt.Errorf("not implemented")
}

func (h *MockClientConfig) GetBaseURL(config *humioapi.Config, req reconcile.Request, hc *humiov1alpha1.HumioCluster) *url.URL {
	baseURL, _ := url.Parse(fmt.Sprintf("http://%s-internal.%s:%d/", hc.Name, hc.Namespace, 8080))
	return baseURL
}

func (h *MockClientConfig) TestAPIToken(config *humioapi.Config, req reconcile.Request) error {
	return nil
}

func (h *MockClientConfig) AddIngestToken(config *humioapi.Config, req reconcile.Request, hit *humiov1alpha1.HumioIngestToken) (*humioapi.IngestToken, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", hit.Spec.ManagedClusterName, hit.Spec.ExternalClusterName)
	if !h.searchDomainNameExists(clusterName, hit.Spec.RepositoryName) {
		return nil, fmt.Errorf("search domain name does not exist")
	}

	key := resourceKey{
		clusterName:      clusterName,
		searchDomainName: hit.Spec.RepositoryName,
		resourceName:     hit.Spec.Name,
	}

	if _, found := h.apiClient.IngestToken[key]; found {
		return nil, fmt.Errorf("ingest token already exists with name %s", hit.Spec.Name)
	}

	value := IngestTokenTransform(hit)
	if value.Token == "" {
		value.Token = kubernetes.RandomString()
	}
	h.apiClient.IngestToken[key] = *value
	return value, nil
}

func (h *MockClientConfig) GetIngestToken(config *humioapi.Config, req reconcile.Request, hit *humiov1alpha1.HumioIngestToken) (*humioapi.IngestToken, error) {
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

func (h *MockClientConfig) UpdateIngestToken(config *humioapi.Config, req reconcile.Request, hit *humiov1alpha1.HumioIngestToken) (*humioapi.IngestToken, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", hit.Spec.ManagedClusterName, hit.Spec.ExternalClusterName),
		searchDomainName: hit.Spec.RepositoryName,
		resourceName:     hit.Spec.Name,
	}

	if _, found := h.apiClient.IngestToken[key]; !found {
		return nil, fmt.Errorf("ingest token not found with name %s, err=%w", hit.Spec.Name, humioapi.EntityNotFound{})
	}

	value := IngestTokenTransform(hit)
	if value.Token == "" {
		value.Token = h.apiClient.IngestToken[key].Token
	}
	h.apiClient.IngestToken[key] = *value
	return value, nil
}

func (h *MockClientConfig) DeleteIngestToken(config *humioapi.Config, req reconcile.Request, hit *humiov1alpha1.HumioIngestToken) error {
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

func (h *MockClientConfig) AddParser(config *humioapi.Config, req reconcile.Request, hp *humiov1alpha1.HumioParser) (*humioapi.Parser, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", hp.Spec.ManagedClusterName, hp.Spec.ExternalClusterName)
	if !h.searchDomainNameExists(clusterName, hp.Spec.RepositoryName) {
		return nil, fmt.Errorf("search domain name does not exist")
	}

	key := resourceKey{
		clusterName:      clusterName,
		searchDomainName: hp.Spec.RepositoryName,
		resourceName:     hp.Spec.Name,
	}

	if _, found := h.apiClient.Parser[key]; found {
		return nil, fmt.Errorf("parser already exists with name %s", hp.Spec.Name)
	}

	value := ParserTransform(hp)
	if value.ID == "" {
		value.ID = kubernetes.RandomString()
	}
	h.apiClient.Parser[key] = *value
	return value, nil
}

func (h *MockClientConfig) GetParser(config *humioapi.Config, req reconcile.Request, hp *humiov1alpha1.HumioParser) (*humioapi.Parser, error) {
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

func (h *MockClientConfig) UpdateParser(config *humioapi.Config, req reconcile.Request, hp *humiov1alpha1.HumioParser) (*humioapi.Parser, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", hp.Spec.ManagedClusterName, hp.Spec.ExternalClusterName),
		searchDomainName: hp.Spec.RepositoryName,
		resourceName:     hp.Spec.Name,
	}

	if _, found := h.apiClient.Parser[key]; !found {
		return nil, fmt.Errorf("parser not found with name %s, err=%w", hp.Spec.Name, humioapi.EntityNotFound{})
	}

	value := ParserTransform(hp)

	h.apiClient.Parser[key] = *value
	return value, nil
}

func (h *MockClientConfig) DeleteParser(config *humioapi.Config, req reconcile.Request, hp *humiov1alpha1.HumioParser) error {
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

func (h *MockClientConfig) AddRepository(config *humioapi.Config, req reconcile.Request, hr *humiov1alpha1.HumioRepository) (*humioapi.Repository, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", hr.Spec.ManagedClusterName, hr.Spec.ExternalClusterName)
	if h.searchDomainNameExists(clusterName, hr.Spec.Name) {
		return nil, fmt.Errorf("search domain name already in use")
	}

	key := resourceKey{
		clusterName:  clusterName,
		resourceName: hr.Spec.Name,
	}

	if _, found := h.apiClient.Repository[key]; found {
		return nil, fmt.Errorf("repository already exists with name %s", hr.Spec.Name)
	}

	value := &humioapi.Repository{
		ID:                     kubernetes.RandomString(),
		Name:                   hr.Spec.Name,
		Description:            hr.Spec.Description,
		RetentionDays:          float64(hr.Spec.Retention.TimeInDays),
		IngestRetentionSizeGB:  float64(hr.Spec.Retention.IngestSizeInGB),
		StorageRetentionSizeGB: float64(hr.Spec.Retention.StorageSizeInGB),
		AutomaticSearch:        helpers.BoolTrue(hr.Spec.AutomaticSearch),
	}

	h.apiClient.Repository[key] = *value
	return value, nil
}

func (h *MockClientConfig) GetRepository(config *humioapi.Config, req reconcile.Request, hr *humiov1alpha1.HumioRepository) (*humioapi.Repository, error) {
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

func (h *MockClientConfig) UpdateRepository(config *humioapi.Config, req reconcile.Request, hr *humiov1alpha1.HumioRepository) (*humioapi.Repository, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:  fmt.Sprintf("%s%s", hr.Spec.ManagedClusterName, hr.Spec.ExternalClusterName),
		resourceName: hr.Spec.Name,
	}

	if _, found := h.apiClient.Repository[key]; !found {
		return nil, fmt.Errorf("repository not found with name %s, err=%w", hr.Spec.Name, humioapi.EntityNotFound{})
	}

	value := &humioapi.Repository{
		ID:                     kubernetes.RandomString(),
		Name:                   hr.Spec.Name,
		Description:            hr.Spec.Description,
		RetentionDays:          float64(hr.Spec.Retention.TimeInDays),
		IngestRetentionSizeGB:  float64(hr.Spec.Retention.IngestSizeInGB),
		StorageRetentionSizeGB: float64(hr.Spec.Retention.StorageSizeInGB),
		AutomaticSearch:        helpers.BoolTrue(hr.Spec.AutomaticSearch),
	}

	h.apiClient.Repository[key] = *value
	return value, nil
}

func (h *MockClientConfig) DeleteRepository(config *humioapi.Config, req reconcile.Request, hr *humiov1alpha1.HumioRepository) error {
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

func (h *MockClientConfig) GetView(config *humioapi.Config, req reconcile.Request, hv *humiov1alpha1.HumioView) (*humioapi.View, error) {
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

func (h *MockClientConfig) AddView(config *humioapi.Config, req reconcile.Request, hv *humiov1alpha1.HumioView) (*humioapi.View, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", hv.Spec.ManagedClusterName, hv.Spec.ExternalClusterName)
	if h.searchDomainNameExists(clusterName, hv.Spec.Name) {
		return nil, fmt.Errorf("search domain name already in use")
	}

	key := resourceKey{
		clusterName:  clusterName,
		resourceName: hv.Spec.Name,
	}

	if _, found := h.apiClient.Repository[key]; found {
		return nil, fmt.Errorf("view already exists with name %s", hv.Spec.Name)
	}

	connections := make([]humioapi.ViewConnection, 0)
	for _, connection := range hv.Spec.Connections {
		connections = append(connections, humioapi.ViewConnection{
			RepoName: connection.RepositoryName,
			Filter:   connection.Filter,
		})
	}

	value := &humioapi.View{
		Name:            hv.Spec.Name,
		Description:     hv.Spec.Description,
		Connections:     connections,
		AutomaticSearch: helpers.BoolTrue(hv.Spec.AutomaticSearch),
	}
	h.apiClient.View[key] = *value
	return value, nil
}

func (h *MockClientConfig) UpdateView(config *humioapi.Config, req reconcile.Request, hv *humiov1alpha1.HumioView) (*humioapi.View, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:  fmt.Sprintf("%s%s", hv.Spec.ManagedClusterName, hv.Spec.ExternalClusterName),
		resourceName: hv.Spec.Name,
	}

	if _, found := h.apiClient.View[key]; !found {
		return nil, fmt.Errorf("view not found with name %s, err=%w", hv.Spec.Name, humioapi.EntityNotFound{})
	}

	connections := make([]humioapi.ViewConnection, 0)
	for _, connection := range hv.Spec.Connections {
		connections = append(connections, humioapi.ViewConnection{
			RepoName: connection.RepositoryName,
			Filter:   connection.Filter,
		})
	}

	value := &humioapi.View{
		Name:            hv.Spec.Name,
		Description:     hv.Spec.Description,
		Connections:     connections,
		AutomaticSearch: helpers.BoolTrue(hv.Spec.AutomaticSearch),
	}
	h.apiClient.View[key] = *value
	return value, nil
}

func (h *MockClientConfig) DeleteView(config *humioapi.Config, req reconcile.Request, hv *humiov1alpha1.HumioView) error {
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

func (h *MockClientConfig) GetLicense(config *humioapi.Config, req reconcile.Request) (humioapi.License, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		resourceName: fmt.Sprintf("%s%s", req.Namespace, req.Name),
	}

	if value, found := h.apiClient.OnPremLicense[key]; found {
		return &value, nil

	}

	return humioapi.OnPremLicense{}, nil
}

func (h *MockClientConfig) InstallLicense(config *humioapi.Config, req reconcile.Request, licenseString string) error {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		resourceName: fmt.Sprintf("%s%s", req.Namespace, req.Name),
	}

	onPremLicense, err := ParseLicenseType(licenseString)
	if err != nil {
		return fmt.Errorf("failed to parse license type: %w", err)
	}

	h.apiClient.OnPremLicense[key] = *onPremLicense
	return nil
}

func (h *MockClientConfig) GetAction(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAction) (*humioapi.Action, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", ha.Spec.ManagedClusterName, ha.Spec.ExternalClusterName),
		searchDomainName: ha.Spec.ViewName,
		resourceName:     ha.Spec.Name,
	}
	if value, found := h.apiClient.Action[key]; found {
		return &value, nil

	}
	return nil, fmt.Errorf("could not find action in view %q with name %q, err=%w", ha.Spec.ViewName, ha.Spec.Name, humioapi.EntityNotFound{})
}

func (h *MockClientConfig) AddAction(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAction) (*humioapi.Action, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", ha.Spec.ManagedClusterName, ha.Spec.ExternalClusterName)
	if !h.searchDomainNameExists(clusterName, ha.Spec.ViewName) {
		return nil, fmt.Errorf("search domain name does not exist")
	}

	key := resourceKey{
		clusterName:      clusterName,
		searchDomainName: ha.Spec.ViewName,
		resourceName:     ha.Spec.Name,
	}

	if _, found := h.apiClient.Action[key]; found {
		return nil, fmt.Errorf("action already exists with name %s", ha.Spec.Name)
	}

	action, err := ActionFromActionCR(ha)
	if err != nil {
		return nil, err
	}
	action.ID = kubernetes.RandomString()

	h.apiClient.Action[key] = *action
	return action, nil
}

func (h *MockClientConfig) UpdateAction(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAction) (*humioapi.Action, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", ha.Spec.ManagedClusterName, ha.Spec.ExternalClusterName),
		searchDomainName: ha.Spec.ViewName,
		resourceName:     ha.Spec.Name,
	}

	currentAction, found := h.apiClient.Action[key]

	if !found {
		return nil, fmt.Errorf("could not find action in view %q with name %q, err=%w", ha.Spec.ViewName, ha.Spec.Name, humioapi.EntityNotFound{})
	}

	action, err := ActionFromActionCR(ha)
	if err != nil {
		return nil, err
	}
	action.ID = currentAction.ID

	h.apiClient.Action[key] = *action
	return action, nil
}

func (h *MockClientConfig) DeleteAction(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAction) error {
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

func (h *MockClientConfig) GetAlert(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAlert) (*humioapi.Alert, error) {
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

func (h *MockClientConfig) AddAlert(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAlert) (*humioapi.Alert, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", ha.Spec.ManagedClusterName, ha.Spec.ExternalClusterName)
	if !h.searchDomainNameExists(clusterName, ha.Spec.ViewName) {
		return nil, fmt.Errorf("search domain name does not exist")
	}

	key := resourceKey{
		clusterName:      clusterName,
		searchDomainName: ha.Spec.ViewName,
		resourceName:     ha.Spec.Name,
	}

	if _, found := h.apiClient.Alert[key]; found {
		return nil, fmt.Errorf("alert already exists with name %s", ha.Spec.Name)
	}
	actionIdMap, err := h.GetActionIDsMapForAlerts(config, req, ha)
	if err != nil {
		return nil, fmt.Errorf("could not get action id mapping: %w", err)
	}

	value := AlertTransform(ha, actionIdMap)
	value.ID = kubernetes.RandomString()

	h.apiClient.Alert[key] = *value
	return value, nil
}

func (h *MockClientConfig) UpdateAlert(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAlert) (*humioapi.Alert, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", ha.Spec.ManagedClusterName, ha.Spec.ExternalClusterName),
		searchDomainName: ha.Spec.ViewName,
		resourceName:     ha.Spec.Name,
	}

	currentAlert, found := h.apiClient.Alert[key]

	if !found {
		return nil, fmt.Errorf("alert not found with name %s, err=%w", ha.Spec.Name, humioapi.EntityNotFound{})
	}
	actionIdMap, err := h.GetActionIDsMapForAlerts(config, req, ha)
	if err != nil {
		return nil, fmt.Errorf("could not get action id mapping: %w", err)
	}

	value := AlertTransform(ha, actionIdMap)
	value.ID = currentAlert.ID

	h.apiClient.Alert[key] = *value
	return value, nil
}

func (h *MockClientConfig) DeleteAlert(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAlert) error {
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

func (h *MockClientConfig) GetActionIDsMapForAlerts(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAlert) (map[string]string, error) {
	actionIdMap := make(map[string]string)
	for _, action := range ha.Spec.Actions {
		hash := sha512.Sum512([]byte(action))
		actionIdMap[action] = hex.EncodeToString(hash[:])
	}
	return actionIdMap, nil
}

func (h *MockClientConfig) GetFilterAlert(config *humioapi.Config, req reconcile.Request, hfa *humiov1alpha1.HumioFilterAlert) (*humioapi.FilterAlert, error) {
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

func (h *MockClientConfig) AddFilterAlert(config *humioapi.Config, req reconcile.Request, hfa *humiov1alpha1.HumioFilterAlert) (*humioapi.FilterAlert, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", hfa.Spec.ManagedClusterName, hfa.Spec.ExternalClusterName)
	if !h.searchDomainNameExists(clusterName, hfa.Spec.ViewName) {
		return nil, fmt.Errorf("search domain name does not exist")
	}

	key := resourceKey{
		clusterName:      clusterName,
		searchDomainName: hfa.Spec.ViewName,
		resourceName:     hfa.Spec.Name,
	}

	if _, found := h.apiClient.FilterAlert[key]; found {
		return nil, fmt.Errorf("filter alert already exists with name %s", hfa.Spec.Name)
	}
	if err := h.ValidateActionsForFilterAlert(config, req, hfa); err != nil {
		return nil, fmt.Errorf("could not get action id mapping: %w", err)
	}

	value := FilterAlertTransform(hfa)
	value.ID = kubernetes.RandomString()

	h.apiClient.FilterAlert[key] = *value
	return value, nil
}

func (h *MockClientConfig) UpdateFilterAlert(config *humioapi.Config, req reconcile.Request, hfa *humiov1alpha1.HumioFilterAlert) (*humioapi.FilterAlert, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", hfa.Spec.ManagedClusterName, hfa.Spec.ExternalClusterName),
		searchDomainName: hfa.Spec.ViewName,
		resourceName:     hfa.Spec.Name,
	}

	currentFilterAlert, found := h.apiClient.FilterAlert[key]

	if !found {
		return nil, fmt.Errorf("could not find filter alert in view %q with name %q, err=%w", hfa.Spec.ViewName, hfa.Spec.Name, humioapi.EntityNotFound{})
	}
	if err := h.ValidateActionsForFilterAlert(config, req, hfa); err != nil {
		return nil, fmt.Errorf("could not get action id mapping: %w", err)
	}

	value := FilterAlertTransform(hfa)
	value.ID = currentFilterAlert.ID

	h.apiClient.FilterAlert[key] = *value
	return value, nil
}

func (h *MockClientConfig) DeleteFilterAlert(config *humioapi.Config, req reconcile.Request, hfa *humiov1alpha1.HumioFilterAlert) error {
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

func (h *MockClientConfig) ValidateActionsForFilterAlert(config *humioapi.Config, req reconcile.Request, hfa *humiov1alpha1.HumioFilterAlert) error {
	return nil
}

func (h *MockClientConfig) GetAggregateAlert(config *humioapi.Config, req reconcile.Request, haa *humiov1alpha1.HumioAggregateAlert) (*humioapi.AggregateAlert, error) {
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

func (h *MockClientConfig) AddAggregateAlert(config *humioapi.Config, req reconcile.Request, haa *humiov1alpha1.HumioAggregateAlert) (*humioapi.AggregateAlert, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", haa.Spec.ManagedClusterName, haa.Spec.ExternalClusterName),
		searchDomainName: haa.Spec.ViewName,
		resourceName:     haa.Spec.Name,
	}

	if _, found := h.apiClient.AggregateAlert[key]; found {
		return nil, fmt.Errorf("aggregate alert already exists with name %s", haa.Spec.Name)
	}
	if err := h.ValidateActionsForAggregateAlert(config, req, haa); err != nil {
		return nil, fmt.Errorf("could not get action id mapping: %w", err)
	}

	value := AggregateAlertTransform(haa)
	value.ID = kubernetes.RandomString()

	h.apiClient.AggregateAlert[key] = *value
	return value, nil
}

func (h *MockClientConfig) UpdateAggregateAlert(config *humioapi.Config, req reconcile.Request, haa *humiov1alpha1.HumioAggregateAlert) (*humioapi.AggregateAlert, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", haa.Spec.ManagedClusterName, haa.Spec.ExternalClusterName),
		searchDomainName: haa.Spec.ViewName,
		resourceName:     haa.Spec.Name,
	}

	currentAggregateAlert, found := h.apiClient.AggregateAlert[key]

	if !found {
		return nil, fmt.Errorf("could not find aggregate alert in view %q with name %q, err=%w", haa.Spec.ViewName, haa.Spec.Name, humioapi.EntityNotFound{})
	}
	if err := h.ValidateActionsForAggregateAlert(config, req, haa); err != nil {
		return nil, fmt.Errorf("could not get action id mapping: %w", err)
	}

	value := AggregateAlertTransform(haa)
	value.ID = currentAggregateAlert.ID

	h.apiClient.AggregateAlert[key] = *value
	return value, nil
}

func (h *MockClientConfig) DeleteAggregateAlert(config *humioapi.Config, req reconcile.Request, haa *humiov1alpha1.HumioAggregateAlert) error {
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

func (h *MockClientConfig) ValidateActionsForAggregateAlert(config *humioapi.Config, req reconcile.Request, haa *humiov1alpha1.HumioAggregateAlert) error {
	return nil
}

func (h *MockClientConfig) AddScheduledSearch(config *humioapi.Config, req reconcile.Request, hss *humiov1alpha1.HumioScheduledSearch) (*humioapi.ScheduledSearch, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	clusterName := fmt.Sprintf("%s%s", hss.Spec.ManagedClusterName, hss.Spec.ExternalClusterName)
	if !h.searchDomainNameExists(clusterName, hss.Spec.ViewName) {
		return nil, fmt.Errorf("search domain name does not exist")
	}

	key := resourceKey{
		clusterName:      clusterName,
		searchDomainName: hss.Spec.ViewName,
		resourceName:     hss.Spec.Name,
	}

	if _, found := h.apiClient.ScheduledSearch[key]; found {
		return nil, fmt.Errorf("scheduled search already exists with name %s", hss.Spec.Name)
	}
	if err := h.ValidateActionsForScheduledSearch(config, req, hss); err != nil {
		return nil, fmt.Errorf("could not get action id mapping: %w", err)
	}

	value := ScheduledSearchTransform(hss)
	value.ID = kubernetes.RandomString()

	h.apiClient.ScheduledSearch[key] = *value
	return value, nil
}

func (h *MockClientConfig) GetScheduledSearch(config *humioapi.Config, req reconcile.Request, hss *humiov1alpha1.HumioScheduledSearch) (*humioapi.ScheduledSearch, error) {
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

func (h *MockClientConfig) UpdateScheduledSearch(config *humioapi.Config, req reconcile.Request, hss *humiov1alpha1.HumioScheduledSearch) (*humioapi.ScheduledSearch, error) {
	humioClientMu.Lock()
	defer humioClientMu.Unlock()

	key := resourceKey{
		clusterName:      fmt.Sprintf("%s%s", hss.Spec.ManagedClusterName, hss.Spec.ExternalClusterName),
		searchDomainName: hss.Spec.ViewName,
		resourceName:     hss.Spec.Name,
	}

	currentScheduledSearch, found := h.apiClient.ScheduledSearch[key]

	if !found {
		return nil, fmt.Errorf("could not find scheduled search in view %q with name %q, err=%w", hss.Spec.ViewName, hss.Spec.Name, humioapi.EntityNotFound{})
	}
	if err := h.ValidateActionsForScheduledSearch(config, req, hss); err != nil {
		return nil, fmt.Errorf("could not get action id mapping: %w", err)
	}

	value := ScheduledSearchTransform(hss)
	value.ID = currentScheduledSearch.ID

	h.apiClient.ScheduledSearch[key] = *value
	return value, nil
}

func (h *MockClientConfig) DeleteScheduledSearch(config *humioapi.Config, req reconcile.Request, hss *humiov1alpha1.HumioScheduledSearch) error {
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

func (h *MockClientConfig) ValidateActionsForScheduledSearch(config *humioapi.Config, req reconcile.Request, hss *humiov1alpha1.HumioScheduledSearch) error {
	return nil
}

func (h *MockClientConfig) GetHumioClient(config *humioapi.Config, req ctrl.Request) *humioapi.Client {
	clusterURL, _ := url.Parse("http://localhost:8080/")
	return humioapi.NewClient(humioapi.Config{Address: clusterURL})
}

func (h *MockClientConfig) ClearHumioClientConnections(repoNameToKeep string) {
	for k := range h.apiClient.Repository {
		if k.resourceName != repoNameToKeep {
			delete(h.apiClient.Repository, k)
		}
	}
	h.apiClient.View = make(map[resourceKey]humioapi.View)
	h.apiClient.IngestToken = make(map[resourceKey]humioapi.IngestToken)
	h.apiClient.Parser = make(map[resourceKey]humioapi.Parser)
	h.apiClient.Action = make(map[resourceKey]humioapi.Action)
	h.apiClient.Alert = make(map[resourceKey]humioapi.Alert)
	h.apiClient.FilterAlert = make(map[resourceKey]humioapi.FilterAlert)
	h.apiClient.AggregateAlert = make(map[resourceKey]humioapi.AggregateAlert)
	h.apiClient.ScheduledSearch = make(map[resourceKey]humioapi.ScheduledSearch)
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

func (h *MockClientConfig) ListAllHumioUsersSingleOrg(config *humioapi.Config, req reconcile.Request) ([]user, error) {
	return []user{}, nil
}

func (h *MockClientConfig) ListAllHumioUsersMultiOrg(config *humioapi.Config, req reconcile.Request, username string, organization string) ([]OrganizationSearchResultEntry, error) {
	return []OrganizationSearchResultEntry{}, nil
}

func (h *MockClientConfig) ExtractExistingHumioAdminUserID(config *humioapi.Config, req reconcile.Request, organizationMode string, username string, organization string) (string, error) {
	return "", nil
}

func (h *MockClientConfig) RotateUserApiTokenAndGet(config *humioapi.Config, req reconcile.Request, userID string) (string, error) {
	return "", nil
}

func (h *MockClientConfig) AddUser(config *humioapi.Config, req reconcile.Request, username string, isRoot bool) (*humioapi.User, error) {
	h.apiClient.User = humioapi.User{
		ID:       "id",
		Username: username,
		IsRoot:   isRoot,
	}
	return &h.apiClient.User, nil
}
