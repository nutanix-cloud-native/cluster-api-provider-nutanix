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

package simulator

import (
	"fmt"
	"os"
	"time"

	"sigs.k8s.io/yaml"
)

// Config is the complete configuration of a simulator instance.
type Config struct {
	// Auth holds the credentials the simulator accepts. When Username is empty
	// every request is accepted regardless of credentials.
	Auth Auth `json:"auth,omitempty"`
	// Timing controls how long asynchronous tasks take to complete.
	Timing Timing `json:"timing,omitempty"`
	// Faults enables fault injection.
	Faults Faults `json:"faults,omitempty"`
	// Limits caps what the simulated Prism Central will hold.
	Limits Limits `json:"limits,omitempty"`
	// Seed describes the infrastructure entities that exist at start-up.
	Seed Seed `json:"seed,omitempty"`
	// APIVersion is the v4 API version reported to SDK version negotiation.
	// Defaults to "v4.2".
	APIVersion string `json:"apiVersion,omitempty"`
}

// Auth is the credential set the simulator validates requests against.
type Auth struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	// APIKey, when set, is accepted in the X-ntnx-api-key header as an
	// alternative to basic auth.
	APIKey string `json:"apiKey,omitempty"`
}

// Timing controls task durations. Zero means the task completes immediately
// (the SDK still sleeps one second before its first poll).
type Timing struct {
	VMCreate  time.Duration `json:"vmCreate,omitempty"`
	VMPowerOn time.Duration `json:"vmPowerOn,omitempty"`
	VMDelete  time.Duration `json:"vmDelete,omitempty"`
	VMUpdate  time.Duration `json:"vmUpdate,omitempty"`
}

// Faults configures fault injection. All counters are "every Nth request";
// zero disables the fault.
type Faults struct {
	// Latency is added to every request before it is handled.
	Latency time.Duration `json:"latency,omitempty"`
	// RateLimitEvery makes every Nth API request answer 429 with errorGroup
	// RATE_LIMIT_EXCEEDED. Note the v4 SDK retries 429 up to five times with
	// a three second interval before the error reaches CAPX.
	RateLimitEvery int `json:"rateLimitEvery,omitempty"`
	// InternalErrorEvery makes every Nth API request answer 500. The SDK does
	// not retry 500, so the error reaches CAPX immediately.
	InternalErrorEvery int `json:"internalErrorEvery,omitempty"`
	// TaskFailureEvery makes every Nth task finish FAILED instead of SUCCEEDED.
	TaskFailureEvery int `json:"taskFailureEvery,omitempty"`
}

// Limits caps the simulated Prism Central.
type Limits struct {
	// MaxVMs rejects VM creation with a 400 once this many VMs exist. Zero
	// means unlimited.
	MaxVMs int `json:"maxVMs,omitempty"`
}

// Seed is the set of pre-existing entities.
type Seed struct {
	Clusters          []ClusterSeed          `json:"clusters,omitempty"`
	Subnets           []SubnetSeed           `json:"subnets,omitempty"`
	Images            []ImageSeed            `json:"images,omitempty"`
	StorageContainers []StorageContainerSeed `json:"storageContainers,omitempty"`
	Categories        []CategorySeed         `json:"categories,omitempty"`
}

// ClusterSeed describes a Prism Element cluster.
type ClusterSeed struct {
	Name string `json:"name"`
	// UUID is generated when empty.
	UUID string `json:"uuid,omitempty"`
}

// SubnetSeed describes a VLAN subnet attached to a cluster.
type SubnetSeed struct {
	Name string `json:"name"`
	UUID string `json:"uuid,omitempty"`
	// Cluster is the name or UUID of the owning cluster. Defaults to the
	// first seeded cluster.
	Cluster string `json:"cluster,omitempty"`
	// CIDR is the subnet range; VM addresses are allocated from it.
	CIDR string `json:"cidr"`
	// Start and End optionally narrow the allocation range within CIDR.
	Start string `json:"start,omitempty"`
	End   string `json:"end,omitempty"`
	// VlanID is reported as the subnet's VLAN.
	VlanID int `json:"vlanId,omitempty"`
}

// ImageSeed describes a disk image.
type ImageSeed struct {
	Name      string `json:"name"`
	UUID      string `json:"uuid,omitempty"`
	SizeBytes int64  `json:"sizeBytes,omitempty"`
}

// StorageContainerSeed describes a storage container on a cluster.
type StorageContainerSeed struct {
	Name    string `json:"name"`
	UUID    string `json:"uuid,omitempty"`
	Cluster string `json:"cluster,omitempty"`
}

// CategorySeed describes a pre-existing category key/value.
type CategorySeed struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// DefaultConfig returns a configuration with one cluster, one subnet, one
// image and one storage container, which is enough for the default CAPX
// cluster templates.
func DefaultConfig() Config {
	return Config{
		APIVersion: "v4.2",
		Seed: Seed{
			Clusters:          []ClusterSeed{{Name: "pe-sim"}},
			Subnets:           []SubnetSeed{{Name: "subnet-sim", CIDR: "10.10.0.0/16", VlanID: 0}},
			Images:            []ImageSeed{{Name: "ubuntu-sim", SizeBytes: 2 * 1024 * 1024 * 1024}},
			StorageContainers: []StorageContainerSeed{{Name: "default-sim"}},
		},
	}
}

// LoadConfig reads a YAML (or JSON) configuration file on top of
// DefaultConfig. A seed section in the file replaces the default seed
// entirely.
func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	if path == "" {
		return cfg, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("reading config %s: %w", path, err)
	}
	var fileCfg Config
	if err := yaml.UnmarshalStrict(raw, &fileCfg); err != nil {
		return Config{}, fmt.Errorf("parsing config %s: %w", path, err)
	}
	if fileCfg.APIVersion != "" {
		cfg.APIVersion = fileCfg.APIVersion
	}
	cfg.Auth = fileCfg.Auth
	cfg.Timing = fileCfg.Timing
	cfg.Faults = fileCfg.Faults
	cfg.Limits = fileCfg.Limits
	if !seedIsEmpty(fileCfg.Seed) {
		cfg.Seed = fileCfg.Seed
	}
	return cfg, nil
}

func seedIsEmpty(s Seed) bool {
	return len(s.Clusters) == 0 && len(s.Subnets) == 0 && len(s.Images) == 0 &&
		len(s.StorageContainers) == 0 && len(s.Categories) == 0
}
