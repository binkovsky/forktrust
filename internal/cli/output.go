package cli

import (
	"encoding/json"
	"os"
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
	Merged            bool   `json:"merged"`
	Pushed            bool   `json:"pushed"`
	WorktreeRemoved   bool   `json:"worktree_removed"`
	BranchDeleted     bool   `json:"branch_deleted"`
	BranchKept        bool   `json:"branch_kept"` // R5: same shape as rmResult — branch -D failed but worktree was removed
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
