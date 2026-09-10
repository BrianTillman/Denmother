package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/BrianTillman/Denmother/internal/haconfig"
)

func TestFetchHAErrorLogFallsBackToSystemLogOn404(t *testing.T) {
	oldGET := haGETFunc
	oldSystemLog := fetchHASystemLogFunc
	defer func() {
		haGETFunc = oldGET
		fetchHASystemLogFunc = oldSystemLog
	}()

	haGETFunc = func(token, endpoint string) ([]byte, error) {
		if !strings.HasSuffix(endpoint, "/api/error_log") {
			t.Fatalf("endpoint = %q, want /api/error_log suffix", endpoint)
		}
		return nil, &haHTTPError{StatusCode: 404}
	}
	fetchHASystemLogFunc = func(config *haconfig.HAConfig, contains string, limit int) (haErrorLogResult, error) {
		if contains != "office" {
			t.Fatalf("contains = %q, want office", contains)
		}
		if limit != 7 {
			t.Fatalf("limit = %d, want 7", limit)
		}
		return haErrorLogResult{
			Lines:  []string{"system log fallback line"},
			Source: systemLogFallbackSource,
		}, nil
	}

	result, err := fetchHAErrorLog(&haconfig.HAConfig{URL: "http://hass.example", Token: "token"}, "office", 7)
	if err != nil {
		t.Fatalf("fetchHAErrorLog returned error: %v", err)
	}
	if result.Source != systemLogFallbackSource {
		t.Fatalf("Source = %q, want %q", result.Source, systemLogFallbackSource)
	}
	if len(result.Lines) != 1 || result.Lines[0] != "system log fallback line" {
		t.Fatalf("Lines = %#v, want fallback line", result.Lines)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "/api/error_log returned 404") {
		t.Fatalf("Warnings = %#v, want 404 fallback warning", result.Warnings)
	}
}

func TestInspectHALogsIncludesErrorLogFallbackSource(t *testing.T) {
	oldGET := haGETFunc
	oldSystemLog := fetchHASystemLogFunc
	oldErrorLog := logsErrorLog
	oldContains := logsContains
	oldLimit := logsLimit
	defer func() {
		haGETFunc = oldGET
		fetchHASystemLogFunc = oldSystemLog
		logsErrorLog = oldErrorLog
		logsContains = oldContains
		logsLimit = oldLimit
	}()

	logsErrorLog = true
	logsContains = "office"
	logsLimit = 0
	haGETFunc = func(token, endpoint string) ([]byte, error) {
		return nil, &haHTTPError{StatusCode: 404}
	}
	fetchHASystemLogFunc = func(config *haconfig.HAConfig, contains string, limit int) (haErrorLogResult, error) {
		if limit != 1 {
			t.Fatalf("limit = %d, want normalized limit 1", limit)
		}
		return haErrorLogResult{
			Lines:  []string{"office fallback"},
			Source: systemLogFallbackSource,
		}, nil
	}

	summary, err := inspectHALogs(&haconfig.HAConfig{URL: "http://hass.example", Token: "token"})
	if err != nil {
		t.Fatalf("inspectHALogs returned error: %v", err)
	}
	if summary.Source != systemLogFallbackSource {
		t.Fatalf("Source = %q, want %q", summary.Source, systemLogFallbackSource)
	}
	if summary.EntryCount != 1 || len(summary.Lines) != 1 {
		t.Fatalf("summary count/lines = %d/%#v, want one fallback line", summary.EntryCount, summary.Lines)
	}
	if len(summary.Warnings) != 1 || !strings.Contains(summary.Warnings[0], "system_log/list fallback") {
		t.Fatalf("Warnings = %#v, want fallback warning", summary.Warnings)
	}
}

func TestFetchHAErrorLogDoesNotFallbackOnNon404(t *testing.T) {
	oldGET := haGETFunc
	oldSystemLog := fetchHASystemLogFunc
	defer func() {
		haGETFunc = oldGET
		fetchHASystemLogFunc = oldSystemLog
	}()

	haGETFunc = func(token, endpoint string) ([]byte, error) {
		return nil, &haHTTPError{StatusCode: 401}
	}
	fetchHASystemLogFunc = func(config *haconfig.HAConfig, contains string, limit int) (haErrorLogResult, error) {
		t.Fatal("system_log/list fallback should not run for non-404 errors")
		return haErrorLogResult{}, nil
	}

	_, err := fetchHAErrorLog(&haconfig.HAConfig{URL: "http://hass.example", Token: "token"}, "", 10)
	if err == nil {
		t.Fatal("fetchHAErrorLog returned nil error, want 401")
	}
	var httpErr *haHTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != 401 {
		t.Fatalf("error = %v, want 401 haHTTPError", err)
	}
}

func TestRawErrorLogLinesKeepsMostRecentMatches(t *testing.T) {
	lines, truncated := rawErrorLogLines([]byte("one\noffice older\ntwo\noffice newer\n"), "office", 1)
	if !truncated {
		t.Fatal("truncated = false, want true")
	}
	if len(lines) != 1 || lines[0] != "office newer" {
		t.Fatalf("lines = %#v, want newest matching raw line", lines)
	}
}

func TestRawErrorLogLinesNormalizesInvalidLimit(t *testing.T) {
	lines, truncated := rawErrorLogLines([]byte("older\nnewer\n"), "", -1)
	if !truncated {
		t.Fatal("truncated = false, want true")
	}
	if len(lines) != 1 || lines[0] != "newer" {
		t.Fatalf("lines = %#v, want one newest line", lines)
	}
}

func TestSystemLogEntriesToLinesKeepsNewestLIFOEntries(t *testing.T) {
	lines, truncated := systemLogEntriesToLines([]haSystemLogEntry{
		{Name: "new", Level: "ERROR", Message: []string{"office newest"}},
		{Name: "old", Level: "ERROR", Message: []string{"office oldest"}},
	}, "office", 1)
	if !truncated {
		t.Fatal("truncated = false, want true")
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "[new]") {
		t.Fatalf("lines = %#v, want first system_log/list entry", lines)
	}
}

func TestSystemLogEntriesToLinesNormalizesInvalidLimit(t *testing.T) {
	lines, truncated := systemLogEntriesToLines([]haSystemLogEntry{
		{Name: "new", Level: "ERROR", Message: []string{"newest"}},
		{Name: "old", Level: "ERROR", Message: []string{"oldest"}},
	}, "", -1)
	if !truncated {
		t.Fatal("truncated = false, want true")
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "[new]") {
		t.Fatalf("lines = %#v, want one newest system-log line", lines)
	}
}

func TestFetchHAErrorLogParsesRawRESTLog(t *testing.T) {
	oldGET := haGETFunc
	oldSystemLog := fetchHASystemLogFunc
	defer func() {
		haGETFunc = oldGET
		fetchHASystemLogFunc = oldSystemLog
	}()

	haGETFunc = func(token, endpoint string) ([]byte, error) {
		return []byte("first line\nOffice warning\nother line\n"), nil
	}
	fetchHASystemLogFunc = func(config *haconfig.HAConfig, contains string, limit int) (haErrorLogResult, error) {
		t.Fatal("system_log/list fallback should not run when REST succeeds")
		return haErrorLogResult{}, nil
	}

	result, err := fetchHAErrorLog(&haconfig.HAConfig{URL: "http://hass.example", Token: "token"}, "office", 10)
	if err != nil {
		t.Fatalf("fetchHAErrorLog returned error: %v", err)
	}
	if result.Source != rawErrorLogSource {
		t.Fatalf("Source = %q, want %q", result.Source, rawErrorLogSource)
	}
	if len(result.Lines) != 1 || result.Lines[0] != "Office warning" {
		t.Fatalf("Lines = %#v, want filtered raw line", result.Lines)
	}
}

func TestFormatHASystemLogEntryIncludesUsefulFields(t *testing.T) {
	line := formatHASystemLogEntry(haSystemLogEntry{
		Name:      "homeassistant.components.test",
		Level:     "ERROR",
		Message:   []string{"Failed to update office sensor"},
		Exception: "Traceback\nValueError: bad value",
		Source:    []any{"components/test/__init__.py", float64(42)},
		Timestamp: 1700000000,
		Count:     3,
	})

	for _, want := range []string{
		"ERROR",
		"[homeassistant.components.test]",
		"Failed to update office sensor",
		"exception=ValueError: bad value",
		"source=components/test/__init__.py:42",
		"count=3",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("formatted line %q does not contain %q", line, want)
		}
	}
}
