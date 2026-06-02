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
			Message: "gh CLI not installed (optional; needed for PR mode in v0.7.4+)",
			Hint:    "install with `brew install gh`"}
	}
	first := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	return doctorCheck{Name: "gh", Scope: "global", Status: statusOK, Message: first}
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
		rep.add(doctorCheck{Name: "repo-config", Scope: scope, Status: statusOK, Message: msg})

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
