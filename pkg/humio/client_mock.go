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
	humioapi "github.com/humio/cli/api"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/kubernetes"
	"net/url"
	"reflect"
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
	Notifier                          humioapi.Notifier
	Alert                             humioapi.Alert
}

type MockClientConfig struct {
	apiClient *ClientMock
	Url       string
	Version   string
}

func NewMockClient(cluster humioapi.Cluster, clusterError error, updateStoragePartitionSchemeError error, updateIngestPartitionSchemeError error, version string) *MockClientConfig {
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
			Notifier:                          humioapi.Notifier{},
			Alert:                             humioapi.Alert{},
		},
		Version: version,
	}

	cluster.StoragePartitions = []humioapi.StoragePartition{storagePartition}
	cluster.IngestPartitions = []humioapi.IngestPartition{ingestPartition}

	return mockClientConfig
}

/*
func (h *MockClientConfig) SetHumioClientConfig(*humioapi.Config, ctrl.Request) {
	return
}
*/

func (h *MockClientConfig) Status(config *humioapi.Config, req reconcile.Request) (humioapi.StatusResponse, error) {
	return humioapi.StatusResponse{
		Status:  "OK",
		Version: h.Version,
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
	baseURL, _ := url.Parse(fmt.Sprintf("http://%s.%s:%d/", hc.Name, hc.Namespace, 8080))
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
	updatedApiClient := h.apiClient
	updatedApiClient.IngestToken = humioapi.IngestToken{}
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
	updatedApiClient := h.apiClient
	updatedApiClient.Repository = humioapi.Repository{}
	return nil
}

func (h *MockClientConfig) GetView(config *humioapi.Config, req reconcile.Request, hv *humiov1alpha1.HumioView) (*humioapi.View, error) {
	return &h.apiClient.View, nil
}

func (h *MockClientConfig) AddView(config *humioapi.Config, req reconcile.Request, hv *humiov1alpha1.HumioView) (*humioapi.View, error) {
	updatedApiClient := h.apiClient

	connections := make([]humioapi.ViewConnection, 0)
	for _, connection := range hv.Spec.Connections {
		connections = append(connections, humioapi.ViewConnection{
			RepoName: connection.RepositoryName,
			Filter:   connection.Filter,
		})
	}

	updatedApiClient.View = humioapi.View{
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
		return fmt.Errorf("failed to parse license type: %s", err)
	}

	if onPremLicense != nil {
		h.apiClient.OnPremLicense = *onPremLicense
	}

	return nil
}

func (h *MockClientConfig) GetNotifier(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAction) (*humioapi.Notifier, error) {
	if h.apiClient.Notifier.Name == "" {
		return nil, fmt.Errorf("could not find notifier in view %s with name: %s", ha.Spec.ViewName, ha.Spec.Name)
	}

	return &h.apiClient.Notifier, nil
}

func (h *MockClientConfig) AddNotifier(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAction) (*humioapi.Notifier, error) {
	updatedApiClient := h.apiClient

	notifier, err := NotifierFromAction(ha)
	if err != nil {
		return notifier, err
	}
	updatedApiClient.Notifier = *notifier
	return &h.apiClient.Notifier, nil
}

func (h *MockClientConfig) UpdateNotifier(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAction) (*humioapi.Notifier, error) {
	return h.AddNotifier(config, req, ha)
}

func (h *MockClientConfig) DeleteNotifier(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAction) error {
	h.apiClient.Notifier = humioapi.Notifier{}
	return nil
}

func (h *MockClientConfig) GetAlert(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAlert) (*humioapi.Alert, error) {
	return &h.apiClient.Alert, nil
}

func (h *MockClientConfig) AddAlert(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAlert) (*humioapi.Alert, error) {
	updatedApiClient := h.apiClient

	actionIdMap, err := h.GetActionIDsMapForAlerts(config, req, ha)
	if err != nil {
		return &humioapi.Alert{}, fmt.Errorf("could not get action id mapping: %s", err)
	}
	alert, err := AlertTransform(ha, actionIdMap)
	if err != nil {
		return alert, err
	}
	updatedApiClient.Alert = *alert
	return &h.apiClient.Alert, nil
}

func (h *MockClientConfig) UpdateAlert(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAlert) (*humioapi.Alert, error) {
	return h.AddAlert(config, req, ha)
}

func (h *MockClientConfig) DeleteAlert(config *humioapi.Config, req reconcile.Request, ha *humiov1alpha1.HumioAlert) error {
	updateApiClient := h.apiClient
	updateApiClient.Alert = humioapi.Alert{}
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
	url, _ := url.Parse("http://localhost:8080/")
	return humioapi.NewClient(humioapi.Config{Address: url})
}
