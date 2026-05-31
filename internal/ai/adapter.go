// Package ai launches an AI coding tool inside a worktree. Adapters are
// declarative: each one says how to invoke a given tool, whether it accepts
// the worktree path as an argument or expects to run in cwd, and what its
// canonical install name is. Adding a new tool is one entry.
package ai

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// Adapter declares how to launch one AI coding tool.
type Adapter struct {
	// Name is the canonical short name users pass on the CLI (claude, cursor).
	Name string
	// Bin is the executable to invoke. Resolved via $PATH at run time.
	Bin string
	// Args is the additional argv. {path} is replaced with the worktree path
	// when present; otherwise the tool runs with cwd set to the worktree.
	Args []string
	// Description is a one-line note shown in `forktrust ai --list`.
	Description string
}

// Registry is the supported adapter list. Keep alphabetized by Name.
// To add a tool: append an Adapter and update README/agent-docs.
var Registry = []Adapter{
	{Name: "aider", Bin: "aider", Description: "Aider (terminal AI pair-programmer, https://aider.chat)"},
	{Name: "auggie", Bin: "auggie", Description: "Auggie CLI (Augment Code)"},
	{Name: "claude", Bin: "claude", Description: "Claude Code (Anthropic, https://claude.com/code)"},
	{Name: "codex", Bin: "codex", Description: "OpenAI Codex CLI"},
	{Name: "continue", Bin: "continue", Description: "Continue CLI (https://continue.dev)"},
	{Name: "copilot", Bin: "gh", Args: []string{"copilot", "suggest"}, Description: "GitHub Copilot via gh CLI"},
	{Name: "cursor", Bin: "cursor", Args: []string{"{path}"}, Description: "Cursor editor (opens worktree)"},
	{Name: "gemini", Bin: "gemini", Description: "Gemini CLI (Google)"},
	{Name: "opencode", Bin: "opencode", Description: "OpenCode (SST, https://opencode.ai)"},
}

// Names returns the supported adapter names in order.
func Names() []string {
	out := make([]string, len(Registry))
	for i, a := range Registry {
		out[i] = a.Name
	}
	sort.Strings(out)
	return out
}

// Find returns the adapter by name, or nil.
func Find(name string) *Adapter {
	name = strings.ToLower(strings.TrimSpace(name))
	for i := range Registry {
		if Registry[i].Name == name {
			return &Registry[i]
		}
	}
	return nil
}

// Launch execs the adapter against the given worktree path. Stdin/stdout/stderr
// inherit from the parent so interactive tools (claude, aider, cursor) work
// naturally. Returns the exit error, if any.
func (a *Adapter) Launch(worktreePath string) error {
	if _, err := exec.LookPath(a.Bin); err != nil {
		return fmt.Errorf("%s not installed (binary %q not in $PATH). Install it and re-run", a.Name, a.Bin)
	}
	args := make([]string, 0, len(a.Args))
	usePathArg := false
	for _, raw := range a.Args {
		if raw == "{path}" {
			args = append(args, worktreePath)
			usePathArg = true
		} else {
			args = append(args, raw)
		}
	}
	cmd := exec.Command(a.Bin, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if !usePathArg {
		cmd.Dir = worktreePath
	}
	return cmd.Run()
}
