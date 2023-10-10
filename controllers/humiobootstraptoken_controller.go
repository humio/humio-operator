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

package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/kubernetes"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
)

const (
	// BootstrapTokenSecretHashedTokenName is the name of the hashed token key inside the bootstrap token secret
	BootstrapTokenSecretHashedTokenName = "hashedToken"
	// BootstrapTokenSecretSecretName is the name of the secret key inside the bootstrap token secret
	BootstrapTokenSecretSecretName = "secret"
)

// HumioBootstrapTokenReconciler reconciles a HumioBootstrapToken object
type HumioBootstrapTokenReconciler struct {
	client.Client
	BaseLogger logr.Logger
	Log        logr.Logger
	Namespace  string
}

type HumioBootstrapTokenSecretData struct {
	Secret      string `json:"secret"`
	HashedToken string `json:"hashedToken"`
}

//+kubebuilder:rbac:groups=core.humio.com,resources=HumioBootstrapTokens,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core.humio.com,resources=HumioBootstrapTokens/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core.humio.com,resources=HumioBootstrapTokens/finalizers,verbs=update

// Reconcile runs the reconciler for a HumioBootstrapToken object
func (r *HumioBootstrapTokenReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioBootstrapToken")

	// Fetch the HumioBootstrapToken
	hbt := &humiov1alpha1.HumioBootstrapToken{}
	if err := r.Get(ctx, req.NamespacedName, hbt); err != nil {
		if k8serrors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	hc := &humiov1alpha1.HumioCluster{}
	hcRequest := types.NamespacedName{
		Name:      hbt.Spec.ManagedClusterName,
		Namespace: hbt.Namespace,
	}
	if err := r.Get(ctx, hcRequest, hc); err != nil {
		if k8serrors.IsNotFound(err) {
			r.Log.Error(err, fmt.Sprintf("humiocluster %s not found", hcRequest.Name))
			return reconcile.Result{}, err
		}
		r.Log.Error(err, fmt.Sprintf("problem fetching humiocluster %s", hcRequest.Name))
		return reconcile.Result{}, err
	}

	if err := r.ensureBootstrapTokenSecret(ctx, hbt, hc); err != nil {
		_ = r.updateStatus(ctx, hbt, humiov1alpha1.HumioBootstrapTokenStateNotReady)
		return reconcile.Result{}, err
	}

	if err := r.ensureBootstrapTokenHashedToken(ctx, hbt, hc); err != nil {
		_ = r.updateStatus(ctx, hbt, humiov1alpha1.HumioBootstrapTokenStateNotReady)
		return reconcile.Result{}, err
	}

	if err := r.updateStatus(ctx, hbt, humiov1alpha1.HumioBootstrapTokenStateReady); err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{RequeueAfter: time.Second * 60}, nil
}

func (r *HumioBootstrapTokenReconciler) updateStatus(ctx context.Context, hbt *humiov1alpha1.HumioBootstrapToken, state string) error {
	hbt.Status.State = state
	if state == humiov1alpha1.HumioBootstrapTokenStateReady {
		hbt.Status.TokenSecretKeyRef = humiov1alpha1.HumioTokenSecretStatus{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: fmt.Sprintf("%s-%s", hbt.Name, kubernetes.BootstrapTokenSecretNameSuffix),
				},
				Key: BootstrapTokenSecretSecretName,
			},
		}
		hbt.Status.HashedTokenSecretKeyRef = humiov1alpha1.HumioHashedTokenSecretStatus{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: fmt.Sprintf("%s-%s", hbt.Name, kubernetes.BootstrapTokenSecretNameSuffix),
				},
				Key: BootstrapTokenSecretHashedTokenName,
			},
		}
	}
	return r.Client.Status().Update(ctx, hbt)
}

func (r *HumioBootstrapTokenReconciler) execCommand(pod *corev1.Pod, args []string) (string, error) {
	configLoader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{},
	)

	// create the Config object
	cfg, err := configLoader.ClientConfig()
	if err != nil {
		return "", err
	}

	// we want to use the core API (namespaces lives here)
	cfg.APIPath = "/api"
	cfg.GroupVersion = &corev1.SchemeGroupVersion
	cfg.NegotiatedSerializer = scheme.Codecs.WithoutConversion()

	// create a RESTClient
	rc, err := rest.RESTClientFor(cfg)
	if err != nil {
		return "", err
	}

	req := rc.Post().
		Resource("pods").
		Name(pod.Name).
		Namespace(pod.Namespace).
		SubResource("exec")
	req.VersionedParams(&corev1.PodExecOptions{
		Container: "humio", // TODO: changeme
		Command:   args,
		Stdin:     false,
		Stdout:    true,
		Stderr:    true,
		TTY:       false,
	}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(cfg, "POST", req.URL())
	if err != nil {
		return "", err
	}
	var stdout, stderr bytes.Buffer
	err = exec.StreamWithContext(context.TODO(), remotecommand.StreamOptions{
		Stdin:  nil,
		Stdout: &stdout,
		Stderr: &stderr,
		Tty:    false,
	})
	if err != nil {
		return "", err
	}
	return stdout.String(), nil
}

func (r *HumioBootstrapTokenReconciler) createPod(ctx context.Context, hbt *humiov1alpha1.HumioBootstrapToken) (*corev1.Pod, error) {
	existingPod := &corev1.Pod{}
	humioCluster := &humiov1alpha1.HumioCluster{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: hbt.Namespace,
		Name:      hbt.Spec.ManagedClusterName,
	}, humioCluster); err != nil {
		if k8serrors.IsNotFound(err) {
			humioCluster = nil
		}
	}
	humioBootstrapTokenConfig := NewHumioBootstrapTokenConfig(hbt, humioCluster)
	pod := ConstructBootstrapPod(&humioBootstrapTokenConfig)
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: pod.Namespace,
		Name:      pod.Name,
	}, existingPod); err != nil {
		if k8serrors.IsNotFound(err) {
			if err := controllerutil.SetControllerReference(hbt, pod, r.Scheme()); err != nil {
				return &corev1.Pod{}, r.logErrorAndReturn(err, "could not set controller reference")
			}
			r.Log.Info("creating onetime pod")
			if err := r.Create(ctx, pod); err != nil {
				return &corev1.Pod{}, r.logErrorAndReturn(err, "could not create pod")
			}
			return pod, nil
		}
	}
	return existingPod, nil
}

func (r *HumioBootstrapTokenReconciler) deletePod(ctx context.Context, hbt *humiov1alpha1.HumioBootstrapToken, hc *humiov1alpha1.HumioCluster) error {
	existingPod := &corev1.Pod{}
	humioBootstrapTokenConfig := NewHumioBootstrapTokenConfig(hbt, hc)
	pod := ConstructBootstrapPod(&humioBootstrapTokenConfig)
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: pod.Namespace,
		Name:      pod.Name,
	}, existingPod); err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return r.logErrorAndReturn(err, "could not delete pod")
	}
	r.Log.Info("deleting onetime pod")
	if err := r.Delete(ctx, pod); err != nil {
		return r.logErrorAndReturn(err, "could not delete pod")
	}
	return nil
}

func (r *HumioBootstrapTokenReconciler) ensureBootstrapTokenSecret(ctx context.Context, hbt *humiov1alpha1.HumioBootstrapToken, hc *humiov1alpha1.HumioCluster) error {
	r.Log.Info("ensuring bootstrap token secret")
	humioBootstrapTokenConfig := NewHumioBootstrapTokenConfig(hbt, hc)
	if _, err := r.getBootstrapTokenSecret(ctx, hbt, hc); err != nil {
		if !k8serrors.IsNotFound(err) {
			return r.logErrorAndReturn(err, "could not get secret")
		}
		secretData := map[string][]byte{}
		if hbt.Spec.TokenSecret.SecretKeyRef != nil {
			secret, err := kubernetes.GetSecret(ctx, r, hbt.Spec.TokenSecret.SecretKeyRef.Name, hbt.Namespace)
			if err != nil {
				return r.logErrorAndReturn(err, fmt.Sprintf("could not get secret %s", hbt.Spec.TokenSecret.SecretKeyRef.Name))
			}
			if secretValue, ok := secret.Data[hbt.Spec.TokenSecret.SecretKeyRef.Key]; ok {
				secretData[BootstrapTokenSecretSecretName] = secretValue
			} else {
				return r.logErrorAndReturn(err, fmt.Sprintf("could not get value from secret %s. "+
					"secret does not contain value for key \"%s\"", hbt.Spec.TokenSecret.SecretKeyRef.Name, hbt.Spec.TokenSecret.SecretKeyRef.Key))
			}
		}
		if hbt.Spec.HashedTokenSecret.SecretKeyRef != nil {
			secret, err := kubernetes.GetSecret(ctx, r, hbt.Spec.TokenSecret.SecretKeyRef.Name, hbt.Namespace)
			if err != nil {
				return r.logErrorAndReturn(err, fmt.Sprintf("could not get secret %s", hbt.Spec.TokenSecret.SecretKeyRef.Name))
			}
			if hashedTokenValue, ok := secret.Data[hbt.Spec.HashedTokenSecret.SecretKeyRef.Key]; ok {
				secretData[BootstrapTokenSecretHashedTokenName] = hashedTokenValue
			} else {
				return r.logErrorAndReturn(err, fmt.Sprintf("could not get value from secret %s. "+
					"secret does not contain value for key \"%s\"", hbt.Spec.HashedTokenSecret.SecretKeyRef.Name, hbt.Spec.HashedTokenSecret.SecretKeyRef.Key))
			}
		}
		if err := humioBootstrapTokenConfig.validate(); err != nil {
			return r.logErrorAndReturn(err, fmt.Sprintf("could not validate bootstrap config for %s", hbt.Name))
		}
		okayToCreate, err := humioBootstrapTokenConfig.create()
		if err != nil {
			return r.logErrorAndReturn(err, "cannot create bootstrap token")
		}
		if okayToCreate {
			secret := kubernetes.ConstructSecret(hbt.Name, hbt.Namespace, humioBootstrapTokenConfig.bootstrapTokenName(), secretData, nil)
			if err := controllerutil.SetControllerReference(hbt, secret, r.Scheme()); err != nil {
				return r.logErrorAndReturn(err, "could not set controller reference")
			}
			r.Log.Info(fmt.Sprintf("creating secret: %s", secret.Name))
			if err := r.Create(ctx, secret); err != nil {
				return r.logErrorAndReturn(err, "could not create secret")
			}
		}
	}
	return nil
}

func (r *HumioBootstrapTokenReconciler) ensureBootstrapTokenHashedToken(ctx context.Context, hbt *humiov1alpha1.HumioBootstrapToken, hc *humiov1alpha1.HumioCluster) error {
	r.Log.Info("ensuring bootstrap hashed token")
	bootstrapTokenSecret, err := r.getBootstrapTokenSecret(ctx, hbt, hc)
	if err != nil {
		return r.logErrorAndReturn(err, "could not get bootstrap token secret")
	}

	defer func(ctx context.Context, hbt *humiov1alpha1.HumioBootstrapToken, hc *humiov1alpha1.HumioCluster) {
		if err := r.deletePod(ctx, hbt, hc); err != nil {
			r.Log.Error(err, "failed to delete pod")
		}
	}(ctx, hbt, hc)

	if _, ok := bootstrapTokenSecret.Data[BootstrapTokenSecretHashedTokenName]; ok {
		return nil
	}

	commandArgs := []string{"/bin/bash", "/app/humio/humio/bin/humio-run-class.sh", "com.humio.main.TokenHashing", "--json"}

	if tokenSecret, ok := bootstrapTokenSecret.Data[BootstrapTokenSecretSecretName]; ok {
		commandArgs = append(commandArgs, string(tokenSecret))
	}

	pod, err := r.createPod(ctx, hbt)
	if err != nil {
		return err
	}

	var podRunning bool
	for i := 0; i < waitForPodTimeoutSeconds; i++ {
		latestPodList, err := kubernetes.ListPods(ctx, r, hbt.GetNamespace(), hbt.GetLabels())
		if err != nil {
			return err
		}
		for _, pod := range latestPodList {
			if pod.Status.Phase == corev1.PodRunning {
				podRunning = true
				break
			}
		}
		r.Log.Info("waiting for bootstrap token pod to start")
		time.Sleep(time.Second * 1)
	}
	if !podRunning {
		return r.logErrorAndReturn(err, "failed to start bootstrap token pod")
	}

	r.Log.Info("execing onetime pod")
	output, err := r.execCommand(pod, commandArgs)
	if err != nil {
		return r.logErrorAndReturn(err, "failed to exec pod")
	}

	var secretData HumioBootstrapTokenSecretData
	err = json.Unmarshal([]byte(output), &secretData)
	if err != nil {
		r.Log.Error(fmt.Errorf("failed to read output from exec command"), "omitting output")
	}

	updatedSecret, err := r.getBootstrapTokenSecret(ctx, hbt, hc)
	if err != nil {
		return err
	}
	// TODO: make tokenHash constant
	updatedSecret.Data = map[string][]byte{BootstrapTokenSecretHashedTokenName: []byte(secretData.HashedToken), BootstrapTokenSecretSecretName: []byte(secretData.Secret)}

	if err = r.Update(ctx, updatedSecret); err != nil {
		return r.logErrorAndReturn(err, "failed to update secret with hashedToken data")
	}
	return nil
}

func (r *HumioBootstrapTokenReconciler) getBootstrapTokenSecret(ctx context.Context, hbt *humiov1alpha1.HumioBootstrapToken, hc *humiov1alpha1.HumioCluster) (*corev1.Secret, error) {
	humioBootstrapTokenConfig := NewHumioBootstrapTokenConfig(hbt, hc)
	existingSecret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{
		Namespace: hbt.Namespace,
		Name:      humioBootstrapTokenConfig.bootstrapTokenName(),
	}, existingSecret)
	return existingSecret, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioBootstrapTokenReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioBootstrapToken{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.Pod{}).
		Complete(r)
}

func (r *HumioBootstrapTokenReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}
