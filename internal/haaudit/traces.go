package haaudit

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/BrianTillman/Denmother/internal/hasync"
)

// AutomationTrace is a single execution record returned by the HA trace/list API.
type AutomationTrace struct {
	RunID     string
	Timestamp time.Time
	State     string // "stopped", "running"
	LastStep  string // last executed action step path, e.g. "action/0"
	Error     string // non-empty if the trace recorded an error
}

// TraceTimestamp accepts trace/list timestamps as a start/finish object or a
// single start-time string.
type TraceTimestamp struct {
	Start  string `json:"start"`
	Finish string `json:"finish"`
}

// UnmarshalJSON handles both string and object forms of the timestamp field.
func (t *TraceTimestamp) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		t.Start = s
		return nil
	}
	type raw TraceTimestamp
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	*t = TraceTimestamp(r)
	return nil
}

// traceListItem is the raw JSON wire format from trace/list.
type traceListItem struct {
	RunID     string         `json:"run_id"`
	Timestamp TraceTimestamp `json:"timestamp"`
	State     string         `json:"state"`
	LastStep  string         `json:"last_step"`
	Error     string         `json:"error"`
}

// TraceAnnotation is attached to a TriggerEventResult when a matching automation
// trace is found near the trigger timestamp.
type TraceAnnotation struct {
	RunID    string
	State    string        // "stopped", "running", etc.
	LastStep string        // last executed step, empty if not present
	Error    string        // non-empty if trace recorded an error
	Lag      time.Duration // trace.Timestamp - trigger.Timestamp (can be negative)
}

// FetchTraceListWS fetches automation traces using a pre-resolved item_id.
// The caller must resolve entity_id → config_id (via AutomationIDResolver)
// before calling this function. Reuse one WSClient for multiple calls.
func FetchTraceListWS(ws *hasync.WSClient, itemID string) ([]AutomationTrace, error) {
	raw, err := ws.SendCommandWithParams("trace/list", map[string]interface{}{
		"domain":  "automation",
		"item_id": itemID,
	})
	if err != nil {
		return nil, fmt.Errorf("trace/list %s: %w", itemID, err)
	}

	var items []traceListItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("trace/list %s: unmarshal: %w", itemID, err)
	}

	traces := make([]AutomationTrace, 0, len(items))
	for _, item := range items {
		t, _ := ParseTime(item.Timestamp.Start)
		traces = append(traces, AutomationTrace{
			RunID:     item.RunID,
			Timestamp: t,
			State:     item.State,
			LastStep:  item.LastStep,
			Error:     item.Error,
		})
	}
	return traces, nil
}

// FetchTraceList fetches the most recent automation traces for a single automation.
// automationID is the full entity_id, including the "automation." domain.
// Connects a WebSocket, resolves entity_id → config_id via the entity registry,
// and fetches traces. For multiple automations, prefer creating one WSClient and
// resolver and calling FetchTraceListWS directly.
func FetchTraceList(baseURL, token, automationID string) ([]AutomationTrace, error) {
	ws := hasync.NewWSClient(baseURL, token)
	if err := ws.Connect(); err != nil {
		return nil, fmt.Errorf("trace/list connect: %w", err)
	}
	defer ws.Close()

	resolver, err := NewAutomationIDResolver(ws)
	if err != nil {
		return nil, fmt.Errorf("trace/list resolver: %w", err)
	}
	itemID := resolver.Resolve(automationID)
	return FetchTraceListWS(ws, itemID)
}

// EnrichWithTraces annotates trigger results with the closest matching trace.
// It reuses a connected WSClient and caches successful fetches per automation.
// Failed fetches emit a warning and may be retried for later results.
func EnrichWithTraces(results []*AuditResult, ws *hasync.WSClient, resolver *AutomationIDResolver) {
	cache := make(map[string][]AutomationTrace)

	for _, result := range results {
		if result.AutomationID == "" {
			continue
		}
		traces, ok := cache[result.AutomationID]
		if !ok {
			itemID := resolver.Resolve(result.AutomationID)
			var err error
			traces, err = FetchTraceListWS(ws, itemID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not fetch traces for %s: %v\n", result.AutomationID, err)
				continue
			}
			cache[result.AutomationID] = traces
		}
		for i := range result.TriggerEvents {
			ev := &result.TriggerEvents[i]
			tr := findClosestTrace(traces, ev.Timestamp)
			if tr == nil {
				continue
			}
			ev.Trace = &TraceAnnotation{
				RunID:    tr.RunID,
				State:    tr.State,
				LastStep: tr.LastStep,
				Error:    tr.Error,
				Lag:      tr.Timestamp.Sub(ev.Timestamp),
			}
		}
	}
}

// AutomationItemID returns the suffix after the first dot, or the input if absent.
// Prefer AutomationIDResolver.Resolve when registry data is available; the
// config ID can differ from the entity suffix.
func AutomationItemID(entityID string) string {
	if i := strings.Index(entityID, "."); i >= 0 {
		return entityID[i+1:]
	}
	return entityID
}

// traceMatchWindow is the maximum time between a trigger event and a trace
// start timestamp for them to be considered matching.
const traceMatchWindow = 60 * time.Second

// findClosestTrace returns the AutomationTrace whose Timestamp is closest to t
// and within traceMatchWindow, or nil if no such trace exists.
func findClosestTrace(traces []AutomationTrace, t time.Time) *AutomationTrace {
	var best *AutomationTrace
	var bestDiff time.Duration

	for i := range traces {
		diff := traces[i].Timestamp.Sub(t)
		if diff < 0 {
			diff = -diff
		}
		if diff > traceMatchWindow {
			continue
		}
		if best == nil || diff < bestDiff {
			best = &traces[i]
			bestDiff = diff
		}
	}
	return best
}
