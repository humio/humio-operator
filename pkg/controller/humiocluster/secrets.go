package humiocluster

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"

	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func (r *ReconcileHumioCluster) constructSecret(hc *corev1alpha1.HumioCluster, secretName string, data map[string][]byte) (*corev1.Secret, error) {
	var secret corev1.Secret
	secret = corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: hc.Namespace,
			Labels:    labelsForHumio(hc.Name),
		},
		Data: data,
	}
	if err := controllerutil.SetControllerReference(hc, &secret, r.scheme); err != nil {
		return &corev1.Secret{}, fmt.Errorf("could not set controller reference: %s", err)
	}
	return &secret, nil
}

func (r *ReconcileHumioCluster) GetSecret(context context.Context, hc *corev1alpha1.HumioCluster, secretName string) (*corev1.Secret, error) {
	var existingSecret corev1.Secret
	err := r.client.Get(context, types.NamespacedName{
		Namespace: hc.Namespace,
		Name:      secretName,
	}, &existingSecret)
	return &existingSecret, err
}

func generatePassword() string {
	rand.Seed(time.Now().UnixNano())
	chars := []rune("ABCDEFGHIJKLMNOPQRSTUVWXYZ" +
		"abcdefghijklmnopqrstuvwxyz" +
		"0123456789")
	length := 32
	var b strings.Builder
	for i := 0; i < length; i++ {
		b.WriteRune(chars[rand.Intn(len(chars))])
	}
	return b.String()
}
