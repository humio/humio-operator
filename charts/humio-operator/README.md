# humio-operator

[humio-operator](https://github.com/humio/humio-operator) Kubernetes Operator for running Humio on top of Kubernetes

## TL;DR

```bash
helm repo add humio-operator https://humio.github.io/humio-operator
helm install humio-operator humio-operator/humio-operator
```

## Introduction

This chart bootstraps a humio-operator deployment on a [Kubernetes](http://kubernetes.io) cluster using the [Helm](https://helm.sh) package manager.

## Prerequisites

- Kubernetes 1.16+

## Installing the CRD's

```bash
kubectl apply -f https://raw.githubusercontent.com/humio/humio-operator/operator-0.0.4/deploy/crds/core.humio.com_humioclusters_crd.yaml
kubectl apply -f https://raw.githubusercontent.com/humio/humio-operator/operator-0.0.4/deploy/crds/core.humio.com_humioexternalclusters_crd.yaml
kubectl apply -f https://raw.githubusercontent.com/humio/humio-operator/operator-0.0.4/deploy/crds/core.humio.com_humioingesttokens_crd.yaml
kubectl apply -f https://raw.githubusercontent.com/humio/humio-operator/operator-0.0.4/deploy/crds/core.humio.com_humioparsers_crd.yaml
kubectl apply -f https://raw.githubusercontent.com/humio/humio-operator/operator-0.0.4/deploy/crds/core.humio.com_humiorepositories_crd.yaml
```

## Installing the Chart

To install the chart with the release name `humio-operator`:

```bash
# Helm v3+
helm install humio-operator humio-operator/humio-operator --namespace humio-operator -f values.yaml

# Helm v2
helm install humio-operator/humio-helm-charts --name humio --namespace humio-operator -f values.yaml
```

The command deploys humio-operator on the Kubernetes cluster in the default configuration. The [configuration](#configuration) section lists the parameters that can be configured during installation.

> **Tip**: List all releases using `helm list`

## Uninstalling the Chart

To uninstall/delete the `humio-operator` deployment:

```bash
helm delete humio-operator --namespace humio-operator
```

The command removes all the Kubernetes components associated with the chart and deletes the release.

## Configuration

The following table lists the configurable parameters of the ingress-nginx chart and their default values.

Parameter | Description | Default
--- | --- | ---
`operator.image.repository` | operator container image repository | `humio/humio-operator`
`operator.image.tag` | operator container image tag | `0.0.4`
`operator.rbac.create` | automatically create operator RBAC resources | `true`
`operator.watchNamespaces` | list of namespaces the operator will watch for resources (if empty, it watches all namespaces) | `[]`
`installCRDs` | automatically install CRDs. NB: if this is set to true, custom resources will be removed if the Helm chart is uninstalled | `false`
`openshift` | install additional RBAC resources specific to OpenShift | `false`

These parameters can be passed via Helm's `--set` option

```bash
# Helm v3+
helm install humio-operator humio-operator/humio-operator \
  --set operator.image.tag=0.0.4

# Helm v2
helm install humio-operator --name humio-operator \
  --set operator.image.tag=0.0.4
```

Alternatively, a YAML file that specifies the values for the parameters can be provided while installing the chart. For example,

```bash
# Helm v3+
helm install humio-operator humio-operator/humio-operator --namespace humio-operator -f values.yaml

# Helm v2
helm install humio-operator/humio-helm-charts --name humio-operator --namespace humio-operator -f values.yaml
```
