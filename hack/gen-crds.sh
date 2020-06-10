#!/usr/bin/env bash

set -x

operator-sdk generate crds

echo "{{- if .Values.installCRDs -}}" > charts/humio-operator/templates/crds.yaml
for c in $(find deploy/crds/ -iname '*crd.yaml'); do
  echo "---" >> charts/humio-operator/templates/crds.yaml
  cat $c >> charts/humio-operator/templates/crds.yaml
done
echo "{{- end }}" >> charts/humio-operator/templates/crds.yaml
