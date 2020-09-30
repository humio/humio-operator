#!/usr/bin/env bash

set -x

crc stop
sleep 5
rm -rf ~/.crc/{cache,machines}
