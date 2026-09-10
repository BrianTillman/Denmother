package cmd

import (
	"strings"
	"testing"

	"github.com/BrianTillman/Denmother/internal/operator"
)

func TestAuditSummaryAccountsForUnevaluatedEvents(t *testing.T) {
	for _, tc := range []struct {
		name    string
		summary auditSummary
		want    operator.Status
	}{
		{"all no data", auditSummary{TriggerEvents: 1, NoData: 1}, operator.StatusWarning},
		{"partial coverage", auditSummary{TriggerEvents: 2, Passed: 1, NoData: 1}, operator.StatusPartial},
		{"failed with gaps", auditSummary{TriggerEvents: 2, Failed: 1, NoData: 1}, operator.StatusFailure},
		{"evaluated", auditSummary{TriggerEvents: 1, Passed: 1}, operator.StatusSuccess},
		{"canceled", auditSummary{TriggerEvents: 1, Canceled: 1}, operator.StatusSuccess},
		{"no activity", auditSummary{}, operator.StatusWarning},
		{"fetch warning", auditSummary{Warnings: []string{"history incomplete"}}, operator.StatusPartial},
		{"trace inventory", auditSummary{TraceOnly: true, TraceCount: 1}, operator.StatusSuccess},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.summary.Status(); got != tc.want {
				t.Fatalf("status = %s, want %s", got, tc.want)
			}
			step := tc.summary.Step("audit", "Audit")
			if step.Status != tc.want || step.Details["no_data"] != tc.summary.NoData {
				t.Fatalf("step lost status or coverage: %+v", step)
			}
			if tc.summary.NoData > 0 {
				if text := auditSummaryText(&tc.summary); strings.Contains(text, "audit passed") || strings.Contains(text, "no production activity") || !strings.Contains(text, "not evaluated") {
					t.Fatalf("misleading summary: %s", text)
				}
			}
		})
	}
}
