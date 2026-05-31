package hooks

import (
	"strings"
	"testing"
)

func TestSecureJoin_Ok(t *testing.T) {
	cases := []struct {
		root, rel string
	}{
		{"/main", ".env"},
		{"/main", "config/local.json"},
		{"/main", "./a/b"},
		{"/main", "a/./b/../c"}, // ends as a/c — fine
		{"/main", "node_modules"},
	}
	for _, c := range cases {
		got, err := secureJoin(c.root, c.rel)
		if err != nil {
			t.Errorf("secureJoin(%q, %q): unexpected error %v", c.root, c.rel, err)
		}
		if !strings.HasPrefix(got, c.root) {
			t.Errorf("secureJoin(%q, %q) = %q, expected prefix %q", c.root, c.rel, got, c.root)
		}
	}
}

func TestSecureJoin_Rejects(t *testing.T) {
	cases := []struct {
		name, root, rel string
	}{
		{"absolute unix", "/main", "/etc/passwd"},
		{"absolute root", "/main", "/"},
		{"parent traversal", "/main", "../secret"},
		{"deep parent traversal", "/main", "../../../etc/passwd"},
		{"cleaned to parent", "/main", "a/../../escape"},
		{"empty", "/main", ""},
		{"single dotdot", "/main", ".."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := secureJoin(c.root, c.rel)
			if err == nil {
				t.Errorf("secureJoin(%q, %q) accepted unsafe input", c.root, c.rel)
			}
		})
	}
}

func TestSecureJoin_AllowsInternalDotDot(t *testing.T) {
	// "a/b/../c" cleans to "a/c" — entirely inside root, OK.
	got, err := secureJoin("/main", "a/b/../c")
	if err != nil {
		t.Fatalf("expected ok: %v", err)
	}
	if got != "/main/a/c" {
		t.Errorf("expected /main/a/c, got %s", got)
	}
}
