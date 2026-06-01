package cli

// Structured exit codes — documented in README and stable across releases.
// AI agents and CI scripts can switch on these.
const (
	ExitOK                = 0  // success
	ExitGenericError      = 1  // generic fallback (cobra defaults to this)
	ExitMergeConflict     = 2  // finish refused: merge into main would conflict
	ExitDirtyMain         = 3  // finish refused: main checkout has uncommitted changes
	ExitPushFailed        = 4  // finish: push to origin failed (auth, non-ff, network)
	ExitWipPushFailed     = 5  // rm: wip/* snapshot push failed, worktree NOT removed
	ExitNoWorktree        = 6  // target slug doesn't match any registered worktree
	ExitAmbiguousSlug     = 7  // slug matches worktrees in multiple projects
	ExitHookFailed        = 8  // pre-commit / post_create hook failed
	ExitNoOriginRemote    = 9  // operation requires origin but none configured
	ExitMainOnWrongBranch = 10 // finish refused: main checkout is not on the mainBranch
	ExitCwdNotRegistered  = 11 // cwd is in a git repo that is not registered (and --project not given)
	ExitAheadUnknown      = 12 // rm/finish refused: could not determine if branch has unpushed work (no main reference resolved)
	ExitBranchNotDeleted  = 13 // rm: worktree removed and ports released, but `git branch -D` failed (branch lingers)
)

// CodedError carries a structured exit code through cobra's error path.
type CodedError struct {
	Code int
	Err  error
}

func (e *CodedError) Error() string { return e.Err.Error() }
func (e *CodedError) Unwrap() error { return e.Err }

func coded(code int, err error) error {
	if err == nil {
		return nil
	}
	return &CodedError{Code: code, Err: err}
}
