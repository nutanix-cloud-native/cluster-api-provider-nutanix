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

// simrun runs CAPX without hardware: it stands up envtest (kube-apiserver and
// etcd), the real CAPI core manager, the CAPX manager and ntnx-sim, then
// drives a Cluster with N Machines to the Provisioned phase and deletes it
// again, printing timings and the simulator's request statistics.
//
// Run it through `make test-sim`, which builds the three binaries and points
// KUBEBUILDER_ASSETS at the envtest binaries.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/utils/ptr"
	capiv1beta1 "sigs.k8s.io/cluster-api/api/core/v1beta1" //nolint:staticcheck // NutanixClusterSpec.ControlPlaneEndpoint is still the v1beta1 type
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	credentials "github.com/nutanix-cloud-native/prism-go-client/environment/credentials"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	"github.com/nutanix-cloud-native/cluster-api-provider-nutanix/test/simulator"
)

const (
	simAddr  = "127.0.0.1:9440"
	simUser  = "admin"
	simPass  = "sim-secret"
	ns       = "sim-test"
	clusterN = "c1"
)

var (
	repoRoot   = flag.String("repo", ".", "CAPX checkout (needs config/crd/bases)")
	binDir     = flag.String("bin", "bin", "directory holding the manager, capi-manager and ntnx-sim binaries")
	assets     = flag.String("assets", os.Getenv("KUBEBUILDER_ASSETS"), "envtest binaries (default $KUBEBUILDER_ASSETS; see `setup-envtest use -p path`)")
	workdir    = flag.String("workdir", "", "working directory for logs and certs")
	machines   = flag.Int("machines", 1, "number of machines to create")
	vmCreate   = flag.Duration("vm-create", 0, "ntnx-sim VM create task duration")
	vmPowerOn  = flag.Duration("vm-power-on", 0, "ntnx-sim VM power-on task duration")
	vmDelete   = flag.Duration("vm-delete", 0, "ntnx-sim VM delete task duration")
	timeout    = flag.Duration("timeout", 10*time.Minute, "overall timeout")
	concurrent = flag.Int("max-concurrent-reconciles", 10, "CAPX --max-concurrent-reconciles")
	keep       = flag.Bool("keep", false, "do not delete the cluster at the end")
)

type procs struct {
	list []*exec.Cmd
}

func (p *procs) start(name, dir string, env []string, bin string, args ...string) error {
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), env...)
	logf, err := os.Create(filepath.Join(dir, name+".log"))
	if err != nil {
		return err
	}
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting %s: %w", name, err)
	}
	p.list = append(p.list, cmd)
	fmt.Printf("started %s (pid %d)\n", name, cmd.Process.Pid)
	return nil
}

func (p *procs) stopAll() {
	for i := len(p.list) - 1; i >= 0; i-- {
		_ = p.list[i].Process.Signal(os.Interrupt)
	}
	for _, c := range p.list {
		done := make(chan struct{})
		go func() { _ = c.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			_ = c.Process.Kill()
		}
	}
}

func main() {
	flag.Parse()
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "simrun:", err)
		os.Exit(1)
	}
}

func run() error {
	if *assets == "" {
		return fmt.Errorf("--assets (or KUBEBUILDER_ASSETS) must point at envtest binaries")
	}
	if *workdir == "" {
		*workdir = filepath.Join(os.TempDir(), fmt.Sprintf("simrun-%d", time.Now().Unix()))
	}
	if err := os.MkdirAll(*workdir, 0o755); err != nil {
		return err
	}
	fmt.Println("workdir:", *workdir)
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	cfg, env, err := startEnvtest()
	if err != nil {
		return err
	}
	defer func() { _ = env.Stop() }()

	p := &procs{}
	defer p.stopAll()
	if err := startProcesses(ctx, p, cfg); err != nil {
		return err
	}

	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)
	_ = infrav1.AddToScheme(scheme)
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return err
	}

	provisionedIn, err := provision(ctx, c)
	if err != nil {
		return err
	}
	if *keep {
		fmt.Println("--keep set; leaving everything running until timeout or Ctrl-C")
		<-ctx.Done()
		return nil
	}
	deletedIn, err := teardown(ctx, c)
	if err != nil {
		return err
	}
	fmt.Printf("\nRESULT: %d machine(s) provisioned in %s and deleted in %s through the unmodified CAPX manager against ntnx-sim\n",
		*machines, provisionedIn.Round(time.Millisecond), deletedIn.Round(time.Millisecond))
	return nil
}

// startEnvtest starts kube-apiserver and etcd with the CAPI and CAPX CRDs.
func startEnvtest() (*rest.Config, *envtest.Environment, error) {
	capiCRDs, err := capiCRDDir()
	if err != nil {
		return nil, nil, err
	}
	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join(*repoRoot, "config/crd/bases"), capiCRDs},
		BinaryAssetsDirectory: *assets,
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	if err != nil {
		return nil, nil, fmt.Errorf("starting envtest: %w", err)
	}
	// kustomize normally stamps the CAPI contract-version label on the CAPX
	// CRDs; CAPI core refuses to resolve infrastructure refs without it.
	if err := labelCAPXCRDs(cfg); err != nil {
		_ = env.Stop()
		return nil, nil, err
	}
	fmt.Println("kube-apiserver:", cfg.Host)
	return cfg, env, nil
}

// startProcesses launches ntnx-sim, the CAPI core manager and the CAPX
// manager and waits for their health endpoints.
func startProcesses(ctx context.Context, p *procs, cfg *rest.Config) error {
	// Webhook serving certs for both managers (nothing calls them; envtest
	// installs no webhook configurations).
	certDir := filepath.Join(*workdir, "webhook-certs")
	if err := os.MkdirAll(certDir, 0o755); err != nil {
		return err
	}
	certPEM, keyPEM, err := simulator.GenerateSelfSignedCert([]string{"localhost", "127.0.0.1"})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(certDir, "tls.crt"), certPEM, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(certDir, "tls.key"), keyPEM, 0o600); err != nil {
		return err
	}
	kubeconfig := filepath.Join(*workdir, "kubeconfig")
	if err := writeKubeconfig(cfg, kubeconfig); err != nil {
		return err
	}
	bin, err := filepath.Abs(*binDir)
	if err != nil {
		return err
	}
	if err := p.start("ntnx-sim", *workdir, nil, filepath.Join(bin, "ntnx-sim"),
		"--listen", simAddr, "--username", simUser, "--password", simPass,
		"--tls-cert-out", filepath.Join(*workdir, "sim-ca.pem"),
		"--vm-create-duration", vmCreate.String(), "--vm-power-on-duration", vmPowerOn.String(),
		"--vm-delete-duration", vmDelete.String(), "--log-level", "debug"); err != nil {
		return err
	}
	if err := waitHTTP(ctx, "https://"+simAddr+"/-/healthz"); err != nil {
		return err
	}
	kenv := []string{"KUBECONFIG=" + kubeconfig}
	if err := p.start("capi-manager", *workdir, kenv, filepath.Join(bin, "capi-manager"),
		"--leader-elect=false", "--webhook-port=29443", "--webhook-cert-dir="+certDir,
		"--health-addr=127.0.0.1:29441", "--diagnostics-address=127.0.0.1:28443", "--insecure-diagnostics", "-v=2"); err != nil {
		return err
	}
	if err := p.start("capx-manager", *workdir, kenv, filepath.Join(bin, "manager"),
		"--leader-elect=false", "--webhook-port=19443", "--webhook-cert-dir="+certDir,
		"--health-addr=127.0.0.1:19441", "--diagnostics-address=127.0.0.1:18443", "--insecure-diagnostics",
		fmt.Sprintf("--max-concurrent-reconciles=%d", *concurrent)); err != nil {
		return err
	}
	if err := waitHTTP(ctx, "http://127.0.0.1:29441/healthz"); err != nil {
		return fmt.Errorf("capi-manager: %w (see %s)", err, filepath.Join(*workdir, "capi-manager.log"))
	}
	if err := waitHTTP(ctx, "http://127.0.0.1:19441/healthz"); err != nil {
		return fmt.Errorf("capx-manager: %w (see %s)", err, filepath.Join(*workdir, "capx-manager.log"))
	}
	fmt.Println("all processes healthy")
	return nil
}

// provision creates the cluster and machines and waits for every Machine to
// reach Provisioned. It returns how long the machines took.
func provision(ctx context.Context, c client.Client) (time.Duration, error) {
	start := time.Now()
	if err := createCluster(ctx, c); err != nil {
		return 0, err
	}
	if err := waitFor(ctx, "NutanixCluster ready and Cluster infrastructure provisioned", func() (bool, string, error) {
		nc := &infrav1.NutanixCluster{}
		if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: clusterN}, nc); err != nil {
			return false, "", err
		}
		cl := &clusterv1.Cluster{}
		if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: clusterN}, cl); err != nil {
			return false, "", err
		}
		prov := cl.Status.Initialization.InfrastructureProvisioned != nil && *cl.Status.Initialization.InfrastructureProvisioned
		return nc.Status.Ready && prov, fmt.Sprintf("nutanixCluster.ready=%v cluster.infrastructureProvisioned=%v phase=%s", nc.Status.Ready, prov, cl.Status.Phase), nil
	}); err != nil {
		return 0, err
	}
	fmt.Printf("cluster infrastructure ready after %s\n", time.Since(start).Round(time.Millisecond))

	mstart := time.Now()
	for i := 0; i < *machines; i++ {
		if err := createMachine(ctx, c, i); err != nil {
			return 0, err
		}
	}
	fmt.Printf("created %d machines\n", *machines)
	if err := waitFor(ctx, fmt.Sprintf("%d machines Provisioned", *machines), func() (bool, string, error) {
		return machinesProvisioned(ctx, c)
	}); err != nil {
		return 0, err
	}
	provisionedIn := time.Since(mstart)
	fmt.Printf("all %d machines Provisioned after %s\n", *machines, provisionedIn.Round(time.Millisecond))
	printMachines(ctx, c)
	printSimStats("after provisioning")
	return provisionedIn, nil
}

// teardown deletes the Cluster and waits for CAPI and CAPX to remove every
// Machine and VM. It returns how long deletion took.
func teardown(ctx context.Context, c client.Client) (time.Duration, error) {
	dstart := time.Now()
	cl := &clusterv1.Cluster{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: clusterN}}
	if err := c.Delete(ctx, cl); err != nil {
		return 0, err
	}
	if err := waitFor(ctx, "cluster and machines deleted", func() (bool, string, error) {
		nms := &infrav1.NutanixMachineList{}
		if err := c.List(ctx, nms, client.InNamespace(ns)); err != nil {
			return false, "", err
		}
		err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: clusterN}, &clusterv1.Cluster{})
		gone := apierrors.IsNotFound(err)
		return gone && len(nms.Items) == 0, fmt.Sprintf("nutanixMachines=%d clusterGone=%v", len(nms.Items), gone), nil
	}); err != nil {
		return 0, err
	}
	deletedIn := time.Since(dstart)
	fmt.Printf("cluster deleted after %s\n", deletedIn.Round(time.Millisecond))
	printSimStats("after deletion")
	return deletedIn, nil
}

func createCluster(ctx context.Context, c client.Client) error {
	objs := []client.Object{
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: clusterN + "-pc-creds"},
			StringData: map[string]string{"credentials": fmt.Sprintf(
				`[{"type":"basic_auth","data":{"prismCentral":{"username":%q,"password":%q},"prismElements":null}}]`, simUser, simPass)},
		},
		&clusterv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: clusterN},
			Spec: clusterv1.ClusterSpec{
				ControlPlaneEndpoint: clusterv1.APIEndpoint{Host: "10.10.255.1", Port: 6443},
				InfrastructureRef: clusterv1.ContractVersionedObjectReference{
					APIGroup: infrav1.GroupVersion.Group, Kind: "NutanixCluster", Name: clusterN,
				},
			},
		},
		&infrav1.NutanixCluster{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: clusterN},
			Spec: infrav1.NutanixClusterSpec{
				ControlPlaneEndpoint: capiv1beta1.APIEndpoint{Host: "10.10.255.1", Port: 6443},
				PrismCentral: &credentials.NutanixPrismEndpoint{
					Address:  "127.0.0.1",
					Port:     9440,
					Insecure: true,
					CredentialRef: &credentials.NutanixCredentialReference{
						Kind: credentials.SecretKind, Name: clusterN + "-pc-creds", Namespace: ns,
					},
				},
			},
		},
	}
	for _, o := range objs {
		if err := c.Create(ctx, o); err != nil {
			return fmt.Errorf("creating %T %s: %w", o, o.GetName(), err)
		}
	}
	return nil
}

func createMachine(ctx context.Context, c client.Client, i int) error {
	name := fmt.Sprintf("%s-md-0-%03d", clusterN, i)
	objs := []client.Object{
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name + "-bootstrap"},
			StringData: map[string]string{"value": "## template: jinja\n#cloud-config\nhostname: {{ ds.meta_data.hostname }}\nruncmd:\n- kubeadm join --config /run/kubeadm/kubeadm-join-config.yaml\n"},
		},
		&infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
			Spec: infrav1.NutanixMachineSpec{
				VCPUsPerSocket: 1,
				VCPUSockets:    2,
				MemorySize:     resource.MustParse("4Gi"),
				SystemDiskSize: resource.MustParse("40Gi"),
				BootType:       infrav1.NutanixBootTypeLegacy,
				Image:          &infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierName, Name: ptr.To("ubuntu-sim")},
				Cluster:        infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierName, Name: ptr.To("pe-sim")},
				Subnets:        []infrav1.NutanixResourceIdentifier{{Type: infrav1.NutanixIdentifierName, Name: ptr.To("subnet-sim")}},
			},
		},
		&clusterv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: ns, Name: name,
				Labels: map[string]string{clusterv1.ClusterNameLabel: clusterN},
			},
			Spec: clusterv1.MachineSpec{
				ClusterName: clusterN,
				Version:     "v1.33.0",
				Bootstrap:   clusterv1.Bootstrap{DataSecretName: ptr.To(name + "-bootstrap")},
				InfrastructureRef: clusterv1.ContractVersionedObjectReference{
					APIGroup: infrav1.GroupVersion.Group, Kind: "NutanixMachine", Name: name,
				},
			},
		},
	}
	for _, o := range objs {
		if err := c.Create(ctx, o); err != nil {
			return fmt.Errorf("creating %T %s: %w", o, o.GetName(), err)
		}
	}
	return nil
}

func machinesProvisioned(ctx context.Context, c client.Client) (bool, string, error) {
	ml := &clusterv1.MachineList{}
	if err := c.List(ctx, ml, client.InNamespace(ns)); err != nil {
		return false, "", err
	}
	nml := &infrav1.NutanixMachineList{}
	if err := c.List(ctx, nml, client.InNamespace(ns)); err != nil {
		return false, "", err
	}
	phases := map[string]int{}
	provisioned := 0
	for _, m := range ml.Items {
		phases[string(m.Status.Phase)]++
		if m.Status.Phase == string(clusterv1.MachinePhaseProvisioned) && m.Spec.ProviderID != "" {
			provisioned++
		}
	}
	ready, withAddr := 0, 0
	for _, nm := range nml.Items {
		if nm.Status.Ready {
			ready++
		}
		if len(nm.Status.Addresses) > 0 {
			withAddr++
		}
	}
	keys := make([]string, 0, len(phases))
	for k := range phases {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, phases[k]))
	}
	stats := simStats()
	return provisioned == *machines,
		fmt.Sprintf("machines[%s] nutanixMachines(ready=%d addresses=%d) sim(vms=%d on=%d tasksPending=%d)",
			strings.Join(parts, " "), ready, withAddr, stats.VMs, stats.VMsPoweredOn, stats.TasksPending), nil
}

func printMachines(ctx context.Context, c client.Client) {
	nml := &infrav1.NutanixMachineList{}
	if err := c.List(ctx, nml, client.InNamespace(ns)); err != nil {
		return
	}
	sort.Slice(nml.Items, func(i, j int) bool { return nml.Items[i].Name < nml.Items[j].Name })
	limit := len(nml.Items)
	if limit > 5 {
		limit = 5
	}
	for _, nm := range nml.Items[:limit] {
		var addrs []string
		for _, a := range nm.Status.Addresses {
			addrs = append(addrs, fmt.Sprintf("%s=%s", a.Type, a.Address))
		}
		fmt.Printf("  %s ready=%v vmUUID=%s providerID=%s addresses=[%s]\n", nm.Name, nm.Status.Ready, nm.Status.VmUUID, nm.Spec.ProviderID, strings.Join(addrs, " "))
	}
	if len(nml.Items) > limit {
		fmt.Printf("  ... and %d more\n", len(nml.Items)-limit)
	}
}

func simStats() simulator.Snapshot {
	var snap simulator.Snapshot
	resp, err := insecureClient().Get("https://" + simAddr + "/-/stats")
	if err != nil {
		return snap
	}
	defer func() { _ = resp.Body.Close() }()
	_ = json.NewDecoder(resp.Body).Decode(&snap)
	return snap
}

func printSimStats(label string) {
	snap := simStats()
	fmt.Printf("ntnx-sim stats %s: vms=%d poweredOn=%d tasksTotal=%d tasksPending=%d\n", label, snap.VMs, snap.VMsPoweredOn, snap.TasksTotal, snap.TasksPending)
	for _, r := range snap.Routes {
		if !strings.Contains(r, "/api/") {
			continue
		}
		rs := snap.Requests[r]
		fmt.Printf("  %-70s %6d  mean %.2fms  %v\n", r, rs.Count, rs.MeanMillis, rs.ByStatus)
	}
	for op, ts := range snap.Tasks {
		fmt.Printf("  task %-14s succeeded=%d failed=%d\n", op, ts.Succeeded, ts.Failed)
	}
}

func waitFor(ctx context.Context, what string, check func() (bool, string, error)) error {
	last := ""
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	report := time.Now()
	for {
		ok, status, err := check()
		if err != nil {
			fmt.Printf("  waiting for %s: error: %v\n", what, err)
		} else if ok {
			fmt.Printf("  %s: %s\n", what, status)
			return nil
		} else if status != last || time.Since(report) > 10*time.Second {
			fmt.Printf("  waiting for %s: %s\n", what, status)
			last, report = status, time.Now()
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for %s (last: %s)", what, last)
		case <-tick.C:
		}
	}
}

func insecureClient() *http.Client {
	return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
}

func waitHTTP(ctx context.Context, url string) error {
	for {
		resp, err := insecureClient().Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s never became healthy: %v", url, err)
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// capiCRDDir locates the CAPI core CRDs inside the module cache, so the
// installed CRDs match the CAPI version CAPX is built against.
func capiCRDDir() (string, error) {
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "sigs.k8s.io/cluster-api")
	cmd.Dir = *repoRoot
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("locating sigs.k8s.io/cluster-api module: %w", err)
	}
	return filepath.Join(string(bytes.TrimSpace(out)), "config", "crd", "bases"), nil
}

func labelCAPXCRDs(cfg *rest.Config) error {
	c, err := client.New(cfg, client.Options{})
	if err != nil {
		return err
	}
	ctx := context.Background()
	crds := &apiextensionsv1.CustomResourceDefinitionList{}
	if err := c.List(ctx, crds); err != nil {
		return err
	}
	for i := range crds.Items {
		crd := &crds.Items[i]
		if crd.Spec.Group != infrav1.GroupVersion.Group {
			continue
		}
		if crd.Labels == nil {
			crd.Labels = map[string]string{}
		}
		crd.Labels["cluster.x-k8s.io/v1beta1"] = "v1beta1"
		if err := c.Update(ctx, crd); err != nil {
			return fmt.Errorf("labelling CRD %s: %w", crd.Name, err)
		}
	}
	return nil
}

func writeKubeconfig(cfg *rest.Config, path string) error {
	kc := clientcmdapi.NewConfig()
	kc.Clusters["envtest"] = &clientcmdapi.Cluster{Server: cfg.Host, CertificateAuthorityData: cfg.CAData}
	kc.AuthInfos["envtest"] = &clientcmdapi.AuthInfo{ClientCertificateData: cfg.CertData, ClientKeyData: cfg.KeyData, Token: cfg.BearerToken}
	kc.Contexts["envtest"] = &clientcmdapi.Context{Cluster: "envtest", AuthInfo: "envtest"}
	kc.CurrentContext = "envtest"
	return clientcmd.WriteToFile(*kc, path)
}
