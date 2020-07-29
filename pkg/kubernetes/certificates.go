package kubernetes

import (
	"context"
	cmapi "github.com/jetstack/cert-manager/pkg/apis/certmanager/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ListCertificates grabs the list of all certificates associated to a an instance of HumioCluster
func ListCertificates(c client.Client, humioClusterNamespace string, matchingLabels client.MatchingLabels) ([]cmapi.Certificate, error) {
	var foundCertificateList cmapi.CertificateList
	err := c.List(context.TODO(), &foundCertificateList, client.InNamespace(humioClusterNamespace), matchingLabels)
	if err != nil {
		return nil, err
	}

	return foundCertificateList.Items, nil
}
