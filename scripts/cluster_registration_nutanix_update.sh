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

NUTANIX_CLUSTER_REGISTRATION_CONTROLLER_VERSION=v0.0.0
NUTANIX_CLUSTER_REGISTRATION_CONTROLLER_REPO=deepakmntnx/test-controller:latest

# helm template k8s-onboarding-operator -n ntnx-system ./scripts/k8s-onboarding-operator-chart-0.1.0.tgz \
#   --include-crds \
#   > templates/cluster-registration/nutanix-cluster-registration.yaml
