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
	"context"
	"encoding/json"
	"net/http"

	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

// Webhook paths (must match config/webhook/mutating_webhook_configuration.yaml).
const (
	PathMutateNutanixMachine        = "/mutate-infrastructure-cluster-x-k8s-io-v1beta1-nutanixmachine"
	PathMutateNutanixMachineTemplate = "/mutate-infrastructure-cluster-x-k8s-io-v1beta1-nutanixmachinetemplate"
)

// nutanixMachineDefaulterHandler implements admission.Handler for NutanixMachine (extension-style).
type nutanixMachineDefaulterHandler struct {
	decoder admission.Decoder
}

// NewNutanixMachineDefaulterHandler returns a handler that defaults NutanixMachine using the decoder.
func NewNutanixMachineDefaulterHandler(decoder admission.Decoder) admission.Handler {
	return &nutanixMachineDefaulterHandler{decoder: decoder}
}

// Handle decodes the request, applies defaulting, and returns a patch response.
func (h *nutanixMachineDefaulterHandler) Handle(ctx context.Context, req admission.Request) admission.Response {
	nm := &infrav1.NutanixMachine{}
	if err := h.decoder.Decode(req, nm); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	defaultNutanixMachineSpec(&nm.Spec)
	current, err := json.Marshal(nm)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}
	return admission.PatchResponseFromRaw(req.Object.Raw, current)
}

// nutanixMachineTemplateDefaulterHandler implements admission.Handler for NutanixMachineTemplate (extension-style).
type nutanixMachineTemplateDefaulterHandler struct {
	decoder admission.Decoder
}

// NewNutanixMachineTemplateDefaulterHandler returns a handler that defaults NutanixMachineTemplate using the decoder.
func NewNutanixMachineTemplateDefaulterHandler(decoder admission.Decoder) admission.Handler {
	return &nutanixMachineTemplateDefaulterHandler{decoder: decoder}
}

// Handle decodes the request, applies defaulting to the template spec, and returns a patch response.
func (h *nutanixMachineTemplateDefaulterHandler) Handle(ctx context.Context, req admission.Request) admission.Response {
	nmt := &infrav1.NutanixMachineTemplate{}
	if err := h.decoder.Decode(req, nmt); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	defaultNutanixMachineSpec(&nmt.Spec.Template.Spec)
	current, err := json.Marshal(nmt)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}
	return admission.PatchResponseFromRaw(req.Object.Raw, current)
}
