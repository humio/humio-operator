#!/bin/bash

set -x

declare -r tmp_kubeconfig=/tmp/kubeconfig

kind create cluster --name kind --image kindest/node:v1.17.2
kind get kubeconfig > $tmp_kubeconfig
