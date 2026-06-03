package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Each tool definition follows JSON Schema draft 7 syntax. We keep schemas
// minimal: type and description per property, no enums or pattern restrictions
// unless they prevent a real footgun. MCP clients (Claude Code) use these for
// argument validation AND for prompting the model on call shape.

var stringSchema = func(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

var optionalString = func(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

var boolSchema = func(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}

// allTools is the static catalog enumerated via tools/list. ORDER IS THE
// CLIENT-VISIBLE ORDER — agents that pick the first matching tool benefit
// from putting the most useful ones first.
var allTools = []tool{
	{
		Name:        "forktrust_list",
		Description: "List all forktrust worktrees across every registered project. Use this to discover slugs before any other call.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	},
	{
		Name:        "forktrust_status",
		Description: "Per-worktree dashboard: dirty count, ahead/behind, allocated ports, age. Optionally filter by project.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project": optionalString("Filter to one registered project name (optional)."),
			},
		},
	},
	{
		Name:        "forktrust_new",
		Description: "Create an isolated worktree on a new branch fork/<slug>. Optionally declare a --scope change contract that finish will enforce.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"slug":    stringSchema("Short identifier for the task; becomes the worktree directory name and branch suffix."),
				"project": optionalString("Target project name (required only if more than one is registered)."),
				"scope":   optionalString("Comma-separated glob patterns the task may modify (e.g. \"internal/auth/**, go.mod\"). finish will refuse out-of-scope edits with exit 16."),
				"from":    optionalString("Explicit base ref (e.g. \"origin/main\", a commit SHA). Default: origin/<mainBranch> > <mainBranch> > HEAD."),
			},
			"required": []string{"slug"},
		},
	},
	{
		Name:        "forktrust_cd",
		Description: "Resolve the absolute worktree path for a slug. Use this to know where to read/write files for a task without invoking shell wrappers.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"slug":    stringSchema("Worktree slug."),
				"project": optionalString("Project name (required if slug is ambiguous across projects)."),
			},
			"required": []string{"slug"},
		},
	},
	{
		Name:        "forktrust_finish",
		Description: "Ship a worktree: commit WIP, run pre-flight (verify + scope gates), merge --no-ff to main, push, cleanup. Refuses on conflict (exit 2). Use dry_run=true first to preview.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"slug":       stringSchema("Worktree slug to finish."),
				"project":    optionalString("Project name (if ambiguous)."),
				"message":    optionalString("Commit message for the auto-WIP commit. Default \"WIP: <slug>\"."),
				"dry_run":    boolSchema("Preview only — report would_refuse without mutating git state."),
				"no_verify":  boolSchema("Skip the [verify] gate (use only with explicit user consent)."),
				"no_scope":   boolSchema("Skip the change-contract scope gate (use only with explicit user consent)."),
			},
			"required": []string{"slug"},
		},
	},
	{
		Name:        "forktrust_rm",
		Description: "Abandon a worktree: snapshot WIP to wip/<branch>-YYYYMMDD-HHMMSS-<sha7> on origin, then remove. Refuses (exit 14) if the worktree has ignored files, unless force=true.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"slug":    stringSchema("Worktree slug to abandon."),
				"project": optionalString("Project name (if ambiguous)."),
				"force":   boolSchema("Skip all guards (drops uncommitted work, ignored files, no wip snapshot). Requires explicit user consent."),
				"dry_run": boolSchema("Preview only — report would_refuse without mutating state."),
			},
			"required": []string{"slug"},
		},
	},
	{
		Name:        "forktrust_scope",
		Description: "Show, set, clear, or check the change-contract scope for a worktree. Modes are mutually exclusive: pass at most one of set/clear/check.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"slug":    stringSchema("Worktree slug."),
				"project": optionalString("Project name (if ambiguous)."),
				"set":     optionalString("Comma-separated globs to set as the new scope."),
				"clear":   boolSchema("Remove the scope file entirely (worktree becomes unrestricted)."),
				"check":   boolSchema("Evaluate the worktree diff against current scope; exits 16 if any file is out-of-scope."),
			},
			"required": []string{"slug"},
		},
	},
	{
		Name:        "forktrust_pr",
		Description: "Push the worktree branch and open a GitHub PR via gh CLI (alternative to direct finish). Requires gh available (exit 17) and origin (exit 9). Worktree stays alive after pr — clean up with forktrust_rm after the PR merges.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"slug":      stringSchema("Worktree slug."),
				"project":   optionalString("Project name (if ambiguous)."),
				"title":     optionalString("PR title (default: first non-WIP commit subject from the branch)."),
				"body":      optionalString("PR body (default: bullet list of commit subjects + forktrust footer)."),
				"base":      optionalString("Base branch (default: project's mainBranch, usually \"main\")."),
				"draft":     boolSchema("Open as a draft PR."),
				"no_verify": boolSchema("Skip the [verify] gate."),
				"no_scope":  boolSchema("Skip the scope contract check."),
				"dry_run":   boolSchema("Preview only — run pre-flight without push/PR-create side effects."),
			},
			"required": []string{"slug"},
		},
	},
	{
		Name:        "forktrust_pr_status",
		Description: "Show the GitHub PR status for a worktree: number, URL, state, mergeable, review decision, CI checks rollup. Exit 0 even when no PR exists (pr_exists=false).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"slug":    stringSchema("Worktree slug."),
				"project": optionalString("Project name (if ambiguous)."),
			},
			"required": []string{"slug"},
		},
	},
	{
		Name:        "forktrust_doctor",
		Description: "Health check across the forktrust install: git, gh availability + auth, brew freshness, ports store, per-project repo state, verify config, scope file orphans, trust gate.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project": optionalString("Focus on one registered project (optional)."),
			},
		},
	},
}

// handlerFunc is the per-tool execution: take MCP args, return tool text +
// isError bool. Implementations should NEVER panic; they should map shell-out
// failures to (errorText, true).
type handlerFunc func(ctx context.Context, binary string, args map[string]any) (text string, isError bool)

// handlers maps tool name → implementation. Each implementation builds a
// forktrust command line and invokes the binary, capturing stdout (which is
// the JSON envelope for --json commands) as the success text. On non-zero
// exit, stderr text is returned with isError=true so the MCP client surfaces
// the failure to the model.
var handlers = map[string]handlerFunc{
	"forktrust_list": func(ctx context.Context, binary string, _ map[string]any) (string, bool) {
		return runForktrust(ctx, binary, "list", "--json")
	},
	"forktrust_status": func(ctx context.Context, binary string, args map[string]any) (string, bool) {
		a := []string{"status", "--json"}
		if v, ok := stringArg(args, "project"); ok {
			a = append(a, "-p", v)
		}
		return runForktrust(ctx, binary, a...)
	},
	"forktrust_new": func(ctx context.Context, binary string, args map[string]any) (string, bool) {
		slug, ok := stringArg(args, "slug")
		if !ok {
			return "missing required argument: slug", true
		}
		a := []string{"new", slug, "--json"}
		if v, ok := stringArg(args, "project"); ok {
			a = append(a, "-p", v)
		}
		if v, ok := stringArg(args, "scope"); ok {
			a = append(a, "--scope", v)
		}
		if v, ok := stringArg(args, "from"); ok {
			a = append(a, "--from", v)
		}
		return runForktrust(ctx, binary, a...)
	},
	"forktrust_cd": func(ctx context.Context, binary string, args map[string]any) (string, bool) {
		slug, ok := stringArg(args, "slug")
		if !ok {
			return "missing required argument: slug", true
		}
		a := []string{"cd", slug}
		if v, ok := stringArg(args, "project"); ok {
			a = append(a, "-p", v)
		}
		out, isErr := runForktrust(ctx, binary, a...)
		// cd prints just the path on stdout (no JSON). Wrap as a minimal
		// JSON object so MCP consumers always get parseable text.
		if !isErr {
			return jsonObject(map[string]any{"slug": slug, "worktree_path": strings.TrimSpace(out)}), false
		}
		return out, isErr
	},
	"forktrust_finish": func(ctx context.Context, binary string, args map[string]any) (string, bool) {
		slug, ok := stringArg(args, "slug")
		if !ok {
			return "missing required argument: slug", true
		}
		a := []string{"finish", slug, "--json"}
		if v, ok := stringArg(args, "project"); ok {
			a = append(a, "-p", v)
		}
		if v, ok := stringArg(args, "message"); ok {
			a = append(a, "-m", v)
		}
		if boolArg(args, "dry_run") {
			a = append(a, "--dry-run")
		}
		if boolArg(args, "no_verify") {
			a = append(a, "--no-verify")
		}
		if boolArg(args, "no_scope") {
			a = append(a, "--no-scope")
		}
		return runForktrust(ctx, binary, a...)
	},
	"forktrust_rm": func(ctx context.Context, binary string, args map[string]any) (string, bool) {
		slug, ok := stringArg(args, "slug")
		if !ok {
			return "missing required argument: slug", true
		}
		a := []string{"rm", slug, "--json"}
		if v, ok := stringArg(args, "project"); ok {
			a = append(a, "-p", v)
		}
		if boolArg(args, "force") {
			a = append(a, "--force")
		}
		if boolArg(args, "dry_run") {
			a = append(a, "--dry-run")
		}
		return runForktrust(ctx, binary, a...)
	},
	"forktrust_scope": func(ctx context.Context, binary string, args map[string]any) (string, bool) {
		slug, ok := stringArg(args, "slug")
		if !ok {
			return "missing required argument: slug", true
		}
		// Enforce mutex on set/clear/check at the MCP layer so users get a
		// clear error before going through the binary's own check.
		modes := 0
		if _, ok := stringArg(args, "set"); ok {
			modes++
		}
		if boolArg(args, "clear") {
			modes++
		}
		if boolArg(args, "check") {
			modes++
		}
		if modes > 1 {
			return "set, clear, and check are mutually exclusive", true
		}
		a := []string{"scope", slug, "--json"}
		if v, ok := stringArg(args, "project"); ok {
			a = append(a, "-p", v)
		}
		if v, ok := stringArg(args, "set"); ok {
			a = append(a, "--set", v)
		}
		if boolArg(args, "clear") {
			a = append(a, "--clear")
		}
		if boolArg(args, "check") {
			a = append(a, "--check")
		}
		return runForktrust(ctx, binary, a...)
	},
	"forktrust_pr": func(ctx context.Context, binary string, args map[string]any) (string, bool) {
		slug, ok := stringArg(args, "slug")
		if !ok {
			return "missing required argument: slug", true
		}
		a := []string{"pr", slug, "--json"}
		if v, ok := stringArg(args, "project"); ok {
			a = append(a, "-p", v)
		}
		if v, ok := stringArg(args, "title"); ok {
			a = append(a, "--title", v)
		}
		if v, ok := stringArg(args, "body"); ok {
			a = append(a, "--body", v)
		}
		if v, ok := stringArg(args, "base"); ok {
			a = append(a, "--base", v)
		}
		if boolArg(args, "draft") {
			a = append(a, "--draft")
		}
		if boolArg(args, "no_verify") {
			a = append(a, "--no-verify")
		}
		if boolArg(args, "no_scope") {
			a = append(a, "--no-scope")
		}
		if boolArg(args, "dry_run") {
			a = append(a, "--dry-run")
		}
		return runForktrust(ctx, binary, a...)
	},
	"forktrust_pr_status": func(ctx context.Context, binary string, args map[string]any) (string, bool) {
		slug, ok := stringArg(args, "slug")
		if !ok {
			return "missing required argument: slug", true
		}
		a := []string{"pr-status", slug, "--json"}
		if v, ok := stringArg(args, "project"); ok {
			a = append(a, "-p", v)
		}
		return runForktrust(ctx, binary, a...)
	},
	"forktrust_doctor": func(ctx context.Context, binary string, args map[string]any) (string, bool) {
		a := []string{"doctor", "--json"}
		if v, ok := stringArg(args, "project"); ok {
			a = append(a, "-p", v)
		}
		return runForktrust(ctx, binary, a...)
	},
}

// runForktrust invokes the forktrust binary with args, capturing stdout. If
// the command exits non-zero, stdout AND stderr are merged into the returned
// text and isError=true is set so the MCP client surfaces the failure.
//
// The forktrust CLI's JSON contract — even error paths emit a JSON envelope
// on stdout — means programmatic MCP consumers can usually parse the text
// regardless of isError. We still flag isError so models notice the failure.
func runForktrust(ctx context.Context, binary string, args ...string) (string, bool) {
	cmd := exec.CommandContext(ctx, binary, args...) //nolint:gosec
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), false
	}
	// Build a structured error text: stdout (likely JSON envelope) first,
	// then exit code, then stderr tail. Keeps the JSON parseable if the
	// consumer just trusts the first line.
	var b strings.Builder
	if s := stdout.String(); s != "" {
		b.WriteString(s)
		if !strings.HasSuffix(s, "\n") {
			b.WriteString("\n")
		}
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		fmt.Fprintf(&b, "(forktrust exited %d)\n", ee.ExitCode())
	} else {
		fmt.Fprintf(&b, "(forktrust failed: %v)\n", err)
	}
	if s := strings.TrimSpace(stderr.String()); s != "" {
		b.WriteString(s)
	}
	return b.String(), true
}

// stringArg safely extracts a string argument from the tool args map. Returns
// (value, true) when present and non-empty; (_, false) otherwise. This avoids
// surfacing JSON null or 0-length strings as "real" values.
func stringArg(args map[string]any, key string) (string, bool) {
	v, ok := args[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	if !ok || s == "" {
		return "", false
	}
	return s, true
}

// boolArg returns the bool value of an arg, false if absent or wrong type.
func boolArg(args map[string]any, key string) bool {
	v, ok := args[key]
	if !ok {
		return false
	}
	b, _ := v.(bool)
	return b
}

// jsonObject marshals a simple map into a JSON string. Used by tools that
// don't have a native JSON output mode (cd) so the MCP response is always
// parseable JSON text.
func jsonObject(m map[string]any) string {
	b, err := json.Marshal(m)
	if err != nil {
		return fmt.Sprintf("{\"error\":%q}", err.Error())
	}
	return string(b)
}
