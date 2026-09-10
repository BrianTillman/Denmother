package haaudit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/BrianTillman/Denmother/internal/hahttp"
)

// HistoryEntry represents a single state snapshot from the HA History API.
type HistoryEntry struct {
	EntityID        string                 `json:"entity_id"`
	State           string                 `json:"state"`
	Attributes      map[string]interface{} `json:"attributes"`
	LastChanged     string                 `json:"last_changed"`
	LastChangedTime time.Time              `json:"-"`
	ContextEntityID string                 `json:"-"`
	ContextKnown    bool                   `json:"-"`
}

// EntityTimeline maps entity_id → chronological history entries.
// Unavailable/unknown observations are retained to preserve transition boundaries.
type EntityTimeline map[string][]HistoryEntry

type logbookEntry struct {
	EntityID        string `json:"entity_id"`
	When            string `json:"when"`
	ContextEntityID string `json:"context_entity_id"`
}

// FetchHistory fetches entity state history from the HA History API for the
// given entity IDs between start and end times.
func FetchHistory(baseURL, token string, entityIDs []string, start, end time.Time) (EntityTimeline, error) {
	return FetchHistoryContext(context.Background(), baseURL, token, entityIDs, start, end)
}
func FetchHistoryContext(ctx context.Context, baseURL, token string, entityIDs []string, start, end time.Time) (EntityTimeline, error) {
	url := fmt.Sprintf("%s/api/history/period/%s?filter_entity_id=%s&end_time=%s&significant_changes_only=0",
		strings.TrimSuffix(baseURL, "/"),
		start.Format(time.RFC3339Nano),
		strings.Join(entityIDs, ","),
		end.Format(time.RFC3339Nano),
	)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch history: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := hahttp.NewClient(30 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch history: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch history: HA API returned status %d", resp.StatusCode)
	}

	var raw [][]HistoryEntry
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("fetch history: %w", err)
	}

	timeline := make(EntityTimeline)
	for _, entries := range raw {
		if len(entries) == 0 {
			continue
		}
		entityID := entries[0].EntityID
		for _, e := range entries {
			var err error
			e.LastChangedTime, err = ParseTime(e.LastChanged)
			if err != nil {
				return nil, fmt.Errorf("fetch history: invalid timestamp for %s: %w", entityID, err)
			}
			timeline[entityID] = append(timeline[entityID], e)
		}
	}

	return timeline, nil
}

// EnrichHistoryContexts attaches logbook attribution to history entries. The
// History API omits context, while the Logbook API identifies the entity or
// automation responsible for a state change.
func EnrichHistoryContexts(baseURL, token string, history EntityTimeline, entityIDs []string, start, end time.Time) error {
	return EnrichHistoryContextsContext(context.Background(), baseURL, token, history, entityIDs, start, end)
}
func EnrichHistoryContextsContext(ctx context.Context, baseURL, token string, history EntityTimeline, entityIDs []string, start, end time.Time) error {
	client := hahttp.NewClient(30 * time.Second)
	for _, entityID := range entityIDs {
		u := fmt.Sprintf("%s/api/logbook/%s?end_time=%s&entity=%s",
			strings.TrimSuffix(baseURL, "/"),
			start.Format(time.RFC3339Nano),
			url.QueryEscape(end.Format(time.RFC3339Nano)),
			url.QueryEscape(entityID),
		)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return fmt.Errorf("fetch logbook context for %s: %w", entityID, err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("fetch logbook context for %s: %w", entityID, err)
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return fmt.Errorf("fetch logbook context for %s: HA API returned status %d", entityID, resp.StatusCode)
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return fmt.Errorf("fetch logbook context for %s: %w", entityID, readErr)
		}

		var entries []logbookEntry
		if err := json.Unmarshal(body, &entries); err != nil {
			return fmt.Errorf("fetch logbook context for %s: %w", entityID, err)
		}
		contexts := make(map[int64]string, len(entries))
		for _, entry := range entries {
			when, err := ParseTime(entry.When)
			if err == nil && entry.EntityID == entityID {
				contexts[when.UnixNano()] = entry.ContextEntityID
			}
		}
		for i := range history[entityID] {
			entry := &history[entityID][i]
			if contextEntityID, ok := contexts[entry.LastChangedTime.UnixNano()]; ok {
				entry.ContextEntityID = contextEntityID
				entry.ContextKnown = true
			}
		}
	}
	return nil
}

// ParseTime parses an RFC3339Nano timestamp string as returned by the HA History API.
func ParseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse timestamp %q: %w", s, err)
	}
	return t, nil
}

// LastBefore returns the last HistoryEntry whose LastChanged time is strictly
// before ts, or nil if no such entry exists. Entries with unparseable
// timestamps (zero LastChangedTime) are skipped.
func LastBefore(entries []HistoryEntry, ts time.Time) *HistoryEntry {
	for i := len(entries) - 1; i >= 0; i-- {
		if !entries[i].LastChangedTime.IsZero() && entries[i].LastChangedTime.Before(ts) {
			return &entries[i]
		}
	}
	return nil
}
