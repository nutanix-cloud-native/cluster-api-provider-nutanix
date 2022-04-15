//go:build !ignore_autogenerated_core
// +build !ignore_autogenerated_core

/*
Copyright 2021.

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
// Code generated by conversion-gen. DO NOT EDIT.

package v1alpha4

import (
	unsafe "unsafe"

	v1beta1 "github.com/nutanix-core/cluster-api-provider-nutanix/api/v1beta1"
	v1 "k8s.io/api/core/v1"
	conversion "k8s.io/apimachinery/pkg/conversion"
	runtime "k8s.io/apimachinery/pkg/runtime"
	apiv1alpha4 "sigs.k8s.io/cluster-api/api/v1alpha4"
	apiv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

func init() {
	localSchemeBuilder.Register(RegisterConversions)
}

// RegisterConversions adds conversion functions to the given scheme.
// Public to allow building arbitrary schemes.
func RegisterConversions(s *runtime.Scheme) error {
	if err := s.AddGeneratedConversionFunc((*NutanixCategoryIdentifier)(nil), (*v1beta1.NutanixCategoryIdentifier)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_v1alpha4_NutanixCategoryIdentifier_To_v1beta1_NutanixCategoryIdentifier(a.(*NutanixCategoryIdentifier), b.(*v1beta1.NutanixCategoryIdentifier), scope)
	}); err != nil {
		return err
	}
	if err := s.AddGeneratedConversionFunc((*v1beta1.NutanixCategoryIdentifier)(nil), (*NutanixCategoryIdentifier)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_v1beta1_NutanixCategoryIdentifier_To_v1alpha4_NutanixCategoryIdentifier(a.(*v1beta1.NutanixCategoryIdentifier), b.(*NutanixCategoryIdentifier), scope)
	}); err != nil {
		return err
	}
	if err := s.AddGeneratedConversionFunc((*NutanixCluster)(nil), (*v1beta1.NutanixCluster)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_v1alpha4_NutanixCluster_To_v1beta1_NutanixCluster(a.(*NutanixCluster), b.(*v1beta1.NutanixCluster), scope)
	}); err != nil {
		return err
	}
	if err := s.AddGeneratedConversionFunc((*v1beta1.NutanixCluster)(nil), (*NutanixCluster)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_v1beta1_NutanixCluster_To_v1alpha4_NutanixCluster(a.(*v1beta1.NutanixCluster), b.(*NutanixCluster), scope)
	}); err != nil {
		return err
	}
	if err := s.AddGeneratedConversionFunc((*NutanixClusterList)(nil), (*v1beta1.NutanixClusterList)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_v1alpha4_NutanixClusterList_To_v1beta1_NutanixClusterList(a.(*NutanixClusterList), b.(*v1beta1.NutanixClusterList), scope)
	}); err != nil {
		return err
	}
	if err := s.AddGeneratedConversionFunc((*v1beta1.NutanixClusterList)(nil), (*NutanixClusterList)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_v1beta1_NutanixClusterList_To_v1alpha4_NutanixClusterList(a.(*v1beta1.NutanixClusterList), b.(*NutanixClusterList), scope)
	}); err != nil {
		return err
	}
	if err := s.AddGeneratedConversionFunc((*v1beta1.NutanixClusterStatus)(nil), (*NutanixClusterStatus)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_v1beta1_NutanixClusterStatus_To_v1alpha4_NutanixClusterStatus(a.(*v1beta1.NutanixClusterStatus), b.(*NutanixClusterStatus), scope)
	}); err != nil {
		return err
	}
	if err := s.AddGeneratedConversionFunc((*NutanixMachine)(nil), (*v1beta1.NutanixMachine)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_v1alpha4_NutanixMachine_To_v1beta1_NutanixMachine(a.(*NutanixMachine), b.(*v1beta1.NutanixMachine), scope)
	}); err != nil {
		return err
	}
	if err := s.AddGeneratedConversionFunc((*v1beta1.NutanixMachine)(nil), (*NutanixMachine)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_v1beta1_NutanixMachine_To_v1alpha4_NutanixMachine(a.(*v1beta1.NutanixMachine), b.(*NutanixMachine), scope)
	}); err != nil {
		return err
	}
	if err := s.AddGeneratedConversionFunc((*NutanixMachineList)(nil), (*v1beta1.NutanixMachineList)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_v1alpha4_NutanixMachineList_To_v1beta1_NutanixMachineList(a.(*NutanixMachineList), b.(*v1beta1.NutanixMachineList), scope)
	}); err != nil {
		return err
	}
	if err := s.AddGeneratedConversionFunc((*v1beta1.NutanixMachineList)(nil), (*NutanixMachineList)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_v1beta1_NutanixMachineList_To_v1alpha4_NutanixMachineList(a.(*v1beta1.NutanixMachineList), b.(*NutanixMachineList), scope)
	}); err != nil {
		return err
	}
	if err := s.AddGeneratedConversionFunc((*v1beta1.NutanixMachineStatus)(nil), (*NutanixMachineStatus)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_v1beta1_NutanixMachineStatus_To_v1alpha4_NutanixMachineStatus(a.(*v1beta1.NutanixMachineStatus), b.(*NutanixMachineStatus), scope)
	}); err != nil {
		return err
	}
	if err := s.AddGeneratedConversionFunc((*NutanixMachineTemplate)(nil), (*v1beta1.NutanixMachineTemplate)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_v1alpha4_NutanixMachineTemplate_To_v1beta1_NutanixMachineTemplate(a.(*NutanixMachineTemplate), b.(*v1beta1.NutanixMachineTemplate), scope)
	}); err != nil {
		return err
	}
	if err := s.AddGeneratedConversionFunc((*v1beta1.NutanixMachineTemplate)(nil), (*NutanixMachineTemplate)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_v1beta1_NutanixMachineTemplate_To_v1alpha4_NutanixMachineTemplate(a.(*v1beta1.NutanixMachineTemplate), b.(*NutanixMachineTemplate), scope)
	}); err != nil {
		return err
	}
	if err := s.AddGeneratedConversionFunc((*NutanixMachineTemplateList)(nil), (*v1beta1.NutanixMachineTemplateList)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_v1alpha4_NutanixMachineTemplateList_To_v1beta1_NutanixMachineTemplateList(a.(*NutanixMachineTemplateList), b.(*v1beta1.NutanixMachineTemplateList), scope)
	}); err != nil {
		return err
	}
	if err := s.AddGeneratedConversionFunc((*v1beta1.NutanixMachineTemplateList)(nil), (*NutanixMachineTemplateList)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_v1beta1_NutanixMachineTemplateList_To_v1alpha4_NutanixMachineTemplateList(a.(*v1beta1.NutanixMachineTemplateList), b.(*NutanixMachineTemplateList), scope)
	}); err != nil {
		return err
	}
	if err := s.AddGeneratedConversionFunc((*NutanixMachineTemplateSpec)(nil), (*v1beta1.NutanixMachineTemplateSpec)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_v1alpha4_NutanixMachineTemplateSpec_To_v1beta1_NutanixMachineTemplateSpec(a.(*NutanixMachineTemplateSpec), b.(*v1beta1.NutanixMachineTemplateSpec), scope)
	}); err != nil {
		return err
	}
	if err := s.AddGeneratedConversionFunc((*v1beta1.NutanixMachineTemplateSpec)(nil), (*NutanixMachineTemplateSpec)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_v1beta1_NutanixMachineTemplateSpec_To_v1alpha4_NutanixMachineTemplateSpec(a.(*v1beta1.NutanixMachineTemplateSpec), b.(*NutanixMachineTemplateSpec), scope)
	}); err != nil {
		return err
	}
	if err := s.AddGeneratedConversionFunc((*NutanixResourceIdentifier)(nil), (*v1beta1.NutanixResourceIdentifier)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_v1alpha4_NutanixResourceIdentifier_To_v1beta1_NutanixResourceIdentifier(a.(*NutanixResourceIdentifier), b.(*v1beta1.NutanixResourceIdentifier), scope)
	}); err != nil {
		return err
	}
	if err := s.AddGeneratedConversionFunc((*v1beta1.NutanixResourceIdentifier)(nil), (*NutanixResourceIdentifier)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_v1beta1_NutanixResourceIdentifier_To_v1alpha4_NutanixResourceIdentifier(a.(*v1beta1.NutanixResourceIdentifier), b.(*NutanixResourceIdentifier), scope)
	}); err != nil {
		return err
	}
	if err := s.AddConversionFunc((*apiv1alpha4.APIEndpoint)(nil), (*apiv1beta1.APIEndpoint)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_v1alpha4_APIEndpoint_To_v1beta1_APIEndpoint(a.(*apiv1alpha4.APIEndpoint), b.(*apiv1beta1.APIEndpoint), scope)
	}); err != nil {
		return err
	}
	if err := s.AddConversionFunc((*NutanixClusterSpec)(nil), (*v1beta1.NutanixClusterSpec)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_v1alpha4_NutanixClusterSpec_To_v1beta1_NutanixClusterSpec(a.(*NutanixClusterSpec), b.(*v1beta1.NutanixClusterSpec), scope)
	}); err != nil {
		return err
	}
	if err := s.AddConversionFunc((*NutanixClusterStatus)(nil), (*v1beta1.NutanixClusterStatus)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_v1alpha4_NutanixClusterStatus_To_v1beta1_NutanixClusterStatus(a.(*NutanixClusterStatus), b.(*v1beta1.NutanixClusterStatus), scope)
	}); err != nil {
		return err
	}
	if err := s.AddConversionFunc((*NutanixMachineSpec)(nil), (*v1beta1.NutanixMachineSpec)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_v1alpha4_NutanixMachineSpec_To_v1beta1_NutanixMachineSpec(a.(*NutanixMachineSpec), b.(*v1beta1.NutanixMachineSpec), scope)
	}); err != nil {
		return err
	}
	if err := s.AddConversionFunc((*NutanixMachineStatus)(nil), (*v1beta1.NutanixMachineStatus)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_v1alpha4_NutanixMachineStatus_To_v1beta1_NutanixMachineStatus(a.(*NutanixMachineStatus), b.(*v1beta1.NutanixMachineStatus), scope)
	}); err != nil {
		return err
	}
	if err := s.AddConversionFunc((*NutanixMachineTemplateResource)(nil), (*v1beta1.NutanixMachineTemplateResource)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_v1alpha4_NutanixMachineTemplateResource_To_v1beta1_NutanixMachineTemplateResource(a.(*NutanixMachineTemplateResource), b.(*v1beta1.NutanixMachineTemplateResource), scope)
	}); err != nil {
		return err
	}
	if err := s.AddConversionFunc((*apiv1alpha4.ObjectMeta)(nil), (*apiv1beta1.ObjectMeta)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_v1alpha4_ObjectMeta_To_v1beta1_ObjectMeta(a.(*apiv1alpha4.ObjectMeta), b.(*apiv1beta1.ObjectMeta), scope)
	}); err != nil {
		return err
	}
	if err := s.AddConversionFunc((*apiv1beta1.APIEndpoint)(nil), (*apiv1alpha4.APIEndpoint)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_v1beta1_APIEndpoint_To_v1alpha4_APIEndpoint(a.(*apiv1beta1.APIEndpoint), b.(*apiv1alpha4.APIEndpoint), scope)
	}); err != nil {
		return err
	}
	if err := s.AddConversionFunc((*v1beta1.NutanixClusterSpec)(nil), (*NutanixClusterSpec)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_v1beta1_NutanixClusterSpec_To_v1alpha4_NutanixClusterSpec(a.(*v1beta1.NutanixClusterSpec), b.(*NutanixClusterSpec), scope)
	}); err != nil {
		return err
	}
	if err := s.AddConversionFunc((*v1beta1.NutanixMachineSpec)(nil), (*NutanixMachineSpec)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_v1beta1_NutanixMachineSpec_To_v1alpha4_NutanixMachineSpec(a.(*v1beta1.NutanixMachineSpec), b.(*NutanixMachineSpec), scope)
	}); err != nil {
		return err
	}
	if err := s.AddConversionFunc((*v1beta1.NutanixMachineTemplateResource)(nil), (*NutanixMachineTemplateResource)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_v1beta1_NutanixMachineTemplateResource_To_v1alpha4_NutanixMachineTemplateResource(a.(*v1beta1.NutanixMachineTemplateResource), b.(*NutanixMachineTemplateResource), scope)
	}); err != nil {
		return err
	}
	if err := s.AddConversionFunc((*apiv1beta1.ObjectMeta)(nil), (*apiv1alpha4.ObjectMeta)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_v1beta1_ObjectMeta_To_v1alpha4_ObjectMeta(a.(*apiv1beta1.ObjectMeta), b.(*apiv1alpha4.ObjectMeta), scope)
	}); err != nil {
		return err
	}
	return nil
}

func autoConvert_v1alpha4_NutanixCategoryIdentifier_To_v1beta1_NutanixCategoryIdentifier(in *NutanixCategoryIdentifier, out *v1beta1.NutanixCategoryIdentifier, s conversion.Scope) error {
	out.Key = in.Key
	out.Value = in.Value
	return nil
}

// Convert_v1alpha4_NutanixCategoryIdentifier_To_v1beta1_NutanixCategoryIdentifier is an autogenerated conversion function.
func Convert_v1alpha4_NutanixCategoryIdentifier_To_v1beta1_NutanixCategoryIdentifier(in *NutanixCategoryIdentifier, out *v1beta1.NutanixCategoryIdentifier, s conversion.Scope) error {
	return autoConvert_v1alpha4_NutanixCategoryIdentifier_To_v1beta1_NutanixCategoryIdentifier(in, out, s)
}

func autoConvert_v1beta1_NutanixCategoryIdentifier_To_v1alpha4_NutanixCategoryIdentifier(in *v1beta1.NutanixCategoryIdentifier, out *NutanixCategoryIdentifier, s conversion.Scope) error {
	out.Key = in.Key
	out.Value = in.Value
	return nil
}

// Convert_v1beta1_NutanixCategoryIdentifier_To_v1alpha4_NutanixCategoryIdentifier is an autogenerated conversion function.
func Convert_v1beta1_NutanixCategoryIdentifier_To_v1alpha4_NutanixCategoryIdentifier(in *v1beta1.NutanixCategoryIdentifier, out *NutanixCategoryIdentifier, s conversion.Scope) error {
	return autoConvert_v1beta1_NutanixCategoryIdentifier_To_v1alpha4_NutanixCategoryIdentifier(in, out, s)
}

func autoConvert_v1alpha4_NutanixCluster_To_v1beta1_NutanixCluster(in *NutanixCluster, out *v1beta1.NutanixCluster, s conversion.Scope) error {
	out.ObjectMeta = in.ObjectMeta
	if err := Convert_v1alpha4_NutanixClusterSpec_To_v1beta1_NutanixClusterSpec(&in.Spec, &out.Spec, s); err != nil {
		return err
	}
	if err := Convert_v1alpha4_NutanixClusterStatus_To_v1beta1_NutanixClusterStatus(&in.Status, &out.Status, s); err != nil {
		return err
	}
	return nil
}

// Convert_v1alpha4_NutanixCluster_To_v1beta1_NutanixCluster is an autogenerated conversion function.
func Convert_v1alpha4_NutanixCluster_To_v1beta1_NutanixCluster(in *NutanixCluster, out *v1beta1.NutanixCluster, s conversion.Scope) error {
	return autoConvert_v1alpha4_NutanixCluster_To_v1beta1_NutanixCluster(in, out, s)
}

func autoConvert_v1beta1_NutanixCluster_To_v1alpha4_NutanixCluster(in *v1beta1.NutanixCluster, out *NutanixCluster, s conversion.Scope) error {
	out.ObjectMeta = in.ObjectMeta
	if err := Convert_v1beta1_NutanixClusterSpec_To_v1alpha4_NutanixClusterSpec(&in.Spec, &out.Spec, s); err != nil {
		return err
	}
	if err := Convert_v1beta1_NutanixClusterStatus_To_v1alpha4_NutanixClusterStatus(&in.Status, &out.Status, s); err != nil {
		return err
	}
	return nil
}

// Convert_v1beta1_NutanixCluster_To_v1alpha4_NutanixCluster is an autogenerated conversion function.
func Convert_v1beta1_NutanixCluster_To_v1alpha4_NutanixCluster(in *v1beta1.NutanixCluster, out *NutanixCluster, s conversion.Scope) error {
	return autoConvert_v1beta1_NutanixCluster_To_v1alpha4_NutanixCluster(in, out, s)
}

func autoConvert_v1alpha4_NutanixClusterList_To_v1beta1_NutanixClusterList(in *NutanixClusterList, out *v1beta1.NutanixClusterList, s conversion.Scope) error {
	out.ListMeta = in.ListMeta
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]v1beta1.NutanixCluster, len(*in))
		for i := range *in {
			if err := Convert_v1alpha4_NutanixCluster_To_v1beta1_NutanixCluster(&(*in)[i], &(*out)[i], s); err != nil {
				return err
			}
		}
	} else {
		out.Items = nil
	}
	return nil
}

// Convert_v1alpha4_NutanixClusterList_To_v1beta1_NutanixClusterList is an autogenerated conversion function.
func Convert_v1alpha4_NutanixClusterList_To_v1beta1_NutanixClusterList(in *NutanixClusterList, out *v1beta1.NutanixClusterList, s conversion.Scope) error {
	return autoConvert_v1alpha4_NutanixClusterList_To_v1beta1_NutanixClusterList(in, out, s)
}

func autoConvert_v1beta1_NutanixClusterList_To_v1alpha4_NutanixClusterList(in *v1beta1.NutanixClusterList, out *NutanixClusterList, s conversion.Scope) error {
	out.ListMeta = in.ListMeta
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]NutanixCluster, len(*in))
		for i := range *in {
			if err := Convert_v1beta1_NutanixCluster_To_v1alpha4_NutanixCluster(&(*in)[i], &(*out)[i], s); err != nil {
				return err
			}
		}
	} else {
		out.Items = nil
	}
	return nil
}

// Convert_v1beta1_NutanixClusterList_To_v1alpha4_NutanixClusterList is an autogenerated conversion function.
func Convert_v1beta1_NutanixClusterList_To_v1alpha4_NutanixClusterList(in *v1beta1.NutanixClusterList, out *NutanixClusterList, s conversion.Scope) error {
	return autoConvert_v1beta1_NutanixClusterList_To_v1alpha4_NutanixClusterList(in, out, s)
}

func autoConvert_v1alpha4_NutanixClusterSpec_To_v1beta1_NutanixClusterSpec(in *NutanixClusterSpec, out *v1beta1.NutanixClusterSpec, s conversion.Scope) error {
	if err := Convert_v1alpha4_APIEndpoint_To_v1beta1_APIEndpoint(&in.ControlPlaneEndpoint, &out.ControlPlaneEndpoint, s); err != nil {
		return err
	}
	return nil
}

func autoConvert_v1beta1_NutanixClusterSpec_To_v1alpha4_NutanixClusterSpec(in *v1beta1.NutanixClusterSpec, out *NutanixClusterSpec, s conversion.Scope) error {
	if err := Convert_v1beta1_APIEndpoint_To_v1alpha4_APIEndpoint(&in.ControlPlaneEndpoint, &out.ControlPlaneEndpoint, s); err != nil {
		return err
	}
	return nil
}

func autoConvert_v1alpha4_NutanixClusterStatus_To_v1beta1_NutanixClusterStatus(in *NutanixClusterStatus, out *v1beta1.NutanixClusterStatus, s conversion.Scope) error {
	out.Ready = in.Ready
	out.FailureDomains = *(*apiv1beta1.FailureDomains)(unsafe.Pointer(&in.FailureDomains))
	out.Conditions = *(*apiv1beta1.Conditions)(unsafe.Pointer(&in.Conditions))
	return nil
}

func autoConvert_v1beta1_NutanixClusterStatus_To_v1alpha4_NutanixClusterStatus(in *v1beta1.NutanixClusterStatus, out *NutanixClusterStatus, s conversion.Scope) error {
	out.Ready = in.Ready
	out.FailureDomains = *(*apiv1alpha4.FailureDomains)(unsafe.Pointer(&in.FailureDomains))
	out.Conditions = *(*apiv1alpha4.Conditions)(unsafe.Pointer(&in.Conditions))
	return nil
}

// Convert_v1beta1_NutanixClusterStatus_To_v1alpha4_NutanixClusterStatus is an autogenerated conversion function.
func Convert_v1beta1_NutanixClusterStatus_To_v1alpha4_NutanixClusterStatus(in *v1beta1.NutanixClusterStatus, out *NutanixClusterStatus, s conversion.Scope) error {
	return autoConvert_v1beta1_NutanixClusterStatus_To_v1alpha4_NutanixClusterStatus(in, out, s)
}

func autoConvert_v1alpha4_NutanixMachine_To_v1beta1_NutanixMachine(in *NutanixMachine, out *v1beta1.NutanixMachine, s conversion.Scope) error {
	out.ObjectMeta = in.ObjectMeta
	if err := Convert_v1alpha4_NutanixMachineSpec_To_v1beta1_NutanixMachineSpec(&in.Spec, &out.Spec, s); err != nil {
		return err
	}
	if err := Convert_v1alpha4_NutanixMachineStatus_To_v1beta1_NutanixMachineStatus(&in.Status, &out.Status, s); err != nil {
		return err
	}
	return nil
}

// Convert_v1alpha4_NutanixMachine_To_v1beta1_NutanixMachine is an autogenerated conversion function.
func Convert_v1alpha4_NutanixMachine_To_v1beta1_NutanixMachine(in *NutanixMachine, out *v1beta1.NutanixMachine, s conversion.Scope) error {
	return autoConvert_v1alpha4_NutanixMachine_To_v1beta1_NutanixMachine(in, out, s)
}

func autoConvert_v1beta1_NutanixMachine_To_v1alpha4_NutanixMachine(in *v1beta1.NutanixMachine, out *NutanixMachine, s conversion.Scope) error {
	out.ObjectMeta = in.ObjectMeta
	if err := Convert_v1beta1_NutanixMachineSpec_To_v1alpha4_NutanixMachineSpec(&in.Spec, &out.Spec, s); err != nil {
		return err
	}
	if err := Convert_v1beta1_NutanixMachineStatus_To_v1alpha4_NutanixMachineStatus(&in.Status, &out.Status, s); err != nil {
		return err
	}
	return nil
}

// Convert_v1beta1_NutanixMachine_To_v1alpha4_NutanixMachine is an autogenerated conversion function.
func Convert_v1beta1_NutanixMachine_To_v1alpha4_NutanixMachine(in *v1beta1.NutanixMachine, out *NutanixMachine, s conversion.Scope) error {
	return autoConvert_v1beta1_NutanixMachine_To_v1alpha4_NutanixMachine(in, out, s)
}

func autoConvert_v1alpha4_NutanixMachineList_To_v1beta1_NutanixMachineList(in *NutanixMachineList, out *v1beta1.NutanixMachineList, s conversion.Scope) error {
	out.ListMeta = in.ListMeta
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]v1beta1.NutanixMachine, len(*in))
		for i := range *in {
			if err := Convert_v1alpha4_NutanixMachine_To_v1beta1_NutanixMachine(&(*in)[i], &(*out)[i], s); err != nil {
				return err
			}
		}
	} else {
		out.Items = nil
	}
	return nil
}

// Convert_v1alpha4_NutanixMachineList_To_v1beta1_NutanixMachineList is an autogenerated conversion function.
func Convert_v1alpha4_NutanixMachineList_To_v1beta1_NutanixMachineList(in *NutanixMachineList, out *v1beta1.NutanixMachineList, s conversion.Scope) error {
	return autoConvert_v1alpha4_NutanixMachineList_To_v1beta1_NutanixMachineList(in, out, s)
}

func autoConvert_v1beta1_NutanixMachineList_To_v1alpha4_NutanixMachineList(in *v1beta1.NutanixMachineList, out *NutanixMachineList, s conversion.Scope) error {
	out.ListMeta = in.ListMeta
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]NutanixMachine, len(*in))
		for i := range *in {
			if err := Convert_v1beta1_NutanixMachine_To_v1alpha4_NutanixMachine(&(*in)[i], &(*out)[i], s); err != nil {
				return err
			}
		}
	} else {
		out.Items = nil
	}
	return nil
}

// Convert_v1beta1_NutanixMachineList_To_v1alpha4_NutanixMachineList is an autogenerated conversion function.
func Convert_v1beta1_NutanixMachineList_To_v1alpha4_NutanixMachineList(in *v1beta1.NutanixMachineList, out *NutanixMachineList, s conversion.Scope) error {
	return autoConvert_v1beta1_NutanixMachineList_To_v1alpha4_NutanixMachineList(in, out, s)
}

func autoConvert_v1alpha4_NutanixMachineSpec_To_v1beta1_NutanixMachineSpec(in *NutanixMachineSpec, out *v1beta1.NutanixMachineSpec, s conversion.Scope) error {
	out.ProviderID = in.ProviderID
	out.VCPUsPerSocket = in.VCPUsPerSocket
	out.VCPUSockets = in.VCPUSockets
	out.MemorySize = in.MemorySize
	if err := Convert_v1alpha4_NutanixResourceIdentifier_To_v1beta1_NutanixResourceIdentifier(&in.Image, &out.Image, s); err != nil {
		return err
	}
	if err := Convert_v1alpha4_NutanixResourceIdentifier_To_v1beta1_NutanixResourceIdentifier(&in.Cluster, &out.Cluster, s); err != nil {
		return err
	}
	out.Subnets = *(*[]v1beta1.NutanixResourceIdentifier)(unsafe.Pointer(&in.Subnets))
	out.AdditionalCategories = *(*[]v1beta1.NutanixCategoryIdentifier)(unsafe.Pointer(&in.AdditionalCategories))
	out.BootType = in.BootType
	out.SystemDiskSize = in.SystemDiskSize
	out.BootstrapRef = (*v1.ObjectReference)(unsafe.Pointer(in.BootstrapRef))
	return nil
}

func autoConvert_v1beta1_NutanixMachineSpec_To_v1alpha4_NutanixMachineSpec(in *v1beta1.NutanixMachineSpec, out *NutanixMachineSpec, s conversion.Scope) error {
	out.ProviderID = in.ProviderID
	out.VCPUsPerSocket = in.VCPUsPerSocket
	out.VCPUSockets = in.VCPUSockets
	out.MemorySize = in.MemorySize
	if err := Convert_v1beta1_NutanixResourceIdentifier_To_v1alpha4_NutanixResourceIdentifier(&in.Image, &out.Image, s); err != nil {
		return err
	}
	if err := Convert_v1beta1_NutanixResourceIdentifier_To_v1alpha4_NutanixResourceIdentifier(&in.Cluster, &out.Cluster, s); err != nil {
		return err
	}
	out.Subnets = *(*[]NutanixResourceIdentifier)(unsafe.Pointer(&in.Subnets))
	out.AdditionalCategories = *(*[]NutanixCategoryIdentifier)(unsafe.Pointer(&in.AdditionalCategories))
	out.BootType = in.BootType
	out.SystemDiskSize = in.SystemDiskSize
	out.BootstrapRef = (*v1.ObjectReference)(unsafe.Pointer(in.BootstrapRef))
	return nil
}

func autoConvert_v1alpha4_NutanixMachineStatus_To_v1beta1_NutanixMachineStatus(in *NutanixMachineStatus, out *v1beta1.NutanixMachineStatus, s conversion.Scope) error {
	out.Ready = in.Ready
	out.Addresses = *(*[]apiv1beta1.MachineAddress)(unsafe.Pointer(&in.Addresses))
	out.VmUUID = in.VmUUID
	out.NodeRef = (*v1.ObjectReference)(unsafe.Pointer(in.NodeRef))
	out.Conditions = *(*apiv1beta1.Conditions)(unsafe.Pointer(&in.Conditions))
	return nil
}

func autoConvert_v1beta1_NutanixMachineStatus_To_v1alpha4_NutanixMachineStatus(in *v1beta1.NutanixMachineStatus, out *NutanixMachineStatus, s conversion.Scope) error {
	out.Ready = in.Ready
	out.Addresses = *(*[]apiv1alpha4.MachineAddress)(unsafe.Pointer(&in.Addresses))
	out.VmUUID = in.VmUUID
	out.NodeRef = (*v1.ObjectReference)(unsafe.Pointer(in.NodeRef))
	out.Conditions = *(*apiv1alpha4.Conditions)(unsafe.Pointer(&in.Conditions))
	return nil
}

// Convert_v1beta1_NutanixMachineStatus_To_v1alpha4_NutanixMachineStatus is an autogenerated conversion function.
func Convert_v1beta1_NutanixMachineStatus_To_v1alpha4_NutanixMachineStatus(in *v1beta1.NutanixMachineStatus, out *NutanixMachineStatus, s conversion.Scope) error {
	return autoConvert_v1beta1_NutanixMachineStatus_To_v1alpha4_NutanixMachineStatus(in, out, s)
}

func autoConvert_v1alpha4_NutanixMachineTemplate_To_v1beta1_NutanixMachineTemplate(in *NutanixMachineTemplate, out *v1beta1.NutanixMachineTemplate, s conversion.Scope) error {
	out.ObjectMeta = in.ObjectMeta
	if err := Convert_v1alpha4_NutanixMachineTemplateSpec_To_v1beta1_NutanixMachineTemplateSpec(&in.Spec, &out.Spec, s); err != nil {
		return err
	}
	return nil
}

// Convert_v1alpha4_NutanixMachineTemplate_To_v1beta1_NutanixMachineTemplate is an autogenerated conversion function.
func Convert_v1alpha4_NutanixMachineTemplate_To_v1beta1_NutanixMachineTemplate(in *NutanixMachineTemplate, out *v1beta1.NutanixMachineTemplate, s conversion.Scope) error {
	return autoConvert_v1alpha4_NutanixMachineTemplate_To_v1beta1_NutanixMachineTemplate(in, out, s)
}

func autoConvert_v1beta1_NutanixMachineTemplate_To_v1alpha4_NutanixMachineTemplate(in *v1beta1.NutanixMachineTemplate, out *NutanixMachineTemplate, s conversion.Scope) error {
	out.ObjectMeta = in.ObjectMeta
	if err := Convert_v1beta1_NutanixMachineTemplateSpec_To_v1alpha4_NutanixMachineTemplateSpec(&in.Spec, &out.Spec, s); err != nil {
		return err
	}
	return nil
}

// Convert_v1beta1_NutanixMachineTemplate_To_v1alpha4_NutanixMachineTemplate is an autogenerated conversion function.
func Convert_v1beta1_NutanixMachineTemplate_To_v1alpha4_NutanixMachineTemplate(in *v1beta1.NutanixMachineTemplate, out *NutanixMachineTemplate, s conversion.Scope) error {
	return autoConvert_v1beta1_NutanixMachineTemplate_To_v1alpha4_NutanixMachineTemplate(in, out, s)
}

func autoConvert_v1alpha4_NutanixMachineTemplateList_To_v1beta1_NutanixMachineTemplateList(in *NutanixMachineTemplateList, out *v1beta1.NutanixMachineTemplateList, s conversion.Scope) error {
	out.ListMeta = in.ListMeta
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]v1beta1.NutanixMachineTemplate, len(*in))
		for i := range *in {
			if err := Convert_v1alpha4_NutanixMachineTemplate_To_v1beta1_NutanixMachineTemplate(&(*in)[i], &(*out)[i], s); err != nil {
				return err
			}
		}
	} else {
		out.Items = nil
	}
	return nil
}

// Convert_v1alpha4_NutanixMachineTemplateList_To_v1beta1_NutanixMachineTemplateList is an autogenerated conversion function.
func Convert_v1alpha4_NutanixMachineTemplateList_To_v1beta1_NutanixMachineTemplateList(in *NutanixMachineTemplateList, out *v1beta1.NutanixMachineTemplateList, s conversion.Scope) error {
	return autoConvert_v1alpha4_NutanixMachineTemplateList_To_v1beta1_NutanixMachineTemplateList(in, out, s)
}

func autoConvert_v1beta1_NutanixMachineTemplateList_To_v1alpha4_NutanixMachineTemplateList(in *v1beta1.NutanixMachineTemplateList, out *NutanixMachineTemplateList, s conversion.Scope) error {
	out.ListMeta = in.ListMeta
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]NutanixMachineTemplate, len(*in))
		for i := range *in {
			if err := Convert_v1beta1_NutanixMachineTemplate_To_v1alpha4_NutanixMachineTemplate(&(*in)[i], &(*out)[i], s); err != nil {
				return err
			}
		}
	} else {
		out.Items = nil
	}
	return nil
}

// Convert_v1beta1_NutanixMachineTemplateList_To_v1alpha4_NutanixMachineTemplateList is an autogenerated conversion function.
func Convert_v1beta1_NutanixMachineTemplateList_To_v1alpha4_NutanixMachineTemplateList(in *v1beta1.NutanixMachineTemplateList, out *NutanixMachineTemplateList, s conversion.Scope) error {
	return autoConvert_v1beta1_NutanixMachineTemplateList_To_v1alpha4_NutanixMachineTemplateList(in, out, s)
}

func autoConvert_v1alpha4_NutanixMachineTemplateResource_To_v1beta1_NutanixMachineTemplateResource(in *NutanixMachineTemplateResource, out *v1beta1.NutanixMachineTemplateResource, s conversion.Scope) error {
	if err := Convert_v1alpha4_ObjectMeta_To_v1beta1_ObjectMeta(&in.ObjectMeta, &out.ObjectMeta, s); err != nil {
		return err
	}
	if err := Convert_v1alpha4_NutanixMachineSpec_To_v1beta1_NutanixMachineSpec(&in.Spec, &out.Spec, s); err != nil {
		return err
	}
	return nil
}

func autoConvert_v1beta1_NutanixMachineTemplateResource_To_v1alpha4_NutanixMachineTemplateResource(in *v1beta1.NutanixMachineTemplateResource, out *NutanixMachineTemplateResource, s conversion.Scope) error {
	if err := Convert_v1beta1_ObjectMeta_To_v1alpha4_ObjectMeta(&in.ObjectMeta, &out.ObjectMeta, s); err != nil {
		return err
	}
	if err := Convert_v1beta1_NutanixMachineSpec_To_v1alpha4_NutanixMachineSpec(&in.Spec, &out.Spec, s); err != nil {
		return err
	}
	return nil
}

func autoConvert_v1alpha4_NutanixMachineTemplateSpec_To_v1beta1_NutanixMachineTemplateSpec(in *NutanixMachineTemplateSpec, out *v1beta1.NutanixMachineTemplateSpec, s conversion.Scope) error {
	if err := Convert_v1alpha4_NutanixMachineTemplateResource_To_v1beta1_NutanixMachineTemplateResource(&in.Template, &out.Template, s); err != nil {
		return err
	}
	return nil
}

// Convert_v1alpha4_NutanixMachineTemplateSpec_To_v1beta1_NutanixMachineTemplateSpec is an autogenerated conversion function.
func Convert_v1alpha4_NutanixMachineTemplateSpec_To_v1beta1_NutanixMachineTemplateSpec(in *NutanixMachineTemplateSpec, out *v1beta1.NutanixMachineTemplateSpec, s conversion.Scope) error {
	return autoConvert_v1alpha4_NutanixMachineTemplateSpec_To_v1beta1_NutanixMachineTemplateSpec(in, out, s)
}

func autoConvert_v1beta1_NutanixMachineTemplateSpec_To_v1alpha4_NutanixMachineTemplateSpec(in *v1beta1.NutanixMachineTemplateSpec, out *NutanixMachineTemplateSpec, s conversion.Scope) error {
	if err := Convert_v1beta1_NutanixMachineTemplateResource_To_v1alpha4_NutanixMachineTemplateResource(&in.Template, &out.Template, s); err != nil {
		return err
	}
	return nil
}

// Convert_v1beta1_NutanixMachineTemplateSpec_To_v1alpha4_NutanixMachineTemplateSpec is an autogenerated conversion function.
func Convert_v1beta1_NutanixMachineTemplateSpec_To_v1alpha4_NutanixMachineTemplateSpec(in *v1beta1.NutanixMachineTemplateSpec, out *NutanixMachineTemplateSpec, s conversion.Scope) error {
	return autoConvert_v1beta1_NutanixMachineTemplateSpec_To_v1alpha4_NutanixMachineTemplateSpec(in, out, s)
}

func autoConvert_v1alpha4_NutanixResourceIdentifier_To_v1beta1_NutanixResourceIdentifier(in *NutanixResourceIdentifier, out *v1beta1.NutanixResourceIdentifier, s conversion.Scope) error {
	out.Type = v1beta1.NutanixIdentifierType(in.Type)
	out.UUID = (*string)(unsafe.Pointer(in.UUID))
	out.Name = (*string)(unsafe.Pointer(in.Name))
	return nil
}

// Convert_v1alpha4_NutanixResourceIdentifier_To_v1beta1_NutanixResourceIdentifier is an autogenerated conversion function.
func Convert_v1alpha4_NutanixResourceIdentifier_To_v1beta1_NutanixResourceIdentifier(in *NutanixResourceIdentifier, out *v1beta1.NutanixResourceIdentifier, s conversion.Scope) error {
	return autoConvert_v1alpha4_NutanixResourceIdentifier_To_v1beta1_NutanixResourceIdentifier(in, out, s)
}

func autoConvert_v1beta1_NutanixResourceIdentifier_To_v1alpha4_NutanixResourceIdentifier(in *v1beta1.NutanixResourceIdentifier, out *NutanixResourceIdentifier, s conversion.Scope) error {
	out.Type = NutanixIdentifierType(in.Type)
	out.UUID = (*string)(unsafe.Pointer(in.UUID))
	out.Name = (*string)(unsafe.Pointer(in.Name))
	return nil
}

// Convert_v1beta1_NutanixResourceIdentifier_To_v1alpha4_NutanixResourceIdentifier is an autogenerated conversion function.
func Convert_v1beta1_NutanixResourceIdentifier_To_v1alpha4_NutanixResourceIdentifier(in *v1beta1.NutanixResourceIdentifier, out *NutanixResourceIdentifier, s conversion.Scope) error {
	return autoConvert_v1beta1_NutanixResourceIdentifier_To_v1alpha4_NutanixResourceIdentifier(in, out, s)
}
