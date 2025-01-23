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

package v1beta1

import (
	"testing"

	"k8s.io/utils/ptr"
)

func TestNutanixResourceIdentifier_String(t *testing.T) {
	type fields struct {
		Type NutanixIdentifierType
		UUID *string
		Name *string
	}
	tests := []struct {
		name   string
		fields fields
		want   string
	}{
		{
			name: "returns name, because type is Name",
			fields: fields{
				Type: NutanixIdentifierName,
				Name: ptr.To("name"),
				UUID: ptr.To("uuid"),
			},
			want: "name",
		},
		{
			name: "returns UUID, because type is  UUID",
			fields: fields{
				Type: NutanixIdentifierUUID,
				Name: ptr.To("name"),
				UUID: ptr.To("uuid"),
			},
			want: "uuid",
		},
		{
			name: "returns empty string, because type is undefined",
			fields: fields{
				Type: "",
				Name: ptr.To("name"),
				UUID: ptr.To("uuid"),
			},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nri := NutanixResourceIdentifier{
				Type: tt.fields.Type,
				UUID: tt.fields.UUID,
				Name: tt.fields.Name,
			}
			if got := nri.String(); got != tt.want {
				t.Errorf("NutanixResourceIdentifier.String() = %v, want %v", got, tt.want)
			}
		})
	}
}
