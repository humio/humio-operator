package resources

import (
	"context"
	"fmt"
	"strings"

	"github.com/Masterminds/semver/v3"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	humiov1beta1 "github.com/humio/humio-operator/api/v1beta1"
	humioapi "github.com/humio/humio-operator/internal/api"
	"github.com/humio/humio-operator/internal/api/humiographql"
	"github.com/humio/humio-operator/internal/controller/suite"
	"github.com/humio/humio-operator/internal/helpers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("Humio Scheduled Search v1beta1", Ordered, Label("envtest", "dummy", "real"), func() {

	var localAction *humiov1alpha1.HumioAction
	localView := &testRepo
	ctx := context.Background()
	processID := GinkgoParallelProcess()
	hssActionName := fmt.Sprintf("hss-action-%d", processID)
	hssName := fmt.Sprintf("example-hss-%d", processID)

	BeforeAll(func() {
		dependentEmailActionSpec := humiov1alpha1.HumioActionSpec{
			ManagedClusterName: clusterKey.Name,
			Name:               hssActionName,
			ViewName:           localView.Spec.Name,
			AllowDataDeletion:  true,
			EmailProperties: &humiov1alpha1.HumioActionEmailProperties{
				Recipients: []string{emailActionExample},
			},
		}

		actionKey := types.NamespacedName{
			Name:      hssActionName,
			Namespace: clusterKey.Namespace,
		}

		toCreateDependentAction := &humiov1alpha1.HumioAction{
			ObjectMeta: metav1.ObjectMeta{
				Name:      actionKey.Name,
				Namespace: actionKey.Namespace,
			},
			Spec: dependentEmailActionSpec,
		}
		Expect(k8sClient.Create(ctx, toCreateDependentAction)).Should(Succeed())

		localAction = &humiov1alpha1.HumioAction{}
		Eventually(func() string {
			_ = k8sClient.Get(ctx, actionKey, localAction)
			return localAction.Status.State
		}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))
	})

	AfterAll(func() {
		// Clean up scheduled searches first (best-effort) to avoid blocking action deletion
		scheduledSearchList := &humiov1beta1.HumioScheduledSearchList{}
		listOpts := []client.ListOption{
			client.InNamespace(clusterKey.Namespace),
		}
		Eventually(func() error {
			return k8sClient.List(ctx, scheduledSearchList, listOpts...)
		}, testTimeout, suite.TestInterval).Should(Succeed())

		for _, hss := range scheduledSearchList.Items {
			// Delete scheduled searches that reference our action
			for _, actionName := range hss.Spec.Actions {
				if actionName == hssActionName {
					_ = k8sClient.Delete(ctx, &hss)
					break
				}
			}
		}

		// Wait a bit for scheduled searches to be cleaned up from LogScale
		Eventually(func() int {
			if err := k8sClient.List(ctx, scheduledSearchList, listOpts...); err != nil {
				return -1
			}
			count := 0
			for _, hss := range scheduledSearchList.Items {
				for _, actionName := range hss.Spec.Actions {
					if actionName == hssActionName {
						count++
						break
					}
				}
			}
			return count
		}, testTimeout, suite.TestInterval).Should(Equal(0))

		action := &humiov1alpha1.HumioAction{}
		actionKey := types.NamespacedName{
			Name:      hssActionName,
			Namespace: clusterKey.Namespace,
		}

		// test ha exists
		Eventually(func() error {
			err := k8sClient.Get(ctx, actionKey, action)
			return err
		}, testTimeout, suite.TestInterval).Should(Succeed())

		// delete ha
		Expect(k8sClient.Delete(ctx, action)).To(Succeed())
		Eventually(func() bool {
			err := k8sClient.Get(ctx, actionKey, action)
			return k8serrors.IsNotFound(err)
		}, testTimeout, suite.TestInterval).Should(BeTrue())
	})
	It("Should succeed and be stored as v1beta1", func() {
		scheduledSearchSpec := humiov1alpha1.HumioScheduledSearchSpec{
			ManagedClusterName: clusterKey.Name,
			Name:               hssName,
			ViewName:           localView.Spec.Name,
			QueryString:        "#repo = humio | error = true",
			QueryStart:         "1h",
			QueryEnd:           "now",
			//SearchIntervalSeconds: 3600,
			//QueryTimestampType:    "IngestTimestamp",
			Schedule: "0 * * * *",
			TimeZone: "UTC",
			//MaxWaitTimeSeconds:    60,
			BackfillLimit:     3,
			Enabled:           true,
			Description:       "humio scheduled search",
			Actions:           []string{localAction.Spec.Name},
			Labels:            []string{"some-label"},
			AllowDataDeletion: true,
		}

		key := types.NamespacedName{
			Name:      hssName,
			Namespace: clusterKey.Namespace,
		}

		toCreateScheduledSearch := &humiov1alpha1.HumioScheduledSearch{
			ObjectMeta: metav1.ObjectMeta{
				Name:      key.Name,
				Namespace: key.Namespace,
			},
			Spec: scheduledSearchSpec,
		}

		// we expect warnings
		var warningBuilder strings.Builder
		// Create a new manager config with warning handler
		cfg := rest.CopyConfig(k8sOperatorManager.GetConfig())
		cfg.WarningHandler = rest.NewWarningWriter(&warningBuilder, rest.WarningWriterOptions{
			Deduplicate: false,
		})

		// Create new client with warning capture
		warningClient, err := client.New(cfg, client.Options{
			Scheme: k8sOperatorManager.GetScheme(),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(warningClient.Create(ctx, toCreateScheduledSearch)).Should(Succeed())
		Expect(warningBuilder.String()).To(ContainSubstring("Warning: core.humio.com/v1alpha1 HumioScheduledSearch is being deprecated; use core.humio.com/v1beta1"))

		// we expect to map to v1beta1
		hssv1beta1 := &humiov1beta1.HumioScheduledSearch{}
		Eventually(func() error {
			err := k8sClient.Get(ctx, key, hssv1beta1)
			return err
		}, testTimeout, suite.TestInterval).Should(Succeed())

		// Verify Ready condition is set
		Eventually(func() bool {
			err := k8sClient.Get(ctx, key, hssv1beta1)
			if err != nil {
				return false
			}
			readyCondition := meta.FindStatusCondition(hssv1beta1.Status.Conditions,
				humiov1beta1.ScheduledSearchConditionTypeReady)
			return readyCondition != nil &&
				readyCondition.Status == metav1.ConditionTrue &&
				readyCondition.Reason == humiov1beta1.ScheduledSearchReasonReady
		}, testTimeout, suite.TestInterval).Should(BeTrue())

		// status.state should be set to Exists (backward compatibility)
		Eventually(func() string {
			_ = k8sClient.Get(ctx, key, hssv1beta1)
			return hssv1beta1.Status.State
		}, testTimeout, suite.TestInterval).Should(Equal(humiov1beta1.HumioScheduledSearchStateExists))
		Expect(hssv1beta1.Status.State).Should(Equal(humiov1beta1.HumioScheduledSearchStateExists))

		Expect(hssv1beta1.Spec.Name).Should(Equal(toCreateScheduledSearch.Spec.Name))
		Expect(hssv1beta1.Spec.SearchIntervalSeconds).Should(Equal(int64(3600)))
		Expect(hssv1beta1.Spec.SearchIntervalOffsetSeconds).Should(Equal(helpers.Int64Ptr(0))) // now means 0
		Expect(hssv1beta1.Spec.QueryTimestampType).Should(Equal(humiographql.QueryTimestampTypeEventtimestamp))
		Expect(hssv1beta1.Spec.QueryString).Should(Equal(toCreateScheduledSearch.Spec.QueryString))
		Expect(hssv1beta1.Spec.Schedule).Should(Equal(toCreateScheduledSearch.Spec.Schedule))
		Expect(hssv1beta1.Spec.TimeZone).Should(Equal(toCreateScheduledSearch.Spec.TimeZone))
		Expect(hssv1beta1.Spec.Enabled).Should(Equal(toCreateScheduledSearch.Spec.Enabled))
		Expect(hssv1beta1.Spec.Description).Should(Equal(toCreateScheduledSearch.Spec.Description))
		Expect(hssv1beta1.Spec.Actions).Should(Equal(toCreateScheduledSearch.Spec.Actions))
		Expect(hssv1beta1.Spec.Labels).Should(Equal(toCreateScheduledSearch.Spec.Labels))

		// we also expect initial version to work
		hssv1alpha1 := &humiov1alpha1.HumioScheduledSearch{}
		Eventually(func() error {
			err := k8sClient.Get(ctx, key, hssv1alpha1)
			return err
		}, testTimeout, suite.TestInterval).Should(Succeed())
		Expect(hssv1alpha1.Spec.Name).Should(Equal(toCreateScheduledSearch.Spec.Name))
		Expect(hssv1alpha1.Spec.QueryStart).Should(Equal(toCreateScheduledSearch.Spec.QueryStart))
		Expect(hssv1alpha1.Spec.QueryEnd).Should(Equal(toCreateScheduledSearch.Spec.QueryEnd))

		// test hss exists
		Eventually(func() error {
			err := k8sClient.Get(ctx, key, toCreateScheduledSearch)
			return err
		}, testTimeout, suite.TestInterval).Should(Succeed())

		// delete hss
		Expect(k8sClient.Delete(ctx, toCreateScheduledSearch)).To(Succeed())
		// check its gone
		Eventually(func() bool {
			err := k8sClient.Get(ctx, key, toCreateScheduledSearch)
			return k8serrors.IsNotFound(err)
		}, testTimeout, suite.TestInterval).Should(BeTrue())

	})

	It("should handle scheduled search correctly", func() {
		ctx := context.Background()
		suite.UsingClusterBy(clusterKey.Name, "HumioScheduledSearch: Should handle scheduled search correctly")
		scheduledSearchSpec := humiov1alpha1.HumioScheduledSearchSpec{
			ManagedClusterName: clusterKey.Name,
			Name:               "example-scheduled-search",
			ViewName:           localView.Spec.Name,
			QueryString:        "#repo = humio | error = true",
			QueryStart:         "1h",
			QueryEnd:           "now",
			Schedule:           "0 * * * *",
			TimeZone:           "UTC",
			BackfillLimit:      3,
			Enabled:            true,
			Description:        "humio scheduled search",
			Actions:            []string{localAction.Spec.Name},
			Labels:             []string{"some-label"},
			AllowDataDeletion:  true,
		}

		key := types.NamespacedName{
			Name:      "humio-scheduled-search",
			Namespace: clusterKey.Namespace,
		}

		toCreateScheduledSearch := &humiov1alpha1.HumioScheduledSearch{
			ObjectMeta: metav1.ObjectMeta{
				Name:      key.Name,
				Namespace: key.Namespace,
			},
			Spec: scheduledSearchSpec,
		}

		suite.UsingClusterBy(clusterKey.Name, "HumioScheduledSearch: Creating the scheduled search successfully")
		Expect(k8sClient.Create(ctx, toCreateScheduledSearch)).Should(Succeed())

		fetchedScheduledSearch := &humiov1alpha1.HumioScheduledSearch{}
		_ = k8sClient.Get(ctx, key, fetchedScheduledSearch)
		Eventually(func() error {
			err = k8sClient.Get(ctx, key, fetchedScheduledSearch)
			return err
		}, testTimeout, suite.TestInterval).Should(Succeed())

		// retrieve both versions
		fetchedScheduledSearchBeta := &humiov1beta1.HumioScheduledSearch{}
		fetchedScheduledSearchAlpha := &humiov1alpha1.HumioScheduledSearch{}
		// fetch as v1beta1
		Eventually(func() error {
			err = k8sClient.Get(ctx, key, fetchedScheduledSearchBeta)
			return err
		}, testTimeout, suite.TestInterval).Should(Succeed())
		// fetch as v1alpha1
		Eventually(func() error {
			err = k8sClient.Get(ctx, key, fetchedScheduledSearchAlpha)
			return err
		}, testTimeout, suite.TestInterval).Should(Succeed())

		// depending on the running LS version
		logscaleVersion, _ := helpers.GetClusterImageVersion(ctx, k8sClient, clusterKey.Namespace, fetchedScheduledSearch.Spec.ManagedClusterName,
			fetchedScheduledSearch.Spec.ExternalClusterName)
		semVersion, _ := semver.NewVersion(logscaleVersion)
		v2MinVersion, _ := semver.NewVersion(humiov1beta1.HumioScheduledSearchV1alpha1DeprecatedInVersion)

		// LS version supports V2
		if semVersion.GreaterThanEqual(v2MinVersion) {
			var scheduledSearch *humiographql.ScheduledSearchDetailsV2
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() error {
				scheduledSearch, err = humioClient.GetScheduledSearchV2(ctx, humioHttpClient, fetchedScheduledSearchBeta)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(scheduledSearch).ToNot(BeNil())
			Eventually(func() error {
				return humioClient.ValidateActionsForScheduledSearchV2(ctx, humioHttpClient, fetchedScheduledSearchBeta)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Expect(humioapi.GetActionNames(scheduledSearch.ActionsV2)).To(Equal(toCreateScheduledSearch.Spec.Actions))
			Expect(scheduledSearch.Name).To(Equal(toCreateScheduledSearch.Spec.Name))
			Expect(scheduledSearch.Description).To(Equal(&toCreateScheduledSearch.Spec.Description))
			Expect(scheduledSearch.Labels).To(Equal(toCreateScheduledSearch.Spec.Labels))
			Expect(scheduledSearch.Enabled).To(Equal(toCreateScheduledSearch.Spec.Enabled))
			Expect(scheduledSearch.QueryString).To(Equal(toCreateScheduledSearch.Spec.QueryString))
			Expect(scheduledSearch.SearchIntervalSeconds).To(Equal(fetchedScheduledSearchBeta.Spec.SearchIntervalSeconds))
			Expect(scheduledSearch.SearchIntervalOffsetSeconds).To(Equal(fetchedScheduledSearchBeta.Spec.SearchIntervalOffsetSeconds))
			Expect(scheduledSearch.Schedule).To(Equal(toCreateScheduledSearch.Spec.Schedule))
			Expect(scheduledSearch.TimeZone).To(Equal(toCreateScheduledSearch.Spec.TimeZone))
		} else { // LS version supports only V1
			var scheduledSearch *humiographql.ScheduledSearchDetails
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() error {
				scheduledSearch, err = humioClient.GetScheduledSearch(ctx, humioHttpClient, fetchedScheduledSearchAlpha)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(scheduledSearch).ToNot(BeNil())
			Eventually(func() error {
				return humioClient.ValidateActionsForScheduledSearch(ctx, humioHttpClient, fetchedScheduledSearchAlpha)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Expect(humioapi.GetActionNames(scheduledSearch.ActionsV2)).To(Equal(toCreateScheduledSearch.Spec.Actions))
			Expect(scheduledSearch.Name).To(Equal(toCreateScheduledSearch.Spec.Name))
			Expect(scheduledSearch.Description).To(Equal(&toCreateScheduledSearch.Spec.Description))
			Expect(scheduledSearch.Labels).To(Equal(toCreateScheduledSearch.Spec.Labels))
			Expect(scheduledSearch.Enabled).To(Equal(toCreateScheduledSearch.Spec.Enabled))
			Expect(scheduledSearch.QueryString).To(Equal(toCreateScheduledSearch.Spec.QueryString))
			Expect(scheduledSearch.Start).To(Equal(toCreateScheduledSearch.Spec.QueryStart))
			Expect(scheduledSearch.End).To(Equal(toCreateScheduledSearch.Spec.QueryEnd))
			Expect(scheduledSearch.Schedule).To(Equal(toCreateScheduledSearch.Spec.Schedule))
			Expect(scheduledSearch.TimeZone).To(Equal(toCreateScheduledSearch.Spec.TimeZone))
		}

		suite.UsingClusterBy(clusterKey.Name, "HumioScheduledSearch: Updating the scheduled search successfully")
		updatedScheduledSearch := toCreateScheduledSearch
		updatedScheduledSearch.Spec.QueryString = "#repo = humio | updated_field = true | error = true"
		updatedScheduledSearch.Spec.QueryStart = "2h"
		updatedScheduledSearch.Spec.QueryEnd = "30m"
		updatedScheduledSearch.Spec.Schedule = "0 0 * * *"
		updatedScheduledSearch.Spec.TimeZone = "UTC-01"
		updatedScheduledSearch.Spec.BackfillLimit = 5
		updatedScheduledSearch.Spec.Enabled = false
		updatedScheduledSearch.Spec.Description = "updated humio scheduled search"
		updatedScheduledSearch.Spec.Actions = []string{localAction.Spec.Name}

		// update CR with new values
		suite.UsingClusterBy(clusterKey.Name, "HumioScheduledSearch: Waiting for the scheduled search to be updated")
		Eventually(func() error {
			_ = k8sClient.Get(ctx, key, fetchedScheduledSearch)
			fetchedScheduledSearch.Spec.QueryString = updatedScheduledSearch.Spec.QueryString
			fetchedScheduledSearch.Spec.QueryStart = updatedScheduledSearch.Spec.QueryStart
			fetchedScheduledSearch.Spec.QueryEnd = updatedScheduledSearch.Spec.QueryEnd
			fetchedScheduledSearch.Spec.Schedule = updatedScheduledSearch.Spec.Schedule
			fetchedScheduledSearch.Spec.TimeZone = updatedScheduledSearch.Spec.TimeZone
			fetchedScheduledSearch.Spec.BackfillLimit = updatedScheduledSearch.Spec.BackfillLimit
			fetchedScheduledSearch.Spec.Enabled = updatedScheduledSearch.Spec.Enabled
			fetchedScheduledSearch.Spec.Description = updatedScheduledSearch.Spec.Description
			return k8sClient.Update(ctx, fetchedScheduledSearch)
		}, testTimeout, suite.TestInterval).Should(Succeed())

		humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})

		// v2
		if semVersion.GreaterThanEqual(v2MinVersion) {
			// refresh beta version
			Eventually(func() error {
				err = k8sClient.Get(ctx, key, fetchedScheduledSearchBeta)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			suite.UsingClusterBy(clusterKey.Name, "HumioScheduledSearchV2: Verifying the scheduled search matches the expected")
			verifiedScheduledSearch := humiographql.ScheduledSearchDetailsV2{
				Name:                        updatedScheduledSearch.Spec.Name,
				QueryString:                 updatedScheduledSearch.Spec.QueryString,
				Description:                 &updatedScheduledSearch.Spec.Description,
				SearchIntervalSeconds:       int64(7200),                   // QueryStart(2h)
				SearchIntervalOffsetSeconds: helpers.Int64Ptr(int64(1800)), // QueryEnd 30m
				Schedule:                    updatedScheduledSearch.Spec.Schedule,
				TimeZone:                    updatedScheduledSearch.Spec.TimeZone,
				Enabled:                     updatedScheduledSearch.Spec.Enabled,
				ActionsV2:                   humioapi.ActionNamesToEmailActions(updatedScheduledSearch.Spec.Actions),
				Labels:                      updatedScheduledSearch.Spec.Labels,
				QueryOwnership: &humiographql.SharedQueryOwnershipTypeOrganizationOwnership{
					Typename: helpers.StringPtr("OrganizationOwnership"),
					QueryOwnershipOrganizationOwnership: humiographql.QueryOwnershipOrganizationOwnership{
						Typename: helpers.StringPtr("OrganizationOwnership"),
					},
				},
				BackfillLimitV2:    helpers.IntPtr(updatedScheduledSearch.Spec.BackfillLimit),
				MaxWaitTimeSeconds: helpers.Int64Ptr(int64(0)),                    // V1 doesn't have this field
				QueryTimestampType: humiographql.QueryTimestampTypeEventtimestamp, // humiographql.QueryTimestampTypeEventtimestamp
			}

			Eventually(func() *humiographql.ScheduledSearchDetailsV2 {
				updatedScheduledSearch, err := humioClient.GetScheduledSearchV2(ctx, humioHttpClient, fetchedScheduledSearchBeta)
				if err != nil {
					return nil
				}
				// Ignore the ID
				updatedScheduledSearch.Id = ""

				return updatedScheduledSearch
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(&verifiedScheduledSearch))
		} else { // v1
			// refresh alpha version
			Eventually(func() error {
				err = k8sClient.Get(ctx, key, fetchedScheduledSearchAlpha)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			suite.UsingClusterBy(clusterKey.Name, "HumioScheduledSearch: Verifying the scheduled search matches the expected")
			verifiedScheduledSearch := humiographql.ScheduledSearchDetails{
				Name:        updatedScheduledSearch.Spec.Name,
				QueryString: updatedScheduledSearch.Spec.QueryString,
				Description: &updatedScheduledSearch.Spec.Description,
				Start:       updatedScheduledSearch.Spec.QueryStart,
				End:         updatedScheduledSearch.Spec.QueryEnd,
				Schedule:    updatedScheduledSearch.Spec.Schedule,
				TimeZone:    updatedScheduledSearch.Spec.TimeZone,
				Enabled:     updatedScheduledSearch.Spec.Enabled,
				ActionsV2:   humioapi.ActionNamesToEmailActions(updatedScheduledSearch.Spec.Actions),
				Labels:      updatedScheduledSearch.Spec.Labels,
				QueryOwnership: &humiographql.SharedQueryOwnershipTypeOrganizationOwnership{
					Typename: helpers.StringPtr("OrganizationOwnership"),
					QueryOwnershipOrganizationOwnership: humiographql.QueryOwnershipOrganizationOwnership{
						Typename: helpers.StringPtr("OrganizationOwnership"),
					},
				},
				BackfillLimit: updatedScheduledSearch.Spec.BackfillLimit,
			}

			Eventually(func() *humiographql.ScheduledSearchDetails {
				updatedScheduledSearch, err := humioClient.GetScheduledSearch(ctx, humioHttpClient, fetchedScheduledSearchAlpha)
				if err != nil {
					return nil
				}
				// Ignore the ID
				updatedScheduledSearch.Id = ""

				return updatedScheduledSearch
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(&verifiedScheduledSearch))
		}

		// delete hss
		Expect(k8sClient.Delete(ctx, toCreateScheduledSearch)).To(Succeed())
		Eventually(func() bool {
			err := k8sClient.Get(ctx, key, fetchedScheduledSearch)
			return k8serrors.IsNotFound(err)
		}, testTimeout, suite.TestInterval).Should(BeTrue())
	})

	Context("Rename Tests", func() {
		It("should block scheduled search rename without annotation", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      fmt.Sprintf("scheduledsearch-rename-blocked-%d", GinkgoRandomSeed()),
				Namespace: clusterKey.Namespace,
			}

			toCreate := &humiov1alpha1.HumioScheduledSearch{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioScheduledSearchSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               fmt.Sprintf("original-scheduledsearch-%d", GinkgoRandomSeed()),
					ViewName:           localView.Spec.Name,
					QueryString:        "#repo = humio | error = true",
					QueryStart:         "1h",
					QueryEnd:           "now",
					Schedule:           "0 * * * *",
					TimeZone:           "UTC",
					BackfillLimit:      3,
					Enabled:            true,
					Description:        "test rename blocking",
					Actions:            []string{localAction.Spec.Name},
					Labels:             []string{"test-label"},
					AllowDataDeletion:  true,
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioScheduledSearch: Creating scheduled search")
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())

			fetched := &humiov1alpha1.HumioScheduledSearch{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, fetched)
				return fetched.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioScheduledSearchStateExists))

			suite.UsingClusterBy(clusterKey.Name, "HumioScheduledSearch: Attempting rename without annotation")
			Eventually(func() error {
				if err := k8sClient.Get(ctx, key, fetched); err != nil {
					return err
				}
				fetched.Spec.Name = fmt.Sprintf("renamed-scheduledsearch-%d", GinkgoRandomSeed())
				return k8sClient.Update(ctx, fetched)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioScheduledSearch: Verifying ConfigError state")
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, fetched)
				return fetched.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioScheduledSearchStateConfigError))

			suite.UsingClusterBy(clusterKey.Name, "HumioScheduledSearch: Cleaning up")
			Expect(k8sClient.Delete(ctx, fetched)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetched)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should delete-recreate scheduled search with annotation", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      fmt.Sprintf("scheduledsearch-rename-allowed-%d", GinkgoRandomSeed()),
				Namespace: clusterKey.Namespace,
			}

			toCreate := &humiov1alpha1.HumioScheduledSearch{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioScheduledSearchSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               fmt.Sprintf("original-scheduledsearch-allowed-%d", GinkgoRandomSeed()),
					ViewName:           localView.Spec.Name,
					QueryString:        "#repo = humio | error = true",
					QueryStart:         "1h",
					QueryEnd:           "now",
					Schedule:           "0 * * * *",
					TimeZone:           "UTC",
					BackfillLimit:      3,
					Enabled:            true,
					Description:        "test rename allowing",
					Actions:            []string{localAction.Spec.Name},
					Labels:             []string{"test-label"},
					AllowDataDeletion:  true,
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioScheduledSearch: Creating scheduled search")
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())

			fetched := &humiov1alpha1.HumioScheduledSearch{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, fetched)
				return fetched.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioScheduledSearchStateExists))

			suite.UsingClusterBy(clusterKey.Name, "HumioScheduledSearch: Adding allow-rename annotation and renaming")
			Eventually(func() error {
				if err := k8sClient.Get(ctx, key, fetched); err != nil {
					return err
				}
				if fetched.Annotations == nil {
					fetched.Annotations = make(map[string]string)
				}
				fetched.Annotations["humio.com/allow-rename"] = "true"
				fetched.Spec.Name = fmt.Sprintf("renamed-scheduledsearch-allowed-%d", GinkgoRandomSeed())
				return k8sClient.Update(ctx, fetched)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioScheduledSearch: Verifying scheduled search was recreated")
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, fetched)
				return fetched.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioScheduledSearchStateExists))

			suite.UsingClusterBy(clusterKey.Name, "HumioScheduledSearch: Cleaning up")
			Expect(k8sClient.Delete(ctx, fetched)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetched)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})
})

var _ = Describe("HumioScheduledSearch - AllowDataDeletion", Ordered, Label("envtest", "dummy", "real"), func() {
	var actionForScheduledSearch *humiov1alpha1.HumioAction

	BeforeAll(func() {
		ctx := context.Background()

		// Create action required by scheduled search
		actionSpec := humiov1alpha1.HumioActionSpec{
			ManagedClusterName: clusterKey.Name,
			Name:               "scheduledsearch-test-action",
			ViewName:           testRepo.Spec.Name,
			EmailProperties: &humiov1alpha1.HumioActionEmailProperties{
				Recipients: []string{emailActionExample},
			},
		}

		actionKey := types.NamespacedName{
			Name:      "scheduledsearch-action",
			Namespace: clusterKey.Namespace,
		}

		actionForScheduledSearch = &humiov1alpha1.HumioAction{
			ObjectMeta: metav1.ObjectMeta{
				Name:      actionKey.Name,
				Namespace: actionKey.Namespace,
			},
			Spec: actionSpec,
		}

		suite.UsingClusterBy(clusterKey.Name, "HumioScheduledSearch AllowDataDeletion: Creating action for scheduled search")
		Expect(k8sClient.Create(ctx, actionForScheduledSearch)).Should(Succeed())

		fetchedAction := &humiov1alpha1.HumioAction{}
		Eventually(func() string {
			_ = k8sClient.Get(ctx, actionKey, fetchedAction)
			return fetchedAction.Status.State
		}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))
	})

	It("should block deletion when allowDataDeletion is false", func() {
		ctx := context.Background()
		key := types.NamespacedName{
			Name:      "humioscheduledsearch-no-deletion",
			Namespace: clusterKey.Namespace,
		}

		toCreateScheduledSearch := &humiov1beta1.HumioScheduledSearch{
			ObjectMeta: metav1.ObjectMeta{
				Name:      key.Name,
				Namespace: key.Namespace,
			},
			Spec: humiov1beta1.HumioScheduledSearchSpec{
				ManagedClusterName:          clusterKey.Name,
				Name:                        "scheduledsearch-no-deletion",
				ViewName:                    testRepo.Spec.Name,
				QueryString:                 "#repo = test",
				QueryTimestampType:          humiographql.QueryTimestampTypeEventtimestamp,
				SearchIntervalOffsetSeconds: helpers.Int64Ptr(int64(60)),
				BackfillLimit:               helpers.IntPtr(int(5)),
				MaxWaitTimeSeconds:          60,
				SearchIntervalSeconds:       3600,
				Schedule:                    "0 * * * *",
				TimeZone:                    "UTC",
				Enabled:                     true,
				Actions:                     []string{actionForScheduledSearch.Spec.Name},
				AllowDataDeletion:           false, // Block deletion
			},
		}

		suite.UsingClusterBy(clusterKey.Name, "HumioScheduledSearch AllowDataDeletion: Creating scheduled search with allowDataDeletion=false")
		Expect(k8sClient.Create(ctx, toCreateScheduledSearch)).Should(Succeed())

		fetchedSearch := &humiov1beta1.HumioScheduledSearch{}
		Eventually(func() string {
			_ = k8sClient.Get(ctx, key, fetchedSearch)
			return fetchedSearch.Status.State
		}, testTimeout, suite.TestInterval).Should(Equal(humiov1beta1.HumioScheduledSearchStateExists))

		// Attempt deletion
		suite.UsingClusterBy(clusterKey.Name, "HumioScheduledSearch AllowDataDeletion: Attempting deletion (should block)")
		Expect(k8sClient.Delete(ctx, fetchedSearch)).Should(Succeed())

		// Verify resource stuck in deletion
		suite.UsingClusterBy(clusterKey.Name, "HumioScheduledSearch AllowDataDeletion: Verifying deletion is blocked")
		Eventually(func() bool {
			err := k8sClient.Get(ctx, key, fetchedSearch)
			return err == nil && fetchedSearch.GetDeletionTimestamp() != nil
		}, testTimeout, suite.TestInterval).Should(BeTrue())
		Consistently(func() bool {
			err := k8sClient.Get(ctx, key, fetchedSearch)
			return err == nil && fetchedSearch.GetDeletionTimestamp() != nil
		}, "10s", suite.TestInterval).Should(BeTrue())

		// Cleanup: Remove the stuck resource by removing its finalizer
		suite.UsingClusterBy(clusterKey.Name, "HumioScheduledSearch AllowDataDeletion: Cleaning up stuck resource")
		Eventually(func() error {
			_ = k8sClient.Get(ctx, key, fetchedSearch)
			fetchedSearch.SetFinalizers([]string{})
			return k8sClient.Update(ctx, fetchedSearch)
		}, testTimeout, suite.TestInterval).Should(Succeed())

		Eventually(func() bool {
			err := k8sClient.Get(ctx, key, fetchedSearch)
			return k8serrors.IsNotFound(err)
		}, testTimeout, suite.TestInterval).Should(BeTrue())
	})

	It("should allow deletion when allowDataDeletion is true", func() {
		ctx := context.Background()
		key := types.NamespacedName{
			Name:      "humioscheduledsearch-with-deletion",
			Namespace: clusterKey.Namespace,
		}

		toCreateScheduledSearch := &humiov1beta1.HumioScheduledSearch{
			ObjectMeta: metav1.ObjectMeta{
				Name:      key.Name,
				Namespace: key.Namespace,
			},
			Spec: humiov1beta1.HumioScheduledSearchSpec{
				ManagedClusterName:          clusterKey.Name,
				Name:                        "scheduledsearch-with-deletion",
				ViewName:                    testRepo.Spec.Name,
				QueryString:                 "#repo = test",
				QueryTimestampType:          humiographql.QueryTimestampTypeEventtimestamp,
				SearchIntervalOffsetSeconds: helpers.Int64Ptr(int64(60)),
				BackfillLimit:               helpers.IntPtr(int(5)),
				MaxWaitTimeSeconds:          60,
				SearchIntervalSeconds:       3600,
				Schedule:                    "0 * * * *",
				TimeZone:                    "UTC",
				Enabled:                     true,
				Actions:                     []string{actionForScheduledSearch.Spec.Name},
				AllowDataDeletion:           true, // Allow deletion
			},
		}

		suite.UsingClusterBy(clusterKey.Name, "HumioScheduledSearch AllowDataDeletion: Creating scheduled search with allowDataDeletion=true")
		Expect(k8sClient.Create(ctx, toCreateScheduledSearch)).Should(Succeed())

		fetchedSearch := &humiov1beta1.HumioScheduledSearch{}
		Eventually(func() string {
			_ = k8sClient.Get(ctx, key, fetchedSearch)
			return fetchedSearch.Status.State
		}, testTimeout, suite.TestInterval).Should(Equal(humiov1beta1.HumioScheduledSearchStateExists))

		// Delete and verify success
		suite.UsingClusterBy(clusterKey.Name, "HumioScheduledSearch AllowDataDeletion: Deleting scheduled search (should succeed)")
		Expect(k8sClient.Delete(ctx, fetchedSearch)).Should(Succeed())

		suite.UsingClusterBy(clusterKey.Name, "HumioScheduledSearch AllowDataDeletion: Verifying scheduled search is deleted")
		Eventually(func() bool {
			err := k8sClient.Get(ctx, key, fetchedSearch)
			return k8serrors.IsNotFound(err)
		}, testTimeout, suite.TestInterval).Should(BeTrue())
	})
})
