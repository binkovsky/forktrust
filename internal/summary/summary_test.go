package summary

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/binkovsky/forktrust/internal/config"
)

func TestCheck_NilConfig(t *testing.T) {
	v, err := Check([]Commit{{SHA: "abc", Subject: "foo"}}, nil)
	if err != nil {
		t.Fatalf("nil cfg should not error: %v", err)
	}
	if v != nil {
		t.Errorf("nil cfg should produce no violations, got %v", v)
	}
}

func TestCheck_RequiredButEmpty(t *testing.T) {
	cfg := &config.SummaryConfig{Required: true}
	v, _ := Check(nil, cfg)
	if len(v) != 1 || v[0].Rule != "required" {
		t.Errorf("expected one 'required' violation, got %v", v)
	}
}

func TestCheck_SubjectPrefix(t *testing.T) {
	cfg := &config.SummaryConfig{RequireSubjectPrefix: []string{"feat", "fix", "docs"}}
	cases := []struct {
		subject string
		wantOK  bool
	}{
		{"feat: add login", true},
		{"feat(auth): add login", true},
		{"feat!: rewrite api", true},
		{"feat(auth)!: rewrite", true},
		{"fix: bug", true},
		{"docs: readme", true},
		{"chore: tidy", false},
		{"feat add login", false},   // missing ":"
		{"feature: add login", false}, // not in list
		{"feat:no space", false},
		{"feat(open: bug", false}, // unclosed paren — actually we'd see "feat(open: bug" → after rest="(open: bug" we look for ")" not found → continue → no other prefix matches → false. Good.
	}
	for _, tc := range cases {
		got := hasAnyPrefix(tc.subject, cfg.RequireSubjectPrefix)
		if got != tc.wantOK {
			t.Errorf("hasAnyPrefix(%q) = %v, want %v", tc.subject, got, tc.wantOK)
		}
	}
}

func TestCheck_BodyLength(t *testing.T) {
	cfg := &config.SummaryConfig{MinBodyLength: 10, MaxBodyLength: 50}
	commits := []Commit{
		{SHA: "a", Subject: "feat: x", Body: "short"},                    // too short
		{SHA: "b", Subject: "feat: x", Body: "this body is just enough"}, // OK
		{SHA: "c", Subject: "feat: x", Body: strings.Repeat("y", 100)},   // too long
	}
	v, _ := Check(commits, cfg)
	rules := map[string]int{}
	for _, x := range v {
		rules[x.Rule]++
	}
	if rules["min_body_length"] != 1 {
		t.Errorf("min_body_length: want 1, got %d", rules["min_body_length"])
	}
	if rules["max_body_length"] != 1 {
		t.Errorf("max_body_length: want 1, got %d", rules["max_body_length"])
	}
}

func TestCheck_TicketPattern(t *testing.T) {
	cfg := &config.SummaryConfig{RequireTicketPattern: `[A-Z]+-[0-9]+`}
	commits := []Commit{
		{SHA: "a", Subject: "feat: PROJ-123 add login"},     // OK (in subject)
		{SHA: "b", Subject: "feat: add login", Body: "PROJ-1"}, // OK (in body)
		{SHA: "c", Subject: "feat: add login", Body: "no ticket"}, // fail
	}
	v, _ := Check(commits, cfg)
	if len(v) != 1 || v[0].CommitSHA != "c" {
		t.Errorf("expected 1 violation on commit c, got %+v", v)
	}
}

func TestCheck_TicketPatternInvalidRegex(t *testing.T) {
	cfg := &config.SummaryConfig{RequireTicketPattern: `[invalid(`}
	_, err := Check([]Commit{{SHA: "a"}}, cfg)
	if err == nil {
		t.Error("invalid regex should error")
	}
}

func TestCheck_ForbiddenPatternsCaseInsensitive(t *testing.T) {
	cfg := &config.SummaryConfig{ForbiddenPatterns: []string{"WIP", "TODO"}}
	commits := []Commit{
		{SHA: "a", Subject: "feat: clean", Body: "all good"},        // OK
		{SHA: "b", Subject: "wip: still working"},                   // case-insensitive hit
		{SHA: "c", Subject: "feat: x", Body: "FIXME: todo later"},   // body hit
	}
	v, _ := Check(commits, cfg)
	if len(v) != 2 {
		t.Errorf("expected 2 violations, got %d: %+v", len(v), v)
	}
}

func TestCheck_MultiViolationPerCommit(t *testing.T) {
	cfg := &config.SummaryConfig{
		RequireSubjectPrefix: []string{"feat"},
		MinBodyLength:        20,
		ForbiddenPatterns:    []string{"WIP"},
	}
	commits := []Commit{
		{SHA: "a", Subject: "chore: WIP", Body: "no"}, // 3 violations
	}
	v, _ := Check(commits, cfg)
	if len(v) != 3 {
		t.Errorf("expected 3 violations on one commit, got %d: %+v", len(v), v)
	}
}

// TestLoadCommits exercises the git-log parser against a real repo.
func TestLoadCommits(t *testing.T) {
	repo := mkRepo(t)
	commit(t, repo, "feat: first\n\nbody one with detail")
	commit(t, repo, "fix: second\n\nbody two")

	commits, err := LoadCommits(repo, "", "HEAD")
	if err != nil {
		t.Fatalf("LoadCommits: %v", err)
	}
	if len(commits) != 2 {
		t.Fatalf("want 2 commits, got %d", len(commits))
	}
	// Newest first
	if commits[0].Subject != "fix: second" {
		t.Errorf("first commit subject = %q, want %q", commits[0].Subject, "fix: second")
	}
	if commits[0].Body != "body two" {
		t.Errorf("first commit body = %q", commits[0].Body)
	}
	if commits[1].Subject != "feat: first" {
		t.Errorf("second commit subject = %q", commits[1].Subject)
	}
}

func TestLoadCommits_Range(t *testing.T) {
	repo := mkRepo(t)
	commit(t, repo, "base: first")
	mustGit(t, repo, "branch", "main")
	commit(t, repo, "feat: second")
	commit(t, repo, "feat: third")

	commits, err := LoadCommits(repo, "main", "HEAD")
	if err != nil {
		t.Fatalf("LoadCommits: %v", err)
	}
	if len(commits) != 2 {
		t.Fatalf("want 2 commits in range, got %d: %+v", len(commits), commits)
	}
}

func TestLoadCommits_BadRef(t *testing.T) {
	repo := mkRepo(t)
	commit(t, repo, "feat: x")
	_, err := LoadCommits(repo, "", "nonexistent-ref-xyz")
	if err == nil {
		t.Error("expected error for bad ref")
	}
}

// --- helpers ---

func mkRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustGit(t, dir, "init", "-q", "-b", "feature")
	mustGit(t, dir, "config", "user.email", "test@test")
	mustGit(t, dir, "config", "user.name", "test")
	mustGit(t, dir, "config", "commit.gpgsign", "false")
	return dir
}

func commit(t *testing.T, repo, msg string) {
	t.Helper()
	// Add a unique file per commit so each is non-empty.
	name := filepath.Join(repo, "f-"+strings.ReplaceAll(strings.Split(msg, "\n")[0], " ", "_")+".txt")
	if err := os.WriteFile(name, []byte(msg), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "add", "-A")
	mustGit(t, repo, "commit", "-q", "-m", msg)
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
