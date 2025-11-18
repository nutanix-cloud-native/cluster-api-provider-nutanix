#!/usr/bin/env bash
# Copyright 2022 Nutanix.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

NUTANIX_CCM_VERSION=0.6.0
NUTANIX_CCM_REPO=ghcr.io/nutanix-cloud-native/cloud-provider-nutanix/controller

helm repo add nutanix https://nutanix.github.io/helm/ --force-update && helm repo update

helm template -n kube-system nutanix-cloud-provider nutanix/nutanix-cloud-provider --version ${NUTANIX_CCM_VERSION} \
  --set prismCentralEndPoint='${NUTANIX_ENDPOINT}',prismCentralPort='${NUTANIX_PORT=9440}',prismCentralInsecure='${NUTANIX_INSECURE=false}' \
  --set image.repository="\${CCM_REPO=$NUTANIX_CCM_REPO}",image.tag="\${CCM_TAG=v$NUTANIX_CCM_VERSION}" \
  --set createSecret=false \
  > templates/ccm/nutanix-ccm.yaml
