#!/usr/bin/env bash

set -x

echo "detected OSTYPE = $OSTYPE"

export RELEASE_VERSION=$(cat VERSION)

echo "{{- if .Values.installCRDs -}}" > charts/humio-operator/templates/crds.yaml
for c in $(find config/crd/bases/ -iname '*.yaml' | sort); do
  # Write base CRD to helm chart file
  cat $c >> charts/humio-operator/templates/crds.yaml

  # Update base CRD's in-place with static values
  if [[ "$OSTYPE" == "linux-gnu"* ]]; then
    sed -i "/^spec:/i \  labels:\n    app: humio-operator\n    app.kubernetes.io/name: humio-operator\n    app.kubernetes.io/instance: humio-operator\n    app.kubernetes.io/managed-by: Helm\n    helm.sh/chart: humio-operator-$RELEASE_VERSION" $c
  elif [[ "$OSTYPE" == "darwin"* ]]; then
    if [[ $(which gsed) ]]; then
      gsed -i "/^spec:/i \  labels:\n    app: humio-operator\n    app.kubernetes.io/name: humio-operator\n    app.kubernetes.io/instance: humio-operator\n    app.kubernetes.io/managed-by: Helm\n    helm.sh/chart: humio-operator-$RELEASE_VERSION" $c
    else
      sed -i '' -E '/^spec:/i\ '$'\n''\  labels:'$'\n' $c
      sed -i '' -E '/^spec:/i\ '$'\n''\    app: '"humio-operator"$'\n' $c
      sed -i '' -E '/^spec:/i\ '$'\n''\    app.kubernetes.io/name: '"humio-operator"$'\n' $c
      sed -i '' -E '/^spec:/i\ '$'\n''\    app.kubernetes.io/instance: '"humio-operator"$'\n' $c
      sed -i '' -E '/^spec:/i\ '$'\n''\    app.kubernetes.io/managed-by: '"Helm"$'\n' $c
      sed -i '' -E '/^spec:/i\ '$'\n''\    helm.sh/chart: '"humio-operator-$RELEASE_VERSION"$'\n' $c
    fi
  else
    echo "$OSTYPE not supported"
    exit 1
  fi
done
echo "{{- end }}" >> charts/humio-operator/templates/crds.yaml

# Update helm chart CRD's with additional chart install values.
if [[ "$OSTYPE" == "linux-gnu"* ]]; then
	sed -i "/^spec:/i \  labels:\n    app: {{ .Chart.Name }}\n    app.kubernetes.io/name: {{ .Chart.Name }}\n    app.kubernetes.io/instance: {{ .Release.Name }}\n    app.kubernetes.io/managed-by: {{ .Release.Service }}\n    helm.sh/chart: {{ template \"operator.chart\" . }}" charts/humio-operator/templates/crds.yaml
elif [[ "$OSTYPE" == "darwin"* ]]; then
  if [[ $(which gsed) ]]; then
	  gsed -i "/^spec:/i \  labels:\n    app: {{ .Chart.Name }}\n    app.kubernetes.io/name: {{ .Chart.Name }}\n    app.kubernetes.io/instance: {{ .Release.Name }}\n    app.kubernetes.io/managed-by: {{ .Release.Service }}\n    helm.sh/chart: {{ template \"operator.chart\" . }}" charts/humio-operator/templates/crds.yaml
  else
    sed -i '' -E '/^spec:/i\ '$'\n''\  labels:'$'\n' charts/humio-operator/templates/crds.yaml
    sed -i '' -E '/^spec:/i\ '$'\n''\    app: '"{{ .Chart.Name }}"$'\n' charts/humio-operator/templates/crds.yaml
    sed -i '' -E '/^spec:/i\ '$'\n''\    app.kubernetes.io/name: '"{{ .Chart.Name }}"$'\n' charts/humio-operator/templates/crds.yaml
    sed -i '' -E '/^spec:/i\ '$'\n''\    app.kubernetes.io/instance: '"{{ .Release.Name }}"$'\n' charts/humio-operator/templates/crds.yaml
    sed -i '' -E '/^spec:/i\ '$'\n''\    app.kubernetes.io/managed-by: '"{{ .Release.Service }}"$'\n' charts/humio-operator/templates/crds.yaml
    sed -i '' -E '/^spec:/i\ '$'\n''\    helm.sh/chart: '"{{ template \"operator.chart\" . }}"$'\n' charts/humio-operator/templates/crds.yaml
  fi
else
  echo "$OSTYPE not supported"
  exit 1
fi
