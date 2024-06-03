#!/bin/bash

# TODO Add Copyright

set -o errexit
set -o pipefail

REPO_ROOT=$(dirname "${BASH_SOURCE[0]}")/..
cd "${REPO_ROOT}" || exit 1

# Expects ubuntu container

# Install prerequisites
apt update
apt install -y make wget

make --version
docker --version

# shellcheck source=./hack/install-go.sh
source "${REPO_ROOT}/hack/install-go.sh"

# shellcheck source=./hack/ensure-go.sh
source "${REPO_ROOT}/hack/ensure-go.sh"

# Make sure the tools binaries are on the path.
export PATH="${REPO_ROOT}/hack/tools/bin:${PATH}"


# Override e2e conf values with CI specific environment variables
MAKE_TARGET=${MAKE_TARGET}
LABEL_FILTERS="${LABEL_FILTERS}"
KUBERNETES_VERSION_MANAGEMENT=${KUBERNETES_VERSION_MANAGEMENT}
NUTANIX_ENDPOINT=${NUTANIX_ENDPOINT}
NUTANIX_USER=${NUTANIX_USER}
NUTANIX_PASSWORD=${NUTANIX_PASSWORD}
NUTANIX_INSECURE=${NUTANIX_INSECURE}
KUBERNETES_VERSION=${KUBERNETES_VERSION}
NUTANIX_SSH_AUTHORIZED_KEY=${NUTANIX_SSH_AUTHORIZED_KEY}
CONTROL_PLANE_ENDPOINT_IP=${CONTROL_PLANE_ENDPOINT_IP}
NUTANIX_PRISM_ELEMENT_CLUSTER_NAME=${NUTANIX_PRISM_ELEMENT_CLUSTER_NAME}
NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME=${NUTANIX_MACHINE_TEMPLATE_IMAGE_NAME}
NUTANIX_SUBNET_NAME=${NUTANIX_SUBNET_NAME}

# Run e2e tests
make ${MAKE_TARGET} LABEL_FILTERS="${LABEL_FILTERS}"
