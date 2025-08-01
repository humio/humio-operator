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

package mcs

import (
	"context"
	"fmt"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/api/humiographql"
	"github.com/humio/humio-operator/internal/controller/suite"
	"github.com/humio/humio-operator/internal/helpers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("HumioMultiClusterSearchView Controller", func() {

	BeforeEach(func() {
		// failed test runs that don't clean up leave resources behind.
		testHumioClient.ClearHumioClientConnections("")
	})

	AfterEach(func() {
		// Add any teardown steps that needs to be executed after each test
		testHumioClient.ClearHumioClientConnections("")
	})

	// Add Tests for OpenAPI validation (or additional CRD features) specified in
	// your API definition.
	// Avoid adding tests for vanilla CRUD operations because they would
	// test Kubernetes API server, which isn't the goal here.
	Context("using two clusters with MCS enabled on both", Label("envtest", "dummy", "real"), func() {
		It("should successfully set up an MCS view", func() {
			keyLocal := types.NamespacedName{
				Name:      "humiocluster-mcs-a",
				Namespace: testProcessNamespace,
			}
			keyRemote := types.NamespacedName{
				Name:      "humiocluster-mcs-b",
				Namespace: testProcessNamespace,
			}
			featureFlagEnvVar := corev1.EnvVar{Name: "INITIAL_FEATURE_FLAGS", Value: "+MultiClusterSearch"}

			toCreateLocal := suite.ConstructBasicSingleNodeHumioCluster(keyLocal, true)
			toCreateLocal.Spec.TLS = &humiov1alpha1.HumioClusterTLSSpec{Enabled: helpers.BoolPtr(false)}
			toCreateLocal.Spec.NodeCount = 1
			toCreateLocal.Spec.EnvironmentVariables = append(toCreateLocal.Spec.EnvironmentVariables, featureFlagEnvVar)
			toCreateRemote := suite.ConstructBasicSingleNodeHumioCluster(keyRemote, true)
			toCreateRemote.Spec.TLS = &humiov1alpha1.HumioClusterTLSSpec{Enabled: helpers.BoolPtr(false)}
			toCreateRemote.Spec.NodeCount = 1
			toCreateRemote.Spec.EnvironmentVariables = append(toCreateRemote.Spec.EnvironmentVariables, featureFlagEnvVar)

			toCreateMCSView := &humiov1alpha1.HumioMultiClusterSearchView{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "mcs-view-happy-path",
					Namespace: keyLocal.Namespace,
				},
				Spec: humiov1alpha1.HumioMultiClusterSearchViewSpec{
					ManagedClusterName: toCreateLocal.Name,
					Name:               "mcs-view",
					Description:        "a view which only contains a local connection",
					Connections: []humiov1alpha1.HumioMultiClusterSearchViewConnection{
						{
							ClusterIdentity: keyLocal.Name,
							Filter:          "*",
							//Tags: nil, // start with no user-configured tags
							Type:           humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeLocal,
							ViewOrRepoName: "humio",
						},
					},
					AutomaticSearch: helpers.BoolPtr(true),
				},
			}

			suite.UsingClusterBy(toCreateMCSView.Name, "Creating both clusters successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreateLocal, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreateLocal)
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreateRemote, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreateRemote)

			suite.UsingClusterBy(toCreateMCSView.Name, "Verifying we can construct humio client for interacting with LogScale cluster where the view should be created")
			clusterConfig, err := helpers.NewCluster(ctx, k8sClient, keyLocal.Name, "", keyLocal.Namespace, helpers.UseCertManager(), true, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(clusterConfig).ToNot(BeNil())
			Expect(clusterConfig.Config()).ToNot(BeNil())

			suite.UsingClusterBy(toCreateMCSView.Name, "Confirming the view does not exist yet")
			// confirm the view does not exist yet
			humioHttpClient := testHumioClient.GetHumioHttpClient(clusterConfig.Config(), reconcile.Request{NamespacedName: keyLocal})
			_, err = testHumioClient.GetMultiClusterSearchView(ctx, humioHttpClient, toCreateMCSView)
			Expect(err).ToNot(Succeed())

			suite.UsingClusterBy(toCreateMCSView.Name, "Creating the custom resource")
			// create the view
			Expect(k8sClient.Create(ctx, toCreateMCSView)).Should(Succeed())

			suite.UsingClusterBy(toCreateMCSView.Name, "Waiting until custom resource reflects that the view was created")
			// wait until custom resource says the view is created
			updatedViewDetails := &humiov1alpha1.HumioMultiClusterSearchView{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, types.NamespacedName{Name: toCreateMCSView.Name, Namespace: toCreateMCSView.Namespace}, updatedViewDetails)
				return updatedViewDetails.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioMultiClusterSearchViewStateExists))

			suite.UsingClusterBy(toCreateMCSView.Name, "Querying the LogScale API directly to confirm the view was created and correctly configured in the initial form")
			// query the humio api directly to confirm the details according to the humio api matches what we expect
			mcsView, err := testHumioClient.GetMultiClusterSearchView(ctx, humioHttpClient, toCreateMCSView)
			Expect(err).Should(Succeed())
			Expect(mcsView.GetIsFederated()).To(BeEquivalentTo(true))
			Expect(mcsView.GetDescription()).To(BeEquivalentTo(&toCreateMCSView.Spec.Description))
			Expect(mcsView.GetAutomaticSearch()).To(BeEquivalentTo(true))
			currentConns := mcsView.GetClusterConnections()
			Expect(currentConns).To(HaveLen(1))
			switch v := currentConns[0].(type) {
			case *humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsLocalClusterConnection:
				Expect(v.GetTargetViewName()).To(Equal(toCreateMCSView.Spec.Connections[0].ViewOrRepoName))
				Expect(v.GetTypename()).To(BeEquivalentTo(helpers.StringPtr("LocalClusterConnection")))
				Expect(v.GetTags()).To(HaveLen(1))
				Expect(v.GetTags()).To(HaveExactElements(
					humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsClusterConnectionTagsClusterConnectionTag{
						Key:   "clusteridentity",
						Value: toCreateMCSView.Spec.Connections[0].ClusterIdentity,
					},
				))
				Expect(v.GetQueryPrefix()).To(Equal(toCreateMCSView.Spec.Connections[0].Filter))
			default:
				Fail(fmt.Sprintf("unexpected type %T", v))
			}

			suite.UsingClusterBy(toCreateMCSView.Name, "Updating the custom resource by appending a remote connection")
			remoteConnection := humiov1alpha1.HumioMultiClusterSearchViewConnection{
				ClusterIdentity: keyRemote.Name,
				Filter:          "*",
				Type:            humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeRemote,
				Url:             fmt.Sprintf("http://%s.%s.svc.cluster.local:8080", keyRemote.Name, keyRemote.Namespace),
				APITokenSource: &humiov1alpha1.HumioMultiClusterSearchViewConnectionAPITokenSpec{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: fmt.Sprintf("%s-admin-token", keyRemote.Name),
						},
						Key: "token",
					},
				},
			}
			updatedDescription := "some updated description"
			Eventually(func() error {
				updatedViewDetails.Spec.Connections = append(updatedViewDetails.Spec.Connections, remoteConnection)
				updatedViewDetails.Spec.Connections[0].Filter = "restrictedfilterstring"
				updatedViewDetails.Spec.Connections[0].ViewOrRepoName = "humio-usage"
				updatedViewDetails.Spec.Connections[0].Tags = []humiov1alpha1.HumioMultiClusterSearchViewConnectionTag{
					{
						Key:   "customkey",
						Value: "customvalue",
					},
				}
				updatedViewDetails.Spec.Description = updatedDescription
				updatedViewDetails.Spec.AutomaticSearch = helpers.BoolPtr(false)
				return k8sClient.Update(ctx, updatedViewDetails)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(toCreateMCSView.Name, "Querying the LogScale API directly to confirm the view was updated and correctly shows the updated list of cluster connections")
			// query the humio api directly to confirm the details according to the humio api reflects that we added a new remote connection
			Eventually(func() []humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsClusterConnection {
				mcsView, err = testHumioClient.GetMultiClusterSearchView(ctx, humioHttpClient, toCreateMCSView)
				Expect(err).Should(Succeed())
				return mcsView.GetClusterConnections()
			}, testTimeout, suite.TestInterval).Should(HaveLen(2))
			Expect(mcsView.GetIsFederated()).To(BeEquivalentTo(true))
			Expect(mcsView.GetDescription()).To(BeEquivalentTo(&updatedDescription))
			Expect(mcsView.GetAutomaticSearch()).To(BeEquivalentTo(false))

			for _, connection := range mcsView.GetClusterConnections() {
				connectionTags := make(map[string]string, len(connection.GetTags()))
				for _, tag := range connection.GetTags() {
					connectionTags[tag.GetKey()] = tag.GetValue()
				}

				switch v := connection.(type) {
				case *humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsLocalClusterConnection:
					Expect(v.GetTypename()).To(BeEquivalentTo(helpers.StringPtr("LocalClusterConnection")))
					Expect(connectionTags).To(HaveLen(2))
					Expect(connectionTags).To(HaveKeyWithValue("clusteridentity", keyLocal.Name))
					Expect(connectionTags).To(HaveKeyWithValue("customkey", "customvalue"))
					Expect(v.GetQueryPrefix()).To(Equal("restrictedfilterstring"))
					Expect(v.GetTargetViewName()).To(Equal("humio-usage"))
				case *humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsRemoteClusterConnection:
					Expect(v.GetTypename()).To(BeEquivalentTo(helpers.StringPtr("RemoteClusterConnection")))
					Expect(connectionTags).To(HaveLen(2))
					Expect(connectionTags).To(HaveKeyWithValue("clusteridentity", keyRemote.Name))
					Expect(connectionTags).To(HaveKey("clusteridentityhash"))
					Expect(v.GetQueryPrefix()).To(Equal(remoteConnection.Filter))
					Expect(v.GetPublicUrl()).To(Equal(remoteConnection.Url))
				default:
					Fail(fmt.Sprintf("unexpected type %T", v))
				}
			}

			// TODO: Consider running query "count(#clusteridentity,distinct=true)" to verify we get the expected connections back

			suite.UsingClusterBy(toCreateMCSView.Name, "Removing the local connection on the custom resource")
			Eventually(func() error {
				updatedViewDetails.Spec.Connections = updatedViewDetails.Spec.Connections[1:]
				return k8sClient.Update(ctx, updatedViewDetails)
			}, testTimeout, suite.TestInterval).Should(Succeed())
			suite.UsingClusterBy(toCreateMCSView.Name, "Querying the LogScale API directly to confirm the view was updated and shows only a single cluster connection")
			Eventually(func() []humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsClusterConnection {
				mcsView, err = testHumioClient.GetMultiClusterSearchView(ctx, humioHttpClient, toCreateMCSView)
				Expect(err).Should(Succeed())
				return mcsView.GetClusterConnections()
			}, testTimeout, suite.TestInterval).Should(HaveLen(1))

			suite.UsingClusterBy(toCreateMCSView.Name, "Verifying all details of the single cluster connection matches what we expect")
			for _, connection := range mcsView.GetClusterConnections() {
				connectionTags := make(map[string]string, len(connection.GetTags()))
				for _, tag := range connection.GetTags() {
					connectionTags[tag.GetKey()] = tag.GetValue()
				}

				switch v := connection.(type) {
				case *humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsRemoteClusterConnection:
					Expect(v.GetTypename()).To(BeEquivalentTo(helpers.StringPtr("RemoteClusterConnection")))
					Expect(connectionTags).To(HaveLen(2))
					Expect(connectionTags).To(HaveKeyWithValue("clusteridentity", keyRemote.Name))
					Expect(connectionTags).To(HaveKey("clusteridentityhash"))
					Expect(v.GetQueryPrefix()).To(Equal(remoteConnection.Filter))
					Expect(v.GetPublicUrl()).To(Equal(remoteConnection.Url))
				default:
					Fail(fmt.Sprintf("unexpected type %T", v))
				}
			}

			suite.UsingClusterBy(toCreateMCSView.Name, "Marking the custom resource as deleted, wait until the custom resource is no longer present which means the finalizer is done")
			Expect(k8sClient.Delete(ctx, updatedViewDetails)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: toCreateMCSView.Name, Namespace: toCreateMCSView.Namespace}, updatedViewDetails)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})
	Context("when MultiClusterSearch feature flag is disabled", Label("real"), func() { // TODO: Currently client_mock.go does not have any details about cluster config, so this is why it is limited to just "real".
		It("should fail to create an MCS view with a local connection when MCS is not enabled on the cluster", func() {
			keyLocal := types.NamespacedName{
				Name:      "humiocluster-missing-featureflag",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(keyLocal, true)
			toCreate.Spec.TLS = &humiov1alpha1.HumioClusterTLSSpec{Enabled: helpers.BoolPtr(false)}
			toCreate.Spec.NodeCount = 1
			toCreate.Spec.EnvironmentVariables = append(toCreate.Spec.EnvironmentVariables, corev1.EnvVar{Name: "INITIAL_FEATURE_FLAGS", Value: "-MultiClusterSearch"})

			toCreateMCSView := &humiov1alpha1.HumioMultiClusterSearchView{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "mcs-view-missing-featureflag-on-local",
					Namespace: keyLocal.Namespace,
				},
				Spec: humiov1alpha1.HumioMultiClusterSearchViewSpec{
					ManagedClusterName: toCreate.Name,
					Name:               "mcs-view",
					Description:        "a view which only contains a local connection",
					Connections: []humiov1alpha1.HumioMultiClusterSearchViewConnection{
						{
							ClusterIdentity: keyLocal.Name,
							Filter:          "*",
							Type:            humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeLocal,
							ViewOrRepoName:  "humio",
						},
					},
					AutomaticSearch: helpers.BoolPtr(true),
				},
			}

			suite.UsingClusterBy(toCreateMCSView.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			clusterConfig, err := helpers.NewCluster(ctx, k8sClient, keyLocal.Name, "", keyLocal.Namespace, helpers.UseCertManager(), true, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(clusterConfig).ToNot(BeNil())
			Expect(clusterConfig.Config()).ToNot(BeNil())

			suite.UsingClusterBy(toCreateMCSView.Name, "Creating the HumioClusterSearchView resource successfully")
			Expect(k8sClient.Create(ctx, toCreateMCSView)).Should(Succeed())

			suite.UsingClusterBy(toCreateMCSView.Name, "Verifying that the state of HumioClusterSearchView get updated to ConfigError")
			updatedViewDetails := &humiov1alpha1.HumioMultiClusterSearchView{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, types.NamespacedName{Name: toCreateMCSView.Name, Namespace: toCreateMCSView.Namespace}, updatedViewDetails)
				return updatedViewDetails.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioMultiClusterSearchViewStateConfigError))

			suite.UsingClusterBy(toCreateMCSView.Name, "Marking the MultiClusterSearchView object as deleted and verifying that the finalizer is done")
			Expect(k8sClient.Delete(ctx, updatedViewDetails)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: toCreateMCSView.Name, Namespace: toCreateMCSView.Namespace}, updatedViewDetails)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})
})
