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
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	vmmcommon "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/common/v1/config"
	vmmresponse "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/common/v1/response"
	vmmprism "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/prism/v4/config"
	vmmconfig "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/ahv/config"
	vmmcontent "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/content"
	"k8s.io/utils/ptr"
)

func (s *Simulator) registerVMMRoutes() {
	m := s.mux
	m.HandleFunc("GET /api/vmm/{version}/ahv/config/vms", s.handleListVMs)
	m.HandleFunc("POST /api/vmm/{version}/ahv/config/vms", s.handleCreateVM)
	m.HandleFunc("GET /api/vmm/{version}/ahv/config/vms/{extId}", s.handleGetVM)
	m.HandleFunc("DELETE /api/vmm/{version}/ahv/config/vms/{extId}", s.handleDeleteVM)
	m.HandleFunc("POST /api/vmm/{version}/ahv/config/vms/{extId}/$actions/power-on", s.handlePowerOnVM)
	m.HandleFunc("POST /api/vmm/{version}/ahv/config/vms/{extId}/$actions/power-off", s.handlePowerOffVM)
	m.HandleFunc("POST /api/vmm/{version}/ahv/config/vms/{extId}/$actions/add-custom-attributes", s.handleAddVMCustomAttributes)

	m.HandleFunc("GET /api/vmm/{version}/content/images", s.handleListImages)
	m.HandleFunc("GET /api/vmm/{version}/content/images/{extId}", s.handleGetImage)
}

func vmmMetadata(total int) *vmmresponse.ApiResponseMetadata {
	md := vmmresponse.NewApiResponseMetadata()
	md.TotalAvailableResults = ptr.To(total)
	return md
}

func taskReference(extID string) vmmprism.TaskReference {
	ref := vmmprism.NewTaskReference()
	ref.ExtId = ptr.To(extID)
	return *ref
}

// --- VMs ---

func (s *Simulator) handleListVMs(w http.ResponseWriter, r *http.Request) {
	q, err := parseListQuery(r)
	if err != nil {
		writeBadRequest(w, err.Error())
		return
	}
	s.store.mu.RLock()
	defer s.store.mu.RUnlock()
	records := make([]*vmRecord, 0, len(s.store.vms))
	for _, rec := range s.store.vms {
		records = append(records, rec)
	}
	page, total, err := selectPage(q, records, func(rec *vmRecord) (map[string]any, error) { return rec.doc.get(rec.vm) })
	if err != nil {
		writeBadRequest(w, err.Error())
		return
	}
	vms := make([]vmmconfig.Vm, 0, len(page))
	for _, rec := range page {
		setETag(rec.vm, etagFor(rec.version))
		vms = append(vms, *rec.vm)
	}
	resp := vmmconfig.NewListVmsApiResponse()
	resp.Metadata = vmmMetadata(total)
	if err := setListData(resp, vms); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, "", resp)
}

func (s *Simulator) handleGetVM(w http.ResponseWriter, r *http.Request) {
	s.store.mu.RLock()
	defer s.store.mu.RUnlock()
	rec, ok := s.store.vms[r.PathValue("extId")]
	if !ok {
		writeNotFound(w, "VM", r.PathValue("extId"))
		return
	}
	s.writeVM(w, rec)
}

// writeVM writes a GetVmApiResponse. The store lock must be held.
func (s *Simulator) writeVM(w http.ResponseWriter, rec *vmRecord) {
	etag := etagFor(rec.version)
	setETag(rec.vm, etag)
	resp := vmmconfig.NewGetVmApiResponse()
	if err := resp.SetData(*rec.vm); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, etag, resp)
}

func (s *Simulator) handleCreateVM(w http.ResponseWriter, r *http.Request) {
	vm := vmmconfig.NewVm()
	if !decodeBody(w, r, vm) {
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	if msg := s.validateNewVM(vm); msg != "" {
		writeBadRequest(w, msg)
		return
	}
	if max := s.cfg.Limits.MaxVMs; max > 0 && len(s.store.vms) >= max {
		writeError(w, http.StatusBadRequest, "VM_LIMIT_EXCEEDED", fmt.Sprintf("simulated Prism Central holds at most %d VMs", max))
		return
	}
	s.initialiseVM(vm)
	rec := &vmRecord{vm: vm, version: 1, ips: map[string]allocatedIP{}}
	s.store.vms[*vm.ExtId] = rec
	task := s.newTask(opVMCreate, entityRef{extID: *vm.ExtId, name: *vm.Name, rel: relVM})
	s.scheduleTask(task, s.cfg.Timing.VMCreate, func() func() {
		rec.touch()
		return s.vmHook(s.hooks.OnVMCreated, rec)
	})
	resp := vmmconfig.NewCreateVmApiResponse()
	_ = resp.SetData(taskReference(*task.task.ExtId))
	writeJSON(w, http.StatusAccepted, "", resp)
}

// validateNewVM checks the references a VM create must carry. It returns an
// error message, or "" when the VM is acceptable. The store lock must be held.
func (s *Simulator) validateNewVM(vm *vmmconfig.Vm) string {
	if vm.Name == nil || *vm.Name == "" {
		return "vm name is required"
	}
	if vm.Cluster == nil || vm.Cluster.ExtId == nil {
		return "cluster reference is required"
	}
	if s.store.clusterByExtID(*vm.Cluster.ExtId) == nil {
		return fmt.Sprintf("cluster %s not found", *vm.Cluster.ExtId)
	}
	for i, nic := range vm.Nics {
		subnetID := nicSubnetExtID(nic)
		if subnetID == "" {
			return fmt.Sprintf("nic %d has no subnet reference", i)
		}
		if s.store.subnetByExtID(subnetID) == nil {
			return fmt.Sprintf("subnet %s referenced by nic %d not found", subnetID, i)
		}
	}
	for i, disk := range vm.Disks {
		if imageID := diskImageExtID(disk); imageID != "" && s.store.imageByExtID(imageID) == nil {
			return fmt.Sprintf("image %s referenced by disk %d not found", imageID, i)
		}
	}
	return ""
}

func nicSubnetExtID(nic vmmconfig.Nic) string {
	if nic.NetworkInfo != nil && nic.NetworkInfo.Subnet != nil && nic.NetworkInfo.Subnet.ExtId != nil {
		return *nic.NetworkInfo.Subnet.ExtId
	}
	if info, ok := nic.GetNicNetworkInfo().(vmmconfig.VirtualEthernetNicNetworkInfo); ok && info.Subnet != nil && info.Subnet.ExtId != nil {
		return *info.Subnet.ExtId
	}
	return ""
}

func diskImageExtID(disk vmmconfig.Disk) string {
	vmDisk, ok := disk.GetBackingInfo().(vmmconfig.VmDisk)
	if !ok || vmDisk.DataSource == nil {
		return ""
	}
	ref, ok := vmDisk.DataSource.GetReference().(vmmconfig.ImageReference)
	if !ok || ref.ImageExtId == nil {
		return ""
	}
	return *ref.ImageExtId
}

// initialiseVM fills in the server-assigned fields of a new VM.
func (s *Simulator) initialiseVM(vm *vmmconfig.Vm) {
	now := time.Now().UTC()
	id := uuid.NewString()
	vm.ExtId = ptr.To(id)
	vm.BiosUuid = ptr.To(id)
	vm.GenerationUuid = ptr.To(uuid.NewString())
	vm.PowerState = ptr.To(vmmconfig.POWERSTATE_OFF)
	vm.CreateTime = ptr.To(now)
	vm.UpdateTime = ptr.To(now)
	if vm.MachineType == nil {
		vm.MachineType = ptr.To(vmmconfig.MACHINETYPE_PC)
	}
	for i := range vm.Nics {
		nic := &vm.Nics[i]
		nic.ExtId = ptr.To(uuid.NewString())
		if nic.BackingInfo == nil {
			nic.BackingInfo = vmmconfig.NewEmulatedNic()
		}
		nic.BackingInfo.MacAddress = ptr.To(randomMAC())
		nic.BackingInfo.IsConnected = ptr.To(true)
	}
	for i := range vm.Disks {
		vm.Disks[i].ExtId = ptr.To(uuid.NewString())
	}
	for i := range vm.CdRoms {
		vm.CdRoms[i].ExtId = ptr.To(uuid.NewString())
	}
}

func randomMAC() string {
	b := uuid.New()
	return fmt.Sprintf("50:6b:8d:%02x:%02x:%02x", b[0], b[1], b[2])
}

// vmHook builds the post-lock callback delivering a VMEvent to hook. The
// store lock must be held.
func (s *Simulator) vmHook(hook func(context.Context, VMEvent), rec *vmRecord) func() {
	if hook == nil {
		return nil
	}
	cp, err := deepCopyVM(rec.vm)
	if err != nil {
		s.log.Error("copying VM for hook", "vm", *rec.vm.ExtId, "err", err)
		return nil
	}
	ev := VMEvent{VM: cp}
	for _, ip := range rec.ips {
		ev.Addresses = append(ev.Addresses, uint32ToIP(ip.ip).String())
	}
	return func() { hook(context.Background(), ev) }
}

func (s *Simulator) lockedVM(w http.ResponseWriter, r *http.Request) (*vmRecord, bool) {
	rec, ok := s.store.vms[r.PathValue("extId")]
	if !ok {
		writeNotFound(w, "VM", r.PathValue("extId"))
		return nil, false
	}
	if !checkIfMatch(w, r, etagFor(rec.version)) {
		return nil, false
	}
	return rec, true
}

func (s *Simulator) handlePowerOnVM(w http.ResponseWriter, r *http.Request) {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	rec, ok := s.lockedVM(w, r)
	if !ok {
		return
	}
	task := s.newTask(opVMPowerOn, entityRef{extID: *rec.vm.ExtId, name: *rec.vm.Name, rel: relVM})
	s.scheduleTask(task, s.cfg.Timing.VMPowerOn, func() func() {
		if _, gone := s.store.vms[*rec.vm.ExtId]; !gone {
			return nil
		}
		rec.vm.PowerState = ptr.To(vmmconfig.POWERSTATE_ON)
		s.assignAddresses(rec)
		rec.touch()
		return s.vmHook(s.hooks.OnVMPoweredOn, rec)
	})
	resp := vmmconfig.NewPowerOnVmApiResponse()
	_ = resp.SetData(taskReference(*task.task.ExtId))
	writeJSON(w, http.StatusAccepted, "", resp)
}

func (s *Simulator) handlePowerOffVM(w http.ResponseWriter, r *http.Request) {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	rec, ok := s.lockedVM(w, r)
	if !ok {
		return
	}
	task := s.newTask(opVMPowerOff, entityRef{extID: *rec.vm.ExtId, name: *rec.vm.Name, rel: relVM})
	s.scheduleTask(task, s.cfg.Timing.VMPowerOn, func() func() {
		if _, gone := s.store.vms[*rec.vm.ExtId]; !gone {
			return nil
		}
		rec.vm.PowerState = ptr.To(vmmconfig.POWERSTATE_OFF)
		s.releaseAddresses(rec)
		rec.touch()
		return s.vmHook(s.hooks.OnVMPoweredOff, rec)
	})
	resp := vmmconfig.NewPowerOffVmApiResponse()
	_ = resp.SetData(taskReference(*task.task.ExtId))
	writeJSON(w, http.StatusAccepted, "", resp)
}

// assignAddresses gives every NIC an address from its subnet pool and reports
// it as a learned IP, the way AHV does once the guest brings the interface
// up. A statically requested address is honoured as-is.
func (s *Simulator) assignAddresses(rec *vmRecord) {
	for i := range rec.vm.Nics {
		nic := &rec.vm.Nics[i]
		if nic.NetworkInfo == nil {
			nic.NetworkInfo = vmmconfig.NewNicNetworkInfo()
		}
		if nic.NetworkInfo.Ipv4Config != nil && nic.NetworkInfo.Ipv4Config.IpAddress != nil {
			continue
		}
		if _, done := rec.ips[*nic.ExtId]; done {
			continue
		}
		subnetID := nicSubnetExtID(*nic)
		pool := s.store.pools[subnetID]
		if pool == nil {
			continue
		}
		ip, ok := pool.allocate()
		if !ok {
			s.log.Warn("subnet pool exhausted", "subnet", subnetID, "vm", *rec.vm.ExtId)
			continue
		}
		rec.ips[*nic.ExtId] = allocatedIP{subnetExtID: subnetID, ip: ip}
		addr := vmmcommon.NewIPv4Address()
		addr.Value = ptr.To(uint32ToIP(ip).String())
		addr.PrefixLength = ptr.To(pool.prefixLen)
		nic.NetworkInfo.Ipv4Info = vmmconfig.NewIpv4Info()
		nic.NetworkInfo.Ipv4Info.LearnedIpAddresses = []vmmcommon.IPv4Address{*addr}
	}
}

func (s *Simulator) releaseAddresses(rec *vmRecord) {
	for nicID, alloc := range rec.ips {
		if pool := s.store.pools[alloc.subnetExtID]; pool != nil {
			pool.release(alloc.ip)
		}
		delete(rec.ips, nicID)
	}
	for i := range rec.vm.Nics {
		if rec.vm.Nics[i].NetworkInfo != nil {
			rec.vm.Nics[i].NetworkInfo.Ipv4Info = nil
		}
	}
}

func (s *Simulator) handleAddVMCustomAttributes(w http.ResponseWriter, r *http.Request) {
	params := vmmconfig.NewUpdateCustomAttributesParams()
	if !decodeBody(w, r, params) {
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	rec, ok := s.lockedVM(w, r)
	if !ok {
		return
	}
	task := s.newTask(opVMUpdate, entityRef{extID: *rec.vm.ExtId, name: *rec.vm.Name, rel: relVM})
	s.scheduleTask(task, s.cfg.Timing.VMUpdate, func() func() {
		if _, gone := s.store.vms[*rec.vm.ExtId]; !gone {
			return nil
		}
		for _, attr := range params.CustomAttributes {
			if !containsString(rec.vm.CustomAttributes, attr) {
				rec.vm.CustomAttributes = append(rec.vm.CustomAttributes, attr)
			}
		}
		rec.touch()
		return nil
	})
	resp := vmmconfig.NewAddVmCustomAttributesApiResponse()
	_ = resp.SetData(taskReference(*task.task.ExtId))
	writeJSON(w, http.StatusAccepted, "", resp)
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func (s *Simulator) handleDeleteVM(w http.ResponseWriter, r *http.Request) {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	rec, ok := s.lockedVM(w, r)
	if !ok {
		return
	}
	task := s.newTask(opVMDelete, entityRef{extID: *rec.vm.ExtId, name: *rec.vm.Name, rel: relVM})
	s.scheduleTask(task, s.cfg.Timing.VMDelete, func() func() {
		if _, present := s.store.vms[*rec.vm.ExtId]; !present {
			return nil
		}
		// Build the event before releasing addresses so the hook sees the
		// address the VM had while it was running.
		after := s.vmHook(s.hooks.OnVMDeleted, rec)
		s.releaseAddresses(rec)
		delete(s.store.vms, *rec.vm.ExtId)
		return after
	})
	resp := vmmconfig.NewDeleteVmApiResponse()
	_ = resp.SetData(taskReference(*task.task.ExtId))
	writeJSON(w, http.StatusAccepted, "", resp)
}

// --- Images ---

func (s *Simulator) handleListImages(w http.ResponseWriter, r *http.Request) {
	q, err := parseListQuery(r)
	if err != nil {
		writeBadRequest(w, err.Error())
		return
	}
	s.store.mu.RLock()
	defer s.store.mu.RUnlock()
	page, total, err := selectPage(q, s.store.images, func(e *seededEntity[vmmcontent.Image]) (map[string]any, error) { return e.doc.get(e.entity) })
	if err != nil {
		writeBadRequest(w, err.Error())
		return
	}
	images := make([]vmmcontent.Image, 0, len(page))
	for _, e := range page {
		images = append(images, *e.entity)
	}
	resp := vmmcontent.NewListImagesApiResponse()
	resp.Metadata = vmmMetadata(total)
	if err := setListData(resp, images); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, "", resp)
}

func (s *Simulator) handleGetImage(w http.ResponseWriter, r *http.Request) {
	s.store.mu.RLock()
	defer s.store.mu.RUnlock()
	image := s.store.imageByExtID(r.PathValue("extId"))
	if image == nil {
		writeNotFound(w, "image", r.PathValue("extId"))
		return
	}
	resp := vmmcontent.NewGetImageApiResponse()
	if err := resp.SetData(*image); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, etagFor(1), resp)
}
