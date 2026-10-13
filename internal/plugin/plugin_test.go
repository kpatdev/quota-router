package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kpatdev/quota-router/internal/quota"
	"github.com/kpatdev/quota-router/internal/session"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

var t0 = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

const secretToken = "sk-ant-oat-SECRET"
const clientKey = "client-proxy-key-SECRET"

type fakeHost struct {
	mu      sync.Mutex
	entries []pluginapi.HostAuthFileEntry
}

func (h *fakeHost) set(provider string, ids ...string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.entries = nil
	for _, id := range ids {
		h.entries = append(h.entries, pluginapi.HostAuthFileEntry{ID: id, AuthIndex: "idx-" + id, Name: id + "@example.com.json", Provider: provider, Email: id + "@example.com"})
	}
}

func (h *fakeHost) List() ([]pluginapi.HostAuthFileEntry, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]pluginapi.HostAuthFileEntry(nil), h.entries...), nil
}

func (h *fakeHost) Get(index string) (json.RawMessage, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, e := range h.entries {
		if e.AuthIndex == index {
			return json.RawMessage(fmt.Sprintf(`{"type":%q,"access_token":%q,"account_uuid":%q}`, e.Provider, secretToken+"-"+e.ID, e.ID)), nil
		}
	}
	return nil, fmt.Errorf("missing")
}

type fakeFetcher struct {
	mu    sync.Mutex
	now   func() time.Time
	reset map[string]time.Duration // account → weekly reset
	used  map[string]float64
}

func (f *fakeFetcher) set(id string, used float64, reset time.Duration) {
	f.mu.Lock()
	f.used[id], f.reset[id] = used, reset
	f.mu.Unlock()
}

func (f *fakeFetcher) Fetch(_ context.Context, c quota.Credential) (quota.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := strings.TrimPrefix(c.AccessToken, secretToken+"-")
	now := f.now()
	at := now.Add(f.reset[id])
	return quota.Snapshot{Provider: c.Type, PolledAt: now, Identity: c.Identity(), Windows: []quota.Window{
		{Kind: quota.Weekly, UsedPercent: f.used[id], ResetsAt: &at, Source: quota.SourcePoll, ObservedAt: now},
	}}, nil
}

type rig struct {
	t       *testing.T
	now     time.Time
	mu      sync.Mutex
	host    *fakeHost
	fetcher *fakeFetcher
	p       *Runtime
	dir     string
}

func newRig(t *testing.T, dir string) *rig {
	g := &rig{t: t, now: t0, host: &fakeHost{}, dir: dir}
	g.fetcher = &fakeFetcher{now: g.clock, reset: map[string]time.Duration{}, used: map[string]float64{}}
	g.p = &Runtime{Host: g.host, Now: g.clock, NewFetcher: func() quota.Fetcher { return g.fetcher }}
	return g
}

func (g *rig) clock() time.Time {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.now
}

func (g *rig) advance(d time.Duration) {
	g.mu.Lock()
	g.now = g.now.Add(d)
	g.mu.Unlock()
}

func (g *rig) call(method string, req any) json.RawMessage {
	g.t.Helper()
	raw, _ := json.Marshal(req)
	var env pluginabi.Envelope
	if err := json.Unmarshal(g.p.Handle(method, raw), &env); err != nil || !env.OK {
		g.t.Fatalf("%s failed: %s", method, env.Error)
	}
	return env.Result
}

func (g *rig) register(yaml string) {
	g.t.Helper()
	g.call(pluginabi.MethodPluginRegister, map[string]any{"config_yaml": []byte(yaml + "\nstate_dir: " + g.dir + "\n")})
	g.t.Cleanup(g.p.Shutdown)
}

func (g *rig) refreshQuota() {
	g.p.SyncRoster()
	g.p.router.RequestRefresh("")
	g.p.PollDue(context.Background(), g.fetcher)
}

func (g *rig) pick(provider, model string, headers http.Header, ids ...string) string {
	g.t.Helper()
	req := pluginapi.SchedulerPickRequest{Provider: provider, Providers: []string{provider}, Model: model}
	req.Options.Headers = headers
	req.Options.Metadata = map[string]any{"requested_model": model, "caller_scope": session.CallerScope(clientKey)}
	for _, id := range ids {
		req.Candidates = append(req.Candidates, pluginapi.SchedulerAuthCandidate{ID: id, Provider: provider})
	}
	var resp pluginapi.SchedulerPickResponse
	if err := json.Unmarshal(g.call(pluginabi.MethodSchedulerPick, req), &resp); err != nil {
		g.t.Fatal(err)
	}
	if !resp.Handled {
		return ""
	}
	return resp.AuthID
}

func (g *rig) usage(provider, model, auth, sessionID string, status int, headers http.Header) {
	rec := pluginapi.UsageRecord{Provider: provider, Model: model, AuthID: auth, SessionID: sessionID, APIKey: clientKey, RequestedAt: g.clock(), ResponseHeaders: headers,
		Detail: pluginapi.UsageDetail{InputTokens: 10, OutputTokens: 5, CacheReadTokens: 100}}
	if status >= 400 {
		rec.Failed = true
		rec.Failure = pluginapi.UsageFailure{StatusCode: status, Body: "upstream said " + secretToken}
	}
	g.call(pluginabi.MethodUsageHandle, rec)
}

func (g *rig) mgmt(method, path string, query url.Values, body string) (int, string) {
	g.t.Helper()
	req := pluginapi.ManagementRequest{Method: method, Path: path, Query: query, Headers: http.Header{"Authorization": {"Bearer management-key-SECRET"}}}
	if body != "" {
		req.Body = []byte(body)
		req.Headers.Set("Content-Type", "application/json")
	}
	var resp pluginapi.ManagementResponse
	if err := json.Unmarshal(g.call(pluginabi.MethodManagementHandle, req), &resp); err != nil {
		g.t.Fatal(err)
	}
	return resp.StatusCode, string(resp.Body)
}

func headersFor(provider, sessionID string) http.Header {
	if provider == "claude" {
		return http.Header{"X-Claude-Code-Session-Id": {sessionID}, "Authorization": {"Bearer " + clientKey}}
	}
	return http.Header{"Session-Id": {sessionID}, "Authorization": {"Bearer " + clientKey}}
}

// TestAcceptanceThroughRPC runs the brief's scenario through the JSON RPC
// surface CPA calls, including a full runtime restart over the same state dir.
func TestAcceptanceThroughRPC(t *testing.T) {
	for _, provider := range []string{"claude", "codex"} {
		t.Run(provider, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "state")
			model := map[string]string{"claude": "claude-opus-4-6", "codex": "gpt-5.5"}[provider]
			g := newRig(t, dir)
			g.host.set(provider, "A", "B")
			g.fetcher.set("A", 20, 20*time.Hour)
			g.fetcher.set("B", 20, 96*time.Hour)
			g.register("mode: active")
			g.refreshQuota()

			h1 := headersFor(provider, "thread-1")
			if got := g.pick(provider, model, h1, "A", "B"); got != "A" {
				t.Fatalf("new thread got %q", got)
			}
			for i := 0; i < 3; i++ {
				g.usage(provider, model, "A", provider+":thread-1", 200, nil)
				if got := g.pick(provider, model, h1, "A", "B"); got != "A" {
					t.Fatalf("follow-up %d got %q", i, got)
				}
			}
			g.fetcher.set("A", 20, 80*time.Hour)
			g.fetcher.set("B", 20, 5*time.Hour)
			g.advance(time.Minute)
			g.refreshQuota()
			if got := g.pick(provider, model, h1, "A", "B"); got != "A" {
				t.Fatalf("ranking change moved the thread to %q", got)
			}
			if got := g.pick(provider, model, headersFor(provider, "thread-2"), "A", "B"); got != "B" {
				t.Fatalf("new thread should prefer B, got %q", got)
			}
			g.fetcher.set("A", 100, 80*time.Hour)
			g.advance(time.Minute)
			g.refreshQuota()
			if got := g.pick(provider, model, h1, "A", "B"); got != "B" {
				t.Fatalf("exhausted A did not fail over, got %q", got)
			}
			g.p.Shutdown()

			// Restart: a fresh runtime over the same directory.
			g2 := newRig(t, dir)
			g2.now = g.clock().Add(time.Minute)
			g2.host.set(provider, "A", "B")
			g2.fetcher.set("A", 0, time.Hour)
			g2.fetcher.set("B", 20, 5*time.Hour)
			g2.register("mode: active")
			g2.refreshQuota()
			if got := g2.pick(provider, model, h1, "A", "B"); got != "B" {
				t.Fatalf("rebinding lost across restart, got %q", got)
			}
		})
	}
}

func TestShadowModeNeverRoutes(t *testing.T) {
	g := newRig(t, t.TempDir())
	g.host.set("claude", "A")
	g.fetcher.set("A", 0, time.Hour)
	g.register("mode: shadow")
	g.refreshQuota()
	if got := g.pick("claude", "m", headersFor("claude", "s"), "A"); got != "" {
		t.Fatalf("shadow mode routed to %q", got)
	}
	code, body := g.mgmt("GET", "/v0/management/plugins/kpatdev-quota-router/activity", nil, "")
	if code != 200 || !strings.Contains(body, `"applied":false`) {
		t.Fatalf("shadow decision not recorded: %d %s", code, body)
	}
}

func TestLegacyConfigAndReconfigureKeepState(t *testing.T) {
	g := newRig(t, t.TempDir())
	g.host.set("claude", "A", "B")
	g.fetcher.set("A", 0, time.Hour)
	g.fetcher.set("B", 0, 2*time.Hour)
	g.register("mode: active\npoll_interval: 5m\nmax_age: 10m\nrequest_timeout: 10s")
	g.refreshQuota()
	h := headersFor("claude", "s")
	if got := g.pick("claude", "m", h, "B"); got != "B" {
		t.Fatalf("got %q", got)
	}
	g.call(pluginabi.MethodPluginReconfigure, map[string]any{"config_yaml": []byte("mode: active\nstrategy: quota_pressure\nstate_dir: " + g.dir)})
	if got := g.pick("claude", "m", h, "A", "B"); got != "B" {
		t.Fatalf("reconfigure lost the binding, got %q", got)
	}
	var env pluginabi.Envelope
	raw, _ := json.Marshal(map[string]any{"config_yaml": []byte("mode: sometimes")})
	_ = json.Unmarshal(g.p.Handle(pluginabi.MethodPluginReconfigure, raw), &env)
	if env.OK {
		t.Fatal("invalid configuration accepted")
	}
	if got := g.pick("claude", "m", h, "A", "B"); got != "B" {
		t.Fatalf("rejected reconfigure disturbed routing, got %q", got)
	}
}

func TestRegistrationDeclaresCapabilities(t *testing.T) {
	g := newRig(t, t.TempDir())
	raw, _ := json.Marshal(map[string]any{"config_yaml": []byte("state_dir: " + g.dir)})
	var env pluginabi.Envelope
	_ = json.Unmarshal(g.p.Handle(pluginabi.MethodPluginRegister, raw), &env)
	t.Cleanup(g.p.Shutdown)
	var reg struct {
		SchemaVersion uint32          `json:"schema_version"`
		Capabilities  map[string]bool `json:"capabilities"`
		Metadata      pluginapi.Metadata
	}
	_ = json.Unmarshal(env.Result, &reg)
	for _, c := range []string{"scheduler", "scheduler_across_priorities", "usage_plugin", "management_api"} {
		if !reg.Capabilities[c] {
			t.Fatalf("missing capability %s: %+v", c, reg.Capabilities)
		}
	}
	if reg.SchemaVersion != pluginabi.SchemaVersion {
		t.Fatalf("schema version %d", reg.SchemaVersion)
	}
	var mr struct {
		Routes    []pluginapi.ManagementRoute `json:"routes"`
		Resources []pluginapi.ResourceRoute   `json:"resources"`
	}
	_ = json.Unmarshal(g.call(pluginabi.MethodManagementRegister, struct{}{}), &mr)
	if len(mr.Routes) < 10 || len(mr.Resources) == 0 || mr.Resources[0].Menu == "" && len(mr.Resources) == 1 {
		t.Fatalf("management registration: %+v", mr)
	}
}

func TestNoSecretsInAnyManagementResponseOrStateFile(t *testing.T) {
	dir := t.TempDir()
	g := newRig(t, dir)
	g.host.set("claude", "A", "B")
	g.fetcher.set("A", 10, time.Hour)
	g.fetcher.set("B", 10, 2*time.Hour)
	g.register("mode: active\naccount_alias: {A: Work}")
	g.refreshQuota()
	h := headersFor("claude", "raw-session-SECRET")
	g.pick("claude", "claude-opus-4-6", h, "A", "B")
	g.usage("claude", "claude-opus-4-6", "A", "claude:raw-session-SECRET", 200, http.Header{"Set-Cookie": {"c=" + secretToken}})
	g.usage("claude", "claude-opus-4-6", "B", "claude:raw-session-SECRET", 500, nil)
	base := "/v0/management/plugins/kpatdev-quota-router"
	var bodies []string
	for _, path := range []string{"/status", "/overview", "/accounts", "/sessions", "/activity", "/usage", "/settings", "/export"} {
		code, body := g.mgmt("GET", base+path, nil, "")
		if code != 200 {
			t.Fatalf("%s → %d %s", path, code, body)
		}
		bodies = append(bodies, body)
	}
	_, body := g.mgmt("GET", base+"/decision", url.Values{"id": {"1"}}, "")
	bodies = append(bodies, body)
	_, body = g.mgmt("POST", base+"/simulate", nil, `{"provider":"claude"}`)
	bodies = append(bodies, body)
	g.p.Flush()
	for _, f := range []string{"state.json", "history.json"} {
		raw, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, string(raw))
	}
	for _, b := range bodies {
		for _, secret := range []string{"SECRET", "management-key", "Bearer"} {
			if strings.Contains(b, secret) {
				t.Fatalf("secret %q leaked in: %.400s", secret, b)
			}
		}
	}
	_, export := g.mgmt("GET", base+"/export", nil, "")
	if strings.Contains(export, "example.com") || strings.Contains(export, `"A"`) {
		t.Fatalf("export must not contain account IDs or e-mails: %.400s", export)
	}
}

func TestManagementMutationsAreGuarded(t *testing.T) {
	g := newRig(t, t.TempDir())
	g.host.set("claude", "A")
	g.fetcher.set("A", 10, time.Hour)
	g.register("mode: active")
	g.refreshQuota()
	g.pick("claude", "m", headersFor("claude", "s"), "A")
	base := "/v0/management/plugins/kpatdev-quota-router"
	if code, _ := g.mgmt("POST", base+"/sessions/clear", nil, `{}`); code != http.StatusPreconditionRequired {
		t.Fatalf("clear without confirmation: %d", code)
	}
	if code, body := g.mgmt("POST", base+"/sessions/clear", nil, `{"confirm":"clear sessions"}`); code != 200 || !strings.Contains(body, `"cleared":1`) {
		t.Fatalf("confirmed clear: %d %s", code, body)
	}
	req := pluginapi.ManagementRequest{Method: "POST", Path: base + "/refresh", Body: []byte(`{}`), Headers: http.Header{}}
	var resp pluginapi.ManagementResponse
	_ = json.Unmarshal(g.call(pluginabi.MethodManagementHandle, req), &resp)
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("mutation without JSON content type: %d", resp.StatusCode)
	}
	g.advance(31 * time.Second)
	if code, _ := g.mgmt("POST", base+"/refresh", nil, `{}`); code != http.StatusAccepted {
		t.Fatalf("refresh: %d", code)
	}
	if code, _ := g.mgmt("POST", base+"/refresh", nil, `{}`); code != http.StatusTooManyRequests {
		t.Fatalf("refresh not rate limited: %d", code)
	}
	if code, body := g.mgmt("POST", base+"/settings/validate", nil, `{"affinity_ttl":"1s"}`); code != 422 || !strings.Contains(body, "affinity_ttl") {
		t.Fatalf("invalid settings: %d %s", code, body)
	}
	if code, body := g.mgmt("POST", base+"/settings/validate", nil, `{"strategy":"balanced"}`); code != 200 || !strings.Contains(body, `"field":"strategy"`) {
		t.Fatalf("valid settings: %d %s", code, body)
	}
	if code, _ := g.mgmt("POST", base+"/simulate", nil, strings.Repeat(" ", 70<<10)+`{}`); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: %d", code)
	}
	if code, _ := g.mgmt("DELETE", base+"/status", nil, ""); code != 404 {
		t.Fatalf("unknown method: %d", code)
	}
}

func TestResourcesAreStaticAndHardened(t *testing.T) {
	g := newRig(t, t.TempDir())
	g.register("mode: shadow")
	req := pluginapi.ManagementRequest{Method: "GET", Path: "/v0/resource/plugins/kpatdev-quota-router/index.html"}
	var resp pluginapi.ManagementResponse
	_ = json.Unmarshal(g.call(pluginabi.MethodManagementHandle, req), &resp)
	if resp.StatusCode != 200 || !strings.Contains(resp.Headers.Get("Content-Security-Policy"), "default-src 'self'") || resp.Headers.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("resource headers: %d %+v", resp.StatusCode, resp.Headers)
	}
	req.Path = "/v0/resource/plugins/kpatdev-quota-router/../../management/x"
	_ = json.Unmarshal(g.call(pluginabi.MethodManagementHandle, req), &resp)
	if strings.Contains(string(resp.Body), "SECRET") {
		t.Fatal("traversal served data")
	}
}

func TestPickPanicsFallBackToHost(t *testing.T) {
	p := &Runtime{Host: &fakeHost{}}
	// Not registered: no router. Must answer "not handled", not fail.
	var env pluginabi.Envelope
	_ = json.Unmarshal(p.Handle(pluginabi.MethodSchedulerPick, []byte(`{"Provider":"claude"}`)), &env)
	if !env.OK || string(env.Result) != `{"AuthID":"","DelegateBuiltin":"","Handled":false,"Reject":false,"RejectReason":"","RejectCode":""}` {
		t.Fatalf("unregistered pick: %s", env.Result)
	}
	_ = json.Unmarshal(p.Handle(pluginabi.MethodSchedulerPick, []byte(`not json`)), &env)
	if !env.OK {
		t.Fatal("malformed pick must still answer")
	}
}

func TestConcurrentPicksUsageAndReconfigure(t *testing.T) {
	g := newRig(t, t.TempDir())
	g.host.set("codex", "A", "B", "C")
	for _, id := range []string{"A", "B", "C"} {
		g.fetcher.set(id, 10, time.Hour)
	}
	g.register("mode: active")
	g.refreshQuota()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				sid := fmt.Sprintf("s%d", j%5)
				got := g.pick("codex", "gpt-5.5", headersFor("codex", sid), "A", "B", "C")
				g.usage("codex", "gpt-5.5", got, "codex:"+sid, 200, nil)
				if j%17 == 0 {
					g.call(pluginabi.MethodPluginReconfigure, map[string]any{"config_yaml": []byte("mode: active\nstate_dir: " + g.dir)})
				}
				if j%13 == 0 {
					g.mgmt("GET", "/v0/management/plugins/kpatdev-quota-router/overview", nil, "")
				}
			}
		}(i)
	}
	wg.Wait()
	sessions := g.p.router.Sessions()
	if len(sessions) != 5 {
		t.Fatalf("expected 5 bindings, got %d", len(sessions))
	}
}
