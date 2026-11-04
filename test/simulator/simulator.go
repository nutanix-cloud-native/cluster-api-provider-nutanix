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
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	vmmconfig "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/ahv/config"
)

// VMEvent describes a VM lifecycle transition delivered to Hooks. VM is a
// deep copy; Addresses lists the IPv4 addresses assigned to its NICs.
type VMEvent struct {
	VM        vmmconfig.Vm
	Addresses []string
}

// Hooks receive VM lifecycle events after the corresponding task has
// completed. They run outside the store lock and may call back into the
// simulator. A scale harness uses them to drive fake nodes.
type Hooks struct {
	OnVMCreated    func(context.Context, VMEvent)
	OnVMPoweredOn  func(context.Context, VMEvent)
	OnVMPoweredOff func(context.Context, VMEvent)
	OnVMDeleted    func(context.Context, VMEvent)
}

// Simulator is an in-memory Prism Central.
type Simulator struct {
	cfg   Config
	hooks Hooks
	log   *slog.Logger
	store *store
	mux   *http.ServeMux
	stats *stats

	reqSeq  atomic.Uint64
	taskSeq atomic.Uint64
}

// Option customises a Simulator.
type Option func(*Simulator)

// WithLogger sets the logger. Requests are logged at debug level.
func WithLogger(l *slog.Logger) Option { return func(s *Simulator) { s.log = l } }

// WithHooks registers lifecycle hooks.
func WithHooks(h Hooks) Option { return func(s *Simulator) { s.hooks = h } }

// New builds a simulator from cfg.
func New(cfg Config, opts ...Option) (*Simulator, error) {
	if cfg.APIVersion == "" {
		cfg.APIVersion = "v4.2"
	}
	st, err := newStore(cfg.Seed)
	if err != nil {
		return nil, fmt.Errorf("seeding store: %w", err)
	}
	s := &Simulator{
		cfg:   cfg,
		log:   slog.Default(),
		store: st,
		stats: newStats(),
	}
	for _, opt := range opts {
		opt(s)
	}
	s.mux = http.NewServeMux()
	s.registerRoutes()
	return s, nil
}

// Handler returns the HTTP handler serving the simulated APIs.
func (s *Simulator) Handler() http.Handler {
	return s.observe(s.faults(s.authenticate(s.mux)))
}

// Config returns the effective configuration.
func (s *Simulator) Config() Config { return s.cfg }

func (s *Simulator) registerRoutes() {
	m := s.mux
	m.HandleFunc("GET /-/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	m.HandleFunc("GET /-/stats", s.handleStats)

	// SDK version negotiation: every v4 SDK module probes this before its
	// first call and keeps probing until it gets an answer.
	m.HandleFunc("OPTIONS /api/{namespace}/unversioned/info", s.handleVersionInfo)

	// prism-go-client's v3 client logs in at construction time when session
	// auth is enabled, which CAPX hard-codes. Nothing else v3 is served.
	m.HandleFunc("GET /api/nutanix/v3/users/me", s.handleV3UsersMe)
	m.HandleFunc("/api/nutanix/v3/", s.handleV3NotImplemented)

	s.registerVMMRoutes()
	s.registerClusterMgmtRoutes()
	s.registerNetworkingRoutes()
	s.registerPrismRoutes()

	m.HandleFunc("/api/", writeNotImplemented)
	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
}

func (s *Simulator) handleVersionInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, "", map[string]any{"data": s.cfg.APIVersion})
}

func (s *Simulator) handleV3UsersMe(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "NTNX_IGW_SESSION",
		Value:    uuid.NewString(),
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
	})
	username := s.cfg.Auth.Username
	if username == "" {
		username = "admin"
	}
	writeJSON(w, http.StatusOK, "", map[string]any{
		"api_version": "3.1",
		"metadata":    map[string]any{"kind": "user", "uuid": uuid.NewString()},
		"spec":        map[string]any{"resources": map[string]any{"user_type": "LOCAL"}},
		"status": map[string]any{
			"state":     "COMPLETE",
			"name":      username,
			"resources": map[string]any{"user_type": "LOCAL", "display_name": username},
		},
	})
}

func (s *Simulator) handleV3NotImplemented(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotImplemented, "", map[string]any{
		"api_version": "3.1",
		"code":        http.StatusNotImplemented,
		"state":       "ERROR",
		"message_list": []map[string]any{{
			"message": fmt.Sprintf("ntnx-sim does not implement v3 %s %s; CAPX should be using v4 here", r.Method, r.URL.Path),
			"reason":  "NOT_IMPLEMENTED",
		}},
	})
}

// authenticate enforces Config.Auth on /api paths.
func (s *Simulator) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Auth.Username == "" || !strings.HasPrefix(r.URL.Path, "/api/") || s.credentialsValid(r) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="ntnx-sim"`)
		writeError(w, http.StatusUnauthorized, "AUTHENTICATION_ERROR", "invalid credentials")
	})
}

func (s *Simulator) credentialsValid(r *http.Request) bool {
	if s.cfg.Auth.APIKey != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-ntnx-api-key")), []byte(s.cfg.Auth.APIKey)) == 1 {
		return true
	}
	if _, err := r.Cookie("NTNX_IGW_SESSION"); err == nil && r.Header.Get("Authorization") == "" {
		// Session cookies are issued by /users/me after a successful login.
		return true
	}
	user, pass, ok := r.BasicAuth()
	return ok &&
		subtle.ConstantTimeCompare([]byte(user), []byte(s.cfg.Auth.Username)) == 1 &&
		subtle.ConstantTimeCompare([]byte(pass), []byte(s.cfg.Auth.Password)) == 1
}

// faults applies latency and periodic error injection to /api paths.
func (s *Simulator) faults(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		if s.cfg.Faults.Latency > 0 {
			select {
			case <-time.After(s.cfg.Faults.Latency):
			case <-r.Context().Done():
				return
			}
		}
		n := s.reqSeq.Add(1)
		if every := s.cfg.Faults.RateLimitEvery; every > 0 && n%uint64(every) == 0 {
			s.stats.faultInjected("rate_limit")
			writeError(w, http.StatusTooManyRequests, "RATE_LIMIT_EXCEEDED", "injected rate limit")
			return
		}
		if every := s.cfg.Faults.InternalErrorEvery; every > 0 && n%uint64(every) == 0 {
			s.stats.faultInjected("internal_error")
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "injected internal error")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// observe records per-route statistics and debug-logs every request.
func (s *Simulator) observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		route := r.Pattern
		if route == "" {
			route = r.Method + " " + r.URL.Path
		}
		s.stats.request(route, rec.status, time.Since(start))
		s.log.Debug("request", "method", r.Method, "path", r.URL.RequestURI(), "status", rec.status, "duration", time.Since(start))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// stats aggregates request and task counters for /-/stats.
type stats struct {
	mu       sync.Mutex
	started  time.Time
	requests map[string]*routeStats
	tasks    map[string]*taskStats
	faults   map[string]uint64
}

type routeStats struct {
	Count      uint64            `json:"count"`
	ByStatus   map[string]uint64 `json:"byStatus"`
	TotalNanos int64             `json:"-"`
	MeanMillis float64           `json:"meanMillis"`
}

type taskStats struct {
	Succeeded uint64 `json:"succeeded"`
	Failed    uint64 `json:"failed"`
}

func newStats() *stats {
	return &stats{
		started:  time.Now(),
		requests: map[string]*routeStats{},
		tasks:    map[string]*taskStats{},
		faults:   map[string]uint64{},
	}
}

func (st *stats) request(route string, status int, d time.Duration) {
	st.mu.Lock()
	defer st.mu.Unlock()
	rs := st.requests[route]
	if rs == nil {
		rs = &routeStats{ByStatus: map[string]uint64{}}
		st.requests[route] = rs
	}
	rs.Count++
	rs.ByStatus[fmt.Sprint(status)]++
	rs.TotalNanos += d.Nanoseconds()
	rs.MeanMillis = float64(rs.TotalNanos) / float64(rs.Count) / 1e6
}

func (st *stats) taskCompleted(operation string, failed bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	ts := st.tasks[operation]
	if ts == nil {
		ts = &taskStats{}
		st.tasks[operation] = ts
	}
	if failed {
		ts.Failed++
	} else {
		ts.Succeeded++
	}
}

func (st *stats) faultInjected(kind string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.faults[kind]++
}

// Snapshot is the JSON document served at /-/stats.
type Snapshot struct {
	UptimeSeconds float64                `json:"uptimeSeconds"`
	VMs           int                    `json:"vms"`
	VMsPoweredOn  int                    `json:"vmsPoweredOn"`
	TasksPending  int                    `json:"tasksPending"`
	TasksTotal    int                    `json:"tasksTotal"`
	Categories    int                    `json:"categories"`
	Requests      map[string]*routeStats `json:"requests"`
	Tasks         map[string]*taskStats  `json:"tasks"`
	Faults        map[string]uint64      `json:"faults"`
	Routes        []string               `json:"routes"`
}

// Stats returns a snapshot of counters and store sizes.
func (s *Simulator) Stats() Snapshot {
	s.store.mu.RLock()
	snap := Snapshot{
		VMs:        len(s.store.vms),
		TasksTotal: len(s.store.tasks),
		Categories: len(s.store.categories),
	}
	for _, rec := range s.store.vms {
		if rec.vm.PowerState != nil && *rec.vm.PowerState == vmmconfig.POWERSTATE_ON {
			snap.VMsPoweredOn++
		}
	}
	for _, rec := range s.store.tasks {
		if rec.task.CompletedTime == nil {
			snap.TasksPending++
		}
	}
	s.store.mu.RUnlock()

	s.stats.mu.Lock()
	defer s.stats.mu.Unlock()
	snap.UptimeSeconds = time.Since(s.stats.started).Seconds()
	snap.Requests = make(map[string]*routeStats, len(s.stats.requests))
	for k, v := range s.stats.requests {
		cp := *v
		cp.ByStatus = make(map[string]uint64, len(v.ByStatus))
		for sk, sv := range v.ByStatus {
			cp.ByStatus[sk] = sv
		}
		snap.Requests[k] = &cp
		snap.Routes = append(snap.Routes, k)
	}
	sort.Strings(snap.Routes)
	snap.Tasks = make(map[string]*taskStats, len(s.stats.tasks))
	for k, v := range s.stats.tasks {
		cp := *v
		snap.Tasks[k] = &cp
	}
	snap.Faults = make(map[string]uint64, len(s.stats.faults))
	for k, v := range s.stats.faults {
		snap.Faults[k] = v
	}
	return snap
}

func (s *Simulator) handleStats(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(s.Stats())
}

// deepCopyVM returns an independent copy of vm via JSON round-trip, which is
// safe for every SDK model.
func deepCopyVM(vm *vmmconfig.Vm) (vmmconfig.Vm, error) {
	raw, err := json.Marshal(vm)
	if err != nil {
		return vmmconfig.Vm{}, err
	}
	var out vmmconfig.Vm
	if err := json.Unmarshal(raw, &out); err != nil {
		return vmmconfig.Vm{}, err
	}
	return out, nil
}
