package quota

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	ClaudeUsageURL = "https://api.anthropic.com/api/oauth/usage"
	CodexUsageURL  = "https://chatgpt.com/backend-api/wham/usage"
	maxUsageBytes  = 128 * 1024
)

// Credential is the subset of a CPA auth file needed to read quota. It is
// held only for the duration of one poll and never stored.
type Credential struct {
	Type         string `json:"type"`
	AccessToken  string `json:"access_token"`
	AccountUUID  string `json:"account_uuid"`
	AccountID    string `json:"account_id"`
	Organization string `json:"organization_uuid"`
	Disabled     bool   `json:"disabled"`
	ProxyURL     string `json:"proxy_url"`
	BaseURL      string `json:"base_url"`
}

// Identity hashes the upstream account identity, falling back to the token
// when the credential carries no account ID.
func (c Credential) Identity() string {
	value := strings.ToLower(strings.TrimSpace(c.Type)) + "\x00" + c.AccountUUID + "\x00" + c.AccountID + "\x00" + c.Organization
	if c.AccountUUID == "" && c.AccountID == "" {
		value += "\x00" + c.AccessToken
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// Error is a sanitized poll failure. Code never contains response bodies.
type Error struct {
	Code       string
	Status     int
	RetryAfter time.Duration
}

func (e *Error) Error() string { return e.Code }

// AuthRejected reports a 401/403 from the quota endpoint.
func (e *Error) AuthRejected() bool { return e.Status == 401 || e.Status == 403 }

// Fetcher reads one account's quota.
type Fetcher interface {
	Fetch(context.Context, Credential) (Snapshot, error)
}

// HTTPFetcher polls the provider usage endpoints directly.
type HTTPFetcher struct {
	Client *http.Client
	Now    func() time.Time
	// ClaudeURL and CodexURL override the endpoints in tests.
	ClaudeURL, CodexURL string
}

// NewHTTPFetcher returns a fetcher that never follows redirects (which could
// forward the bearer token) and ignores proxy environment variables.
func NewHTTPFetcher() *HTTPFetcher {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxIdleConns = 2
	transport.MaxIdleConnsPerHost = 1
	return &HTTPFetcher{
		Client: &http.Client{
			Transport:     transport,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		Now: time.Now,
	}
}

// Close releases idle connections.
func (f *HTTPFetcher) Close() { f.Client.CloseIdleConnections() }

func (f *HTTPFetcher) Fetch(ctx context.Context, cred Credential) (Snapshot, error) {
	provider := strings.ToLower(strings.TrimSpace(cred.Type))
	if (provider != "claude" && provider != "codex") || cred.Disabled || strings.TrimSpace(cred.AccessToken) == "" {
		return Snapshot{}, &Error{Code: "ineligible_credential"}
	}
	// Polling connects directly; refuse rather than bypass a per-account proxy or endpoint.
	if cred.ProxyURL != "" || cred.BaseURL != "" {
		return Snapshot{}, &Error{Code: "custom_transport_unsupported"}
	}
	url := ClaudeUsageURL
	if f.ClaudeURL != "" {
		url = f.ClaudeURL
	}
	if provider == "codex" {
		url = CodexUsageURL
		if f.CodexURL != "" {
			url = f.CodexURL
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Snapshot{}, &Error{Code: "quota_request_failed"}
	}
	req.Header.Set("Authorization", "Bearer "+cred.AccessToken)
	req.Header.Set("Accept", "application/json")
	if provider == "claude" {
		req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	} else {
		req.Header.Set("User-Agent", "codex_cli_rs/0.76.0")
		if strings.TrimSpace(cred.AccountID) != "" {
			req.Header.Set("Chatgpt-Account-Id", cred.AccountID)
		}
	}
	resp, err := f.Client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Snapshot{}, &Error{Code: "quota_canceled"}
		}
		return Snapshot{}, &Error{Code: "quota_transport_failed"}
	}
	defer resp.Body.Close()
	now := f.Now().UTC()
	if resp.StatusCode != http.StatusOK {
		e := &Error{Code: fmt.Sprintf("quota_http_%d", resp.StatusCode), Status: resp.StatusCode}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
			e.RetryAfter = RetryAfter(resp.Header.Get("Retry-After"), now)
		}
		return Snapshot{}, e
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxUsageBytes+1))
	if err != nil || len(body) > maxUsageBytes {
		return Snapshot{}, &Error{Code: "invalid_quota_body"}
	}
	var snap Snapshot
	if provider == "claude" {
		snap, err = ParseClaudeUsage(body, now)
	} else {
		snap, err = ParseCodexUsage(body, now)
	}
	if err != nil {
		return Snapshot{}, &Error{Code: err.Error()}
	}
	snap.PolledAt = now
	snap.Provider = provider
	snap.Identity = cred.Identity()
	return snap, nil
}

// RetryAfter parses a Retry-After header, capped at one hour.
func RetryAfter(raw string, now time.Time) time.Duration {
	delay := time.Duration(0)
	if seconds, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil {
		if seconds > 3600 {
			return time.Hour
		}
		if seconds > 0 {
			delay = time.Duration(seconds) * time.Second
		}
	} else if deadline, err := http.ParseTime(raw); err == nil {
		delay = deadline.Sub(now)
	}
	return min(time.Hour, max(0, delay))
}

// IsError unwraps a sanitized poll error.
func IsError(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}
