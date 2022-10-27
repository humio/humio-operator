#!/usr/bin/env bash

set -x

start=$(date +%s)

# Extract humio images and tags from go source
DEFAULT_IMAGE=$(grep '^\s*Image\s*=' controllers/humiocluster_defaults.go | cut -d '"' -f 2)
PRE_UPDATE_IMAGES=$(grep 'Version\s* = ' controllers/suite/clusters/humiocluster_controller_test.go | grep -v oldUnsupportedHumioVersion | grep -v 1.x.x | cut -d '"' -f 2 | sort -u)

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
docker build --no-cache --pull -t testcontainer -f test.Dockerfile .
kind load docker-image testcontainer

end=$(date +%s)
echo "Preloading images into kind took $((end-start)) seconds"
