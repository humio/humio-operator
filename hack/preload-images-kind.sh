#!/usr/bin/env bash

set -x

# Extract humio images and tags from go source
DEFAULT_IMAGE=$(grep '^\s*image' controllers/humiocluster_defaults.go | cut -d '"' -f 2)
PRE_UPDATE_IMAGES=$(grep '^\s*toCreate\.Spec\.Image' controllers/humiocluster_controller_test.go | cut -d '"' -f 2 | sort -u)

# Preload default image used by tests
docker pull $DEFAULT_IMAGE
kind load docker-image --name kind $DEFAULT_IMAGE

# Preload image used by e2e update tests
for image in $PRE_UPDATE_IMAGES
do
  docker pull $image
  kind load docker-image --name kind $image
done

# Preload image we will run e2e tests from within
docker build -t testcontainer -f test.Dockerfile .
kind load docker-image testcontainer
