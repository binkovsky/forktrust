package cli

import (
	"encoding/json"
	"testing"
)

// TestGhPRInfo_ParseRealisticJSON verifies we can decode a representative
// `gh pr view --json` payload without losing the fields forktrust uses.
func TestGhPRInfo_ParseRealisticJSON(t *testing.T) {
	raw := `{
		"number": 42,
		"url": "https://github.com/owner/repo/pull/42",
		"state": "OPEN",
		"isDraft": false,
		"mergeable": "MERGEABLE",
		"reviewDecision": "APPROVED",
		"title": "Add auth flow",
		"body": "Closes #1",
		"additions": 120,
		"deletions": 15,
		"changedFiles": 7,
		"baseRefName": "main",
		"headRefName": "fork/add-auth",
		"author": {"login": "dimas"},
		"updatedAt": "2026-06-01T12:34:56Z",
		"statusCheckRollup": [
			{"name": "lint", "status": "COMPLETED", "conclusion": "SUCCESS", "workflowName": "CI"},
			{"name": "tests", "status": "COMPLETED", "conclusion": "SUCCESS", "workflowName": "CI"}
		]
	}`
	var info ghPRInfo
	if err := json.Unmarshal([]byte(raw), &info); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if info.Number != 42 || info.URL != "https://github.com/owner/repo/pull/42" {
		t.Errorf("number/url mismatch: %+v", info)
	}
	if info.State != "OPEN" || info.Mergeable != "MERGEABLE" || info.ReviewDecision != "APPROVED" {
		t.Errorf("state/mergeable/review mismatch: %+v", info)
	}
	if info.Additions != 120 || info.Deletions != 15 || info.ChangedFiles != 7 {
		t.Errorf("diff stats mismatch: %+v", info)
	}
	if info.Author.Login != "dimas" {
		t.Errorf("author mismatch: %+v", info)
	}
	if len(info.StatusCheckRollup) != 2 {
		t.Fatalf("expected 2 checks, got %d", len(info.StatusCheckRollup))
	}
}

func TestChecksSummary(t *testing.T) {
	tests := []struct {
		name    string
		checks  []ghCheckRun
		want    checksSummary
	}{
		{
			name:   "no checks → NONE",
			checks: nil,
			want:   checksSummary{Overall: "NONE", Total: 0},
		},
		{
			name: "all SUCCESS → SUCCESS",
			checks: []ghCheckRun{
				{Name: "a", Status: "COMPLETED", Conclusion: "SUCCESS"},
				{Name: "b", Status: "COMPLETED", Conclusion: "SUCCESS"},
			},
			want: checksSummary{Overall: "SUCCESS", Total: 2, Passing: 2},
		},
		{
			name: "any failure → FAILURE (overrides pending)",
			checks: []ghCheckRun{
				{Name: "a", Status: "COMPLETED", Conclusion: "SUCCESS"},
				{Name: "b", Status: "COMPLETED", Conclusion: "FAILURE"},
				{Name: "c", Status: "IN_PROGRESS", Conclusion: ""},
			},
			want: checksSummary{Overall: "FAILURE", Total: 3, Passing: 1, Failing: 1, Pending: 1},
		},
		{
			name: "pending with no failure → PENDING",
			checks: []ghCheckRun{
				{Name: "a", Status: "COMPLETED", Conclusion: "SUCCESS"},
				{Name: "b", Status: "QUEUED", Conclusion: ""},
			},
			want: checksSummary{Overall: "PENDING", Total: 2, Passing: 1, Pending: 1},
		},
		{
			name: "neutral and skipped count as passing",
			checks: []ghCheckRun{
				{Name: "a", Status: "COMPLETED", Conclusion: "NEUTRAL"},
				{Name: "b", Status: "COMPLETED", Conclusion: "SKIPPED"},
			},
			want: checksSummary{Overall: "SUCCESS", Total: 2, Passing: 2},
		},
		{
			name: "cancelled/timed_out count as failures",
			checks: []ghCheckRun{
				{Name: "a", Status: "COMPLETED", Conclusion: "TIMED_OUT"},
				{Name: "b", Status: "COMPLETED", Conclusion: "CANCELLED"},
			},
			want: checksSummary{Overall: "FAILURE", Total: 2, Failing: 2},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &ghPRInfo{StatusCheckRollup: tt.checks}
			got := info.summarize()
			if got != tt.want {
				t.Errorf("summarize() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
