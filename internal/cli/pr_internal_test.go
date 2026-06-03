package cli

import "testing"

// TestIsWIPSubject covers the heuristic that protects PR titles from being
// auto-WIP commit subjects.
func TestIsWIPSubject(t *testing.T) {
	tests := []struct {
		subject string
		slug    string
		want    bool
	}{
		{"", "anything", true},                                  // empty subject
		{"   ", "anything", true},                               // whitespace-only
		{"WIP: my-task", "my-task", true},                       // exact auto-WIP from finish/pr
		{"WIP: anything", "anything", true},
		{"WIP: not-the-slug", "actual-slug", true},              // any WIP: prefix
		{"WIP something", "x", true},
		{"wip: lowercase", "x", true},
		{"wip lowercase", "x", true},
		{"WIP snapshot before worktree removal (2026-06-01)", "x", true}, // rm's wip commit
		{"Add login flow", "auth-fix", false},                   // real subject
		{"Fix #123: payment race", "fix-payment", false},
		{"WIPPER not actually wip", "x", false},                 // not a wip prefix
		{"wipo no space after", "x", false},
	}
	for _, tt := range tests {
		got := isWIPSubject(tt.subject, tt.slug)
		if got != tt.want {
			t.Errorf("isWIPSubject(%q, %q) = %v, want %v", tt.subject, tt.slug, got, tt.want)
		}
	}
}
