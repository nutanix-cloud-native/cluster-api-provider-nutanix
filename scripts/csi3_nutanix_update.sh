#!/usr/bin/env bash
# Copyright 2021 Nutanix.
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

NUTANIX_CSI_SNAPSHOT_VERSION=6.3.3
NUTANIX_CSI_STORAGE_VERSION=3.3.4

helm repo add nutanix-helm-releases https://nutanix.github.io/helm-releases/ --force-update && helm repo update

helm template -n ntnx-system nutanix-snapshot nutanix-helm-releases/nutanix-csi-snapshot --set tls.source=secret > templates/csi3/nutanix-csi-snapshot.yaml

ex templates/csi3/nutanix-csi-snapshot.yaml <<EOF
%s/caBundle: /caBundle: \${WEBHOOK_CA}/
xit
EOF

helm template -n ntnx-system nutanix-storage nutanix-helm-releases/nutanix-csi-storage --version ${NUTANIX_CSI_STORAGE_VERSION} --set createSecret=false --set createPrismCentralSecret=false > templates/csi3/nutanix-csi-storage.yaml

# Fix 1: Add namespace to nutanix-storage-precheck-job
# Helm generates the Job without a namespace field, but it needs one for proper deployment
ex templates/csi3/nutanix-csi-storage.yaml <<EOF
/kind: Job/;/name: nutanix-storage-precheck-job/a
  namespace: ntnx-system
.
xit
EOF

# Fix 2: Change --http-endpoint to --metrics-address for csi-snapshotter
# Helm generates --http-endpoint but the csi-snapshotter sidecar expects --metrics-address
ex templates/csi3/nutanix-csi-storage.yaml <<EOF
%s/--http-endpoint=:9812/--metrics-address=:9812/
xit
EOF
