#!/bin/bash

echo -n "Building binaries... "
docker run --rm -v $PWD:/odaotool rocketpool/smartnode-builder:latest /odaotool/build_binaries.sh
echo "done!"
