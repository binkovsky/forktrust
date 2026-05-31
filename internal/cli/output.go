package cli

import (
	"encoding/json"
	"os"
)

// finishResult is the stable JSON schema for `forktrust finish [--json]`.
// AI agents and scripts can rely on these field names not breaking
// across minor versions.
type finishResult struct {
	Project          string `json:"project"`
	Slug             string `json:"slug"`
	WorktreePath     string `json:"worktree_path"`
	Branch           string `json:"branch"`
	MainBranch       string `json:"main_branch"`
	DryRun           bool   `json:"dry_run"`
	Message          string `json:"message,omitempty"`
	UncommittedFiles int    `json:"uncommitted_files"`
	CommittedWIP     bool   `json:"committed_wip"`
	CommitsAhead     int    `json:"commits_ahead"`
	MainDirty        int    `json:"main_dirty,omitempty"`
	Merged           bool   `json:"merged"`
	Pushed           bool   `json:"pushed"`
	WorktreeRemoved  bool   `json:"worktree_removed"`
	BranchDeleted    bool   `json:"branch_deleted"`
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
	WipBranch        string `json:"wip_branch,omitempty"`
	WipPushed        bool   `json:"wip_pushed"`
	WorktreeRemoved  bool   `json:"worktree_removed"`
	BranchDeleted    bool   `json:"branch_deleted"`
	BranchKept       bool   `json:"branch_kept"`
}

// newResult is the stable JSON schema for `forktrust new [--json]`.
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

func emitFinish(r finishResult) error { return emitJSON(finishJSON, r) }
func emitRm(r rmResult) error         { return emitJSON(rmJSON, r) }
func emitNew(r newResult) error       { return emitJSON(newJSON, r) }
func emitList(r listResult) error     { return emitJSON(listJSON, r) }

func emitJSON(on bool, v interface{}) error {
	if !on {
		return nil
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
