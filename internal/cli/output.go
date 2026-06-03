package cli

import (
	"encoding/json"
	"os"

	"github.com/binkovsky/forktrust/internal/summary"
)

// finishResult is the stable JSON schema for `forktrust finish [--json]`.
// AI agents and scripts can rely on these field names not breaking
// across minor versions.
type finishResult struct {
	Project           string `json:"project"`
	Slug              string `json:"slug"`
	WorktreePath      string `json:"worktree_path"`
	Branch            string `json:"branch"`
	MainBranch        string `json:"main_branch"`
	DryRun            bool   `json:"dry_run"`
	Message           string `json:"message,omitempty"`
	UncommittedFiles  int    `json:"uncommitted_files"`
	CommittedWIP      bool   `json:"committed_wip"`
	CommitsAhead      int    `json:"commits_ahead"`
	MainDirty         int    `json:"main_dirty,omitempty"`
	MainCurrentBranch string `json:"main_current_branch,omitempty"` // dry-run accuracy: actual HEAD of main checkout
	WouldRefuse       string `json:"would_refuse,omitempty"`        // dry-run: reason actual command would refuse, "" if it would proceed
	HasOrigin         bool   `json:"has_origin"`
	// Verify gate (v0.7.2). All four fields are present whether or not the
	// repo has a [verify] section, so JSON consumers can rely on them:
	//   - VerifyConfigured: repo declared a [verify] section
	//   - VerifyRan: verify was executed in this invocation (false on --no-verify or dry-run or no config)
	//   - VerifyPassed: every command exited zero AND require_clean (if set) is satisfied
	//   - VerifyFailedCommand: the command that failed (empty if passed)
	//   - VerifyRanCommands: the list of commands actually attempted
	VerifyConfigured    bool     `json:"verify_configured"`
	VerifyRan           bool     `json:"verify_ran"`
	VerifyPassed        bool     `json:"verify_passed"`
	VerifyRanCommands   []string `json:"verify_ran_commands,omitempty"`
	VerifyFailedCommand string   `json:"verify_failed_command,omitempty"`
	VerifyOutput        string   `json:"verify_output,omitempty"` // tail of failing command's combined stdout+stderr (capped)
	NoVerify            bool     `json:"no_verify,omitempty"`     // true when --no-verify bypassed the gate
	// Scope gate (v0.7.3). Same shape contract as verify:
	//   - ScopeConfigured: the worktree has a .forktrust/scopes/<slug>.toml
	//   - ScopeChecked: scope was evaluated (false on --no-scope, dry-run, or no scope file)
	//   - ScopePassed: every changed file matches at least one allowed glob
	//   - ScopeAllowed: the declared allowed globs (mirror of the file, for inspection)
	//   - ScopeViolations: list of changed files NOT matching any allowed glob (truncated for JSON: see ScopeViolationCount)
	//   - ScopeViolationCount: full count even if ScopeViolations is truncated
	//   - NoScope: --no-scope bypassed the gate
	ScopeConfigured     bool     `json:"scope_configured"`
	ScopeChecked        bool     `json:"scope_checked"`
	ScopePassed         bool     `json:"scope_passed"`
	ScopeAllowed        []string `json:"scope_allowed,omitempty"`
	ScopeViolations     []string `json:"scope_violations,omitempty"`
	ScopeViolationCount int      `json:"scope_violation_count,omitempty"`
	NoScope             bool     `json:"no_scope,omitempty"`
	// Summary gate (v0.7.7). Same shape contract as scope/verify:
	//   - SummaryConfigured: .forktrustconfig has a [summary] section
	//   - SummaryChecked: the gate was evaluated (false on --no-summary, no config)
	//   - SummaryPassed: every commit in range satisfies every rule
	//   - SummaryCommits: number of commits inspected (informational)
	//   - SummaryViolations: list of rule failures (truncated; full count in SummaryViolationCount)
	//   - NoSummary: --no-summary bypassed the gate
	SummaryConfigured     bool                `json:"summary_configured"`
	SummaryChecked        bool                `json:"summary_checked"`
	SummaryPassed         bool                `json:"summary_passed"`
	SummaryCommits        int                 `json:"summary_commits,omitempty"`
	SummaryViolations     []summary.Violation `json:"summary_violations,omitempty"`
	SummaryViolationCount int                 `json:"summary_violation_count,omitempty"`
	NoSummary             bool                `json:"no_summary,omitempty"`
	Merged                bool                `json:"merged"`
	Pushed              bool     `json:"pushed"`
	WorktreeRemoved     bool     `json:"worktree_removed"`
	BranchDeleted       bool     `json:"branch_deleted"`
	BranchKept          bool     `json:"branch_kept"` // R5: same shape as rmResult — branch -D failed but worktree was removed
}

// rmResult is the stable JSON schema for `forktrust rm [--json]`.
type rmResult struct {
	Project          string `json:"project"`
	Slug             string `json:"slug"`
	WorktreePath     string `json:"worktree_path"`
	Branch           string `json:"branch"`
	DryRun           bool   `json:"dry_run"`
	Force            bool   `json:"force"`
	UncommittedFiles int    `json:"uncommitted_files"`
	CommitsAhead     int    `json:"commits_ahead"`          // commits this branch has past main; 0 also valid when AheadKnown=false
	AheadKnown       bool   `json:"ahead_known"`            // false means CommitsAhead is meaningless (no main ref resolved)
	WouldPushWip     bool   `json:"would_push_wip"`         // dry-run: actual rm would snapshot to wip/*
	WouldRefuse      string `json:"would_refuse,omitempty"` // dry-run: reason actual rm would refuse, "" if it would proceed
	WipBranch        string `json:"wip_branch,omitempty"`
	WipPushed        bool   `json:"wip_pushed"`
	WorktreeRemoved  bool   `json:"worktree_removed"`
	BranchDeleted    bool   `json:"branch_deleted"`
	BranchKept       bool   `json:"branch_kept"`
}

// newResult is the stable JSON schema for `forktrust new [--json]`.
// prResult is the stable JSON schema for `forktrust pr [--json]`.
// Shape mirrors finishResult's pre-flight fields so a consumer that already
// understands finish JSON can read pr JSON without learning a second schema.
type prResult struct {
	Project      string `json:"project"`
	Slug         string `json:"slug"`
	WorktreePath string `json:"worktree_path"`
	Branch       string `json:"branch"`        // fork/<slug>
	BaseBranch   string `json:"base_branch"`   // typically "main"
	DryRun       bool   `json:"dry_run"`
	HasOrigin    bool   `json:"has_origin"`
	GhAvailable  bool   `json:"gh_available"`

	// Pre-flight (verify + scope) — same fields as finishResult.
	VerifyConfigured    bool     `json:"verify_configured"`
	VerifyRan           bool     `json:"verify_ran"`
	VerifyPassed        bool     `json:"verify_passed"`
	VerifyRanCommands   []string `json:"verify_ran_commands,omitempty"`
	VerifyFailedCommand string   `json:"verify_failed_command,omitempty"`
	VerifyOutput        string   `json:"verify_output,omitempty"`
	NoVerify            bool     `json:"no_verify,omitempty"`
	ScopeConfigured     bool     `json:"scope_configured"`
	ScopeChecked        bool     `json:"scope_checked"`
	ScopePassed         bool     `json:"scope_passed"`
	ScopeAllowed        []string `json:"scope_allowed,omitempty"`
	ScopeViolations     []string `json:"scope_violations,omitempty"`
	ScopeViolationCount int      `json:"scope_violation_count,omitempty"`
	NoScope             bool     `json:"no_scope,omitempty"`
	// Summary gate (v0.7.7) — same shape as finishResult.
	SummaryConfigured     bool                `json:"summary_configured"`
	SummaryChecked        bool                `json:"summary_checked"`
	SummaryPassed         bool                `json:"summary_passed"`
	SummaryCommits        int                 `json:"summary_commits,omitempty"`
	SummaryViolations     []summary.Violation `json:"summary_violations,omitempty"`
	SummaryViolationCount int                 `json:"summary_violation_count,omitempty"`
	NoSummary             bool                `json:"no_summary,omitempty"`

	// PR-specific.
	WouldRefuse      string `json:"would_refuse,omitempty"`
	UncommittedFiles int    `json:"uncommitted_files"`
	CommittedWIP     bool   `json:"committed_wip"`
	BranchPushed     bool   `json:"branch_pushed"`
	PRExisted        bool   `json:"pr_existed"` // true if a PR for this branch already existed (we just updated)
	PRCreated        bool   `json:"pr_created"`
	PRNumber         int    `json:"pr_number,omitempty"`
	PRURL            string `json:"pr_url,omitempty"`
	PRState          string `json:"pr_state,omitempty"`
	PRTitle          string `json:"pr_title,omitempty"`
	PRIsDraft        bool   `json:"pr_is_draft,omitempty"`
}

// prStatusResult is the stable JSON schema for `forktrust pr-status [--json]`.
type prStatusResult struct {
	Project        string        `json:"project"`
	Slug           string        `json:"slug"`
	Branch         string        `json:"branch"`
	GhAvailable    bool          `json:"gh_available"`
	PRExists       bool          `json:"pr_exists"`
	PRNumber       int           `json:"pr_number,omitempty"`
	PRURL          string        `json:"pr_url,omitempty"`
	PRState        string        `json:"pr_state,omitempty"`
	PRIsDraft      bool          `json:"pr_is_draft,omitempty"`
	Mergeable      string        `json:"mergeable,omitempty"`
	ReviewDecision string        `json:"review_decision,omitempty"`
	Checks         checksSummary `json:"checks"`
	Title          string        `json:"title,omitempty"`
	BaseBranch     string        `json:"base_branch,omitempty"`
	Author         string        `json:"author,omitempty"`
	Additions      int           `json:"additions,omitempty"`
	Deletions      int           `json:"deletions,omitempty"`
	ChangedFiles   int           `json:"changed_files,omitempty"`
	UpdatedAt      string        `json:"updated_at,omitempty"`
}

type newResult struct {
	Project           string   `json:"project"`
	Slug              string   `json:"slug"`
	WorktreePath      string   `json:"worktree_path"`
	Branch            string   `json:"branch"`
	BranchReused      bool     `json:"branch_reused"`
	EnvFilesCopied    int      `json:"env_files_copied"`
	HooksRun          []string `json:"hooks_run,omitempty"`
	Ports             []int    `json:"ports,omitempty"`
	PredictedOverlaps []string `json:"predicted_overlaps,omitempty"`
	// Scope (v0.7.3): the allowed-globs change contract this task was created
	// with, if any. Stored at <repo>/.forktrust/scopes/<slug>.toml.
	Scope []string `json:"scope,omitempty"`
}

// listResult is the stable JSON schema for `forktrust list [--json]`.
type listResult struct {
	Worktrees []worktreeEntry `json:"worktrees"`
}

type worktreeEntry struct {
	Project  string `json:"project"`
	Path     string `json:"path"`
	Branch   string `json:"branch"`
	Detached bool   `json:"detached"`
	Dirty    int    `json:"dirty"`
	IsMain   bool   `json:"is_main"`
}

func emitFinish(r finishResult) error     { return emitJSON(finishJSON, r) }
func emitRm(r rmResult) error             { return emitJSON(rmJSON, r) }
func emitNew(r newResult) error           { return emitJSON(newJSON, r) }
func emitList(r listResult) error         { return emitJSON(listJSON, r) }
func emitPR(r prResult) error             { return emitJSON(prJSON, r) }
func emitPRStatus(r prStatusResult) error { return emitJSON(prStatusJSON, r) }

func emitJSON(on bool, v interface{}) error {
	if !on {
		return nil
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
