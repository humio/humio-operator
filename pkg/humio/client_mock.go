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
	"reflect"

	humioapi "github.com/humio/cli/api"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type ClientMock struct {
	Cluster                           humioapi.Cluster
	ClusterError                      error
	UpdateStoragePartitionSchemeError error
	UpdateIngestPartitionSchemeError  error
	IngestToken                       humioapi.IngestToken
	Parser                            humioapi.Parser
	Repository                        humioapi.Repository
	View                              humioapi.View
	OnPremLicense                     humioapi.OnPremLicense
	Action                            humioapi.Action
	Alert                             humioapi.Alert
	User                              humioapi.User
}

type MockClientConfig struct {
	apiClient *ClientMock
}

func NewMockClient(cluster humioapi.Cluster, clusterError error, updateStoragePartitionSchemeError error, updateIngestPartitionSchemeError error) *MockClientConfig {
	storagePartition := humioapi.StoragePartition{}
	ingestPartition := humioapi.IngestPartition{}

	mockClientConfig := &MockClientConfig{
		apiClient: &ClientMock{
			Cluster:                           cluster,
			ClusterError:                      clusterError,
			UpdateStoragePartitionSchemeError: updateStoragePartitionSchemeError,
			UpdateIngestPartitionSchemeError:  updateIngestPartitionSchemeError,
			IngestToken:                       humioapi.IngestToken{},
			Parser:                            humioapi.Parser{},
			Repository:                        humioapi.Repository{},
			View:                              humioapi.View{},
			OnPremLicense:                     humioapi.OnPremLicense{},
			Action:                            humioapi.Action{},
			Alert:                             humioapi.Alert{},
		},
	}

	cluster.StoragePartitions = []humioapi.StoragePartition{storagePartition}
	cluster.IngestPartitions = []humioapi.IngestPartition{ingestPartition}

	return mockClientConfig
}

func (h *MockClientConfig) Status(config *humioapi.Config, req reconcile.Request) (humioapi.StatusResponse, error) {
	return humioapi.StatusResponse{
		Status:  "OK",
		Version: "x.y.z",
	}, nil
}

func (h *MockClientConfig) GetClusters(config *humioapi.Config, req reconcile.Request) (humioapi.Cluster, error) {
	if h.apiClient.ClusterError != nil {
		return humioapi.Cluster{}, h.apiClient.ClusterError
	}
	return h.apiClient.Cluster, nil
}

func (h *MockClientConfig) UpdateStoragePartitionScheme(config *humioapi.Config, req reconcile.Request, sps []humioapi.StoragePartitionInput) error {
	if h.apiClient.UpdateStoragePartitionSchemeError != nil {
		return h.apiClient.UpdateStoragePartitionSchemeError
	}

	var storagePartitions []humioapi.StoragePartition
	for _, storagePartitionInput := range sps {
		var nodeIdsList []int
		for _, nodeID := range storagePartitionInput.NodeIDs {
			nodeIdsList = append(nodeIdsList, int(nodeID))
		}
		storagePartitions = append(storagePartitions, humioapi.StoragePartition{Id: int(storagePartitionInput.ID), NodeIds: nodeIdsList})
	}
	h.apiClient.Cluster.StoragePartitions = storagePartitions

	return nil
}

func (h *MockClientConfig) UpdateIngestPartitionScheme(config *humioapi.Config, req reconcile.Request, ips []humioapi.IngestPartitionInput) error {
	if h.apiClient.UpdateIngestPartitionSchemeError != nil {
		return h.apiClient.UpdateIngestPartitionSchemeError
	}

	var ingestPartitions []humioapi.IngestPartition
	for _, ingestPartitionInput := range ips {
		var nodeIdsList []int
		for _, nodeID := range ingestPartitionInput.NodeIDs {
			nodeIdsList = append(nodeIdsList, int(nodeID))
		}
		ingestPartitions = append(ingestPartitions, humioapi.IngestPartition{Id: int(ingestPartitionInput.ID), NodeIds: nodeIdsList})
	}
	h.apiClient.Cluster.IngestPartitions = ingestPartitions

	return nil
}

func (h *MockClientConfig) SuggestedStoragePartitions(config *humioapi.Config, req reconcile.Request) ([]humioapi.StoragePartitionInput, error) {
	return []humioapi.StoragePartitionInput{}, nil
}

func (h *MockClientConfig) SuggestedIngestPartitions(config *humioapi.Config, req reconcile.Request) ([]humioapi.IngestPartitionInput, error) {
	return []humioapi.IngestPartitionInput{}, nil
}

func (h *MockClientConfig) GetBaseURL(config *humioapi.Config, req reconcile.Request, hc *humiov1alpha1.HumioCluster) *url.URL {
	baseURL, _ := url.Parse(fmt.Sprintf("http://%s-headless.%s:%d/", hc.Name, hc.Namespace, 8080))
	return baseURL
}

func (h *MockClientConfig) TestAPIToken(config *humioapi.Config, req reconcile.Request) error {
	return nil
}

func (h *MockClientConfig) AddIngestToken(config *humioapi.Config, req reconcile.Request, hit *humiov1alpha1.HumioIngestToken) (*humioapi.IngestToken, error) {
	h.apiClient.IngestToken = humioapi.IngestToken{
		Name:           hit.Spec.Name,
		AssignedParser: hit.Spec.ParserName,
		Token:          "mocktoken",
	}
	return &h.apiClient.IngestToken, nil
}

func (h *MockClientConfig) GetIngestToken(config *humioapi.Config, req reconcile.Request, hit *humiov1alpha1.HumioIngestToken) (*humioapi.IngestToken, error) {
	return &h.apiClient.IngestToken, nil
}

func (h *MockClientConfig) UpdateIngestToken(config *humioapi.Config, req reconcile.Request, hit *humiov1alpha1.HumioIngestToken) (*humioapi.IngestToken, error) {
	return h.AddIngestToken(config, req, hit)
}

func (h *MockClientConfig) DeleteIngestToken(config *humioapi.Config, req reconcile.Request, hit *humiov1alpha1.HumioIngestToken) error {
	h.apiClient.IngestToken = humioapi.IngestToken{}
	return nil
}

func (h *MockClientConfig) AddParser(config *humioapi.Config, req reconcile.Request, hp *humiov1alpha1.HumioParser) (*humioapi.Parser, error) {
	h.apiClient.Parser = humioapi.Parser{
		Name:      hp.Spec.Name,
		Script:    hp.Spec.ParserScript,
		TagFields: hp.Spec.TagFields,
		Tests:     hp.Spec.TestData,
	}
	return &h.apiClient.Parser, nil
}

func (h *MockClientConfig) GetParser(config *humioapi.Config, req reconcile.Request, hp *humiov1alpha1.HumioParser) (*humioapi.Parser, error) {
	if h.apiClient.Parser.Name == "" {
		return nil, fmt.Errorf("could not find parser in view %q with name %q, err=%w", hp.Spec.RepositoryName, hp.Spec.Name, humioapi.EntityNotFound{})
	}

	return &h.apiClient.Parser, nil
}

func (h *MockClientConfig) UpdateParser(config *humioapi.Config, req reconcile.Request, hp *humiov1alpha1.HumioParser) (*humioapi.Parser, error) {
	return h.AddParser(config, req, hp)
}

func (h *MockClientConfig) DeleteParser(config *humioapi.Config, req reconcile.Request, hp *humiov1alpha1.HumioParser) error {
	h.apiClient.Parser = humioapi.Parser{}
	return nil
}

func (h *MockClientConfig) AddRepository(config *humioapi.Config, req reconcile.Request, hr *humiov1alpha1.HumioRepository) (*humioapi.Repository, error) {
	h.apiClient.Repository = humioapi.Repository{
		ID:                     kubernetes.RandomString(),
		Name:                   hr.Spec.Name,
		Description:            hr.Spec.Description,
		RetentionDays:          float64(hr.Spec.Retention.TimeInDays),
		IngestRetentionSizeGB:  float64(hr.Spec.Retention.IngestSizeInGB),
		StorageRetentionSizeGB: float64(hr.Spec.Retention.StorageSizeInGB),
	}
	return &h.apiClient.Repository, nil
}

func (h *MockClientConfig) GetRepository(config *humioapi.Config, req reconcile.Request, hr *humiov1alpha1.HumioRepository) (*humioapi.Repository, error) {
	return &h.apiClient.Repository, nil
}

func (h *MockClientConfig) UpdateRepository(config *humioapi.Config, req reconcile.Request, hr *humiov1alpha1.HumioRepository) (*humioapi.Repository, error) {
	return h.AddRepository(config, req, hr)
}

func (h *MockClientConfig) DeleteRepository(config *humioapi.Config, req reconcile.Request, hr *humiov1alpha1.HumioRepository) error {
	h.apiClient.Repository = humioapi.Repository{}
	return nil
}

func (h *MockClientConfig) AddUser(config *humioapi.Config, req reconcile.Request, hu *humiov1alpha1.HumioUser) (*humioapi.User, error) {
	h.apiClient.User = humioapi.User{
		Username:    hu.Spec.Username,
		ID:          kubernetes.RandomString(),
		FullName:    hu.Spec.FullName,
		Email:       hu.Spec.Email,
		Company:     hu.Spec.Company,
		CountryCode: hu.Spec.CountryCode,
		Picture:     hu.Spec.Picture,
		IsRoot:      hu.Spec.IsRoot,
	}
	return &h.apiClient.User, nil
}

func (h *MockClientConfig) GetUser(config *humioapi.Config, req reconcile.Request, hu *humiov1alpha1.HumioUser) (*humioapi.User, error) {
	return &h.apiClient.User, nil
}

func (h *MockClientConfig) UpdateUser(config *humioapi.Config, req reconcile.Request, hu *humiov1alpha1.HumioUser) (*humioapi.User, error) {
	return h.AddUser(config, req, hu)
}

func (h *MockClientConfig) DeleteUser(config *humioapi.Config, req reconcile.Request, hu *humiov1alpha1.HumioUser) error {
	h.apiClient.User = humioapi.User{}
	return nil
}

func (h *MockClientConfig) GetView(config *humioapi.Config, req reconcile.Request, hv *humiov1alpha1.HumioView) (*humioapi.View, error) {
	return &h.apiClient.View, nil
}

func (h *MockClientConfig) AddView(config *humioapi.Config, req reconcile.Request, hv *humiov1alpha1.HumioView) (*humioapi.View, error) {
	connections := make([]humioapi.ViewConnection, 0)
	for _, connection := range hv.Spec.Connections {
		connections = append(connections, humioapi.ViewConnection{
			RepoName: connection.RepositoryName,
			Filter:   connection.Filter,
		})
	}

	h.apiClient.View = humioapi.View{
		Name:        hv.Spec.Name,
		Connections: connections,
	}
	return &h.apiClient.View, nil
}

func (h *MockClientConfig) UpdateView(config *humioapi.Config, req reconcile.Request, hv *humiov1alpha1.HumioView) (*humioapi.View, error) {
	return h.AddView(config, req, hv)
}

func (h *MockClientConfig) DeleteView(config *humioapi.Config, req reconcile.Request, hv *humiov1alpha1.HumioView) error {
	h.apiClient.View = humioapi.View{}
	return nil
}

func (h *MockClientConfig) GetLicense(config *humioapi.Config, req reconcile.Request) (humioapi.License, error) {
	emptyOnPremLicense := humioapi.OnPremLicense{}

	if !reflect.DeepEqual(h.apiClient.OnPremLicense, emptyOnPremLicense) {
		return h.apiClient.OnPremLicense, nil
	}

	// by default, humio starts without a license
	return emptyOnPremLicense, nil
}

func (h *MockClientConfig) InstallLicense(config *humioapi.Config, req reconcile.Request, licenseString string) error {
	onPremLicense, err := ParseLicenseType(licenseString)
	if err != nil {
		return fmt.Errorf("failed to parse license type: %w", err)
	}

	if onPremLicense != nil {
		h.apiClient.OnPremLicense = *onPremLicense
	}

	return nil
}

func (h *MockClientConfig) GetAction(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAction) (*humioapi.Action, error) {
	if h.apiClient.Action.Name == "" {
		return nil, fmt.Errorf("could not find action in view %q with name %q, err=%w", ha.Spec.ViewName, ha.Spec.Name, humioapi.EntityNotFound{})
	}

	return &h.apiClient.Action, nil
}

func (h *MockClientConfig) AddAction(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAction) (*humioapi.Action, error) {
	action, err := ActionFromActionCR(ha)
	if err != nil {
		return action, err
	}
	h.apiClient.Action = *action
	return &h.apiClient.Action, nil
}

func (h *MockClientConfig) UpdateAction(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAction) (*humioapi.Action, error) {
	return h.AddAction(config, req, ha)
}

func (h *MockClientConfig) DeleteAction(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAction) error {
	h.apiClient.Action = humioapi.Action{}
	return nil
}

func (h *MockClientConfig) GetAlert(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAlert) (*humioapi.Alert, error) {
	if h.apiClient.Alert.Name == "" {
		return nil, fmt.Errorf("could not find alert in view %q with name %q, err=%w", ha.Spec.ViewName, ha.Spec.Name, humioapi.EntityNotFound{})
	}
	return &h.apiClient.Alert, nil
}

func (h *MockClientConfig) AddAlert(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAlert) (*humioapi.Alert, error) {
	actionIdMap, err := h.GetActionIDsMapForAlerts(config, req, ha)
	if err != nil {
		return &humioapi.Alert{}, fmt.Errorf("could not get action id mapping: %w", err)
	}
	alert, err := AlertTransform(ha, actionIdMap)
	if err != nil {
		return alert, err
	}
	h.apiClient.Alert = *alert
	return &h.apiClient.Alert, nil
}

func (h *MockClientConfig) UpdateAlert(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAlert) (*humioapi.Alert, error) {
	return h.AddAlert(config, req, ha)
}

func (h *MockClientConfig) DeleteAlert(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAlert) error {
	h.apiClient.Alert = humioapi.Alert{}
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

func (h *MockClientConfig) GetHumioClient(config *humioapi.Config, req ctrl.Request) *humioapi.Client {
	clusterURL, _ := url.Parse("http://localhost:8080/")
	return humioapi.NewClient(humioapi.Config{Address: clusterURL})
}

func (h *MockClientConfig) ClearHumioClientConnections() {
	h.apiClient.IngestToken = humioapi.IngestToken{}
	h.apiClient.Parser = humioapi.Parser{}
	h.apiClient.Repository = humioapi.Repository{}
	h.apiClient.View = humioapi.View{}
	h.apiClient.OnPremLicense = humioapi.OnPremLicense{}
	h.apiClient.Action = humioapi.Action{}
	h.apiClient.Alert = humioapi.Alert{}
}
