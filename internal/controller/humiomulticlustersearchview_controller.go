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

package controller

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	humioapi "github.com/humio/humio-operator/internal/api"
	"github.com/humio/humio-operator/internal/api/humiographql"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/humio"
	"github.com/humio/humio-operator/internal/kubernetes"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// HumioMultiClusterSearchViewReconciler reconciles a HumioMultiClusterSearchView object
type HumioMultiClusterSearchViewReconciler struct {
	client.Client
	CommonConfig
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humiomulticlustersearchviews,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humiomulticlustersearchviews/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humiomulticlustersearchviews/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *HumioMultiClusterSearchViewReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioMultiClusterSearchView")

	// Fetch the HumioMultiClusterSearchView instance
	hv := &humiov1alpha1.HumioMultiClusterSearchView{}
	err := r.Get(ctx, req.NamespacedName, hv)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	r.Log = r.Log.WithValues("Request.UID", hv.UID)

	cluster, err := helpers.NewCluster(ctx, r, hv.Spec.ManagedClusterName, hv.Spec.ExternalClusterName, hv.Namespace, helpers.UseCertManager(), true, false)
	if err != nil || cluster == nil || cluster.Config() == nil {
		setStateErr := r.setState(ctx, humiov1alpha1.HumioMultiClusterSearchViewStateConfigError, hv)
		if setStateErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(setStateErr, "unable to set cluster state")
		}
		return reconcile.Result{RequeueAfter: 5 * time.Second}, r.logErrorAndReturn(err, "unable to obtain humio client config")
	}
	humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	// Delete
	r.Log.Info("Checking if view is marked to be deleted")
	isMarkedForDeletion := hv.GetDeletionTimestamp() != nil
	if isMarkedForDeletion {
		r.Log.Info("View marked to be deleted")
		if helpers.ContainsElement(hv.GetFinalizers(), humioFinalizer) {
			_, err := r.HumioClient.GetMultiClusterSearchView(ctx, humioHttpClient, hv)
			if errors.As(err, &humioapi.EntityNotFound{}) {
				hv.SetFinalizers(helpers.RemoveElement(hv.GetFinalizers(), humioFinalizer))
				err := r.Update(ctx, hv)
				if err != nil {
					return reconcile.Result{}, err
				}
				r.Log.Info("Finalizer removed successfully")
				return reconcile.Result{Requeue: true}, nil
			}

			// Run finalization logic for humioFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.Log.Info("Deleting View")
			if err := r.HumioClient.DeleteMultiClusterSearchView(ctx, humioHttpClient, hv); err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, "Delete view returned error")
			}
			// If no error was detected, we need to requeue so that we can remove the finalizer
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, nil
	}

	// Add finalizer for this CR
	if !helpers.ContainsElement(hv.GetFinalizers(), humioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to view")
		hv.SetFinalizers(append(hv.GetFinalizers(), humioFinalizer))
		err := r.Update(ctx, hv)
		if err != nil {
			return reconcile.Result{}, err
		}

		return reconcile.Result{Requeue: true}, nil
	}
	defer func(ctx context.Context, hv *humiov1alpha1.HumioMultiClusterSearchView) {
		_, err := r.HumioClient.GetMultiClusterSearchView(ctx, humioHttpClient, hv)
		if errors.As(err, &humioapi.EntityNotFound{}) {
			_ = r.setState(ctx, humiov1alpha1.HumioMultiClusterSearchViewStateNotFound, hv)
			return
		}
		if err != nil {
			_ = r.setState(ctx, humiov1alpha1.HumioMultiClusterSearchViewStateUnknown, hv)
			return
		}
		_ = r.setState(ctx, humiov1alpha1.HumioMultiClusterSearchViewStateExists, hv)
	}(ctx, hv)

	connectionDetailsIncludingAPIToken, err := r.getConnectionDetailsIncludingAPIToken(ctx, hv)
	if err != nil {
		return reconcile.Result{}, err
	}

	r.Log.Info("get current view")
	curView, err := r.HumioClient.GetMultiClusterSearchView(ctx, humioHttpClient, hv)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			r.Log.Info("View doesn't exist. Now adding view")
			addErr := r.HumioClient.AddMultiClusterSearchView(ctx, humioHttpClient, hv, connectionDetailsIncludingAPIToken)
			if addErr != nil {
				if strings.Contains(addErr.Error(), "The feature MultiClusterSearch is not enabled") {
					setStateErr := r.setState(ctx, humiov1alpha1.HumioMultiClusterSearchViewStateConfigError, hv)
					if setStateErr != nil {
						return reconcile.Result{}, err
					}
				}
				return reconcile.Result{}, r.logErrorAndReturn(addErr, "could not create view")
			}
			r.Log.Info("created view", "ViewName", hv.Spec.Name)
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not check if view exists")
	}

	expectedView := customResourceWithClusterIdentityTags(hv, connectionDetailsIncludingAPIToken)

	if asExpected, diffKeysAndValues := mcsViewAlreadyAsExpected(expectedView, curView); !asExpected {
		r.Log.Info("information differs, triggering update",
			"diff", diffKeysAndValues,
		)
		updateErr := r.HumioClient.UpdateMultiClusterSearchView(ctx, humioHttpClient, hv, connectionDetailsIncludingAPIToken)
		if updateErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(updateErr, "could not update view")
		}
	}

	r.Log.Info("done reconciling, will requeue", "requeuePeriod", r.RequeuePeriod.String())
	return reconcile.Result{RequeueAfter: r.RequeuePeriod}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioMultiClusterSearchViewReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioMultiClusterSearchView{}).
		Named("humiomulticlustersearchview").
		Complete(r)
}

func (r *HumioMultiClusterSearchViewReconciler) setState(ctx context.Context, state string, hr *humiov1alpha1.HumioMultiClusterSearchView) error {
	if hr.Status.State == state {
		return nil
	}
	r.Log.Info(fmt.Sprintf("setting view state to %s", state))
	hr.Status.State = state
	return r.Status().Update(ctx, hr)
}

func (r *HumioMultiClusterSearchViewReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}

func (r *HumioMultiClusterSearchViewReconciler) getConnectionDetailsIncludingAPIToken(ctx context.Context, hv *humiov1alpha1.HumioMultiClusterSearchView) ([]humio.ConnectionDetailsIncludingAPIToken, error) {
	connectionDetailsIncludingAPIToken := make([]humio.ConnectionDetailsIncludingAPIToken, len(hv.Spec.Connections))
	for idx, conn := range hv.Spec.Connections {
		if conn.Type == humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeLocal {
			connectionDetailsIncludingAPIToken[idx] = humio.ConnectionDetailsIncludingAPIToken{
				HumioMultiClusterSearchViewConnection: conn,
			}
		}
		if conn.Type == humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeRemote {
			apiTokenSecret := corev1.Secret{}
			if getErr := r.Get(ctx, types.NamespacedName{
				Namespace: hv.GetNamespace(),
				Name:      conn.APITokenSource.SecretKeyRef.Name,
			}, &apiTokenSecret); getErr != nil {
				return nil, getErr
			}
			remoteAPIToken, found := apiTokenSecret.Data["token"]
			if !found {
				return nil, fmt.Errorf("secret %s does not contain a key named %q", apiTokenSecret.Name, "token")
			}
			connectionDetailsIncludingAPIToken[idx] = humio.ConnectionDetailsIncludingAPIToken{
				HumioMultiClusterSearchViewConnection: conn,
				APIToken:                              string(remoteAPIToken),
			}
		}
	}
	return connectionDetailsIncludingAPIToken, nil
}

// mcsViewAlreadyAsExpected compares fromKubernetesCustomResource and fromGraphQL. It returns a boolean indicating
// if the details from GraphQL already matches what is in the desired state of the custom resource.
// If they do not match, a map is returned with details on what the diff is.
func mcsViewAlreadyAsExpected(fromKubernetesCustomResource *humiov1alpha1.HumioMultiClusterSearchView, fromGraphQL *humiographql.GetMultiClusterSearchViewSearchDomainView) (bool, map[string]string) {
	keyValues := map[string]string{}

	currentClusterConnections := fromGraphQL.GetClusterConnections()
	expectedClusterConnections := convertHumioMultiClusterSearchViewToGraphQLClusterConnectionsVariant(fromKubernetesCustomResource)
	sortAndSanitizeClusterConnections(currentClusterConnections)
	sortAndSanitizeClusterConnections(expectedClusterConnections)
	if diff := cmp.Diff(currentClusterConnections, expectedClusterConnections); diff != "" {
		keyValues["viewClusterConnections"] = diff
	}

	if diff := cmp.Diff(fromGraphQL.GetDescription(), &fromKubernetesCustomResource.Spec.Description); diff != "" {
		keyValues["description"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetAutomaticSearch(), helpers.BoolTrue(fromKubernetesCustomResource.Spec.AutomaticSearch)); diff != "" {
		keyValues["automaticSearch"] = diff
	}

	return len(keyValues) == 0, keyValues
}

func sortAndSanitizeClusterConnections(connections []humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsClusterConnection) {
	// ignore connection id when comparing cluster connections
	for idx := range connections {
		switch v := connections[idx].(type) {
		case *humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsLocalClusterConnection:
			v.Id = ""
			sort.SliceStable(v.Tags, func(i, j int) bool {
				return v.Tags[i].Key > v.Tags[j].Key
			})
			connections[idx] = v
		case *humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsRemoteClusterConnection:
			v.Id = ""
			sort.SliceStable(v.Tags, func(i, j int) bool {
				return v.Tags[i].Key > v.Tags[j].Key
			})
			connections[idx] = v
		}
	}

	sort.SliceStable(connections, func(i, j int) bool {
		return connections[i].GetClusterId() > connections[j].GetClusterId()
	})
}

func convertHumioMultiClusterSearchViewToGraphQLClusterConnectionsVariant(hv *humiov1alpha1.HumioMultiClusterSearchView) []humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsClusterConnection {
	viewClusterConnections := make([]humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsClusterConnection, 0)
	for _, connection := range hv.Spec.Connections {
		tags := make([]humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsClusterConnectionTagsClusterConnectionTag, len(connection.Tags))
		for idx, tag := range connection.Tags {
			tags[idx] = humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsClusterConnectionTagsClusterConnectionTag{
				Key:   tag.Key,
				Value: tag.Value,
			}
		}

		if connection.Type == humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeLocal {
			viewClusterConnections = append(viewClusterConnections, &humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsLocalClusterConnection{
				Typename:       helpers.StringPtr("LocalClusterConnection"),
				ClusterId:      connection.ClusterIdentity,
				QueryPrefix:    connection.Filter,
				Tags:           tags,
				TargetViewName: connection.ViewOrRepoName,
			})
		}
		if connection.Type == humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeRemote {
			viewClusterConnections = append(viewClusterConnections, &humiographql.GetMultiClusterSearchViewSearchDomainViewClusterConnectionsRemoteClusterConnection{
				Typename:    helpers.StringPtr("RemoteClusterConnection"),
				ClusterId:   connection.ClusterIdentity,
				QueryPrefix: connection.Filter,
				Tags:        tags,
				PublicUrl:   connection.Url,
			})
		}
	}
	return viewClusterConnections
}

func customResourceWithClusterIdentityTags(hv *humiov1alpha1.HumioMultiClusterSearchView, connectionDetailsIncludingAPIToken []humio.ConnectionDetailsIncludingAPIToken) *humiov1alpha1.HumioMultiClusterSearchView {
	copyOfCustomResourceWithClusterIdentityTags := hv.DeepCopy()
	for idx := range connectionDetailsIncludingAPIToken {
		tags := []humiov1alpha1.HumioMultiClusterSearchViewConnectionTag{
			{
				Key:   "clusteridentity",
				Value: connectionDetailsIncludingAPIToken[idx].ClusterIdentity,
			},
		}
		if copyOfCustomResourceWithClusterIdentityTags.Spec.Connections[idx].Type == humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeRemote {
			tags = append(tags, humiov1alpha1.HumioMultiClusterSearchViewConnectionTag{
				Key:   "clusteridentityhash",
				Value: helpers.AsSHA256(fmt.Sprintf("%s|%s", connectionDetailsIncludingAPIToken[idx].Url, connectionDetailsIncludingAPIToken[idx].APIToken)),
			})
		}

		sort.SliceStable(tags, func(i, j int) bool {
			return tags[i].Key > tags[j].Key
		})

		copyOfCustomResourceWithClusterIdentityTags.Spec.Connections[idx] = humiov1alpha1.HumioMultiClusterSearchViewConnection{
			ClusterIdentity: connectionDetailsIncludingAPIToken[idx].ClusterIdentity,
			Filter:          connectionDetailsIncludingAPIToken[idx].Filter,
			Tags:            tags,
			Type:            connectionDetailsIncludingAPIToken[idx].Type,
			ViewOrRepoName:  connectionDetailsIncludingAPIToken[idx].ViewOrRepoName,
			Url:             connectionDetailsIncludingAPIToken[idx].Url,
			APITokenSource:  nil, // ignore "source" as we already fetched the api token and added the correct tag above
		}
	}
	return copyOfCustomResourceWithClusterIdentityTags
}
