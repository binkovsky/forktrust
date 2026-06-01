package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRepoConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, RepoConfigFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Regression for the v0.6.1 P2 finding: ports.vars wrote raw strings into
// .env.local with no validation. A var name like "PORT\nINJECTED=1" produced
// two dotenv lines and was a trivial injection path. Validation MUST reject
// such names.
func TestValidate_PortsVars_RejectsNewlineInjection(t *testing.T) {
	dir := writeRepoConfig(t, "[ports]\nrange = \"3000-3099\"\nsize = 10\nvars = [\"PORT\\nINJECTED=1\"]\n")
	_, err := LoadRepoConfig(dir)
	if err == nil {
		t.Fatal("expected rejection of newline in var name")
	}
	if !strings.Contains(err.Error(), "POSIX env var name") && !strings.Contains(err.Error(), "must match") {
		t.Errorf("error should mention POSIX name rule, got %q", err.Error())
	}
}

func TestValidate_PortsVars_RejectsBadChars(t *testing.T) {
	cases := []string{
		`vars = [" PORT"]`,         // leading space
		`vars = ["PORT-X"]`,        // dash
		`vars = ["PORT=2"]`,        // equals
		`vars = ["1PORT"]`,         // digit start
		`vars = ["PORT;rm -rf /"]`, // shell metacharacters
		`vars = ["PORT\tX"]`,       // tab (Go literal escape)
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			dir := writeRepoConfig(t, "[ports]\nrange = \"3000-3099\"\nsize = 10\n"+c+"\n")
			if _, err := LoadRepoConfig(dir); err == nil {
				t.Errorf("expected rejection for %s", c)
			}
		})
	}
}

// R3 backward-compat: duplicates and reserved names are now accepted at parse
// time (no hard fail) and filtered at write time by SanitizedPortsVars with
// a user-visible warning. This keeps repos that had vars=["PORT_END"] from
// before v0.6.2 R1 working after upgrade.

func TestValidate_PortsVars_DuplicatesAcceptedAtParse(t *testing.T) {
	dir := writeRepoConfig(t, "[ports]\nrange = \"3000-3099\"\nsize = 10\nvars = [\"PORT\", \"PORT\"]\n")
	cfg, err := LoadRepoConfig(dir)
	if err != nil {
		t.Fatalf("duplicates must NOT hard-fail at parse (got %v)", err)
	}
	vars, warns := cfg.SanitizedPortsVars()
	if len(vars) != 1 || vars[0] != "PORT" {
		t.Errorf("SanitizedPortsVars should dedupe, got %v", vars)
	}
	if len(warns) == 0 || !strings.Contains(warns[0], "more than once") {
		t.Errorf("expected dedup warning, got %v", warns)
	}
}

func TestValidate_PortsVars_ReservedAcceptedAtParse(t *testing.T) {
	for _, name := range []string{"PORT_END", "FORKTRUST_PORT_START", "FORKTRUST_PORT_END", "FORKTRUST_PORT_SIZE"} {
		t.Run(name, func(t *testing.T) {
			dir := writeRepoConfig(t, "[ports]\nrange = \"3000-3099\"\nsize = 10\nvars = [\""+name+"\"]\n")
			cfg, err := LoadRepoConfig(dir)
			if err != nil {
				t.Fatalf("reserved name must NOT hard-fail at parse (backward-compat); got %v", err)
			}
			vars, warns := cfg.SanitizedPortsVars()
			if len(vars) != 0 {
				t.Errorf("reserved name should be filtered out, got %v", vars)
			}
			if len(warns) == 0 || !strings.Contains(warns[0], "reserved") {
				t.Errorf("expected reserved-name warning, got %v", warns)
			}
		})
	}
}

func TestValidate_PortsVars_AcceptsValid(t *testing.T) {
	dir := writeRepoConfig(t, `
[ports]
range = "3000-3099"
size = 10
vars = ["PORT", "NEXT_PUBLIC_PORT", "_PRIVATE_PORT", "SERVER_PORT_8080"]
`)
	if _, err := LoadRepoConfig(dir); err != nil {
		t.Errorf("expected accept, got %v", err)
	}
}
