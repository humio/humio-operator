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
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"
	"time"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/retry"

	cmapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/kubernetes"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
)

const (
	waitForNodeCertificateTimeoutSeconds = 30
)

func getCASecretName(hc *humiov1alpha1.HumioCluster) string {
	if hc.Spec.TLS != nil && hc.Spec.TLS.CASecretName != "" {
		return hc.Spec.TLS.CASecretName
	}
	return fmt.Sprintf("%s-ca-keypair", hc.Name)
}

func useExistingCA(hc *humiov1alpha1.HumioCluster) bool {
	return hc.Spec.TLS != nil && hc.Spec.TLS.CASecretName != ""
}

func validCASecret(ctx context.Context, k8sclient client.Client, namespace, secretName string) (bool, error) {
	// look up k8s secret
	secret, err := kubernetes.GetSecret(ctx, k8sclient, secretName, namespace)
	if err != nil {
		return false, nil
	}
	keys := []string{"tls.crt", "tls.key"}
	for _, key := range keys {
		_, found := secret.Data[key]
		if !found {
			return false, fmt.Errorf("did not find key %s in secret %s", key, secretName)
		}
	}
	// TODO: figure out if we want to validate more
	return true, nil
}

func validCAIssuer(ctx context.Context, k8sclient client.Client, namespace, issuerName string) (bool, error) {
	issuer := &cmapi.Issuer{}
	err := k8sclient.Get(ctx, types.NamespacedName{Name: issuerName, Namespace: namespace}, issuer)
	if err != nil {
		return false, err
	}

	for _, c := range issuer.Status.Conditions {
		if c.Type == cmapi.IssuerConditionReady {
			if c.Status == cmmeta.ConditionTrue {
				return true, nil
			}
		}
	}

	return false, nil
}

type CACert struct {
	Certificate []byte
	Key         []byte
}

func generateCACertificate() (CACert, error) {
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().Unix()),
		Subject: pkix.Name{
			SerialNumber: fmt.Sprintf("%d", time.Now().Unix()),
			CommonName:   "humio-operator",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(10, 0, 0), // TODO: Not sure if/how we want to deal with CA cert rotations
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}

	caPrivateKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return CACert{}, err
	}

	caBytes, err := x509.CreateCertificate(rand.Reader, ca, ca, &caPrivateKey.PublicKey, caPrivateKey)
	if err != nil {
		return CACert{}, err
	}

	caCertificatePEM := new(bytes.Buffer)
	if err = pem.Encode(caCertificatePEM, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caBytes,
	}); err != nil {
		return CACert{}, fmt.Errorf("could not encode CA certificate as PEM")
	}

	caPrivateKeyPEM := new(bytes.Buffer)
	if err = pem.Encode(caPrivateKeyPEM, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(caPrivateKey),
	}); err != nil {
		return CACert{}, fmt.Errorf("could not encode CA private key as PEM")
	}

	return CACert{
		Certificate: caCertificatePEM.Bytes(),
		Key:         caPrivateKeyPEM.Bytes(),
	}, nil
}

func constructCAIssuer(hc *humiov1alpha1.HumioCluster) cmapi.Issuer {
	return cmapi.Issuer{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: hc.Namespace,
			Name:      hc.Name,
			Labels:    kubernetes.LabelsForHumio(hc.Name),
		},
		Spec: cmapi.IssuerSpec{
			IssuerConfig: cmapi.IssuerConfig{
				CA: &cmapi.CAIssuer{
					SecretName: getCASecretName(hc),
				},
			},
		},
	}
}

func constructClusterCACertificateBundle(hc *humiov1alpha1.HumioCluster) cmapi.Certificate {
	return cmapi.Certificate{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: hc.Namespace,
			Name:      hc.Name,
			Labels:    kubernetes.MatchingLabelsForHumio(hc.Name),
		},
		Spec: cmapi.CertificateSpec{
			DNSNames: []string{
				fmt.Sprintf("%s.%s", hc.Name, hc.Namespace),
				fmt.Sprintf("%s-headless.%s", hc.Name, hc.Namespace),
			},
			IssuerRef: cmmeta.ObjectReference{
				Name: constructCAIssuer(hc).Name,
			},
			SecretName: hc.Name,
		},
	}
}

func ConstructNodeCertificate(hnp *HumioNodePool, nodeSuffix string) cmapi.Certificate {
	return cmapi.Certificate{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{},
			Namespace:   hnp.GetNamespace(),
			Name:        fmt.Sprintf("%s-core-%s", hnp.GetNodePoolName(), nodeSuffix),
			Labels:      hnp.GetNodePoolLabels(),
		},
		Spec: cmapi.CertificateSpec{
			DNSNames: []string{
				fmt.Sprintf("%s-core-%s.%s.%s", hnp.GetNodePoolName(), nodeSuffix, headlessServiceName(hnp.GetClusterName()), hnp.GetNamespace()), // Used for intra-cluster communication
				fmt.Sprintf("%s-core-%s", hnp.GetNodePoolName(), nodeSuffix),                                                                      // Used for auth sidecar
				fmt.Sprintf("%s.%s", hnp.GetNodePoolName(), hnp.GetNamespace()),                                                                   // Used by humio-operator and ingress controllers to reach the Humio API
				fmt.Sprintf("%s-headless.%s", hnp.GetClusterName(), hnp.GetNamespace()),                                                           // Used by humio-operator and ingress controllers to reach the Humio API
			},
			IssuerRef: cmmeta.ObjectReference{
				Name: hnp.GetClusterName(),
			},
			SecretName: fmt.Sprintf("%s-core-%s", hnp.GetNodePoolName(), nodeSuffix),
			Keystores: &cmapi.CertificateKeystores{
				JKS: &cmapi.JKSKeystore{
					Create: true,
					PasswordSecretRef: cmmeta.SecretKeySelector{
						LocalObjectReference: cmmeta.LocalObjectReference{
							Name: fmt.Sprintf("%s-keystore-passphrase", hnp.GetClusterName()),
						},
						Key: "passphrase",
					},
				},
			},
		},
	}
}

func GetDesiredCertHash(hnp *HumioNodePool) string {
	certForHash := ConstructNodeCertificate(hnp, "")

	// Keystores will always contain a new pointer when constructing a certificate.
	// To work around this, we override it to nil before calculating the hash,
	// if we do not do this, the hash will always be different.
	certForHash.Spec.Keystores = nil

	b, _ := json.Marshal(certForHash)
	desiredCertificateHash := helpers.AsSHA256(string(b))
	return desiredCertificateHash
}

func (r *HumioClusterReconciler) waitForNewNodeCertificate(ctx context.Context, hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool, expectedCertCount int) error {
	for i := 0; i < waitForNodeCertificateTimeoutSeconds; i++ {
		existingNodeCertCount, err := r.updateNodeCertificates(ctx, hc, hnp)
		if err != nil {
			return err
		}
		r.Log.Info(fmt.Sprintf("validating new pod certificate was created. expected pod certificate count %d, current pod certificate count %d", expectedCertCount, existingNodeCertCount))
		if existingNodeCertCount >= expectedCertCount {
			return nil
		}
		time.Sleep(time.Second * 1)
	}
	return fmt.Errorf("timed out waiting to validate new pod certificate was created")
}

// updateNodeCertificates updates existing node certificates that have been changed. Returns the count of existing node
// certificates
func (r *HumioClusterReconciler) updateNodeCertificates(ctx context.Context, hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool) (int, error) {
	certificates, err := kubernetes.ListCertificates(ctx, r, hnp.GetNamespace(), hnp.GetNodePoolLabels())
	if err != nil {
		return -1, err
	}

	existingNodeCertCount := 0
	for _, cert := range certificates {
		if strings.HasPrefix(cert.Name, fmt.Sprintf("%s-core", hnp.GetNodePoolName())) {
			existingNodeCertCount++

			// Check if we should update the existing certificate
			desiredCertificateHash := GetDesiredCertHash(hnp)

			err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				currentCertificate := &cmapi.Certificate{}
				err := r.Client.Get(ctx, types.NamespacedName{
					Namespace: cert.Namespace,
					Name:      cert.Name}, currentCertificate)
				if err != nil {
					return err
				}
				currentCertificateHash := currentCertificate.Annotations[certHashAnnotation]
				if currentCertificateHash != desiredCertificateHash {
					r.Log.Info(fmt.Sprintf("node certificate %s doesn't have expected hash, got: %s, expected: %s",
						currentCertificate.Name, currentCertificateHash, desiredCertificateHash))
					currentCertificateNameSubstrings := strings.Split(currentCertificate.Name, "-")
					currentCertificateSuffix := currentCertificateNameSubstrings[len(currentCertificateNameSubstrings)-1]

					desiredCertificate := ConstructNodeCertificate(hnp, currentCertificateSuffix)
					desiredCertificate.ResourceVersion = currentCertificate.ResourceVersion
					desiredCertificate.Annotations[certHashAnnotation] = desiredCertificateHash
					r.Log.Info(fmt.Sprintf("updating node TLS certificate with name %s", desiredCertificate.Name))
					if err := controllerutil.SetControllerReference(hc, &desiredCertificate, r.Scheme()); err != nil {
						return r.logErrorAndReturn(err, "could not set controller reference")
					}
					return r.Update(ctx, &desiredCertificate)
				}
				return r.Status().Update(ctx, hc)
			})
			if err != nil {
				if !k8serrors.IsNotFound(err) {
					return existingNodeCertCount, r.logErrorAndReturn(err, "failed to update resource status")
				}
			}
		}
	}
	return existingNodeCertCount, nil
}
