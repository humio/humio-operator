#!/usr/bin/env bash

set -x

# Extract humio images and tags from go source
DEFAULT_IMAGE=$(grep '^\s*image' controllers/humiocluster_defaults.go | cut -d '"' -f 2)
PRE_UPDATE_IMAGE=$(grep '^\s*toCreate\.Spec\.Image' controllers/humiocluster_controller_test.go | cut -d '"' -f 2)

# Preload default image used by tests
docker pull $DEFAULT_IMAGE
kind load docker-image --name kind $DEFAULT_IMAGE

# Preload image used by e2e update tests
docker pull $PRE_UPDATE_IMAGE
kind load docker-image --name kind $PRE_UPDATE_IMAGE

# Preload image we will run e2e tests from within
docker build -t testcontainer -f test.Dockerfile .
kind load docker-image testcontainer
