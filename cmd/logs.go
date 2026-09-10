package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/BrianTillman/Denmother/internal/hahttp"

	"github.com/BrianTillman/Denmother/internal/haconfig"
	"github.com/BrianTillman/Denmother/internal/hasync"
	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/spf13/cobra"
)

var (
	logsProdURL   string
	logsProdToken string
	logsDevURL    string
	logsDevToken  string
	logsHAURL     string
	logsHAToken   string
	logsJSON      bool
	logsEntity    string
	logsContains  string
	logsWindow    time.Duration
	logsLimit     int
	logsErrorLog  bool
)

var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Inspect Home Assistant logbook or error log",
	Long: `Inspect read-only Home Assistant runtime observations. By default this
queries the logbook for the selected window. Use --error-log to fetch and filter
the raw Home Assistant error log when available, or the WebSocket system log when
Home Assistant has no raw log file route registered.`,
	RunE: runLogs,
}

type logsSummary struct {
	Mode       string           `json:"mode"`
	Source     string           `json:"source,omitempty"`
	Entity     string           `json:"entity,omitempty"`
	Contains   string           `json:"contains,omitempty"`
	Window     string           `json:"window,omitempty"`
	Entries    []map[string]any `json:"entries,omitempty"`
	Lines      []string         `json:"lines,omitempty"`
	Truncated  bool             `json:"truncated,omitempty"`
	EntryCount int              `json:"entry_count"`
	Warnings   []string         `json:"warnings,omitempty"`
}

type haErrorLogResult struct {
	Lines     []string
	Truncated bool
	Source    string
	Warnings  []string
}

type haHTTPError struct {
	StatusCode int
}

func (e *haHTTPError) Error() string {
	return fmt.Sprintf("HA API returned status %d", e.StatusCode)
}

type haSystemLogEntry struct {
	Name          string   `json:"name"`
	Message       []string `json:"message"`
	Level         string   `json:"level"`
	Source        []any    `json:"source"`
	Timestamp     float64  `json:"timestamp"`
	Exception     string   `json:"exception"`
	Count         int      `json:"count"`
	FirstOccurred float64  `json:"first_occurred"`
}

var (
	haGETFunc               = haGET
	fetchHASystemLogFunc    = fetchHASystemLog
	rawErrorLogMissingHint  = "raw /api/error_log returned 404; used WebSocket system_log/list fallback"
	systemLogFallbackSource = "system_log/list"
	rawErrorLogSource       = "/api/error_log"
)

func init() {
	logsCmd.Flags().StringVar(&logsDevURL, "dev-url", "", "Development Home Assistant URL")
	logsCmd.Flags().StringVar(&logsDevToken, "dev-token", "", "Development Home Assistant token")
	logsCmd.Flags().StringVar(&logsProdURL, "prod-url", "", "Production Home Assistant URL")
	logsCmd.Flags().StringVar(&logsProdToken, "prod-token", "", "Production Home Assistant token")
	logsCmd.Flags().StringVar(&logsHAURL, "ha-url", "", "Home Assistant URL (legacy, profile-specific)")
	logsCmd.Flags().StringVar(&logsHAToken, "ha-token", "", "Home Assistant token (legacy, profile-specific)")
	logsCmd.Flags().BoolVar(&logsJSON, "json", false, "Emit a machine-readable JSON summary")
	logsCmd.Flags().StringVar(&logsEntity, "entity", "", "Filter logbook entries to one entity_id")
	logsCmd.Flags().StringVar(&logsContains, "contains", "", "Filter entries or log lines by case-insensitive substring")
	logsCmd.Flags().DurationVar(&logsWindow, "window", 2*time.Hour, "Lookback window for logbook mode")
	logsCmd.Flags().IntVar(&logsLimit, "limit", 50, "Maximum entries or lines to return")
	logsCmd.Flags().BoolVar(&logsErrorLog, "error-log", false, "Fetch the HA error log; falls back to WebSocket system_log/list when the raw route is unavailable")

	rootCmd.AddCommand(logsCmd)
}

func runLogs(cmd *cobra.Command, args []string) error {
	rt := newCommandRuntime("logs", logsJSON, cmd.OutOrStdout())

	flags := haconfig.InstanceFlags{ConfigPath: configFile(""),
		ProdURL:     logsProdURL,
		ProdToken:   logsProdToken,
		DevURL:      logsDevURL,
		DevToken:    logsDevToken,
		LegacyURL:   logsHAURL,
		LegacyToken: logsHAToken,
	}
	resolved, err := operator.ResolveTargetContext(cmd.Context(), haconfig.InstanceDev, flags, operator.ModeReadOnly, true)
	if resolved != nil {
		rt.SetTarget(resolved.Target)
	}
	rt.PrintPreflight()
	if err != nil {
		rt.AddStep(operator.Step{
			ID:      "resolve-target",
			Title:   "Resolve Home Assistant target",
			Status:  operator.StatusFailure,
			Summary: err.Error(),
		})
		return rt.Complete(operator.StatusFailure, "log inspection could not start")
	}

	summary, inspectErr := inspectHALogs(resolved.Config)
	if inspectErr != nil {
		rt.AddStep(operator.Step{
			ID:      "inspect-logs",
			Title:   "Inspect Home Assistant logs",
			Status:  operator.StatusFailure,
			Summary: inspectErr.Error(),
			Details: map[string]any{
				"next_commands": []string{"./dm logs --json"},
			},
		})
		return rt.Complete(operator.StatusFailure, "log inspection failed")
	}

	status := operator.StatusSuccess
	if summary.EntryCount == 0 {
		status = operator.StatusWarning
	}
	step := operator.Step{
		ID:      "inspect-logs",
		Title:   "Inspect Home Assistant logs",
		Status:  status,
		Summary: fmt.Sprintf("%d %s item(s) matched", summary.EntryCount, summary.Mode),
		Details: map[string]any{
			"logs":          summary,
			"next_commands": []string{"./dm logs --json"},
		},
	}
	if status == operator.StatusWarning {
		step.Hints = append(step.Hints, "Broaden --window, remove --contains, or use --error-log when looking for startup/runtime errors.")
	}
	step.Hints = append(step.Hints, summary.Warnings...)
	rt.AddStep(step)

	if !logsJSON {
		printLogsSummary(cmd.OutOrStdout(), summary)
	}

	return rt.Complete(status, logsSummaryText(status, summary))
}

func inspectHALogs(config *haconfig.HAConfig) (*logsSummary, error) {
	logsLimit = normalizeLineLimit(logsLimit)
	if logsErrorLog {
		errorLog, err := fetchHAErrorLog(config, logsContains, logsLimit)
		if err != nil {
			return nil, err
		}
		return &logsSummary{
			Mode:       "error_log",
			Source:     errorLog.Source,
			Contains:   logsContains,
			Lines:      errorLog.Lines,
			Truncated:  errorLog.Truncated,
			EntryCount: len(errorLog.Lines),
			Warnings:   errorLog.Warnings,
		}, nil
	}

	entries, truncated, err := fetchHALogbook(config, logsEntity, logsContains, logsWindow, logsLimit)
	if err != nil {
		return nil, err
	}
	return &logsSummary{
		Mode:       "logbook",
		Entity:     logsEntity,
		Contains:   logsContains,
		Window:     logsWindow.String(),
		Entries:    entries,
		Truncated:  truncated,
		EntryCount: len(entries),
	}, nil
}

func fetchHALogbook(config *haconfig.HAConfig, entityID, contains string, window time.Duration, limit int) ([]map[string]any, bool, error) {
	end := time.Now().UTC()
	start := end.Add(-window)

	query := url.Values{}
	query.Set("end_time", end.Format(time.RFC3339Nano))
	if entityID != "" {
		query.Set("entity", entityID)
	}
	endpoint := fmt.Sprintf("%s/api/logbook/%s?%s",
		strings.TrimSuffix(config.URL, "/"),
		url.PathEscape(start.Format(time.RFC3339Nano)),
		query.Encode(),
	)

	body, err := haGETFunc(config.Token, endpoint)
	if err != nil {
		return nil, false, err
	}

	var raw []map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, false, fmt.Errorf("decode logbook: %w", err)
	}

	filtered := make([]map[string]any, 0, len(raw))
	needle := strings.ToLower(contains)
	for _, entry := range raw {
		if needle != "" {
			encoded, _ := json.Marshal(entry)
			if !strings.Contains(strings.ToLower(string(encoded)), needle) {
				continue
			}
		}
		filtered = append(filtered, entry)
	}

	truncated := false
	if len(filtered) > limit {
		truncated = true
		filtered = filtered[len(filtered)-limit:]
	}
	return filtered, truncated, nil
}

func fetchHAErrorLog(config *haconfig.HAConfig, contains string, limit int) (haErrorLogResult, error) {
	limit = normalizeLineLimit(limit)
	endpoint := strings.TrimSuffix(config.URL, "/") + "/api/error_log"
	body, err := haGETFunc(config.Token, endpoint)
	if err != nil {
		if !haStatusCodeIs(err, http.StatusNotFound) {
			return haErrorLogResult{}, err
		}
		fallback, fallbackErr := fetchHASystemLogFunc(config, contains, limit)
		if fallbackErr != nil {
			return haErrorLogResult{}, fmt.Errorf("raw HA error log unavailable (%w); %s fallback failed: %w", err, systemLogFallbackSource, fallbackErr)
		}
		fallback.Warnings = append([]string{rawErrorLogMissingHint}, fallback.Warnings...)
		return fallback, nil
	}

	lines, truncated := rawErrorLogLines(body, contains, limit)
	return haErrorLogResult{
		Lines:     lines,
		Truncated: truncated,
		Source:    rawErrorLogSource,
	}, nil
}

func fetchHASystemLog(config *haconfig.HAConfig, contains string, limit int) (haErrorLogResult, error) {
	limit = normalizeLineLimit(limit)
	ws := hasync.NewWSClient(config.URL, config.Token)
	ws.SetContext(commandContext())
	if err := ws.ConnectContext(commandContext()); err != nil {
		return haErrorLogResult{}, fmt.Errorf("WebSocket connect: %w", err)
	}
	defer ws.Close()

	raw, err := ws.SendCommand(systemLogFallbackSource)
	if err != nil {
		return haErrorLogResult{}, err
	}

	var entries []haSystemLogEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return haErrorLogResult{}, fmt.Errorf("decode system log: %w", err)
	}

	lines, truncated := systemLogEntriesToLines(entries, contains, limit)
	return haErrorLogResult{
		Lines:     lines,
		Truncated: truncated,
		Source:    systemLogFallbackSource,
	}, nil
}

func rawErrorLogLines(body []byte, contains string, limit int) ([]string, bool) {
	limit = normalizeLineLimit(limit)
	needle := strings.ToLower(contains)
	var lines []string
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(line), needle) {
			continue
		}
		lines = append(lines, line)
	}

	if len(lines) > limit {
		return lines[len(lines)-limit:], true
	}
	return lines, false
}

func systemLogEntriesToLines(entries []haSystemLogEntry, contains string, limit int) ([]string, bool) {
	limit = normalizeLineLimit(limit)
	needle := strings.ToLower(contains)
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		line := formatHASystemLogEntry(entry)
		if needle != "" && !strings.Contains(strings.ToLower(systemLogSearchText(entry, line)), needle) {
			continue
		}
		lines = append(lines, line)
	}

	truncated := false
	if len(lines) > limit {
		truncated = true
		lines = lines[:limit]
	}
	return lines, truncated
}

func normalizeLineLimit(limit int) int {
	if limit < 1 {
		return 1
	}
	return limit
}

func haStatusCodeIs(err error, status int) bool {
	var httpErr *haHTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == status
	}
	return false
}

func formatHASystemLogEntry(entry haSystemLogEntry) string {
	var parts []string
	if when := entryTime(entry.Timestamp); !when.IsZero() {
		parts = append(parts, when.Format(time.RFC3339))
	}
	if entry.Level != "" {
		parts = append(parts, entry.Level)
	}
	if entry.Name != "" {
		parts = append(parts, "["+entry.Name+"]")
	}
	message := strings.Join(entry.Message, " | ")
	if message == "" {
		message = exceptionSummary(entry.Exception)
	}
	if message == "" {
		message = "(no message)"
	}
	parts = append(parts, message)

	if summary := exceptionSummary(entry.Exception); summary != "" && !strings.Contains(message, summary) {
		parts = append(parts, "exception="+summary)
	}
	if source := systemLogSource(entry.Source); source != "" {
		parts = append(parts, "source="+source)
	}
	if entry.Count > 1 {
		parts = append(parts, fmt.Sprintf("count=%d", entry.Count))
	}
	return strings.Join(parts, " ")
}

func systemLogSearchText(entry haSystemLogEntry, formatted string) string {
	return strings.Join([]string{
		formatted,
		entry.Name,
		entry.Level,
		strings.Join(entry.Message, "\n"),
		entry.Exception,
		systemLogSource(entry.Source),
	}, "\n")
}

func entryTime(timestamp float64) time.Time {
	if timestamp <= 0 {
		return time.Time{}
	}
	return time.Unix(0, int64(timestamp*float64(time.Second))).UTC()
}

func exceptionSummary(exception string) string {
	lines := strings.Split(exception, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			return line
		}
	}
	return ""
}

func systemLogSource(source []any) string {
	if len(source) < 2 {
		return ""
	}
	path, _ := source[0].(string)
	lineNumber, _ := source[1].(float64)
	if path == "" {
		return ""
	}
	if lineNumber <= 0 {
		return path
	}
	return fmt.Sprintf("%s:%d", path, int(lineNumber))
}

func haGET(token, endpoint string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := hahttp.NewClient(30 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, &haHTTPError{StatusCode: resp.StatusCode}
	}
	return io.ReadAll(resp.Body)
}

func printLogsSummary(w io.Writer, summary *logsSummary) {
	fmt.Fprintf(w, "\nHome Assistant %s\n", summary.Mode)
	if summary.Source != "" {
		fmt.Fprintf(w, "  Source: %s\n", summary.Source)
	}
	for _, warning := range summary.Warnings {
		fmt.Fprintf(w, "  Warning: %s\n", warning)
	}
	if summary.EntryCount == 0 {
		fmt.Fprintln(w, "  No matching entries.")
		return
	}
	for _, entry := range summary.Entries {
		when, _ := entry["when"].(string)
		entity, _ := entry["entity_id"].(string)
		message, _ := entry["message"].(string)
		name, _ := entry["name"].(string)
		fmt.Fprintf(w, "  %s %s %s %s\n", when, entity, name, message)
	}
	for _, line := range summary.Lines {
		fmt.Fprintf(w, "  %s\n", line)
	}
	if summary.Truncated {
		fmt.Fprintln(w, "  ... older matches omitted by --limit")
	}
}

func logsSummaryText(status operator.Status, summary *logsSummary) string {
	if status == operator.StatusWarning {
		return fmt.Sprintf("no %s entries matched", summary.Mode)
	}
	return fmt.Sprintf("log inspection matched %d %s item(s)", summary.EntryCount, summary.Mode)
}
