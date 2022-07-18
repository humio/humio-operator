#!/usr/bin/env bash

set -x

echo "detected OSTYPE = $OSTYPE"

export RELEASE_VERSION=$(cat VERSION)

rm -rf charts/humio-operator/crds
mkdir -p charts/humio-operator/crds
for c in $(find config/crd/bases/ -iname '*.yaml' | sort); do
  # Update base CRD's in-place with static values
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

  # Write base CRD to helm chart file
  cp $c charts/humio-operator/crds/$(basename $c)
done
