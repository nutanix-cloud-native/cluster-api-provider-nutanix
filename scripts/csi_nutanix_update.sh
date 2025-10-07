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
NUTANIX_CSI_STORAGE_VERSION=2.6.10

helm repo add nutanix https://nutanix.github.io/helm/ --force-update && helm repo update

helm template -n ntnx-system nutanix-snapshot nutanix/nutanix-csi-snapshot --set tls.source=secret > templates/csi/nutanix-csi-snapshot.yaml

ex templates/csi/nutanix-csi-snapshot.yaml <<EOF
%s/caBundle: /caBundle: \${WEBHOOK_CA}/
xit
EOF

helm template -n ntnx-system nutanix-storage nutanix/nutanix-csi-storage --version ${NUTANIX_CSI_STORAGE_VERSION} --set createSecret=false > templates/csi/nutanix-csi-storage.yaml
