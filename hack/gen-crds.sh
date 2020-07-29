#!/usr/bin/env bash

set -x

echo "detected OSTYPE = $OSTYPE"

operator-sdk generate crds
export RELEASE_VERSION=$(grep "Version =" version/version.go | awk -F'"' '{print $2}')
# TODO: Figure out what the sed command looks like on linux vs mac and if we even want to depend on gsed on mac's

echo "{{- if .Values.installCRDs -}}" > charts/humio-operator/templates/crds.yaml
for c in $(find deploy/crds/ -iname '*crd.yaml'); do
  echo "---" >> charts/humio-operator/templates/crds.yaml
  cat $c >> charts/humio-operator/templates/crds.yaml

  if [[ "$OSTYPE" == "linux-gnu"* ]]; then
    sed -i "/^spec:/i \  labels:\n    app: 'humio-operator'\n    app.kubernetes.io/name: 'humio-operator'\n    app.kubernetes.io/instance: 'humio-operator'\n    app.kubernetes.io/managed-by: 'Helm'\n    helm.sh/chart: 'humio-operator-$RELEASE_VERSION'" $c
  elif [[ "$OSTYPE" == "darwin"* ]]; then
    if [[ $(which gsed) ]]; then
      gsed -i "/^spec:/i \  labels:\n    app: 'humio-operator'\n    app.kubernetes.io/name: 'humio-operator'\n    app.kubernetes.io/instance: 'humio-operator'\n    app.kubernetes.io/managed-by: 'Helm'\n    helm.sh/chart: 'humio-operator-$RELEASE_VERSION'" $c
    else
      sed -i '' -E '/^spec:/i\ '$'\n''\  labels:'$'\n' $c
      sed -i '' -E '/^spec:/i\ '$'\n''\    app: '"'humio-operator'"$'\n' $c
      sed -i '' -E '/^spec:/i\ '$'\n''\    app.kubernetes.io/name: '"'humio-operator'"$'\n' $c
      sed -i '' -E '/^spec:/i\ '$'\n''\    app.kubernetes.io/instance: '"'humio-operator'"$'\n' $c
      sed -i '' -E '/^spec:/i\ '$'\n''\    app.kubernetes.io/managed-by: '"'Helm'"$'\n' $c
      sed -i '' -E '/^spec:/i\ '$'\n''\    helm.sh/chart: '"'humio-operator-$RELEASE_VERSION'"$'\n' $c
    fi
  else
    echo "$OSTYPE not supported"
    exit 1
  fi
done
echo "{{- end }}" >> charts/humio-operator/templates/crds.yaml

if [[ "$OSTYPE" == "linux-gnu"* ]]; then
	sed -i "/^spec:/i \  labels:\n    app: '{{ .Chart.Name }}'\n    app.kubernetes.io/name: '{{ .Chart.Name }}'\n    app.kubernetes.io/instance: '{{ .Release.Name }}'\n    app.kubernetes.io/managed-by: '{{ .Release.Service }}'\n    helm.sh/chart: '{{ template \"humio.chart\" . }}'" charts/humio-operator/templates/crds.yaml
elif [[ "$OSTYPE" == "darwin"* ]]; then
  if [[ $(which gsed) ]]; then
	  gsed -i "/^spec:/i \  labels:\n    app: '{{ .Chart.Name }}'\n    app.kubernetes.io/name: '{{ .Chart.Name }}'\n    app.kubernetes.io/instance: '{{ .Release.Name }}'\n    app.kubernetes.io/managed-by: '{{ .Release.Service }}'\n    helm.sh/chart: '{{ template \"humio.chart\" . }}'" charts/humio-operator/templates/crds.yaml
  else
    sed -i '' -E '/^spec:/i\ '$'\n''\  labels:'$'\n' charts/humio-operator/templates/crds.yaml
    sed -i '' -E '/^spec:/i\ '$'\n''\    app: '"'{{ .Chart.Name }}'"$'\n' charts/humio-operator/templates/crds.yaml
    sed -i '' -E '/^spec:/i\ '$'\n''\    app.kubernetes.io/name: '"'{{ .Chart.Name }}'"$'\n' charts/humio-operator/templates/crds.yaml
    sed -i '' -E '/^spec:/i\ '$'\n''\    app.kubernetes.io/instance: '"'{{ .Release.Name }}'"$'\n' charts/humio-operator/templates/crds.yaml
    sed -i '' -E '/^spec:/i\ '$'\n''\    app.kubernetes.io/managed-by: '"'{{ .Release.Service }}'"$'\n' charts/humio-operator/templates/crds.yaml
    sed -i '' -E '/^spec:/i\ '$'\n''\    helm.sh/chart: '"'{{ template \"humio.chart\" . }}'"$'\n' charts/humio-operator/templates/crds.yaml
  fi
else
  echo "$OSTYPE not supported"
  exit 1
fi
