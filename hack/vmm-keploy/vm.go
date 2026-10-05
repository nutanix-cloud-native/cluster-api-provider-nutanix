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

package main

import "os"

func baseVM(name string) map[string]any {
	return map[string]any{
		"name":                  name,
		"memorySizeBytes":       4294967296,
		"numSockets":            2,
		"numCoresPerSocket":     2,
		"hardwareClockTimezone": "UTC",
		"cluster":               map[string]any{"extId": os.Getenv("PE_UUID")},
		"nics": []any{map[string]any{
			"nicNetworkInfo": map[string]any{
				"$objectType": "vmm.v4.ahv.config.VirtualEthernetNicNetworkInfo",
				"subnet":      map[string]any{"extId": os.Getenv("SUBNET_UUID")},
			},
		}},
		"disks": []any{map[string]any{
			"backingInfo": map[string]any{
				"$objectType":   "vmm.v4.ahv.config.VmDisk",
				"diskSizeBytes": 21474836480,
				"dataSource": map[string]any{
					"reference": map[string]any{
						"$objectType": "vmm.v4.ahv.config.ImageReference",
						"imageExtId":  os.Getenv("IMAGE_UUID"),
					},
				},
			},
		}},
	}
}

func requirePlacement() error {
	if os.Getenv("PE_UUID") == "" || os.Getenv("SUBNET_UUID") == "" || os.Getenv("IMAGE_UUID") == "" {
		return errMissingPlacement
	}
	return nil
}

var errMissingPlacement = fmtError("set PE_UUID, SUBNET_UUID, and IMAGE_UUID (postman discover, or the environment)")

type fmtError string

func (e fmtError) Error() string { return string(e) }
