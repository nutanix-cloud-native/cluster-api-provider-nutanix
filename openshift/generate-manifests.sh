#!/bin/bash

set -o errexit
set -o pipefail

echo "Running generate-manifests.sh"

STAGE=release MANIFEST_DIR=openshift BUILD_DIR=openshift make manifests
