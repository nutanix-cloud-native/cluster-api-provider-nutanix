/*
Copyright 2023 Nutanix

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

package services

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	imageModels "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/content"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	nctx "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/context"
	converged "github.com/nutanix-cloud-native/prism-go-client/converged"
	v4Converged "github.com/nutanix-cloud-native/prism-go-client/converged/v4"

	"go.uber.org/mock/gomock"
	"k8s.io/utils/ptr"
)

func TestGetImageByNameOrUUID(t *testing.T) {
	tests := []struct {
		name          string
		clientBuilder func() *v4Converged.Client
		id            infrav1.NutanixResourceIdentifier
		project       *nctx.ProjectInfo
		pcVersion     string
		want          *imageModels.Image
		wantErr       bool
	}{
		{
			name: "missing name and UUID in the input",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)

				return convergedClient.Client
			},
			id:      infrav1.NutanixResourceIdentifier{},
			wantErr: true,
		},
		{
			name: "image UUID not found (classified ErrNotFound)",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().Get(gomock.Any(), gomock.Any()).
					Return(nil, &converged.APIError{Kind: converged.ErrNotFound, Cause: errors.New("entity not found")})

				return convergedClient.Client
			},
			id: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
			},
			wantErr: true,
		},
		{
			name: "image UUID not found (unclassified error)",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, errors.New("generic API error"))

				return convergedClient.Client
			},
			id: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
			},
			wantErr: true,
		},
		{
			name: "image name query fails",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, errors.New("fake error"))

				return convergedClient.Client
			},
			id: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("example"),
			},
			wantErr: true,
		},
		{
			name: "image UUID found",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().Get(gomock.Any(), gomock.Any()).Return(
					&imageModels.Image{
						ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
						Name:  ptr.To("example"),
					}, nil,
				)

				return convergedClient.Client
			},
			id: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
			},
			want: &imageModels.Image{
				ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
				Name:  ptr.To("example"),
			},
			wantErr: false,
		},
		{
			name: "image name found",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]imageModels.Image{
						{
							ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
							Name:  ptr.To("example"),
						},
					}, nil)

				return convergedClient.Client
			},
			id: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("example"),
			},
			want: &imageModels.Image{
				ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
				Name:  ptr.To("example"),
			},
		},
		{
			name: "image name matches multiple images",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]imageModels.Image{
						{
							ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
							Name:  ptr.To("example"),
						},
						{
							ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c6"),
							Name:  ptr.To("example"),
						},
						{
							ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c7"),
							Name:  ptr.To("example"),
						},
					}, nil)

				return convergedClient.Client
			},
			id: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("example"),
			},
			wantErr: true,
		},
		{
			name: "image name matches zero images",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]imageModels.Image{}, nil,
				)

				return convergedClient.Client
			},
			id: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("example"),
			},
			wantErr: true,
		},
		{
			name: "image name matches zero images",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]imageModels.Image{}, nil,
				)

				return convergedClient.Client
			},
			id: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("example"),
			},
			wantErr: true,
		},
		{
			name: "image name matches one image",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]imageModels.Image{
						{
							ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
							Name:  ptr.To("example"),
						},
					}, nil,
				)

				return convergedClient.Client
			},
			id: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierName,
				Name: ptr.To("example"),
			},
			want: &imageModels.Image{
				ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
				Name:  ptr.To("example"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Log("Running test case ", tt.name)
			ctx := context.Background()
			got, err := GetImage(ctx, tt.clientBuilder(), tt.id, tt.project, tt.pcVersion)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetImageByNameOrUUID() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetImageByNameOrUUID() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetImageByLookup(t *testing.T) {
	tests := []struct {
		name          string
		clientBuilder func() *v4Converged.Client
		baseOS        string
		imageTemplate string
		k8sVersion    string
		want          *imageModels.Image
		wantErr       bool
		errorMessage  string
	}{
		{
			name: "successful image lookup with v prefix",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]imageModels.Image{
						{
							ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
							Name:  ptr.To("capx-ubuntu-1.31.4"),
						},
					}, nil,
				)
				return convergedClient.Client
			},
			baseOS:        "ubuntu",
			imageTemplate: "capx-{{.BaseOS}}-{{.K8sVersion}}",
			k8sVersion:    "v1.31.4",
			want: &imageModels.Image{
				ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
				Name:  ptr.To("capx-ubuntu-1.31.4"),
			},
			wantErr: false,
		},
		{
			name: "successful image lookup without v prefix",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]imageModels.Image{
						{
							ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
							Name:  ptr.To("capx-ubuntu-1.31.4"),
						},
					}, nil,
				)
				return convergedClient.Client
			},
			baseOS:        "ubuntu",
			imageTemplate: "capx-{{.BaseOS}}-{{.K8sVersion}}",
			k8sVersion:    "1.31.4",
			want: &imageModels.Image{
				ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
				Name:  ptr.To("capx-ubuntu-1.31.4"),
			},
			wantErr: false,
		},
		{
			name: "successful image lookup with complex template",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]imageModels.Image{
						{
							ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
							Name:  ptr.To("k8s-ubuntu-20.04-1.31.4"),
						},
					}, nil,
				)
				return convergedClient.Client
			},
			baseOS:        "ubuntu-20.04",
			imageTemplate: "k8s-{{.BaseOS}}-{{.K8sVersion}}",
			k8sVersion:    "v1.31.4",
			want: &imageModels.Image{
				ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
				Name:  ptr.To("k8s-ubuntu-20.04-1.31.4"),
			},
			wantErr: false,
		},
		{
			name: "failed template parsing with invalid field",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				return convergedClient.Client
			},
			baseOS:        "ubuntu",
			imageTemplate: "invalid-template-{{.InvalidField}}",
			k8sVersion:    "v1.31.4",
			want:          nil,
			wantErr:       true,
			errorMessage:  "failed to substitute string",
		},
		{
			name: "failed template parsing with malformed template",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				return convergedClient.Client
			},
			baseOS:        "ubuntu",
			imageTemplate: "invalid-template-{{.BaseOS",
			k8sVersion:    "v1.31.4",
			want:          nil,
			wantErr:       true,
			errorMessage:  "failed to parse template",
		},
		{
			name: "failed template execution with invalid data",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				return convergedClient.Client
			},
			baseOS:        "ubuntu",
			imageTemplate: "{{.BaseOS}}-{{.K8sVersion}}-{{.NonExistentField}}",
			k8sVersion:    "v1.31.4",
			want:          nil,
			wantErr:       true,
			errorMessage:  "failed to substitute string",
		},
		{
			name: "client list images fails",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, errors.New("API error"))
				return convergedClient.Client
			},
			baseOS:        "ubuntu",
			imageTemplate: "capx-{{.BaseOS}}-{{.K8sVersion}}",
			k8sVersion:    "v1.31.4",
			want:          nil,
			wantErr:       true,
			errorMessage:  "API error",
		},
		{
			name: "no matching image found",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]imageModels.Image{}, nil,
				)
				return convergedClient.Client
			},
			baseOS:        "ubuntu",
			imageTemplate: "capx-{{.BaseOS}}-{{.K8sVersion}}",
			k8sVersion:    "v1.31.4",
			want:          nil,
			wantErr:       true,
			errorMessage:  "failed to find image with filter",
		},
		{
			name: "multiple images, return latest by creation time",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]imageModels.Image{
						{
							ExtId:      ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
							Name:       ptr.To("capx-ubuntu-1.31.4"),
							CreateTime: ptr.To(time.Date(2023, 10, 1, 0, 0, 0, 0, time.UTC)),
						},
						{
							ExtId:      ptr.To("32432daf-fb0e-4202-b444-2439f43a24c6"),
							Name:       ptr.To("capx-ubuntu-1.31.4"),
							CreateTime: ptr.To(time.Date(2023, 10, 2, 0, 0, 0, 0, time.UTC)),
						},
						{
							ExtId:      ptr.To("32432daf-fb0e-4202-b444-2439f43a24c7"),
							Name:       ptr.To("capx-ubuntu-1.31.4"),
							CreateTime: ptr.To(time.Date(2023, 10, 3, 0, 0, 0, 0, time.UTC)),
						},
					}, nil,
				)
				return convergedClient.Client
			},
			baseOS:        "ubuntu",
			imageTemplate: "capx-{{.BaseOS}}-{{.K8sVersion}}",
			k8sVersion:    "v1.31.4",
			want: &imageModels.Image{
				ExtId:      ptr.To("32432daf-fb0e-4202-b444-2439f43a24c7"),
				Name:       ptr.To("capx-ubuntu-1.31.4"),
				CreateTime: ptr.To(time.Date(2023, 10, 3, 0, 0, 0, 0, time.UTC)),
			},
			wantErr: false,
		},
		{
			name: "multiple images with nil creation times, prioritize non-nil",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]imageModels.Image{
						{
							ExtId:      ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
							Name:       ptr.To("capx-ubuntu-1.31.4"),
							CreateTime: nil,
						},
						{
							ExtId:      ptr.To("32432daf-fb0e-4202-b444-2439f43a24c6"),
							Name:       ptr.To("capx-ubuntu-1.31.4"),
							CreateTime: ptr.To(time.Date(2023, 10, 2, 0, 0, 0, 0, time.UTC)),
						},
						{
							ExtId:      ptr.To("32432daf-fb0e-4202-b444-2439f43a24c7"),
							Name:       ptr.To("capx-ubuntu-1.31.4"),
							CreateTime: nil,
						},
					}, nil,
				)
				return convergedClient.Client
			},
			baseOS:        "ubuntu",
			imageTemplate: "capx-{{.BaseOS}}-{{.K8sVersion}}",
			k8sVersion:    "v1.31.4",
			want: &imageModels.Image{
				ExtId:      ptr.To("32432daf-fb0e-4202-b444-2439f43a24c6"),
				Name:       ptr.To("capx-ubuntu-1.31.4"),
				CreateTime: ptr.To(time.Date(2023, 10, 2, 0, 0, 0, 0, time.UTC)),
			},
			wantErr: false,
		},
		{
			name: "multiple images with all nil creation times, return first",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]imageModels.Image{
						{
							ExtId:      ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
							Name:       ptr.To("capx-ubuntu-1.31.4"),
							CreateTime: nil,
						},
						{
							ExtId:      ptr.To("32432daf-fb0e-4202-b444-2439f43a24c6"),
							Name:       ptr.To("capx-ubuntu-1.31.4"),
							CreateTime: nil,
						},
					}, nil,
				)
				return convergedClient.Client
			},
			baseOS:        "ubuntu",
			imageTemplate: "capx-{{.BaseOS}}-{{.K8sVersion}}",
			k8sVersion:    "v1.31.4",
			want: &imageModels.Image{
				ExtId:      ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
				Name:       ptr.To("capx-ubuntu-1.31.4"),
				CreateTime: nil,
			},
			wantErr: false,
		},
		{
			name: "k8s version with multiple v prefixes",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]imageModels.Image{
						{
							ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
							Name:  ptr.To("capx-ubuntu-v1.31.4"),
						},
					}, nil,
				)
				return convergedClient.Client
			},
			baseOS:        "ubuntu",
			imageTemplate: "capx-{{.BaseOS}}-{{.K8sVersion}}",
			k8sVersion:    "vv1.31.4",
			want: &imageModels.Image{
				ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
				Name:  ptr.To("capx-ubuntu-v1.31.4"),
			},
			wantErr: false,
		},
		{
			name: "k8s version with v in the middle",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]imageModels.Image{
						{
							ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
							Name:  ptr.To("capx-ubuntu-1.31.4"),
						},
					}, nil,
				)
				return convergedClient.Client
			},
			baseOS:        "ubuntu",
			imageTemplate: "capx-{{.BaseOS}}-{{.K8sVersion}}",
			k8sVersion:    "1.v31.4",
			want: &imageModels.Image{
				ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
				Name:  ptr.To("capx-ubuntu-1.31.4"),
			},
			wantErr: false,
		},
		{
			name: "template with special characters",
			clientBuilder: func() *v4Converged.Client {
				mockctrl := gomock.NewController(t)
				convergedClient := NewMockConvergedClient(mockctrl)
				convergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]imageModels.Image{
						{
							ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
							Name:  ptr.To("k8s-ubuntu-20.04-1.31.4"),
						},
					}, nil,
				)
				return convergedClient.Client
			},
			baseOS:        "ubuntu-20.04",
			imageTemplate: "k8s-{{.BaseOS}}-{{.K8sVersion}}",
			k8sVersion:    "v1.31.4",
			want: &imageModels.Image{
				ExtId: ptr.To("32432daf-fb0e-4202-b444-2439f43a24c5"),
				Name:  ptr.To("k8s-ubuntu-20.04-1.31.4"),
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Log("Running test case ", tt.name)
			ctx := context.Background()
			got, err := GetImageByLookup(
				ctx,
				tt.clientBuilder(),
				&tt.imageTemplate,
				&tt.baseOS,
				&tt.k8sVersion,
				nil,
				"",
			)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetImageByLookup() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetImageByLookup() = %v, want %v", got, tt.want)
			}
			if tt.errorMessage != "" && err != nil {
				if !strings.Contains(err.Error(), tt.errorMessage) {
					t.Errorf("GetImageByLookup() error message = %v, want to contain %v", err.Error(), tt.errorMessage)
				}
			}
		})
	}
}
