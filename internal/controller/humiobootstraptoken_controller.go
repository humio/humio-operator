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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"

	"github.com/go-logr/logr"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/kubernetes"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// BootstrapTokenSecretHashedTokenName is the name of the hashed token key inside the bootstrap token secret
	BootstrapTokenSecretHashedTokenName = "hashedToken"
	// BootstrapTokenSecretSecretName is the name of the secret key inside the bootstrap token secret
	BootstrapTokenSecretSecretName = "secret"
)

// HumioBootstrapTokenConfigurationError represents an error in user configuration that should result in ConfigError state
type HumioBootstrapTokenConfigurationError struct {
	message string
}

func (e HumioBootstrapTokenConfigurationError) Error() string {
	return e.message
}

// NewHumioBootstrapTokenConfigurationError creates a new bootstrap token configuration error
func NewHumioBootstrapTokenConfigurationError(message string) HumioBootstrapTokenConfigurationError {
	return HumioBootstrapTokenConfigurationError{message: message}
}

// HumioBootstrapTokenReconciler reconciles a HumioBootstrapToken object
type HumioBootstrapTokenReconciler struct {
	client.Client
	CommonConfig
	BaseLogger logr.Logger
	Log        logr.Logger
	Namespace  string
}

type HumioBootstrapTokenSecretData struct {
	Secret      string `json:"secret"` // #nosec G117
	HashedToken string `json:"hashedToken"`
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humiobootstraptokens,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humiobootstraptokens/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humiobootstraptokens/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
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
			r.Log.Info(fmt.Sprintf("humiocluster %s not found, will process bootstrap token anyway", hcRequest.Name))
			hc = nil
		} else {
			r.Log.Error(err, fmt.Sprintf("problem fetching humiocluster %s", hcRequest.Name))
			return reconcile.Result{}, err
		}
	}

	if err := r.ensureBootstrapTokenSecret(ctx, hbt, hc); err != nil {
		_ = r.setCondition(ctx, hbt, humiov1alpha1.BootstrapTokenConditionTypeReady, metav1.ConditionFalse, humiov1alpha1.BootstrapTokenReasonNotReady, fmt.Sprintf("Failed to ensure bootstrap token secret: %v", err))
		return reconcile.Result{}, err
	}

	// Generate hashed token regardless of cluster existence
	// The hashed token generation is self-contained and doesn't require the cluster to exist
	if err := r.ensureBootstrapTokenHashedToken(ctx, hbt, hc); err != nil {
		_ = r.setCondition(ctx, hbt, humiov1alpha1.BootstrapTokenConditionTypeReady, metav1.ConditionFalse, humiov1alpha1.BootstrapTokenReasonNotReady, fmt.Sprintf("Failed to ensure bootstrap token: %v", err))
		return reconcile.Result{}, err
	}

	if err := r.setCondition(ctx, hbt, humiov1alpha1.BootstrapTokenConditionTypeReady, metav1.ConditionTrue, humiov1alpha1.BootstrapTokenReasonReady, "Bootstrap token is ready"); err != nil {
		return reconcile.Result{}, err
	}

	r.Log.Info("done reconciling, will requeue", "requeuePeriod", r.RequeuePeriod.String())
	return reconcile.Result{RequeueAfter: r.RequeuePeriod}, nil
}

// setCondition sets a condition on the HumioBootstrapToken resource and maintains backward compatibility with the State field
//
//nolint:unparam // conditionType is kept as parameter for future use with additional condition types (e.g., Synced)
func (r *HumioBootstrapTokenReconciler) setCondition(ctx context.Context,
	hbt *humiov1alpha1.HumioBootstrapToken,
	conditionType string,
	status metav1.ConditionStatus,
	reason, message string) error {

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &humiov1alpha1.HumioBootstrapToken{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(hbt), latest); err != nil {
			return err
		}

		meta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
			Type:               conditionType,
			Status:             status,
			ObservedGeneration: latest.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             reason,
			Message:            message,
		})

		// BACKWARD COMPATIBILITY: Update State field based on condition
		latest.Status.State = bootstrapTokenStateFromCondition(status)

		// Preserve other status fields
		if status == metav1.ConditionTrue && latest.Status.State == humiov1alpha1.HumioBootstrapTokenStateReady {
			latest.Status.TokenSecretKeyRef = humiov1alpha1.HumioTokenSecretStatus{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: fmt.Sprintf("%s-%s", latest.Name, kubernetes.BootstrapTokenSecretNameSuffix),
					},
					Key: BootstrapTokenSecretSecretName,
				},
			}
			latest.Status.HashedTokenSecretKeyRef = humiov1alpha1.HumioHashedTokenSecretStatus{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: fmt.Sprintf("%s-%s", latest.Name, kubernetes.BootstrapTokenSecretNameSuffix),
					},
					Key: BootstrapTokenSecretHashedTokenName,
				},
			}
		}

		return r.Status().Update(ctx, latest)
	})
}

func bootstrapTokenStateFromCondition(status metav1.ConditionStatus) string {
	if status == metav1.ConditionTrue {
		return humiov1alpha1.HumioBootstrapTokenStateReady
	}
	return humiov1alpha1.HumioBootstrapTokenStateNotReady
}

func (r *HumioBootstrapTokenReconciler) updateStatusImage(ctx context.Context, hbt *humiov1alpha1.HumioBootstrapToken, image string) error {
	hbt.Status.BootstrapImage = image
	return r.Status().Update(ctx, hbt)
}

func (r *HumioBootstrapTokenReconciler) execCommand(ctx context.Context, pod *corev1.Pod, args []string) (string, error) {
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

	exec, err := remotecommand.NewSPDYExecutor(cfg, http.MethodPost, req.URL())
	if err != nil {
		return "", err
	}
	var stdout, stderr bytes.Buffer
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
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
	pod, err := r.constructBootstrapPod(ctx, &humioBootstrapTokenConfig)
	if err != nil {
		return pod, r.logErrorAndReturn(err, "could not construct pod")
	}
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
	pod, err := r.constructBootstrapPod(ctx, &humioBootstrapTokenConfig)
	if err != nil {
		return r.logErrorAndReturn(err, "could not construct pod")
	}
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
				if k8serrors.IsNotFound(err) {
					// User-provided secret is missing - this is a configuration error
					_ = r.setCondition(ctx, hbt, humiov1alpha1.BootstrapTokenConditionTypeReady, metav1.ConditionFalse, humiov1alpha1.BootstrapTokenReasonNotReady, fmt.Sprintf("user-provided TokenSecret %s not found", hbt.Spec.TokenSecret.SecretKeyRef.Name))
					return NewHumioBootstrapTokenConfigurationError(fmt.Sprintf("user-provided TokenSecret %s not found. Please create the secret or remove the tokenSecret.secretKeyRef from the HumioBootstrapToken spec", hbt.Spec.TokenSecret.SecretKeyRef.Name))
				}
				return r.logErrorAndReturn(err, fmt.Sprintf("could not get secret %s", hbt.Spec.TokenSecret.SecretKeyRef.Name))
			}
			if secretValue, ok := secret.Data[hbt.Spec.TokenSecret.SecretKeyRef.Key]; ok {
				secretData[BootstrapTokenSecretSecretName] = secretValue
			} else {
				// User-provided secret is missing the expected key - this is a configuration error
				_ = r.setCondition(ctx, hbt, humiov1alpha1.BootstrapTokenConditionTypeReady, metav1.ConditionFalse, humiov1alpha1.BootstrapTokenReasonNotReady, fmt.Sprintf("user-provided TokenSecret %s does not contain key \"%s\"", hbt.Spec.TokenSecret.SecretKeyRef.Name, hbt.Spec.TokenSecret.SecretKeyRef.Key))
				return NewHumioBootstrapTokenConfigurationError(fmt.Sprintf("user-provided TokenSecret %s does not contain key \"%s\". Please add the key or update the tokenSecret.secretKeyRef.key in the HumioBootstrapToken spec", hbt.Spec.TokenSecret.SecretKeyRef.Name, hbt.Spec.TokenSecret.SecretKeyRef.Key))
			}
		}
		if hbt.Spec.HashedTokenSecret.SecretKeyRef != nil {
			secret, err := kubernetes.GetSecret(ctx, r, hbt.Spec.HashedTokenSecret.SecretKeyRef.Name, hbt.Namespace)
			if err != nil {
				if k8serrors.IsNotFound(err) {
					// User-provided secret is missing - this is a configuration error
					_ = r.setCondition(ctx, hbt, humiov1alpha1.BootstrapTokenConditionTypeReady, metav1.ConditionFalse, humiov1alpha1.BootstrapTokenReasonNotReady, fmt.Sprintf("user-provided HashedTokenSecret %s not found", hbt.Spec.HashedTokenSecret.SecretKeyRef.Name))
					return NewHumioBootstrapTokenConfigurationError(fmt.Sprintf("user-provided HashedTokenSecret %s not found. Please create the secret or remove the hashedTokenSecret.secretKeyRef from the HumioBootstrapToken spec", hbt.Spec.HashedTokenSecret.SecretKeyRef.Name))
				}
				return r.logErrorAndReturn(err, fmt.Sprintf("could not get secret %s", hbt.Spec.HashedTokenSecret.SecretKeyRef.Name))
			}
			if hashedTokenValue, ok := secret.Data[hbt.Spec.HashedTokenSecret.SecretKeyRef.Key]; ok {
				secretData[BootstrapTokenSecretHashedTokenName] = hashedTokenValue
			} else {
				// User-provided secret is missing the expected key - this is a configuration error
				_ = r.setCondition(ctx, hbt, humiov1alpha1.BootstrapTokenConditionTypeReady, metav1.ConditionFalse, humiov1alpha1.BootstrapTokenReasonNotReady, fmt.Sprintf("user-provided HashedTokenSecret %s does not contain key \"%s\"", hbt.Spec.HashedTokenSecret.SecretKeyRef.Name, hbt.Spec.HashedTokenSecret.SecretKeyRef.Key))
				return NewHumioBootstrapTokenConfigurationError(fmt.Sprintf("user-provided HashedTokenSecret %s does not contain key \"%s\". Please add the key or update the hashedTokenSecret.secretKeyRef.key in the HumioBootstrapToken spec", hbt.Spec.HashedTokenSecret.SecretKeyRef.Name, hbt.Spec.HashedTokenSecret.SecretKeyRef.Key))
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
			secret := kubernetes.ConstructSecret(hbt.Name, hbt.Namespace, humioBootstrapTokenConfig.bootstrapTokenSecretName(), secretData, nil, nil)
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

	// Handle case where tokenSecret and hashedTokenSecret are provided as separate secrets
	if hbt.Spec.TokenSecret.SecretKeyRef != nil && hbt.Spec.HashedTokenSecret.SecretKeyRef != nil {
		// Check if they point to different secrets
		if hbt.Spec.TokenSecret.SecretKeyRef.Name != hbt.Spec.HashedTokenSecret.SecretKeyRef.Name {
			r.Log.Info("tokenSecret and hashedTokenSecret provided as separate secrets, combining them")

			// Get the hashed token from the separate hashed token secret
			hashedSecret, err := kubernetes.GetSecret(ctx, r, hbt.Spec.HashedTokenSecret.SecretKeyRef.Name, hbt.Namespace)
			if err != nil {
				return r.logErrorAndReturn(err, fmt.Sprintf("could not get hashed token secret %s", hbt.Spec.HashedTokenSecret.SecretKeyRef.Name))
			}

			hashedTokenValue, ok := hashedSecret.Data[hbt.Spec.HashedTokenSecret.SecretKeyRef.Key]
			if !ok {
				return r.logErrorAndReturn(fmt.Errorf("key not found"), fmt.Sprintf("could not get hashed token value from secret %s, key %s", hbt.Spec.HashedTokenSecret.SecretKeyRef.Name, hbt.Spec.HashedTokenSecret.SecretKeyRef.Key))
			}

			// Update the bootstrap token secret with both values
			bootstrapTokenSecret.Data[BootstrapTokenSecretHashedTokenName] = hashedTokenValue

			if err = r.Update(ctx, bootstrapTokenSecret); err != nil {
				return r.logErrorAndReturn(err, "failed to update secret with hashedToken data from separate secret")
			}

			return nil
		} else {
			// Both point to the same secret - check if hashedToken key already exists with a value
			if hashedTokenValue, exists := bootstrapTokenSecret.Data[hbt.Spec.HashedTokenSecret.SecretKeyRef.Key]; exists && len(hashedTokenValue) > 0 {
				r.Log.Info("hashedToken already provided in secret, using existing value")
				// Ensure the hashedToken is also available under the standard key name if different
				if hbt.Spec.HashedTokenSecret.SecretKeyRef.Key != BootstrapTokenSecretHashedTokenName {
					bootstrapTokenSecret.Data[BootstrapTokenSecretHashedTokenName] = hashedTokenValue
					if err = r.Update(ctx, bootstrapTokenSecret); err != nil {
						return r.logErrorAndReturn(err, "failed to update secret with existing hashedToken data")
					}
				}
				return nil
			}
		}
		// If they point to the same secret but the hashedToken key doesn't exist, continue with normal pod creation logic
	}

	commandArgs := []string{"env", "JVM_TMP_DIR=/tmp", "/app/humio/humio/bin/humio-run-class.sh", "-Dlog4j2.configurationFile=bin/tools-log4j2.xml", "com.humio.main.TokenHashing", "--json"}

	if tokenSecret, ok := bootstrapTokenSecret.Data[BootstrapTokenSecretSecretName]; ok {
		commandArgs = append(commandArgs, string(tokenSecret)) // #nosec G117
	}

	pod, err := r.createPod(ctx, hbt)
	if err != nil {
		return err
	}

	var podRunning bool
	var foundPod corev1.Pod
	for i := 0; i < waitForPodTimeoutSeconds; i++ {
		err := r.Get(ctx, types.NamespacedName{
			Namespace: pod.Namespace,
			Name:      pod.Name,
		}, &foundPod)
		if err == nil {
			if foundPod.Status.Phase == corev1.PodRunning {
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
	output, err := r.execCommand(ctx, &foundPod, commandArgs)
	if err != nil {
		return r.logErrorAndReturn(err, "failed to exec pod")
	}

	var jsonOutput string
	var includeLine bool
	outputLines := strings.Split(output, "\n")
	for _, line := range outputLines {
		if line == "{" {
			includeLine = true
		}
		if line == "}" {
			jsonOutput += "}"
			includeLine = false
		}
		if includeLine {
			jsonOutput += fmt.Sprintf("%s\n", line)
		}
	}
	var secretData HumioBootstrapTokenSecretData
	err = json.Unmarshal([]byte(jsonOutput), &secretData)
	if err != nil {
		return r.logErrorAndReturn(err, "failed to read output from exec command: output omitted")
	}

	updatedSecret, err := r.getBootstrapTokenSecret(ctx, hbt, hc)
	if err != nil {
		return err
	}
	updatedSecret.Data = map[string][]byte{BootstrapTokenSecretHashedTokenName: []byte(secretData.HashedToken), BootstrapTokenSecretSecretName: []byte(secretData.Secret)} // #nosec G117

	if err = r.Update(ctx, updatedSecret); err != nil {
		return r.logErrorAndReturn(err, "failed to update secret with hashedToken data")
	}

	if err := r.updateStatusImage(ctx, hbt, pod.Spec.Containers[0].Image); err != nil {
		return r.logErrorAndReturn(err, "failed to update bootstrap token image status")
	}

	return nil
}

func (r *HumioBootstrapTokenReconciler) getBootstrapTokenSecret(ctx context.Context, hbt *humiov1alpha1.HumioBootstrapToken, hc *humiov1alpha1.HumioCluster) (*corev1.Secret, error) {
	humioBootstrapTokenConfig := NewHumioBootstrapTokenConfig(hbt, hc)
	existingSecret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{
		Namespace: hbt.Namespace,
		Name:      humioBootstrapTokenConfig.bootstrapTokenSecretName(),
	}, existingSecret)
	return existingSecret, err
}

func (r *HumioBootstrapTokenReconciler) constructBootstrapPod(ctx context.Context, bootstrapConfig *HumioBootstrapTokenConfig) (*corev1.Pod, error) {
	userID := int64(65534)
	var image string

	if bootstrapConfig.imageSource() == nil {
		image = bootstrapConfig.image()
	} else {
		configMap, err := kubernetes.GetConfigMap(ctx, r, bootstrapConfig.imageSource().ConfigMapRef.Name, bootstrapConfig.Namespace())
		if err != nil {
			return &corev1.Pod{}, r.logErrorAndReturn(err, "failed to get imageFromSource")
		}
		if imageValue, ok := configMap.Data[bootstrapConfig.imageSource().ConfigMapRef.Key]; ok {
			image = imageValue
		}
	}

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      bootstrapConfig.PodName(),
			Namespace: bootstrapConfig.Namespace(),
		},
		Spec: corev1.PodSpec{
			ImagePullSecrets: bootstrapConfig.imagePullSecrets(),
			Affinity:         bootstrapConfig.affinity(),
			Tolerations:      bootstrapConfig.tolerations(),
			Containers: []corev1.Container{
				{
					Name:    HumioContainerName,
					Image:   image,
					Command: []string{"/bin/sleep", "900"},
					Env: []corev1.EnvVar{
						{
							Name:  "HUMIO_LOG4J_CONFIGURATION",
							Value: "log4j2-json-stdout.xml",
						},
					},
					Resources: bootstrapConfig.resources(),
					SecurityContext: &corev1.SecurityContext{
						Privileged:               helpers.BoolPtr(false),
						AllowPrivilegeEscalation: helpers.BoolPtr(false),
						ReadOnlyRootFilesystem:   helpers.BoolPtr(true),
						RunAsUser:                &userID,
						Capabilities: &corev1.Capabilities{
							Drop: []corev1.Capability{
								"ALL",
							},
						},
					},
				},
			},
		},
	}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioBootstrapTokenReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioBootstrapToken{}).
		Named("humiobootstraptoken").
		Owns(&corev1.Secret{}).
		Owns(&corev1.Pod{}).
		Complete(r)
}

func (r *HumioBootstrapTokenReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}
