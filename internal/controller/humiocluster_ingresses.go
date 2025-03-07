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
	"fmt"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/kubernetes"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	nginxProxyBodySizeValue = "512m"
	nginxProxyHttpVersion   = "1.1"
)

func constructNginxIngressAnnotations(hc *humiov1alpha1.HumioCluster, hostname string, ingressSpecificAnnotations map[string]string) map[string]string {
	annotations := make(map[string]string)
	annotations["nginx.ingress.kubernetes.io/configuration-snippet"] = `
more_set_headers "Expect-CT: max-age=604800, enforce";
more_set_headers "Referrer-Policy: no-referrer";
more_set_headers "X-Content-Type-Options: nosniff";
more_set_headers "X-Frame-Options: DENY";
more_set_headers "X-XSS-Protection: 1; mode=block";`

	annotations["nginx.ingress.kubernetes.io/cors-allow-credentials"] = "false"
	annotations["nginx.ingress.kubernetes.io/cors-allow-headers"] = "DNT,X-CustomHeader,Keep-Alive,User-Agent,X-Requested-With,If-Modified-Since,Cache-Control,Content-Type,Authorization"
	annotations["nginx.ingress.kubernetes.io/cors-allow-methods"] = "GET, PUT, POST, DELETE, PATCH, OPTIONS"
	annotations["nginx.ingress.kubernetes.io/cors-allow-origin"] = fmt.Sprintf("https://%s", hostname)
	annotations["nginx.ingress.kubernetes.io/enable-cors"] = helpers.TrueStr
	annotations["nginx.ingress.kubernetes.io/upstream-vhost"] = hostname

	if ingressTLSOrDefault(hc) {
		annotations["nginx.ingress.kubernetes.io/force-ssl-redirect"] = helpers.TrueStr
	}

	if helpers.TLSEnabled(hc) {
		annotations["nginx.ingress.kubernetes.io/backend-protocol"] = "HTTPS"
		annotations["nginx.ingress.kubernetes.io/proxy-ssl-name"] = fmt.Sprintf("%s.%s", hc.Name, hc.Namespace)
		annotations["nginx.ingress.kubernetes.io/proxy-ssl-server-name"] = fmt.Sprintf("%s.%s", hc.Name, hc.Namespace)
		annotations["nginx.ingress.kubernetes.io/proxy-ssl-secret"] = fmt.Sprintf("%s/%s", hc.Namespace, hc.Name)
		annotations["nginx.ingress.kubernetes.io/proxy-ssl-verify"] = "on"
	}

	for k, v := range ingressSpecificAnnotations {
		annotations[k] = v
	}
	return annotations
}

func ConstructGeneralIngress(hc *humiov1alpha1.HumioCluster, hostname string) *networkingv1.Ingress {
	annotations := make(map[string]string)
	annotations["nginx.ingress.kubernetes.io/proxy-body-size"] = nginxProxyBodySizeValue
	annotations["nginx.ingress.kubernetes.io/proxy-http-version"] = nginxProxyHttpVersion
	annotations["nginx.ingress.kubernetes.io/proxy-read-timeout"] = "25"
	return constructIngress(
		hc,
		fmt.Sprintf("%s-general", hc.Name),
		hostname,
		[]string{humioPathOrDefault(hc)},
		HumioPort,
		certificateSecretNameOrDefault(hc),
		constructNginxIngressAnnotations(hc, hostname, annotations),
	)
}

func ConstructStreamingQueryIngress(hc *humiov1alpha1.HumioCluster, hostname string) *networkingv1.Ingress {
	annotations := make(map[string]string)
	annotations["nginx.ingress.kubernetes.io/proxy-body-size"] = nginxProxyBodySizeValue
	annotations["nginx.ingress.kubernetes.io/proxy-http-version"] = nginxProxyHttpVersion
	annotations["nginx.ingress.kubernetes.io/proxy-read-timeout"] = "4h"
	annotations["nginx.ingress.kubernetes.io/use-regex"] = helpers.TrueStr
	annotations["nginx.ingress.kubernetes.io/proxy-buffering"] = "off"
	return constructIngress(
		hc,
		fmt.Sprintf("%s-streaming-query", hc.Name),
		hostname,
		[]string{fmt.Sprintf("%sapi/v./(dataspaces|repositories)/[^/]+/query$", humioPathOrDefault(hc))},
		HumioPort,
		certificateSecretNameOrDefault(hc),
		constructNginxIngressAnnotations(hc, hostname, annotations),
	)
}

func ConstructIngestIngress(hc *humiov1alpha1.HumioCluster, hostname string) *networkingv1.Ingress {
	annotations := make(map[string]string)
	annotations["nginx.ingress.kubernetes.io/proxy-body-size"] = nginxProxyBodySizeValue
	annotations["nginx.ingress.kubernetes.io/proxy-http-version"] = nginxProxyHttpVersion
	annotations["nginx.ingress.kubernetes.io/proxy-read-timeout"] = "90"
	annotations["nginx.ingress.kubernetes.io/use-regex"] = helpers.TrueStr
	return constructIngress(
		hc,
		fmt.Sprintf("%s-ingest", hc.Name),
		hostname,
		[]string{
			fmt.Sprintf("%sapi/v./(dataspaces|repositories)/[^/]+/(ingest|logplex)", humioPathOrDefault(hc)),
			fmt.Sprintf("%sapi/v1/ingest", humioPathOrDefault(hc)),
			fmt.Sprintf("%sservices/collector", humioPathOrDefault(hc)),
			fmt.Sprintf("%s_bulk", humioPathOrDefault(hc)),
		},
		HumioPort,
		certificateSecretNameOrDefault(hc),
		constructNginxIngressAnnotations(hc, hostname, annotations),
	)
}

func ConstructESIngestIngress(hc *humiov1alpha1.HumioCluster, esHostname string) *networkingv1.Ingress {
	annotations := make(map[string]string)
	annotations["nginx.ingress.kubernetes.io/proxy-body-size"] = nginxProxyBodySizeValue
	annotations["nginx.ingress.kubernetes.io/proxy-http-version"] = nginxProxyHttpVersion
	annotations["nginx.ingress.kubernetes.io/proxy-read-timeout"] = "90"
	return constructIngress(
		hc,
		fmt.Sprintf("%s-es-ingest", hc.Name),
		esHostname,
		[]string{humioPathOrDefault(hc)},
		ElasticPort,
		esCertificateSecretNameOrDefault(hc),
		constructNginxIngressAnnotations(hc, esHostname, annotations),
	)
}

func constructIngress(hc *humiov1alpha1.HumioCluster, name string, hostname string, paths []string, port int32, secretName string, annotations map[string]string) *networkingv1.Ingress {
	httpIngressPaths := make([]networkingv1.HTTPIngressPath, len(paths))
	pathTypeImplementationSpecific := networkingv1.PathTypeImplementationSpecific
	idx := 0
	for _, path := range paths {
		httpIngressPaths[idx] = networkingv1.HTTPIngressPath{
			Path:     path,
			PathType: &pathTypeImplementationSpecific,
			Backend: networkingv1.IngressBackend{
				Service: &networkingv1.IngressServiceBackend{
					Name: (*ConstructService(NewHumioNodeManagerFromHumioCluster(hc))).Name,
					Port: networkingv1.ServiceBackendPort{
						Number: port,
					},
				},
			},
		}
		idx++
	}
	ingress := networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   hc.Namespace,
			Annotations: annotations,
			Labels:      kubernetes.MatchingLabelsForHumio(hc.Name),
		},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{
				{
					Host: hostname,
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: httpIngressPaths,
						},
					},
				},
			},
		},
	}
	if ingressTLSOrDefault(hc) {
		ingress.Spec.TLS = []networkingv1.IngressTLS{
			{
				Hosts:      []string{hostname},
				SecretName: secretName,
			},
		}
	}

	for k, v := range hc.Spec.Ingress.Annotations {
		ingress.ObjectMeta.Annotations[k] = v
	}
	return &ingress
}