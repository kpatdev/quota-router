// Package plugin implements the CPA plugin RPC surface: lifecycle,
// scheduler.pick, usage.handle, and management routes, plus the background
// roster sync, quota poller, and state persistence.
package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kpatdev/quota-router/internal/config"
	"github.com/kpatdev/quota-router/internal/mgmt"
	"github.com/kpatdev/quota-router/internal/quota"
	"github.com/kpatdev/quota-router/internal/router"
	"github.com/kpatdev/quota-router/internal/session"
	"github.com/kpatdev/quota-router/internal/store"
	"github.com/kpatdev/quota-router/internal/telemetry"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// Version is the plugin release version.
const Version = "1.0.0"

const (
	rosterEvery    = 30 * time.Second
	saveEvery      = time.Second
	historyEvery   = time.Minute
	housekeepEvery = 5 * time.Minute
	pollWorkers    = 3
	stateFile      = "state.json"
	historyFile    = "history.json"
)

// Host is the subset of CPA host callbacks the plugin uses.
type Host interface {
	List() ([]pluginapi.HostAuthFileEntry, error)
	Get(authIndex string) (json.RawMessage, error)
}

// Runtime is the plugin instance. The zero value plus Host is usable.
type Runtime struct {
	Host Host
	// Now and NewFetcher are replaced in tests.
	Now        func() time.Time
	NewFetcher func() quota.Fetcher

	mu          sync.Mutex
	cfg         config.Config
	router      *router.Router
	tel         *telemetry.Recorder
	store       *store.Store
	loadRes     string
	fetcher     quota.Fetcher
	server      *mgmt.Server
	cancel      context.CancelFunc
	done        chan struct{}
	wake        chan struct{}
	started     time.Time
	saveErr     string
	savedAt     time.Time
	panics      atomic.Uint64
	failed      atomic.Bool
	running     atomic.Bool
	rosterEmpty atomic.Bool
	configErr   string
}

func (p *Runtime) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func success(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		return failure("response_encode_failed")
	}
	out, _ := json.Marshal(pluginabi.Envelope{OK: true, Result: raw})
	return out
}

func failure(code string) []byte {
	raw, _ := json.Marshal(pluginabi.Envelope{Error: &pluginabi.Error{Code: code, Message: code}})
	return raw
}

// Handle answers one RPC call. It never panics: a panic in pick or usage
// handling returns "not handled" so CPA falls back to its own scheduler.
func (p *Runtime) Handle(method string, raw []byte) (out []byte) {
	defer func() {
		if recover() != nil {
			p.panics.Add(1)
			if method == pluginabi.MethodSchedulerPick {
				out = success(pluginapi.SchedulerPickResponse{})
				return
			}
			out = failure("internal_plugin_error")
		}
	}()
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		var req struct {
			ConfigYAML []byte `json:"config_yaml"`
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &req); err != nil {
				return failure("invalid_lifecycle_request")
			}
		}
		cfg, err := config.Decode(req.ConfigYAML)
		if err != nil {
			p.mu.Lock()
			p.configErr = err.Error()
			p.mu.Unlock()
			return failure("invalid_configuration")
		}
		if p.Host == nil {
			return failure("host_unavailable")
		}
		p.Configure(cfg)
		return success(registration(cfg))
	case pluginabi.MethodPluginQuiesce:
		p.Stop()
		return success(struct{}{})
	case pluginabi.MethodPluginShutdown:
		p.Shutdown()
		return success(struct{}{})
	case pluginabi.MethodSchedulerPick:
		var req pluginapi.SchedulerPickRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return success(pluginapi.SchedulerPickResponse{})
		}
		return success(p.Pick(req))
	case pluginabi.MethodUsageHandle:
		var rec pluginapi.UsageRecord
		if err := json.Unmarshal(raw, &rec); err == nil {
			p.Usage(rec)
		}
		return success(struct{}{})
	case pluginabi.MethodManagementRegister:
		return success(struct {
			Routes    []pluginapi.ManagementRoute `json:"routes"`
			Resources []pluginapi.ResourceRoute   `json:"resources"`
		}{mgmt.Routes(), mgmt.Resources()})
	case pluginabi.MethodManagementHandle:
		var req pluginapi.ManagementRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return success(pluginapi.ManagementResponse{StatusCode: http.StatusBadRequest})
		}
		srv := p.serverOrNil()
		if srv == nil {
			return success(pluginapi.ManagementResponse{StatusCode: http.StatusServiceUnavailable})
		}
		return success(srv.Handle(req))
	default:
		return failure("unsupported_method")
	}
}

func registration(cfg config.Config) any {
	var fields []pluginapi.ConfigField
	for _, f := range config.Schema() {
		t := pluginapi.ConfigFieldType(f.Type)
		if f.Type == "duration" {
			t = pluginapi.ConfigFieldTypeString
		}
		fields = append(fields, pluginapi.ConfigField{Name: f.Name, Type: t, EnumValues: f.Enum, Description: f.Description})
	}
	return struct {
		SchemaVersion uint32             `json:"schema_version"`
		Metadata      pluginapi.Metadata `json:"metadata"`
		Capabilities  map[string]bool    `json:"capabilities"`
	}{
		pluginabi.SchemaVersion,
		pluginapi.Metadata{
			Name: config.PluginID, Version: Version, Author: "kpatdev (fork of Shreyash/webdevcaptain's quota-reset-router)",
			GitHubRepository: "https://github.com/kpatdev/quota-router", ConfigFields: fields,
		},
		map[string]bool{
			"scheduler":                   true,
			"scheduler_across_priorities": cfg.ScheduleAcrossPriorities,
			"usage_plugin":                true,
			"management_api":              true,
		},
	}
}

func (p *Runtime) serverOrNil() *mgmt.Server {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.server
}

// Configure applies settings. The first call creates state (loading it from
// disk); later calls keep bindings, quota, and history.
func (p *Runtime) Configure(cfg config.Config) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.configErr = ""
	if p.router == nil {
		p.started = p.now()
		p.router = router.New(cfg, p.Now)
		p.tel = telemetry.New(cfg.HistoryRetention)
		p.openStoreLocked(cfg.StateDir)
		p.server = &mgmt.Server{Router: p.router, Telemetry: p.tel, Now: p.now, Info: p.info, Refresh: p.refresh}
	} else {
		p.router.Configure(cfg)
		p.tel.SetRetention(cfg.HistoryRetention)
	}
	p.cfg = cfg
	if p.cancel == nil {
		p.startLocked()
	}
}

func (p *Runtime) openStoreLocked(dir string) {
	s, err := store.Open(dir)
	if err != nil {
		p.loadRes = "unavailable"
		return
	}
	p.store = s
	var rs router.State
	res, _ := s.Load(stateFile, &rs)
	p.loadRes = string(res)
	if res == store.Loaded {
		p.router.Import(rs)
	}
	var hs telemetry.State
	if res, _ := s.Load(historyFile, &hs); res == store.Loaded {
		p.tel.Import(hs)
	}
}

func (p *Runtime) startLocked() {
	if p.NewFetcher != nil {
		p.fetcher = p.NewFetcher()
	} else {
		p.fetcher = quota.NewHTTPFetcher()
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan struct{})
	p.wake = make(chan struct{}, 1)
	done, wake, fetcher := p.done, p.wake, p.fetcher
	p.running.Store(true)
	go func() {
		defer close(done)
		defer p.running.Store(false)
		defer func() {
			if recover() != nil {
				p.failed.Store(true)
			}
		}()
		p.loop(ctx, wake, fetcher)
	}()
}

// Stop halts background work and flushes state. Picks keep working from memory.
func (p *Runtime) Stop() {
	p.mu.Lock()
	cancel, done, fetcher := p.cancel, p.done, p.fetcher
	p.cancel, p.done = nil, nil
	p.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	if f, ok := fetcher.(*quota.HTTPFetcher); ok {
		f.Close()
	}
	p.Flush()
}

// Shutdown stops, flushes, and releases the state lock.
func (p *Runtime) Shutdown() {
	p.Stop()
	p.mu.Lock()
	if p.store != nil {
		p.store.Close()
	}
	p.mu.Unlock()
}

// Flush writes state and history now.
func (p *Runtime) Flush() {
	p.mu.Lock()
	r, tel, st := p.router, p.tel, p.store
	p.mu.Unlock()
	if r == nil || st == nil {
		return
	}
	r.TakeDirty()
	p.recordSave(st.Save(stateFile, r.Export()))
	tel.TakeDirty()
	p.recordSave(st.Save(historyFile, tel.Export()))
}

func (p *Runtime) recordSave(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err != nil {
		p.saveErr = err.Error()
		return
	}
	p.saveErr = ""
	p.savedAt = p.now()
}

func (p *Runtime) loop(ctx context.Context, wake chan struct{}, fetcher quota.Fetcher) {
	p.SyncRoster()
	go p.pollLoop(ctx, wake, fetcher)
	tick := time.NewTicker(saveEvery)
	defer tick.Stop()
	var lastRoster, lastHistory, lastHousekeep, lastSample time.Time
	lastRoster = time.Now()
	started := lastRoster
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		now := time.Now()
		// CPA loads auth files after plugins register, so poll the roster
		// quickly until it shows accounts (or the first minute passes).
		every := rosterEvery
		if p.rosterEmpty.Load() && now.Sub(started) < time.Minute {
			every = 2 * time.Second
		}
		if now.Sub(lastRoster) >= every {
			lastRoster = now
			if p.SyncRoster() > 0 {
				p.wakePoller()
			}
		}
		p.mu.Lock()
		r, tel, st := p.router, p.tel, p.store
		p.mu.Unlock()
		if now.Sub(lastSample) >= time.Minute {
			lastSample = now
			p.sampleQuota()
		}
		if st != nil && r.TakeDirty() {
			p.recordSave(st.Save(stateFile, r.Export()))
		}
		if st != nil && now.Sub(lastHistory) >= historyEvery && tel.TakeDirty() {
			lastHistory = now
			p.recordSave(st.Save(historyFile, tel.Export()))
		}
		if now.Sub(lastHousekeep) >= housekeepEvery {
			lastHousekeep = now
			tel.Prune(p.now())
			r.Sweep()
		}
	}
}

func (p *Runtime) pollLoop(ctx context.Context, wake chan struct{}, fetcher quota.Fetcher) {
	for {
		p.PollDue(ctx, fetcher)
		wait := 30 * time.Second
		if next := p.router.NextPollAt(); !next.IsZero() {
			wait = min(wait, max(time.Second, next.Sub(p.now())))
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

// SyncRoster reads the credential list from CPA and returns how many
// Claude/Codex accounts it found.
func (p *Runtime) SyncRoster() int {
	entries, err := p.Host.List()
	if err != nil {
		p.router.RosterFailed("credential_list_failed")
		return 0
	}
	n := 0
	roster := make([]router.RosterEntry, 0, len(entries))
	for _, e := range entries {
		provider := e.Provider
		if provider == "" {
			provider = e.Type
		}
		if p := strings.ToLower(strings.TrimSpace(provider)); p == "claude" || p == "codex" {
			n++
		}
		roster = append(roster, router.RosterEntry{
			ID: e.ID, AuthIndex: e.AuthIndex, Provider: provider, Name: e.Name, Label: e.Label, Email: e.Email,
			Account: e.Account, AccountType: e.AccountType, Status: e.Status, StatusMsg: e.StatusMessage,
			Disabled: e.Disabled, Unavailable: e.Unavailable, RuntimeOnly: e.RuntimeOnly, Priority: e.Priority,
			Path: e.Path, Size: e.Size, ModTime: e.ModTime, BaseURL: e.BaseURL, NextRetry: e.NextRetryAfter,
		})
	}
	p.router.SyncRoster(roster)
	p.rosterEmpty.Store(n == 0)
	return n
}

func (p *Runtime) wakePoller() {
	p.mu.Lock()
	wake := p.wake
	p.mu.Unlock()
	if wake != nil {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}

// PollDue polls every account that is due, a few at a time.
func (p *Runtime) PollDue(ctx context.Context, fetcher quota.Fetcher) {
	tasks := p.router.DuePolls()
	if len(tasks) == 0 {
		return
	}
	sem := make(chan struct{}, pollWorkers)
	var wg sync.WaitGroup
	for _, t := range tasks {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(t router.PollTask) {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				if recover() != nil {
					p.panics.Add(1)
				}
			}()
			snap, plan, err := p.pollOne(ctx, fetcher, t)
			if ctx.Err() != nil {
				return
			}
			p.router.ApplyPoll(t.AuthID, snap, plan, err)
			if err == nil {
				for _, w := range snap.Windows {
					p.tel.Quota(w.ObservedAt, t.AuthID, w.Key(), w.UsedPercent, w.ResetsAt)
				}
			}
		}(t)
	}
	wg.Wait()
}

func (p *Runtime) pollOne(ctx context.Context, fetcher quota.Fetcher, t router.PollTask) (quota.Snapshot, string, error) {
	raw, err := p.Host.Get(t.AuthIndex)
	if err != nil {
		return quota.Snapshot{}, "", &quota.Error{Code: "credential_read_failed"}
	}
	var cred quota.Credential
	var extra struct {
		PlanType string `json:"plan_type"`
	}
	if json.Unmarshal(raw, &cred) != nil {
		return quota.Snapshot{}, "", &quota.Error{Code: "credential_decode_failed"}
	}
	_ = json.Unmarshal(raw, &extra)
	if strings.TrimSpace(cred.Type) == "" {
		cred.Type = t.Provider
	}
	p.mu.Lock()
	timeout := p.cfg.RequestTimeout
	p.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	snap, err := fetcher.Fetch(ctx, cred)
	return snap, planLabel(extra.PlanType), err
}

func planLabel(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || len(p) > 24 {
		return ""
	}
	return p
}

func (p *Runtime) sampleQuota() {
	for _, a := range p.router.Accounts() {
		for _, w := range a.Windows {
			p.tel.Quota(w.ObservedAt, a.ID, w.Key, w.Used, w.ResetsAt)
		}
	}
}

func (p *Runtime) refresh(authID string) (int, time.Duration) {
	n, wait := p.router.RequestRefresh(authID)
	if n > 0 {
		p.wakePoller()
	}
	return n, wait
}

func (p *Runtime) info() mgmt.RuntimeInfo {
	p.mu.Lock()
	defer p.mu.Unlock()
	i := mgmt.RuntimeInfo{
		Version: Version, StartedAt: p.started, WorkerRunning: p.running.Load(), WorkerFailed: p.failed.Load(),
		StoreLoad: p.loadRes, LastSaveError: p.saveErr, LastSavedAt: p.savedAt, Generation: p.cfg.Generation(),
		ConfigError: p.configErr, ScheduleAcross: p.cfg.ScheduleAcrossPriorities, Panics: p.panics.Load(),
	}
	if p.store != nil {
		i.StoreWritable, i.StoreReason = p.store.Writable()
	} else {
		i.StoreReason = "state_dir_unavailable"
	}
	return i
}

// Pick answers scheduler.pick.
func (p *Runtime) Pick(req pluginapi.SchedulerPickRequest) pluginapi.SchedulerPickResponse {
	r := p.routerOrNil()
	if r == nil {
		return pluginapi.SchedulerPickResponse{}
	}
	d := r.Pick(toPickRequest(req))
	if d.Provider == "claude" || d.Provider == "codex" {
		p.tel.Decision(d.At, d.Provider, d.AuthID, d.Outcome)
	}
	if !d.Applied {
		return pluginapi.SchedulerPickResponse{}
	}
	return pluginapi.SchedulerPickResponse{AuthID: d.AuthID, Handled: true}
}

func (p *Runtime) routerOrNil() *router.Router {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.router
}

func toPickRequest(req pluginapi.SchedulerPickRequest) router.PickRequest {
	provider := strings.ToLower(strings.TrimSpace(req.Provider))
	if provider == "" && len(req.Providers) == 1 {
		provider = strings.ToLower(strings.TrimSpace(req.Providers[0]))
	}
	for _, other := range req.Providers {
		if strings.ToLower(strings.TrimSpace(other)) != provider {
			provider = "mixed"
		}
	}
	out := router.PickRequest{Provider: provider, Model: req.Model, Stream: req.Stream}
	out.Session, out.HasSession = session.FromRequest(http.Header(req.Options.Headers), req.Options.Metadata)
	if pinned, ok := req.Options.Metadata["pinned_auth_id"].(string); ok {
		out.PinnedAuthID = strings.TrimSpace(pinned)
	}
	for _, c := range req.Candidates {
		out.Candidates = append(out.Candidates, router.Candidate{ID: strings.TrimSpace(c.ID), Provider: c.Provider, Priority: c.Priority, Status: c.Status})
	}
	return out
}

// Usage answers usage.handle. The client API key is used only to derive the
// caller scope and is not retained; failure bodies are ignored.
func (p *Runtime) Usage(rec pluginapi.UsageRecord) {
	r := p.routerOrNil()
	if r == nil {
		return
	}
	provider := strings.ToLower(strings.TrimSpace(rec.Provider))
	if provider != "claude" && provider != "codex" {
		return
	}
	at := rec.RequestedAt.Add(rec.Latency)
	if rec.RequestedAt.IsZero() {
		at = p.now()
	}
	o := router.Outcome{At: at, AuthID: rec.AuthID, Provider: provider, Model: rec.Model, Failed: rec.Failed, Status: rec.Failure.StatusCode, Headers: rec.ResponseHeaders}
	if !rec.Failed && o.Status == 0 {
		o.Status = http.StatusOK
	}
	o.Session, o.HasSession = session.FromUsage(rec.SessionID, rec.ParentSessionID, rec.APIKey)
	r.Observe(o)
	d := rec.Detail
	p.tel.Usage(at, provider, rec.AuthID, rec.Failed, rec.Failure.StatusCode, d.InputTokens, d.OutputTokens, d.ReasoningTokens, d.CacheReadTokens, d.CacheCreationTokens)
}
