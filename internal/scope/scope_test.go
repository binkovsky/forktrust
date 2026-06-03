package scope

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseCSV(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"a/**", []string{"a/**"}},
		{"a/**,b/**", []string{"a/**", "b/**"}},
		{"a/**, b/**", []string{"a/**", "b/**"}},
		{"  src/**  ", []string{"src/**"}},
		{"a, , b", []string{"a", "b"}},
		{",,", nil},
		{"a/**, b/**, c.go", []string{"a/**", "b/**", "c.go"}},
	}
	for _, tt := range tests {
		got := ParseCSV(tt.in)
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("ParseCSV(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestScope_Validate(t *testing.T) {
	tests := []struct {
		name    string
		allowed []string
		wantErr string
	}{
		{"single ok", []string{"a/**"}, ""},
		{"multiple ok", []string{"a/**", "b/**.go", "go.mod"}, ""},
		{"star-star ok", []string{"**"}, ""},
		{"empty list rejected", []string{}, "is empty"},
		{"nil list rejected", nil, "is empty"},
		{"empty entry rejected", []string{"a/**", ""}, "is empty string"},
		{"absolute path rejected", []string{"/etc/**"}, "absolute paths are not allowed"},
		{"bad glob rejected", []string{"a/[unterminated"}, "invalid glob syntax"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Scope{Allowed: tt.allowed}
			err := s.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("Validate() unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Validate() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestCheck(t *testing.T) {
	tests := []struct {
		name           string
		allowed        []string
		changed        []string
		wantViolations []string
	}{
		{
			name:           "all in scope under single dir",
			allowed:        []string{"src/**"},
			changed:        []string{"src/a.go", "src/sub/b.go"},
			wantViolations: nil,
		},
		{
			name:           "exact file allowed",
			allowed:        []string{"go.mod", "go.sum"},
			changed:        []string{"go.mod", "go.sum"},
			wantViolations: nil,
		},
		{
			name:           "out-of-scope file flagged",
			allowed:        []string{"internal/auth/**"},
			changed:        []string{"internal/auth/login.go", "package-lock.json"},
			wantViolations: []string{"package-lock.json"},
		},
		{
			name:           "multiple violations preserved in order",
			allowed:        []string{"src/**"},
			changed:        []string{"src/a.go", "README.md", "go.mod", "src/b.go", "Makefile"},
			wantViolations: []string{"README.md", "go.mod", "Makefile"},
		},
		{
			name:           "star-star matches everything",
			allowed:        []string{"**"},
			changed:        []string{"a", "b/c", "d/e/f.go"},
			wantViolations: nil,
		},
		{
			name:           "alternation with braces",
			allowed:        []string{"{src,internal}/**"},
			changed:        []string{"src/a.go", "internal/b.go", "vendor/c.go"},
			wantViolations: []string{"vendor/c.go"},
		},
		{
			// doublestar v4 semantics: `**` matches across directory
			// boundaries. The canonical "all .md under docs/" is
			// `docs/**/*.md` (covers nested .md). Top-level .md needs a
			// second pattern `docs/*.md` — or use `docs/**` to cover both.
			name:           "extension glob via two patterns",
			allowed:        []string{"docs/*.md", "docs/**/*.md"},
			changed:        []string{"docs/a.md", "docs/sub/b.md", "docs/c.txt"},
			wantViolations: []string{"docs/c.txt"},
		},
		{
			name:           "empty changed = no violations",
			allowed:        []string{"src/**"},
			changed:        nil,
			wantViolations: nil,
		},
		{
			name:           "single-segment * does NOT cross directories",
			allowed:        []string{"src/*.go"},
			changed:        []string{"src/a.go", "src/sub/b.go"},
			wantViolations: []string{"src/sub/b.go"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Check(tt.allowed, tt.changed)
			if !reflect.DeepEqual(got, tt.wantViolations) {
				t.Errorf("Check() = %v, want %v", got, tt.wantViolations)
			}
		})
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	repo := t.TempDir()
	s := &Scope{
		Allowed:   []string{"internal/auth/**", "cmd/api/**", "go.mod"},
		CreatedBy: "forktrust new my-task --scope ...",
		CreatedAt: "2026-06-01T12:34:56Z",
	}
	if err := Save(repo, "my-task", s); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// File must exist at expected path.
	expected := filepath.Join(repo, ".forktrust", "scopes", "my-task.toml")
	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("scope file missing: %v", err)
	}

	// Round-trip via Load.
	got, err := Load(repo, "my-task")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got.Allowed, s.Allowed) {
		t.Errorf("allowed mismatch: got %v want %v", got.Allowed, s.Allowed)
	}
	if got.CreatedBy != s.CreatedBy {
		t.Errorf("created_by mismatch: got %q want %q", got.CreatedBy, s.CreatedBy)
	}
}

func TestLoad_MissingFile_ReturnsNilNil(t *testing.T) {
	repo := t.TempDir()
	s, err := Load(repo, "never-created")
	if err != nil {
		t.Errorf("Load on missing file should return nil err, got: %v", err)
	}
	if s != nil {
		t.Errorf("Load on missing file should return nil scope, got: %+v", s)
	}
}

func TestRemove_Idempotent(t *testing.T) {
	repo := t.TempDir()
	// Remove on missing — no error.
	if err := Remove(repo, "never-created"); err != nil {
		t.Errorf("Remove on missing should be no-op, got: %v", err)
	}
	// Save + remove + remove again.
	if err := Save(repo, "x", &Scope{Allowed: []string{"**"}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := Remove(repo, "x"); err != nil {
		t.Errorf("Remove existing: %v", err)
	}
	if err := Remove(repo, "x"); err != nil {
		t.Errorf("second Remove: %v", err)
	}
}

func TestSave_RejectsInvalidScope(t *testing.T) {
	repo := t.TempDir()
	err := Save(repo, "x", &Scope{Allowed: []string{}})
	if err == nil {
		t.Error("Save with empty Allowed should error")
	}
}
