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

	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/kubernetes"
	cmapi "github.com/jetstack/cert-manager/pkg/apis/certmanager/v1beta1"
	cmmeta "github.com/jetstack/cert-manager/pkg/apis/meta/v1"
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
		return false, nil
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
	pem.Encode(caCertificatePEM, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caBytes,
	})

	caPrivateKeyPEM := new(bytes.Buffer)
	pem.Encode(caPrivateKeyPEM, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(caPrivateKey),
	})

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
			Labels:    kubernetes.MatchingLabelsForHumio(hc.Name),
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
			},
			IssuerRef: cmmeta.ObjectReference{
				Name: constructCAIssuer(hc).Name,
			},
			SecretName: hc.Name,
		},
	}
}

func constructNodeCertificate(hc *humiov1alpha1.HumioCluster, nodeSuffix string) cmapi.Certificate {
	return cmapi.Certificate{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{},
			Namespace:   hc.Namespace,
			Name:        fmt.Sprintf("%s-core-%s", hc.Name, nodeSuffix),
			Labels:      kubernetes.MatchingLabelsForHumio(hc.Name),
		},
		Spec: cmapi.CertificateSpec{
			DNSNames: []string{
				fmt.Sprintf("%s-core-%s.%s.%s", hc.Name, nodeSuffix, hc.Name, hc.Namespace), // Used for intra-cluster communication
				fmt.Sprintf("%s-core-%s", hc.Name, nodeSuffix),                              // Used for auth sidecar
				fmt.Sprintf("%s.%s", hc.Name, hc.Namespace),                                 // Used by humio-operator and ingress controllers to reach the Humio API
			},
			IssuerRef: cmmeta.ObjectReference{
				Name: constructCAIssuer(hc).Name,
			},
			SecretName: fmt.Sprintf("%s-core-%s", hc.Name, nodeSuffix),
			Keystores: &cmapi.CertificateKeystores{
				JKS: &cmapi.JKSKeystore{
					Create: true,
					PasswordSecretRef: cmmeta.SecretKeySelector{
						LocalObjectReference: cmmeta.LocalObjectReference{
							Name: fmt.Sprintf("%s-keystore-passphrase", hc.Name),
						},
						Key: "passphrase",
					},
				},
			},
		},
	}
}

func (r *HumioClusterReconciler) waitForNewNodeCertificate(ctx context.Context, hc *humiov1alpha1.HumioCluster, expectedCertCount int) error {
	for i := 0; i < waitForNodeCertificateTimeoutSeconds; i++ {
		existingNodeCertCount, err := r.updateNodeCertificates(ctx, hc)
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
func (r *HumioClusterReconciler) updateNodeCertificates(ctx context.Context, hc *humiov1alpha1.HumioCluster) (int, error) {
	certificates, err := kubernetes.ListCertificates(r, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))
	if err != nil {
		return -1, err
	}

	existingNodeCertCount := 0
	for _, cert := range certificates {
		if strings.HasPrefix(cert.Name, fmt.Sprintf("%s-core", hc.Name)) {
			existingNodeCertCount++

			// Check if we should update the existing certificate
			certForHash := constructNodeCertificate(hc, "")

			// Keystores will always contain a new pointer when constructing a certificate.
			// To work around this, we override it to nil before calculating the hash,
			// if we do not do this, the hash will always be different.
			certForHash.Spec.Keystores = nil

			b, _ := json.Marshal(certForHash)
			desiredCertificateHash := helpers.AsSHA256(string(b))
			currentCertificateHash, _ := cert.Annotations[certHashAnnotation]
			if currentCertificateHash != desiredCertificateHash {
				r.Log.Info(fmt.Sprintf("node certificate %s doesn't have expected hash, got: %s, expected: %s",
					cert.Name, currentCertificateHash, desiredCertificateHash))
				currentCertificateNameSubstrings := strings.Split(cert.Name, "-")
				currentCertificateSuffix := currentCertificateNameSubstrings[len(currentCertificateNameSubstrings)-1]

				desiredCertificate := constructNodeCertificate(hc, currentCertificateSuffix)
				desiredCertificate.ResourceVersion = cert.ResourceVersion
				desiredCertificate.Annotations[certHashAnnotation] = desiredCertificateHash
				r.Log.Info(fmt.Sprintf("updating node TLS certificate with name %s", desiredCertificate.Name))
				if err := controllerutil.SetControllerReference(hc, &desiredCertificate, r.Scheme()); err != nil {
					r.Log.Error(err, "could not set controller reference")
					return existingNodeCertCount, err
				}
				err = r.Update(ctx, &desiredCertificate)
				if err != nil {
					return existingNodeCertCount, err
				}
			}
		}
	}
	return existingNodeCertCount, nil
}
