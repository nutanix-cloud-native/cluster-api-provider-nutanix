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

// ntnx-sim serves a simulated Prism Central for running CAPX without hardware.
//
// Point CAPX at it with NutanixCluster.spec.prismCentral (address, port,
// credentialRef, and either insecure: true or additionalTrustBundle set to
// the certificate written by --tls-cert-out).
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/nutanix-cloud-native/cluster-api-provider-nutanix/test/simulator"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ntnx-sim:", err)
		os.Exit(1)
	}
}

type flags struct {
	listen     string
	configPath string
	tlsCert    string
	tlsKey     string
	tlsCertOut string
	tlsHosts   string
	username   string
	password   string
	logLevel   string

	vmCreate   time.Duration
	vmPowerOn  time.Duration
	vmDelete   time.Duration
	vmUpdate   time.Duration
	latency    time.Duration
	rateLimit  int
	internal   int
	taskFail   int
	maxVMs     int
	apiVersion string
	profiler   string
}

func parseFlags() *flags {
	f := &flags{}
	flag.StringVar(&f.listen, "listen", ":9440", "address to listen on")
	flag.StringVar(&f.configPath, "config", "", "YAML config file (seed entities, timing, faults); see simulator.Config")
	flag.StringVar(&f.tlsCert, "tls-cert", "", "PEM certificate to serve; a self-signed one is generated when empty")
	flag.StringVar(&f.tlsKey, "tls-key", "", "PEM private key for --tls-cert")
	flag.StringVar(&f.tlsCertOut, "tls-cert-out", "", "write the (generated) certificate PEM here, for use as CAPX's additionalTrustBundle")
	flag.StringVar(&f.tlsHosts, "tls-hosts", "localhost,127.0.0.1", "comma-separated hosts for the generated certificate")
	flag.StringVar(&f.username, "username", "", "required basic-auth username (overrides config; empty accepts anything)")
	flag.StringVar(&f.password, "password", "", "required basic-auth password (overrides config)")
	flag.StringVar(&f.logLevel, "log-level", "info", "debug logs every request")
	flag.DurationVar(&f.vmCreate, "vm-create-duration", -1, "override task duration for VM create")
	flag.DurationVar(&f.vmPowerOn, "vm-power-on-duration", -1, "override task duration for VM power on/off")
	flag.DurationVar(&f.vmDelete, "vm-delete-duration", -1, "override task duration for VM delete")
	flag.DurationVar(&f.vmUpdate, "vm-update-duration", -1, "override task duration for VM update (custom attributes)")
	flag.DurationVar(&f.latency, "latency", -1, "override per-request latency")
	flag.IntVar(&f.rateLimit, "rate-limit-every", -1, "override: answer 429 to every Nth request")
	flag.IntVar(&f.internal, "internal-error-every", -1, "override: answer 500 to every Nth request")
	flag.IntVar(&f.taskFail, "task-failure-every", -1, "override: fail every Nth task")
	flag.IntVar(&f.maxVMs, "max-vms", -1, "override: reject VM creation beyond this many VMs")
	flag.StringVar(&f.apiVersion, "api-version", "", "override the v4 API version reported to SDK negotiation")
	flag.StringVar(&f.profiler, "profiler-address", ":6060", "pprof listen address; empty disables")
	flag.Parse()
	return f
}

func (f *flags) apply(cfg *simulator.Config) {
	if f.username != "" {
		cfg.Auth = simulator.Auth{Username: f.username, Password: f.password}
	}
	if f.vmCreate >= 0 {
		cfg.Timing.VMCreate = f.vmCreate
	}
	if f.vmPowerOn >= 0 {
		cfg.Timing.VMPowerOn = f.vmPowerOn
	}
	if f.vmDelete >= 0 {
		cfg.Timing.VMDelete = f.vmDelete
	}
	if f.vmUpdate >= 0 {
		cfg.Timing.VMUpdate = f.vmUpdate
	}
	if f.latency >= 0 {
		cfg.Faults.Latency = f.latency
	}
	if f.rateLimit >= 0 {
		cfg.Faults.RateLimitEvery = f.rateLimit
	}
	if f.internal >= 0 {
		cfg.Faults.InternalErrorEvery = f.internal
	}
	if f.taskFail >= 0 {
		cfg.Faults.TaskFailureEvery = f.taskFail
	}
	if f.maxVMs >= 0 {
		cfg.Limits.MaxVMs = f.maxVMs
	}
	if f.apiVersion != "" {
		cfg.APIVersion = f.apiVersion
	}
}

func run() error {
	f := parseFlags()
	var level slog.Level
	if err := level.UnmarshalText([]byte(f.logLevel)); err != nil {
		return fmt.Errorf("invalid --log-level: %w", err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	cfg, err := simulator.LoadConfig(f.configPath)
	if err != nil {
		return err
	}
	f.apply(&cfg)

	cert, err := f.certificate()
	if err != nil {
		return err
	}

	sim, err := simulator.New(cfg, simulator.WithLogger(logger))
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if f.profiler != "" {
		go func() {
			_ = http.ListenAndServe(f.profiler, nil) //nolint:gosec // pprof, same as CAPI managers
		}()
	}
	logger.Info("ntnx-sim listening", "addr", f.listen,
		"clusters", len(cfg.Seed.Clusters), "subnets", len(cfg.Seed.Subnets), "images", len(cfg.Seed.Images),
		"vmCreate", cfg.Timing.VMCreate, "vmPowerOn", cfg.Timing.VMPowerOn, "vmDelete", cfg.Timing.VMDelete)
	return sim.ServeTLS(ctx, f.listen, cert)
}

func (f *flags) certificate() (tls.Certificate, error) {
	if f.tlsCert != "" || f.tlsKey != "" {
		if f.tlsCert == "" || f.tlsKey == "" {
			return tls.Certificate{}, fmt.Errorf("--tls-cert and --tls-key must be set together")
		}
		cert, err := tls.LoadX509KeyPair(f.tlsCert, f.tlsKey)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("loading certificate: %w", err)
		}
		if f.tlsCertOut != "" {
			raw, err := os.ReadFile(f.tlsCert)
			if err != nil {
				return tls.Certificate{}, err
			}
			if err := os.WriteFile(f.tlsCertOut, raw, 0o600); err != nil {
				return tls.Certificate{}, err
			}
		}
		return cert, nil
	}
	certPEM, keyPEM, err := simulator.GenerateSelfSignedCert(strings.Split(f.tlsHosts, ","))
	if err != nil {
		return tls.Certificate{}, err
	}
	if f.tlsCertOut != "" {
		if err := os.WriteFile(f.tlsCertOut, certPEM, 0o600); err != nil {
			return tls.Certificate{}, fmt.Errorf("writing --tls-cert-out: %w", err)
		}
	}
	return tls.X509KeyPair(certPEM, keyPEM)
}
