package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/binkovsky/forktrust/internal/config"
	"github.com/binkovsky/forktrust/internal/git"
	"github.com/binkovsky/forktrust/internal/ports"
)

var (
	doctorJSON    bool
	doctorProject string
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Diagnose forktrust setup and project health",
	Long: `Run a battery of health checks across the forktrust install and registered
projects. Reports each check as ok / warn / fail with a one-line hint.

Without --project: checks every registered project + global state.
With --project <name>: focuses checks on that project.

Exit codes:
  0  all checks passed (warnings allowed)
  1  one or more checks failed`,
	Args: cobra.NoArgs,
	RunE: runDoctor,
}

func init() {
	doctorCmd.Flags().BoolVar(&doctorJSON, "json", false, "emit checks as JSON for programmatic consumption")
	doctorCmd.Flags().StringVarP(&doctorProject, "project", "p", "", "focus checks on one registered project")
}

// checkStatus is one of ok | warn | fail (also used as the JSON status field).
type checkStatus string

const (
	statusOK   checkStatus = "ok"
	statusWarn checkStatus = "warn"
	statusFail checkStatus = "fail"
)

// doctorCheck is one report row.
type doctorCheck struct {
	Name    string      `json:"name"`
	Scope   string      `json:"scope,omitempty"` // "global" or project name
	Status  checkStatus `json:"status"`
	Message string      `json:"message"`
	Hint    string      `json:"hint,omitempty"`
}

type doctorReport struct {
	Version string        `json:"version"`
	Checks  []doctorCheck `json:"checks"`
	Summary struct {
		OK   int `json:"ok"`
		Warn int `json:"warn"`
		Fail int `json:"fail"`
	} `json:"summary"`
}

func runDoctor(_ *cobra.Command, _ []string) error {
	rep := doctorReport{Version: version}

	cfg, err := config.Load()
	if err != nil {
		rep.add(doctorCheck{Name: "config-load", Scope: "global", Status: statusFail,
			Message: fmt.Sprintf("cannot read forktrust config: %v", err),
			Hint:    "try `forktrust config add <repo-path>` to initialize"})
		return rep.emit()
	}

	rep.add(checkGitInstalled())
	rep.add(checkGhInstalled())
	rep.add(checkBrewFreshness())
	rep.add(checkPortsStore())

	projects := cfg.AllProjects()
	if doctorProject != "" {
		filtered := projects[:0]
		for _, p := range projects {
			if p.Name == doctorProject {
				filtered = append(filtered, p)
			}
		}
		projects = filtered
		if len(projects) == 0 {
			rep.add(doctorCheck{Name: "project-exists", Scope: doctorProject, Status: statusFail,
				Message: fmt.Sprintf("no project named %q registered", doctorProject),
				Hint:    "list with `forktrust config list`"})
			return rep.emit()
		}
	}

	if len(projects) == 0 {
		rep.add(doctorCheck{Name: "projects-registered", Scope: "global", Status: statusWarn,
			Message: "no projects registered",
			Hint:    "register a repo with `forktrust config add <path>`"})
	}

	for _, p := range projects {
		checkProject(&rep, p)
	}

	return rep.emit()
}

// add appends a check, updating the running summary counters.
func (r *doctorReport) add(c doctorCheck) {
	r.Checks = append(r.Checks, c)
	switch c.Status {
	case statusOK:
		r.Summary.OK++
	case statusWarn:
		r.Summary.Warn++
	case statusFail:
		r.Summary.Fail++
	}
}

// emit prints the report (json or human) and returns the final coded error.
func (r *doctorReport) emit() error {
	if doctorJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(r)
	} else {
		fmt.Printf("forktrust doctor (v%s)\n", r.Version)
		fmt.Println(strings.Repeat("-", 40))
		for _, c := range r.Checks {
			mark := "[ ok ]"
			switch c.Status {
			case statusWarn:
				mark = "[warn]"
			case statusFail:
				mark = "[FAIL]"
			}
			scope := c.Scope
			if scope == "" {
				scope = "global"
			}
			fmt.Printf("%s %-22s %s: %s\n", mark, c.Name, scope, c.Message)
			if c.Hint != "" {
				fmt.Printf("       hint: %s\n", c.Hint)
			}
		}
		fmt.Println(strings.Repeat("-", 40))
		fmt.Printf("summary: %d ok, %d warn, %d fail\n", r.Summary.OK, r.Summary.Warn, r.Summary.Fail)
	}
	if r.Summary.Fail > 0 {
		return &CodedError{Code: ExitGenericError, Err: fmt.Errorf("%d check(s) failed", r.Summary.Fail)}
	}
	return nil
}

// ---------- individual checks ----------

func checkGitInstalled() doctorCheck {
	out, err := exec.Command("git", "--version").Output()
	if err != nil {
		return doctorCheck{Name: "git", Scope: "global", Status: statusFail,
			Message: "git binary not found in PATH",
			Hint:    "install git: https://git-scm.com/downloads"}
	}
	return doctorCheck{Name: "git", Scope: "global", Status: statusOK,
		Message: strings.TrimSpace(string(out))}
}

func checkGhInstalled() doctorCheck {
	out, err := exec.Command("gh", "--version").Output()
	if err != nil {
		return doctorCheck{Name: "gh", Scope: "global", Status: statusWarn,
			Message: "gh CLI not installed (needed for forktrust pr / pr-status — v0.7.4+)",
			Hint:    "install with `brew install gh`"}
	}
	first := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	// Also probe authentication — having `gh` installed but unauthenticated
	// is a common source of confusion when `forktrust pr` fails at runtime.
	if authOut, authErr := exec.Command("gh", "auth", "status").CombinedOutput(); authErr != nil {
		return doctorCheck{Name: "gh", Scope: "global", Status: statusWarn,
			Message: first + " (installed but NOT authenticated)",
			Hint:    "run `gh auth login` — without it, `forktrust pr` will exit 17. Details: " + truncateOutput(strings.TrimSpace(string(authOut)), 200)}
	}
	return doctorCheck{Name: "gh", Scope: "global", Status: statusOK, Message: first + " (authenticated)"}
}

func checkBrewFreshness() doctorCheck {
	// If brew is not installed (Linux without brew), skip silently.
	if _, err := exec.LookPath("brew"); err != nil {
		return doctorCheck{Name: "brew-version", Scope: "global", Status: statusOK,
			Message: "brew not installed (skipped)"}
	}
	// Fast path: check brew list output. Avoids network round-trip.
	out, err := exec.Command("brew", "list", "--versions", "binkovsky/forktrust/forktrust").Output()
	if err != nil || len(out) == 0 {
		return doctorCheck{Name: "brew-version", Scope: "global", Status: statusWarn,
			Message: "forktrust not installed via brew tap",
			Hint:    "install with `brew install binkovsky/forktrust/forktrust`"}
	}
	installed := strings.TrimSpace(string(out))
	if !strings.Contains(installed, version) {
		return doctorCheck{Name: "brew-version", Scope: "global", Status: statusWarn,
			Message: fmt.Sprintf("brew has %q but running binary is %s", installed, version),
			Hint:    "run `brew upgrade binkovsky/forktrust/forktrust`"}
	}
	return doctorCheck{Name: "brew-version", Scope: "global", Status: statusOK, Message: installed}
}

func checkPortsStore() doctorCheck {
	path, err := ports.DefaultPath()
	if err != nil {
		return doctorCheck{Name: "ports-store", Scope: "global", Status: statusWarn,
			Message: fmt.Sprintf("cannot determine ports store path: %v", err)}
	}
	if _, err := os.Stat(path); err != nil {
		// Missing is fine — store is lazy-created.
		return doctorCheck{Name: "ports-store", Scope: "global", Status: statusOK,
			Message: "no allocations yet"}
	}
	return doctorCheck{Name: "ports-store", Scope: "global", Status: statusOK,
		Message: path}
}

func checkProject(rep *doctorReport, p config.Project) {
	scope := p.Name

	// 0. .forktrust/scopes/ orphan check (v0.7.3+).
	if cFound := checkScopesDir(p.Name, p.Path); cFound.Name != "" {
		rep.add(cFound)
	}

	// 1. Repo path exists and is a git directory.
	if _, err := os.Stat(filepath.Join(p.Path, ".git")); err != nil {
		rep.add(doctorCheck{Name: "repo-path", Scope: scope, Status: statusFail,
			Message: fmt.Sprintf("%s is not a git repo (.git missing)", p.Path),
			Hint:    "fix path, or `forktrust config remove " + p.Name + "`"})
		return
	}
	rep.add(doctorCheck{Name: "repo-path", Scope: scope, Status: statusOK,
		Message: p.Path})

	// 2. Main branch resolves.
	mainBranch := p.MainBranch
	if mainBranch == "" {
		mainBranch = "main"
	}
	hasOrigin := git.HasOrigin(p.Path)
	switch {
	case hasOrigin && git.HasRemoteBranch(p.Path, "origin", mainBranch):
		rep.add(doctorCheck{Name: "main-ref", Scope: scope, Status: statusOK,
			Message: "origin/" + mainBranch + " resolves"})
	case git.HasBranch(p.Path, mainBranch):
		rep.add(doctorCheck{Name: "main-ref", Scope: scope, Status: statusOK,
			Message: "local " + mainBranch + " resolves (no origin/" + mainBranch + ")"})
	default:
		hint := fmt.Sprintf("push or create %q first; finish/rm will exit 12 otherwise", mainBranch)
		rep.add(doctorCheck{Name: "main-ref", Scope: scope, Status: statusFail,
			Message: fmt.Sprintf("no %q reference found (tried origin and local)", mainBranch),
			Hint:    hint})
	}

	// 3. .forktrust/ excluded from git.
	excludePath := filepath.Join(p.Path, ".git", "info", "exclude")
	if data, err := os.ReadFile(excludePath); err == nil && strings.Contains(string(data), ".forktrust") {
		rep.add(doctorCheck{Name: "exclude-entry", Scope: scope, Status: statusOK,
			Message: ".forktrust/ in .git/info/exclude"})
	} else {
		rep.add(doctorCheck{Name: "exclude-entry", Scope: scope, Status: statusWarn,
			Message: ".forktrust/ not in .git/info/exclude (auto-added on next `forktrust new`)"})
	}

	// 4. .forktrustconfig syntax (if present).
	repoCfg, err := config.LoadRepoConfig(p.Path)
	switch {
	case err != nil:
		rep.add(doctorCheck{Name: "repo-config", Scope: scope, Status: statusFail,
			Message: fmt.Sprintf("invalid .forktrustconfig: %v", err),
			Hint:    "fix the file or remove it"})
	case repoCfg == nil:
		rep.add(doctorCheck{Name: "repo-config", Scope: scope, Status: statusOK,
			Message: "no .forktrustconfig (zero-config mode)"})
	default:
		nHooks := len(repoCfg.Hooks.PostCreate)
		msg := fmt.Sprintf("%d post_create hook(s)", nHooks)
		if repoCfg.Ports != nil {
			msg += "; ports configured"
		}
		if repoCfg.Verify != nil {
			msg += fmt.Sprintf("; [verify] %d command(s)", len(repoCfg.Verify.Commands))
		}
		rep.add(doctorCheck{Name: "repo-config", Scope: scope, Status: statusOK, Message: msg})

		// 5a. Verify config sanity (commands actually look runnable).
		// Validate.LoadRepoConfig already rejected empty commands; here we
		// surface commands that look like a typo or a missing binary, since
		// the cost of a typo is a finish-time exit 15 deep into the pipeline.
		if repoCfg.Verify != nil {
			rep.add(checkVerifyConfig(p.Name, repoCfg.Verify))
		}

		// 5. If command hooks exist, check trust.
		if repoCfg.HasCommandHooks() {
			ts, err := config.LoadTrust()
			if err != nil {
				rep.add(doctorCheck{Name: "trust-gate", Scope: scope, Status: statusWarn,
					Message: fmt.Sprintf("cannot load trust store: %v", err)})
			} else if trusted, reason := ts.Check(p.Path); trusted {
				rep.add(doctorCheck{Name: "trust-gate", Scope: scope, Status: statusOK,
					Message: "command hooks trusted (SHA pinned)"})
			} else {
				rep.add(doctorCheck{Name: "trust-gate", Scope: scope, Status: statusWarn,
					Message: "command hooks present but config not trusted: " + reason,
					Hint:    "run `forktrust trust -p " + p.Name + "` to pin the SHA"})
			}
		}
	}
}

// checkVerifyConfig surfaces v0.7.2 [verify] section sanity issues that
// LoadRepoConfig.Validate accepts but a user would want to know about:
//   - the first token of each command exists on PATH (catches `npm tset` typo)
//   - timeout_seconds is sane (default if absent; warning if extremely small)
// We do NOT execute the commands — that would be a side effect, and Doctor
// must remain a pure read.
func checkVerifyConfig(projName string, v *config.VerifyConfig) doctorCheck {
	type bad struct {
		idx int
		cmd string
		why string
	}
	var bads []bad
	for i, cmd := range v.Commands {
		// Take the first word; if it contains '/', it's a path — skip the
		// existence check (would require resolving relative-to-worktree).
		trimmed := strings.TrimSpace(cmd)
		if trimmed == "" {
			bads = append(bads, bad{i, cmd, "command is empty/whitespace"})
			continue
		}
		first := strings.Fields(trimmed)[0]
		if strings.ContainsAny(first, "$&|;`<>(){}*?") {
			// Shell metacharacters — heuristic check would lie; skip.
			continue
		}
		if strings.ContainsRune(first, '/') {
			continue
		}
		if _, err := exec.LookPath(first); err != nil {
			bads = append(bads, bad{i, cmd, fmt.Sprintf("first token %q not in PATH", first)})
		}
	}
	if len(bads) > 0 {
		// Build a concise list (cap at 3 for the message).
		var b strings.Builder
		for i, x := range bads {
			if i > 0 {
				b.WriteString("; ")
			}
			if i == 3 {
				fmt.Fprintf(&b, "... and %d more", len(bads)-3)
				break
			}
			fmt.Fprintf(&b, "commands[%d]: %s", x.idx, x.why)
		}
		return doctorCheck{Name: "verify-config", Scope: projName, Status: statusWarn,
			Message: b.String(),
			Hint:    "test the command manually with `forktrust exec <slug> -- <cmd>` before relying on it; finish/pr will exit 15 on a runtime failure"}
	}
	if v.TimeoutSeconds > 0 && v.TimeoutSeconds < 5 {
		return doctorCheck{Name: "verify-config", Scope: projName, Status: statusWarn,
			Message: fmt.Sprintf("[verify].timeout_seconds = %d is very short; most test suites need more", v.TimeoutSeconds),
			Hint:    "raise to a realistic value (default is 600 = 10min per command)"}
	}
	msg := fmt.Sprintf("%d command(s) look runnable", len(v.Commands))
	if v.RequireClean {
		msg += "; require_clean"
	}
	return doctorCheck{Name: "verify-config", Scope: projName, Status: statusOK, Message: msg}
}

// checkScopesDir enumerates <repoPath>/.forktrust/scopes/*.toml and reports
// scope files that no longer correspond to an existing worktree
// (.forktrust/worktrees/<slug>/). These are "orphan" scopes left over after
// a manual `rm -rf` of the worktree, and they will silently re-attach to a
// future `forktrust new <same-slug>` — a documented confusion source.
// Returns an empty doctorCheck (Name=="") when no scopes dir exists.
func checkScopesDir(projName, repoPath string) doctorCheck {
	scopesDir := filepath.Join(repoPath, ".forktrust", "scopes")
	entries, err := os.ReadDir(scopesDir)
	if err != nil {
		// Missing dir is fine; means no scopes have ever been created.
		return doctorCheck{}
	}
	wtDir := filepath.Join(repoPath, ".forktrust", "worktrees")
	var orphans []string
	scopeCount := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".toml") {
			continue
		}
		scopeCount++
		slug := strings.TrimSuffix(name, ".toml")
		if _, err := os.Stat(filepath.Join(wtDir, slug)); err != nil {
			orphans = append(orphans, slug)
		}
	}
	if len(orphans) > 0 {
		var b strings.Builder
		for i, slug := range orphans {
			if i > 0 {
				b.WriteString(", ")
			}
			if i == 5 {
				fmt.Fprintf(&b, "... and %d more", len(orphans)-5)
				break
			}
			b.WriteString(slug)
		}
		return doctorCheck{Name: "scopes-dir", Scope: projName, Status: statusWarn,
			Message: fmt.Sprintf("%d orphan scope file(s): %s", len(orphans), b.String()),
			Hint:    "these will re-attach to future `forktrust new <slug>` with the same slug; remove with `rm .forktrust/scopes/<slug>.toml` or use `forktrust scope <slug> --clear` after `forktrust new`"}
	}
	if scopeCount == 0 {
		return doctorCheck{}
	}
	return doctorCheck{Name: "scopes-dir", Scope: projName, Status: statusOK,
		Message: fmt.Sprintf("%d scope file(s); all attached to live worktrees", scopeCount)}
}
