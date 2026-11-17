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
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	prismgoclient "github.com/nutanix-cloud-native/prism-go-client"
	"github.com/nutanix-cloud-native/prism-go-client/converged"
	v4Converged "github.com/nutanix-cloud-native/prism-go-client/converged/v4"
	prismclientv3 "github.com/nutanix-cloud-native/prism-go-client/v3"
	clustermgmtconfig "github.com/nutanix/ntnx-api-golang-clients/clustermgmt-go-client/v4/models/clustermgmt/v4/config"
	prismconfig "github.com/nutanix/ntnx-api-golang-clients/prism-go-client/v4/models/prism/v4/config"
	vmmconfig "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/ahv/config"
	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"
)

const (
	testUser = "admin"
	testPass = "sim-secret"
)

type testEnv struct {
	sim    *Simulator
	server *httptest.Server
	addr   string
	creds  prismgoclient.Credentials
}

func newTestEnv(t *testing.T, cfg Config, opts ...Option) *testEnv {
	t.Helper()
	cfg.Auth = Auth{Username: testUser, Password: testPass}
	opts = append(opts, WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	sim, err := New(cfg, opts...)
	require.NoError(t, err)
	server := httptest.NewUnstartedServer(sim.Handler())
	server.StartTLS()
	t.Cleanup(server.Close)
	addr := server.Listener.Addr().String()
	return &testEnv{
		sim:    sim,
		server: server,
		addr:   addr,
		creds: prismgoclient.Credentials{
			URL:      addr,
			Endpoint: addr,
			Username: testUser,
			Password: testPass,
			Insecure: true,
		},
	}
}

func (e *testEnv) convergedClient(t *testing.T) *v4Converged.Client {
	t.Helper()
	client, err := v4Converged.NewClient(e.creds)
	require.NoError(t, err)
	return client
}

func (e *testEnv) rawClient() *http.Client {
	return &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} //nolint:gosec // test server uses a self-signed certificate
}

func (e *testEnv) rawRequest(t *testing.T, method, path string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, e.server.URL+path, nil)
	require.NoError(t, err)
	req.SetBasicAuth(testUser, testPass)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := e.rawClient().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// capxVM builds a VM the way NutanixMachineReconciler.getOrCreateVM does.
func capxVM(name, clusterExtID, subnetExtID, imageExtID string) *vmmconfig.Vm {
	vm := vmmconfig.NewVm()
	vm.Name = ptr.To(name)
	vm.MemorySizeBytes = ptr.To(int64(4 * 1024 * 1024 * 1024))
	vm.NumCoresPerSocket = ptr.To(2)
	vm.NumSockets = ptr.To(1)
	vm.HardwareClockTimezone = ptr.To("UTC")
	vm.Cluster = vmmconfig.NewClusterReference()
	vm.Cluster.ExtId = ptr.To(clusterExtID)

	nic := vmmconfig.NewNic()
	nic.NetworkInfo = vmmconfig.NewNicNetworkInfo()
	nic.NetworkInfo.Subnet = vmmconfig.NewSubnetReference()
	nic.NetworkInfo.Subnet.ExtId = ptr.To(subnetExtID)
	vm.Nics = []vmmconfig.Nic{*nic}

	vmDisk := vmmconfig.NewVmDisk()
	vmDisk.DiskSizeBytes = ptr.To(int64(40 * 1024 * 1024 * 1024))
	vmDisk.DataSource = vmmconfig.NewDataSource()
	imageRef := vmmconfig.NewImageReference()
	imageRef.ImageExtId = ptr.To(imageExtID)
	_ = vmDisk.DataSource.SetReference(*imageRef)
	vmDisk.DataSource.ReferenceItemDiscriminator_ = nil
	disk := vmmconfig.NewDisk()
	_ = disk.SetBackingInfo(*vmDisk)
	vm.Disks = []vmmconfig.Disk{*disk}
	return vm
}

func TestV3SessionLogin(t *testing.T) {
	env := newTestEnv(t, DefaultConfig())
	creds := env.creds
	creds.SessionAuth = true
	// CAPX builds its v3 client with session auth, which logs in at
	// construction via GET /api/nutanix/v3/users/me.
	client, err := prismclientv3.NewV3Client(creds)
	require.NoError(t, err)
	require.NotNil(t, client)

	// Anything else on v3 is unsupported and must fail loudly.
	_, err = client.V3.ListAllProject(context.Background(), "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not implement")

	creds.Password = "wrong"
	_, err = prismclientv3.NewV3Client(creds)
	require.Error(t, err)
}

func TestCAPXMachineLifecycle(t *testing.T) {
	var (
		mu     sync.Mutex
		events []string
		addrs  []string
	)
	record := func(kind string) func(context.Context, VMEvent) {
		return func(_ context.Context, ev VMEvent) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, kind)
			addrs = append(addrs, ev.Addresses...)
		}
	}
	env := newTestEnv(t, DefaultConfig(), WithHooks(Hooks{
		OnVMCreated:   record("created"),
		OnVMPoweredOn: record("powered-on"),
		OnVMDeleted:   record("deleted"),
	}))
	client := env.convergedClient(t)
	ctx := context.Background()

	// Cluster, subnet, image and storage container lookups with the exact
	// filters CAPX's helpers issue.
	clusters, err := client.Clusters.List(ctx, converged.WithFilter("name eq 'pe-sim'"))
	require.NoError(t, err)
	require.Len(t, clusters, 1)
	require.Contains(t, clusters[0].Config.ClusterFunction, clustermgmtconfig.CLUSTERFUNCTIONREF_AOS)
	peExtID := *clusters[0].ExtId
	pe, err := client.Clusters.Get(ctx, peExtID)
	require.NoError(t, err)
	require.True(t, *pe.Config.IsAvailable)

	subnets, err := client.Subnets.List(ctx, converged.WithFilter("name eq 'subnet-sim'"))
	require.NoError(t, err)
	require.Len(t, subnets, 1)
	require.Equal(t, peExtID, *subnets[0].ClusterReference)
	subnetExtID := *subnets[0].ExtId
	_, err = client.Subnets.Get(ctx, subnetExtID)
	require.NoError(t, err)

	images, err := client.Images.List(ctx, converged.WithFilter("name eq 'ubuntu-sim'"))
	require.NoError(t, err)
	require.Len(t, images, 1)
	imageExtID := *images[0].ExtId
	image, err := client.Images.Get(ctx, imageExtID)
	require.NoError(t, err)
	require.Equal(t, "ubuntu-sim", *image.Name)

	containers, err := client.StorageContainers.List(ctx,
		converged.WithFilter(fmt.Sprintf("name eq 'default-sim' and clusterExtId eq '%s'", peExtID)))
	require.NoError(t, err)
	require.Len(t, containers, 1)

	// Categories: get-or-create as NutanixClusterReconciler.reconcileCategories does.
	found, err := client.Categories.List(ctx, converged.WithFilter("key eq 'KubernetesClusterName' and value eq 'c1'"))
	require.NoError(t, err)
	require.Empty(t, found)
	category := prismconfig.NewCategory()
	category.Key = ptr.To("KubernetesClusterName")
	category.Value = ptr.To("c1")
	created, err := client.Categories.Create(ctx, category)
	require.NoError(t, err)
	require.NotEmpty(t, *created.ExtId)
	found, err = client.Categories.List(ctx, converged.WithFilter("key eq 'KubernetesClusterName' and value eq 'c1'"))
	require.NoError(t, err)
	require.Len(t, found, 1)
	_, err = client.Categories.Create(ctx, category)
	require.Error(t, err, "duplicate category must be rejected")

	// VM create, as getOrCreateVM does, then the providerID custom attribute.
	vmSpec := capxVM("c1-md-0-abc", peExtID, subnetExtID, imageExtID)
	vmSpec.Categories = []vmmconfig.CategoryReference{{ExtId: created.ExtId}}
	vm, err := client.VMs.Create(ctx, vmSpec)
	require.NoError(t, err)
	vmExtID := *vm.ExtId
	require.NotEmpty(t, vmExtID)
	require.Equal(t, vmmconfig.POWERSTATE_OFF, *vm.PowerState)
	require.Equal(t, vmExtID, *vm.BiosUuid)

	byName, err := client.VMs.List(ctx, converged.WithFilter("name eq 'c1-md-0-abc'"))
	require.NoError(t, err)
	require.Len(t, byName, 1)
	require.Equal(t, vmExtID, *byName[0].ExtId)

	updated, err := client.VMs.AddVmCustomAttributes(ctx, vmExtID, []string{"providerid:nutanix://" + vmExtID})
	require.NoError(t, err)
	require.Contains(t, updated.CustomAttributes, "providerid:nutanix://"+vmExtID)

	// Power on and read the learned IP that populates status.addresses.
	powerOn, err := client.VMs.PowerOnVM(vmExtID)
	require.NoError(t, err)
	_, err = powerOn.Wait(ctx)
	require.NoError(t, err)
	vm, err = client.VMs.Get(ctx, vmExtID)
	require.NoError(t, err)
	require.Equal(t, vmmconfig.POWERSTATE_ON, *vm.PowerState)
	require.Len(t, vm.Nics, 1)
	learned := vm.Nics[0].NetworkInfo.Ipv4Info.LearnedIpAddresses
	require.Len(t, learned, 1)
	ip := net.ParseIP(*learned[0].Value)
	require.NotNil(t, ip)
	require.True(t, strings.HasPrefix(ip.String(), "10.10."), ip.String())

	// VmHasTaskInProgress: no tasks still running for the VM.
	running, err := client.Tasks.List(ctx, converged.WithFilter(fmt.Sprintf(
		"entitiesAffected/any(a:a/extId eq '%s') and (status eq Prism.Config.TaskStatus'RUNNING' or status eq Prism.Config.TaskStatus'QUEUED')", vmExtID)))
	require.NoError(t, err)
	require.Empty(t, running)
	all, err := client.Tasks.List(ctx, converged.WithFilter(fmt.Sprintf("entitiesAffected/any(a:a/extId eq '%s')", vmExtID)))
	require.NoError(t, err)
	require.Len(t, all, 3, "create, update and power-on tasks")

	// Delete and confirm NotFound classification.
	del, err := client.VMs.DeleteAsync(ctx, vmExtID)
	require.NoError(t, err)
	_, err = del.Wait(ctx)
	require.NoError(t, err)
	_, err = client.VMs.Get(ctx, vmExtID)
	require.Error(t, err)
	require.True(t, converged.IsNotFound(err), err.Error())
	byName, err = client.VMs.List(ctx, converged.WithFilter("name eq 'c1-md-0-abc'"))
	require.NoError(t, err)
	require.Empty(t, byName)

	require.NoError(t, client.Categories.Delete(ctx, *created.ExtId))
	_, err = client.Categories.Get(ctx, *created.ExtId)
	require.True(t, converged.IsNotFound(err))

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{"created", "powered-on", "deleted"}, events)
	require.Equal(t, []string{ip.String(), ip.String()}, addrs, "power-on and delete events carry the address")

	snap := env.sim.Stats()
	require.Equal(t, 0, snap.VMs)
	require.Equal(t, uint64(1), snap.Tasks[opVMCreate].Succeeded)
}

func TestCreateVMValidation(t *testing.T) {
	env := newTestEnv(t, DefaultConfig())
	client := env.convergedClient(t)
	ctx := context.Background()

	_, err := client.VMs.Create(ctx, capxVM("bad", "missing-cluster", "missing-subnet", "missing-image"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "cluster missing-cluster not found")
}

func TestVMLimit(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Limits.MaxVMs = 1
	env := newTestEnv(t, cfg)
	client := env.convergedClient(t)
	ctx := context.Background()
	pe, subnet, image := seededIDs(t, env)

	_, err := client.VMs.Create(ctx, capxVM("one", pe, subnet, image))
	require.NoError(t, err)
	_, err = client.VMs.Create(ctx, capxVM("two", pe, subnet, image))
	require.Error(t, err)
	require.Contains(t, err.Error(), "VM_LIMIT_EXCEEDED")
}

func seededIDs(t *testing.T, env *testEnv) (pe, subnet, image string) {
	t.Helper()
	env.sim.store.mu.RLock()
	defer env.sim.store.mu.RUnlock()
	return *env.sim.store.clusters[0].entity.ExtId, *env.sim.store.subnets[0].entity.ExtId, *env.sim.store.images[0].entity.ExtId
}

func TestListPagination(t *testing.T) {
	env := newTestEnv(t, DefaultConfig())
	pe, subnet, image := seededIDs(t, env)
	const n = 120
	env.sim.store.mu.Lock()
	for i := 0; i < n; i++ {
		vm := capxVM(fmt.Sprintf("vm-%03d", i), pe, subnet, image)
		env.sim.initialiseVM(vm)
		env.sim.store.vms[*vm.ExtId] = &vmRecord{vm: vm, version: 1, ips: map[string]allocatedIP{}}
	}
	env.sim.store.mu.Unlock()

	client := env.convergedClient(t)
	// No paging options: the converged iterator walks every page.
	vms, err := client.VMs.List(context.Background())
	require.NoError(t, err)
	require.Len(t, vms, n)

	page, err := client.VMs.List(context.Background(), converged.WithPage(1), converged.WithLimit(50))
	require.NoError(t, err)
	require.Len(t, page, 50)
}

func TestTaskFailureInjection(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Faults.TaskFailureEvery = 1
	env := newTestEnv(t, cfg)
	client := env.convergedClient(t)
	pe, subnet, image := seededIDs(t, env)

	_, err := client.VMs.Create(context.Background(), capxVM("doomed", pe, subnet, image))
	require.Error(t, err)
	require.Contains(t, err.Error(), "injected task failure")
	require.Equal(t, uint64(1), env.sim.Stats().Tasks[opVMCreate].Failed)
}

func TestInternalErrorInjection(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Faults.InternalErrorEvery = 1
	env := newTestEnv(t, cfg)
	client := env.convergedClient(t)
	pe, _, _ := seededIDs(t, env)

	_, err := client.Clusters.Get(context.Background(), pe)
	require.Error(t, err)
	require.True(t, converged.IsInternal(err), err.Error())
}

func TestIfMatchIsEnforced(t *testing.T) {
	env := newTestEnv(t, DefaultConfig())
	pe, subnet, image := seededIDs(t, env)
	client := env.convergedClient(t)
	vm, err := client.VMs.Create(context.Background(), capxVM("etag", pe, subnet, image))
	require.NoError(t, err)
	path := "/api/vmm/v4.2/ahv/config/vms/" + *vm.ExtId

	resp := env.rawRequest(t, http.MethodPost, path+"/$actions/power-on", nil)
	require.Equal(t, http.StatusPreconditionFailed, resp.StatusCode)

	resp = env.rawRequest(t, http.MethodPost, path+"/$actions/power-on", map[string]string{"If-Match": "\"stale\""})
	require.Equal(t, http.StatusPreconditionFailed, resp.StatusCode)

	get := env.rawRequest(t, http.MethodGet, path, nil)
	require.Equal(t, http.StatusOK, get.StatusCode)
	etag := get.Header.Get("ETag")
	require.NotEmpty(t, etag)
	var envelope struct {
		Reserved map[string]string `json:"$reserved"`
		Data     struct {
			Reserved map[string]string `json:"$reserved"`
		} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(get.Body).Decode(&envelope))
	require.Equal(t, etag, envelope.Reserved["ETag"], "envelope $reserved carries the ETag")
	require.Equal(t, etag, envelope.Data.Reserved["ETag"], "entity $reserved carries the ETag")
	require.Equal(t, "v4.r2", envelope.Data.Reserved["$fv"], "SDK-provided $reserved fields are preserved")

	resp = env.rawRequest(t, http.MethodPost, path+"/$actions/power-on", map[string]string{"If-Match": etag})
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
}

func TestUnsupportedPathsFailLoudly(t *testing.T) {
	env := newTestEnv(t, DefaultConfig())
	resp := env.rawRequest(t, http.MethodGet, "/api/vmm/v4.2/content/templates", nil)
	require.Equal(t, http.StatusNotImplemented, resp.StatusCode)
	resp = env.rawRequest(t, http.MethodGet, "/api/nutanix/v3/projects/list", nil)
	require.Equal(t, http.StatusNotImplemented, resp.StatusCode)

	resp = env.rawRequest(t, http.MethodOptions, "/api/clustermgmt/unversioned/info", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.JSONEq(t, `{"data":"v4.2"}`, string(body))
}

func TestAuthentication(t *testing.T) {
	env := newTestEnv(t, DefaultConfig())
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, env.server.URL+"/api/clustermgmt/v4.2/config/clusters", nil)
	require.NoError(t, err)
	resp, err := env.rawClient().Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	healthz := env.rawRequest(t, http.MethodGet, "/-/healthz", nil)
	require.Equal(t, http.StatusOK, healthz.StatusCode)
}

func TestLatencyInjection(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Faults.Latency = 150 * time.Millisecond
	env := newTestEnv(t, cfg)
	start := time.Now()
	resp := env.rawRequest(t, http.MethodGet, "/api/clustermgmt/v4.2/config/clusters", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.GreaterOrEqual(t, time.Since(start), cfg.Faults.Latency)
}
