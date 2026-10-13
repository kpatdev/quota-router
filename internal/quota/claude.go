package quota

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// familyKey matches seven_day_<family> keys that name a model family
// (seven_day_opus, seven_day_fable). Multi-word suffixes such as
// seven_day_oauth_apps describe non-model limits and are ignored.
var familyKey = regexp.MustCompile(`^seven_day_([a-z][a-z0-9]*)$`)

type claudeWindow struct {
	Utilization *float64   `json:"utilization"`
	ResetsAt    *time.Time `json:"resets_at"`
}

// ParseClaudeUsage parses GET https://api.anthropic.com/api/oauth/usage.
// Utilization is reported as 0–100 percent used.
func ParseClaudeUsage(raw []byte, now time.Time) (Snapshot, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return Snapshot{}, errors.New("invalid_quota_json")
	}
	snap := Snapshot{Provider: "claude"}
	weekly, ok := doc["seven_day"]
	if !ok {
		return Snapshot{}, errors.New("missing_weekly_window")
	}
	w, present, err := claudePollWindow(weekly, Weekly, "", now)
	if err != nil || !present {
		return Snapshot{}, errors.New("invalid_weekly_window")
	}
	snap.Windows = append(snap.Windows, w)
	if raw, ok := doc["five_hour"]; ok {
		w, present, err := claudePollWindow(raw, FiveHour, "", now)
		if err != nil {
			return Snapshot{}, errors.New("invalid_five_hour_window")
		}
		if present {
			snap.Windows = append(snap.Windows, w)
		}
	}
	for key, raw := range doc {
		m := familyKey.FindStringSubmatch(key)
		if m == nil {
			continue
		}
		w, present, err := claudePollWindow(raw, ModelWeekly, m[1], now)
		if err != nil {
			return Snapshot{}, errors.New("invalid_model_window")
		}
		if present {
			snap.Windows = append(snap.Windows, w)
		}
	}
	snap.sort()
	return snap, nil
}

func claudePollWindow(raw json.RawMessage, kind Kind, family string, now time.Time) (Window, bool, error) {
	if string(raw) == "null" {
		return Window{}, false, nil
	}
	var cw claudeWindow
	if err := json.Unmarshal(raw, &cw); err != nil {
		return Window{}, false, err
	}
	if cw.Utilization == nil || !validPercent(*cw.Utilization) {
		return Window{}, false, errors.New("invalid utilization")
	}
	if cw.ResetsAt != nil && cw.ResetsAt.IsZero() {
		return Window{}, false, errors.New("zero reset")
	}
	// A window with usage must say when it resets; an unused window may not have started.
	if cw.ResetsAt == nil && *cw.Utilization != 0 {
		return Window{}, false, errors.New("missing reset")
	}
	w := Window{Kind: kind, Family: family, UsedPercent: *cw.Utilization, Source: SourcePoll, ObservedAt: now}
	if cw.ResetsAt != nil {
		t := cw.ResetsAt.UTC()
		w.ResetsAt = &t
	}
	return w, true, nil
}

func validPercent(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 100
}

// ParseClaudeHeaders reads Anthropic unified rate-limit response headers.
// Utilization headers are 0–1 fractions and are scaled to percent here.
// A window is reported only when its utilization and reset are both valid,
// or when its status is "rejected" with a valid reset.
func ParseClaudeHeaders(h http.Header, now time.Time) []Window {
	if h == nil {
		return nil
	}
	var out []Window
	for _, spec := range []struct {
		prefix string
		kind   Kind
	}{{"5h", FiveHour}, {"7d", Weekly}} {
		base := "Anthropic-Ratelimit-Unified-" + spec.prefix + "-"
		reset, okReset := unixHeader(h.Get(base + "Reset"))
		if !okReset {
			continue
		}
		status := strings.ToLower(strings.TrimSpace(h.Get(base + "Status")))
		util, okUtil := fractionHeader(h.Get(base + "Utilization"))
		rejected := status == "rejected"
		if !okUtil && !rejected {
			continue
		}
		used := util * 100
		if rejected {
			used = math.Max(used, 100)
		}
		used = math.Min(used, 100)
		t := reset
		out = append(out, Window{Kind: spec.kind, UsedPercent: used, ResetsAt: &t, Rejected: rejected, Source: SourcePassive, ObservedAt: now})
	}
	return out
}

func fractionHeader(raw string) (float64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1.5 {
		return 0, false
	}
	return v, true
}

func unixHeader(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
		if v > 1e12 { // milliseconds
			v /= 1000
		}
		return time.Unix(v, 0).UTC(), true
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC(), true
	}
	return time.Time{}, false
}

// ClaudeRejection describes a 429's provider attribution.
type ClaudeRejection struct {
	// Shared is true when the five-hour or weekly shared window rejected.
	Shared bool
	// ModelOnly is true when only an overage or model-specific claim rejected.
	ModelOnly bool
	Until     *time.Time
}

// ClassifyClaude429 interprets the unified rate-limit headers of a 429.
func ClassifyClaude429(h http.Header, now time.Time) ClaudeRejection {
	var r ClaudeRejection
	if h == nil {
		return r
	}
	s5 := strings.ToLower(strings.TrimSpace(h.Get("Anthropic-Ratelimit-Unified-5h-Status")))
	s7 := strings.ToLower(strings.TrimSpace(h.Get("Anthropic-Ratelimit-Unified-7d-Status")))
	unified := strings.ToLower(strings.TrimSpace(h.Get("Anthropic-Ratelimit-Unified-Status")))
	if s5 == "rejected" || s7 == "rejected" {
		r.Shared = true
		return r
	}
	if unified == "rejected" {
		r.ModelOnly = true
		if t, ok := unixHeader(h.Get("Anthropic-Ratelimit-Unified-Reset")); ok && t.After(now) {
			r.Until = &t
		}
	}
	return r
}
