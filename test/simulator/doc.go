/*
Copyright 2026 Nutanix

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package simulator implements ntnx-sim, an in-memory Prism Central simulator
// that serves the subset of the Nutanix v4 REST API used by CAPX.
//
// The simulator is a library (an http.Handler over an in-memory store) with a
// thin binary in cmd/ntnx-sim. It lets an unmodified CAPX manager run without
// Nutanix hardware, which is what a scale test or a hardware-free CI job needs.
//
// Wire compatibility is obtained by construction: every response is produced by
// marshalling the same ntnx-api-golang-clients model structs that CAPX (via
// prism-go-client) unmarshals, so envelopes, discriminators and enum spellings
// match the SDK CAPX is built against.
//
// Scope (phase 1): everything the NutanixCluster and NutanixMachine reconcilers
// call on a non-metro cluster template without a project:
//
//   - OPTIONS /api/{namespace}/unversioned/info  (SDK version negotiation)
//   - GET     /api/nutanix/v3/users/me            (session-auth login CAPX performs at client construction)
//   - vmm:         VMs list/get/create/delete, power-on, power-off, add-custom-attributes; images list/get
//   - clustermgmt: clusters list/get; storage containers list
//   - networking:  subnets list/get
//   - prism:       categories list/get/create/delete; tasks list/get
//
// Every other path under /api returns 501 so an unexpected call fails loudly.
//
// Behaviours CAPX depends on and the simulator honours:
//
//   - Mutations return 202 with a task reference. Tasks move RUNNING -> SUCCEEDED
//     after a configurable duration and carry the affected entity reference so
//     prism-go-client's Operation.Wait can resolve the entity.
//   - GET responses carry an ETag (header and $reserved); mutations validate
//     If-Match and answer 412 on mismatch.
//   - $filter is evaluated by a small OData parser that understands the
//     expressions CAPX issues, including the entitiesAffected/any(...) lambda
//     and enum-typed literals such as Prism.Config.TaskStatus'RUNNING'.
//   - Powering a VM on allocates an address from the subnet's pool and reports
//     it as a learned IP on the NIC, which is what populates
//     NutanixMachine.status.addresses.
//
// Lifecycle hooks (Hooks) fire when a VM is created, powered on/off or deleted,
// which is where a scale harness plugs in its fake-node backend. Fault
// injection (Config.Faults) adds latency, periodic 429/500 responses and task
// failures so CAPX's error handling and requeue behaviour can be exercised.
package simulator
