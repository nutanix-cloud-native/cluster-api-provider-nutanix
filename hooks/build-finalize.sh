#!/bin/bash
set -x
set -e

BUILD_DIR=~/project/package/docker
cp -r ~/project/api $BUILD_DIR
cp -r ~/project/controllers $BUILD_DIR
cp -r ~/project/pkg $BUILD_DIR
cp go.mod go.sum main.go $BUILD_DIR

