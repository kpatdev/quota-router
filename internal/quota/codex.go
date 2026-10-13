package quota

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	fiveHourSeconds = 18000
	weekSeconds     = 604800
	monthMinSeconds = 2419200
	monthMaxSeconds = 2678400
	fiveHourMinutes = 300
	weekMinutes     = 10080
	monthMinMinutes = 40320
	monthMaxMinutes = 44640
)

// ParseCodexUsage parses GET https://chatgpt.com/backend-api/wham/usage.
// used_percent is 0–100 utilization. limit_reached or allowed=false force 100%.
func ParseCodexUsage(raw []byte, now time.Time) (Snapshot, error) {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return Snapshot{}, errors.New("invalid_quota_json")
	}
	rateLimit, ok := mapValue(doc, "rate_limit", "rateLimit")
	if !ok {
		return Snapshot{}, errors.New("invalid_codex_rate_limit")
	}
	snap := Snapshot{Provider: "codex", PlanType: stringValue(doc, "plan_type", "planType")}
	hasLong := false
	for index, names := range [][2]string{{"primary_window", "primaryWindow"}, {"secondary_window", "secondaryWindow"}} {
		windowRaw, ok := mapValue(rateLimit, names[0], names[1])
		if !ok {
			continue
		}
		w, valid := parseCodexWindow(windowRaw, now, index)
		if !valid {
			return Snapshot{}, errors.New("invalid_codex_window")
		}
		if w.Long() {
			hasLong = true
		}
		for _, existing := range snap.Windows {
			if existing.Key() == w.Key() {
				return Snapshot{}, errors.New("ambiguous_codex_windows")
			}
		}
		snap.Windows = append(snap.Windows, w)
	}
	if !hasLong {
		return Snapshot{}, errors.New("invalid_codex_long_window")
	}
	snap.sort()
	return snap, nil
}

func parseCodexWindow(raw map[string]any, now time.Time, position int) (Window, bool) {
	used, ok := numberValue(raw, "used_percent", "usedPercent")
	if !ok || !validPercent(used) {
		return Window{}, false
	}
	seconds, hasSeconds := numberValue(raw, "limit_window_seconds", "limitWindowSeconds")
	kind := Weekly
	switch {
	case hasSeconds && seconds == fiveHourSeconds:
		kind = FiveHour
	case hasSeconds && seconds == weekSeconds:
		kind = Weekly
	case hasSeconds && seconds >= monthMinSeconds && seconds <= monthMaxSeconds:
		kind = Monthly
	case hasSeconds:
		return Window{}, false
	case position == 0:
		kind = FiveHour
	}
	if allowed, ok := boolValue(raw, "allowed"); ok && !allowed {
		used = 100
	}
	limitReached, _ := boolValue(raw, "limit_reached", "limitReached")
	if limitReached {
		used = 100
	}
	w := Window{Kind: kind, UsedPercent: used, Rejected: limitReached, Source: SourcePoll, ObservedAt: now}
	if reset, ok := codexResetAt(raw, now); ok {
		w.ResetsAt = &reset
	}
	if w.ResetsAt == nil && used != 0 {
		return Window{}, false
	}
	return w, true
}

func codexResetAt(raw map[string]any, now time.Time) (time.Time, bool) {
	if seconds, ok := numberValue(raw, "reset_at", "resetAt"); ok && seconds > 0 {
		return time.Unix(int64(seconds), 0).UTC(), true
	}
	if seconds, ok := numberValue(raw, "reset_after_seconds", "resetAfterSeconds"); ok && seconds >= 0 {
		return now.Add(time.Duration(seconds * float64(time.Second))).UTC(), true
	}
	return time.Time{}, false
}

// ParseCodexHeaders reads X-Codex-{Primary,Secondary}-* response headers.
// Used-Percent is already 0–100. A window is reported only when its length
// and reset are both known, so a header set never invents a window kind.
func ParseCodexHeaders(h http.Header, now time.Time) []Window {
	if h == nil {
		return nil
	}
	var out []Window
	seen := map[string]bool{}
	for _, slot := range []string{"Primary", "Secondary"} {
		base := "X-Codex-" + slot + "-"
		usedRaw := strings.TrimSpace(h.Get(base + "Used-Percent"))
		if usedRaw == "" {
			continue
		}
		used, err := strconv.ParseFloat(usedRaw, 64)
		if err != nil || !validPercent(used) {
			continue
		}
		minutes, err := strconv.ParseFloat(strings.TrimSpace(h.Get(base+"Window-Minutes")), 64)
		if err != nil || math.IsNaN(minutes) {
			continue
		}
		var kind Kind
		switch {
		case minutes == fiveHourMinutes:
			kind = FiveHour
		case minutes == weekMinutes:
			kind = Weekly
		case minutes >= monthMinMinutes && minutes <= monthMaxMinutes:
			kind = Monthly
		default:
			continue
		}
		var reset time.Time
		if t, ok := unixHeader(h.Get(base + "Reset-At")); ok {
			reset = t
		} else if secs, err := strconv.ParseFloat(strings.TrimSpace(h.Get(base+"Reset-After-Seconds")), 64); err == nil && secs >= 0 && !math.IsInf(secs, 0) {
			reset = now.Add(time.Duration(secs * float64(time.Second))).UTC()
		} else {
			continue
		}
		if seen[string(kind)] {
			continue
		}
		seen[string(kind)] = true
		out = append(out, Window{Kind: kind, UsedPercent: used, ResetsAt: &reset, Source: SourcePassive, ObservedAt: now})
	}
	if strings.EqualFold(strings.TrimSpace(h.Get("X-Codex-Limit-Reached")), "true") {
		for i := range out {
			if out[i].UsedPercent >= 100 {
				out[i].Rejected = true
			}
		}
	}
	return out
}

func mapValue(m map[string]any, keys ...string) (map[string]any, bool) {
	for _, key := range keys {
		for actual, value := range m {
			if strings.EqualFold(actual, key) {
				result, ok := value.(map[string]any)
				return result, ok
			}
		}
	}
	return nil, false
}

func stringValue(m map[string]any, keys ...string) string {
	for _, key := range keys {
		for actual, value := range m {
			if strings.EqualFold(actual, key) {
				result, _ := value.(string)
				return result
			}
		}
	}
	return ""
}

func numberValue(m map[string]any, keys ...string) (float64, bool) {
	for _, key := range keys {
		for actual, value := range m {
			if !strings.EqualFold(actual, key) {
				continue
			}
			switch result := value.(type) {
			case float64:
				return result, true
			case json.Number:
				parsed, err := result.Float64()
				return parsed, err == nil
			}
		}
	}
	return 0, false
}

func boolValue(m map[string]any, keys ...string) (bool, bool) {
	for _, key := range keys {
		for actual, value := range m {
			if strings.EqualFold(actual, key) {
				result, ok := value.(bool)
				return result, ok
			}
		}
	}
	return false, false
}
