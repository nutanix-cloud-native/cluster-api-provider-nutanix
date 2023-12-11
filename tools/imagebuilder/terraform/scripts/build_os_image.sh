#!/usr/bin/env bash
# Copyright 2022 Nutanix
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

git clone https://github.com/kubernetes-sigs/image-builder.git
export PATH=$PATH:~/.local/bin
cd ~/image-builder/images/capi
git checkout nutanix-imagebuilder

cp ~/nutanix.json packer/nutanix/nutanix.json

chmod +x hack/*
make deps-nutanix
make build-nutanix-ubuntu-2004