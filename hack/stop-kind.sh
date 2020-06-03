#!/usr/bin/env bash

set -x

# Clean up old stuff
kind delete cluster --name kind
