#!/bin/bash

set -x

crc setup
crc start --pull-secret-file=.crc-pull-secret.txt --memory 20480 --cpus 6
eval $(crc oc-env)
eval $(crc console --credentials | grep "To login as an admin, run" | cut -f2 -d"'")
