#!/usr/bin/env bash

set -x

declare -r tmp_kubeconfig=$HOME/.crc/machines/crc/kubeconfig
declare -r kubectl="oc --kubeconfig $tmp_kubeconfig"

crc setup
crc start --pull-secret-file=.crc-pull-secret.txt --memory 20480 --cpus 6
eval $(crc oc-env)
eval $(crc console --credentials | grep "To login as an admin, run" | cut -f2 -d"'")

$kubectl label node --all failure-domain.beta.kubernetes.io/zone=az1
