package operator

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestExitCodeForStatus(t *testing.T) {
	t.Parallel()

	cases := map[Status]int{
		StatusSuccess: ExitSuccess,
		StatusWarning: ExitWarning,
		StatusPartial: ExitPartial,
		StatusFailure: ExitFailure,
	}

	for status, want := range cases {
		if got := ExitCodeForStatus(status); got != want {
			t.Fatalf("ExitCodeForStatus(%q) = %d, want %d", status, got, want)
		}
	}
}

func TestMergeStatusPrefersHigherSeverity(t *testing.T) {
	t.Parallel()

	if got := MergeStatus(StatusWarning, StatusFailure); got != StatusFailure {
		t.Fatalf("MergeStatus warning->failure = %q, want %q", got, StatusFailure)
	}
	if got := MergeStatus(StatusPartial, StatusSuccess); got != StatusPartial {
		t.Fatalf("MergeStatus partial->success = %q, want %q", got, StatusPartial)
	}
}

func TestRuntimeCompleteJSONIncludesExitCode(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	rt := NewRuntime("check", true, &out)

	err := rt.Complete(StatusPartial, "follow-up needed")
	if err == nil {
		t.Fatal("expected non-nil exit error for partial result")
	}

	exitErr, ok := err.(*ExitError)
	if !ok {
		t.Fatalf("expected ExitError, got %T", err)
	}
	if exitErr.ExitCode() != ExitPartial {
		t.Fatalf("ExitCode() = %d, want %d", exitErr.ExitCode(), ExitPartial)
	}

	var result Result
	if decodeErr := json.Unmarshal(out.Bytes(), &result); decodeErr != nil {
		t.Fatalf("failed to decode runtime JSON: %v", decodeErr)
	}
	if result.Command != "check" {
		t.Fatalf("result.Command = %q, want %q", result.Command, "check")
	}
	if result.SchemaVersion != SchemaVersion {
		t.Fatalf("result.SchemaVersion = %q, want %q", result.SchemaVersion, SchemaVersion)
	}
	if result.RunID == "" {
		t.Fatal("expected result.RunID to be populated")
	}
	if result.CWD == "" {
		t.Fatal("expected result.CWD to be populated")
	}
	if result.ExitCode != ExitPartial {
		t.Fatalf("result.ExitCode = %d, want %d", result.ExitCode, ExitPartial)
	}
	if result.Status != StatusPartial {
		t.Fatalf("result.Status = %q, want %q", result.Status, StatusPartial)
	}
}

func TestRuntimePromotesCommonStepDetails(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	rt := NewRuntime("agent", true, &out)
	rt.AddStep(Step{
		ID:      "review",
		Title:   "Review",
		Status:  StatusSuccess,
		Summary: "ok",
		Details: map[string]any{
			"artifacts":     []string{"AGENTS.md"},
			"next_commands": []string{"./dm agent lint --json"},
		},
	})
	if err := rt.Complete(StatusSuccess, "ok"); err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}

	var result Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(result.Steps) != 1 {
		t.Fatalf("len(result.Steps) = %d, want 1", len(result.Steps))
	}
	if got := result.Steps[0].Artifacts; len(got) != 1 || got[0] != "AGENTS.md" {
		t.Fatalf("Artifacts = %#v, want AGENTS.md", got)
	}
	if got := result.Steps[0].NextCommands; len(got) != 1 || got[0] != "./dm agent lint --json" {
		t.Fatalf("NextCommands = %#v", got)
	}
}

func TestResultJSONSchemaDocumentsContractVersion(t *testing.T) {
	t.Parallel()

	schema := ResultJSONSchema()
	if !bytes.Contains([]byte(schema), []byte(SchemaVersion)) {
		t.Fatalf("schema does not contain schema version %q", SchemaVersion)
	}
	if !bytes.Contains([]byte(schema), []byte("run_id")) {
		t.Fatal("schema does not document run_id")
	}
}
