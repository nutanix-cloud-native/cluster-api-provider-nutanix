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

title="csi-snapshot-webhook"
namespace="ntnx-system"
csrName=${title}.${namespace}
tmpdir=$(mktemp -d)
echo "creating certs in tmpdir ${tmpdir} "

openssl genrsa -out ${tmpdir}/ca.key 2048
openssl req -x509 -new -nodes -key ${tmpdir}/ca.key -days 100000 -out ${tmpdir}/ca.crt -subj "/CN=admission_ca"

cat <<EOF >> ${tmpdir}/server.conf
[req]
req_extensions = v3_req
distinguished_name = req_distinguished_name
[req_distinguished_name]
[ v3_req ]
basicConstraints = CA:FALSE
keyUsage = nonRepudiation, digitalSignature, keyEncipherment
extendedKeyUsage = clientAuth, serverAuth
subjectAltName = DNS:${title}, DNS:${title}.${namespace}, DNS:${title}.${namespace}.svc
[alt_names]
DNS.1 = ${title}
DNS.2 = ${title}.${namespace}
DNS.3 = ${title}.${namespace}.svc
EOF

openssl genrsa -out ${tmpdir}/server.key 2048
openssl req -new -key ${tmpdir}/server.key -out ${tmpdir}/server.csr -subj "/CN=${title}.${namespace}.svc" -config ${tmpdir}/server.conf

openssl x509 -req -in ${tmpdir}/server.csr -CA ${tmpdir}/ca.crt -CAkey ${tmpdir}/ca.key -CAcreateserial -out ${tmpdir}/server.crt -days 100000 -extensions v3_req -extfile ${tmpdir}/server.conf


export WEBHOOK_KEY=`cat ${tmpdir}/server.key | base64`
export WEBHOOK_CERT=`cat ${tmpdir}/server.crt | base64`
export WEBHOOK_CA=`cat ${tmpdir}/ca.crt | base64`
