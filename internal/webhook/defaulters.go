/*
Copyright 2022 Nutanix

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

package webhook

import (
	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

// DefaultNamePlaceholder is the value set by the mutating webhook when
// NutanixResourceIdentifier has type "name" but name is missing or empty.
const DefaultNamePlaceholder = "placeholder-image"

// defaultNutanixResourceIdentifier sets name to placeholder when type is "name"
// and name is nil or empty.
func defaultNutanixResourceIdentifier(nri *infrav1.NutanixResourceIdentifier) {
	if nri == nil {
		return
	}
	if nri.Type != infrav1.NutanixIdentifierName {
		return
	}
	if nri.Name == nil || *nri.Name == "" {
		placeholder := DefaultNamePlaceholder
		nri.Name = &placeholder
	}
}

// defaultNutanixResourceIdentifierValue defaults a value-type NutanixResourceIdentifier.
func defaultNutanixResourceIdentifierValue(nri *infrav1.NutanixResourceIdentifier) {
	if nri == nil {
		return
	}
	defaultNutanixResourceIdentifier(nri)
}

// defaultNutanixMachineSpec walks all NutanixResourceIdentifier fields in spec
// and sets name to placeholder when type is "name" and name is empty.
func defaultNutanixMachineSpec(spec *infrav1.NutanixMachineSpec) {
	if spec == nil {
		return
	}
	// image
	defaultNutanixResourceIdentifier(spec.Image)
	// cluster (value type)
	defaultNutanixResourceIdentifierValue(&spec.Cluster)
	// subnets
	for i := range spec.Subnets {
		defaultNutanixResourceIdentifierValue(&spec.Subnets[i])
	}
	// project
	defaultNutanixResourceIdentifier(spec.Project)
	// dataDisks: dataSource and storageConfig.storageContainer
	for i := range spec.DataDisks {
		defaultNutanixResourceIdentifier(spec.DataDisks[i].DataSource)
		if spec.DataDisks[i].StorageConfig != nil {
			defaultNutanixResourceIdentifier(spec.DataDisks[i].StorageConfig.StorageContainer)
		}
	}
}
