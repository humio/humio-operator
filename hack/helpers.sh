#!/bin/bash

declare -r python_version=3.7

# Search for python version $python_version
get_python_binary() {
  python_bin=$(which python)
  for p in $python_bin "${python_bin}${python_version}" /usr/local/bin/python /usr/local/bin/python${python_version}; do
    if [ -f $p ]; then
      version=$($p --version 2>&1)
      if [[ $version =~ $python_version ]]; then
        echo $p
        return
      fi
    fi
  done
  echo $python_bin
}