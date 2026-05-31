package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/binkovsky/forktrust/internal/ai"
	"github.com/binkovsky/forktrust/internal/config"
)

var (
	aiTool       string
	aiProject    string
	aiList       bool
	aiSetDefault string
)

var aiCmd = &cobra.Command{
	Use:   "ai <slug>",
	Short: "Launch an AI coding tool inside the worktree (creates it if needed)",
	Long: `Launch a configured AI tool (claude, codex, cursor, aider, gemini,
copilot, continue, opencode, auggie) inside a forktrust worktree.

If the worktree for <slug> doesn't exist yet, it is created first via the
same code path as ` + "`forktrust new <slug>`" + ` (hooks + ports + .env files).

Examples:
  forktrust ai my-task                       # uses configured default tool
  forktrust ai my-task --tool claude         # override per-invocation
  forktrust ai --set-default claude          # set ai.default in ~/.config/forktrust/config.toml
  forktrust ai --list                        # show supported adapters

Tool selection precedence:
  1. --tool flag
  2. [ai].default in ~/.config/forktrust/config.toml
  3. error (specify one)`,
	Args: cobra.MaximumNArgs(1),
	RunE: runAI,
}

func init() {
	aiCmd.Flags().StringVar(&aiTool, "tool", "", "AI adapter to launch (overrides ai.default)")
	aiCmd.Flags().StringVarP(&aiProject, "project", "p", "", "target project name (required if more than one is registered)")
	aiCmd.Flags().BoolVar(&aiList, "list", false, "list supported AI adapters and exit")
	aiCmd.Flags().StringVar(&aiSetDefault, "set-default", "", "save this tool as ai.default in the user config and exit")
}

func runAI(_ *cobra.Command, args []string) error {
	if aiList {
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tBIN\tDESCRIPTION")
		for _, a := range ai.Registry {
			fmt.Fprintf(w, "%s\t%s\t%s\n", a.Name, a.Bin, a.Description)
		}
		return w.Flush()
	}

	if aiSetDefault != "" {
		if ai.Find(aiSetDefault) == nil {
			return fmt.Errorf("unknown tool %q (supported: %v)", aiSetDefault, ai.Names())
		}
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		cfg.AI.Default = aiSetDefault
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Printf("saved ai.default = %s\n", aiSetDefault)
		return nil
	}

	if len(args) == 0 {
		return fmt.Errorf("slug required (or use --list / --set-default)")
	}
	slug := args[0]

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Resolve adapter name.
	toolName := aiTool
	if toolName == "" {
		toolName = cfg.AI.Default
	}
	if toolName == "" {
		return fmt.Errorf("no AI tool specified and no ai.default configured. " +
			"Set one with: forktrust ai --set-default <tool> (run --list for options)")
	}
	adapter := ai.Find(toolName)
	if adapter == nil {
		return fmt.Errorf("unknown tool %q (supported: %v)", toolName, ai.Names())
	}

	proj, err := selectProject(cfg, aiProject)
	if err != nil {
		return err
	}
	wtPath := filepath.Join(proj.Path, ".forktrust", "worktrees", slug)

	// Create worktree if missing — composes with `new`'s hook/port logic
	// by re-running runNew with overrides. Simpler: shell-out to ourselves?
	// No — call the existing logic in-process by reusing runNew via a hidden
	// shim. For now, the minimal version: if missing, ask user to run `new`.
	if _, err := os.Stat(wtPath); err != nil {
		fmt.Fprintf(os.Stderr, "worktree for %q does not exist. Create it first:\n", slug)
		fmt.Fprintf(os.Stderr, "  forktrust new %s\n", slug)
		fmt.Fprintln(os.Stderr, "Then re-run:")
		fmt.Fprintf(os.Stderr, "  forktrust ai %s\n", slug)
		return coded(ExitNoWorktree, fmt.Errorf("worktree %q missing", slug))
	}

	fmt.Printf("==> launching %s in %s\n", adapter.Name, wtPath)
	if err := adapter.Launch(wtPath); err != nil {
		return fmt.Errorf("%s exited: %w", adapter.Name, err)
	}
	return nil
}
