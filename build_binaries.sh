#!/bin/bash

cd /odaotool

export CGO_ENABLED=1

# Build x64 version
CGO_CFLAGS="-O -D__BLST_PORTABLE__" GOARCH=amd64 GOOS=linux go build -o Releases/odaotool-linux-amd64 -buildvcs=false

# Build the arm64 version
CC=aarch64-linux-gnu-gcc CXX=aarch64-linux-gnu-cpp CGO_CFLAGS="-O -D__BLST_PORTABLE__" GOARCH=arm64 GOOS=linux go build -o Releases/odaotool-linux-arm64 -buildvcs=false
