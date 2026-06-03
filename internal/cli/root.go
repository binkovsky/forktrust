// Package cli wires the cobra command tree for the forktrust CLI.
package cli

import (
	"github.com/spf13/cobra"
)

var version string

// SetVersion is called from main with the build-time version string.
func SetVersion(v string) { version = v }

var rootCmd = &cobra.Command{
	Use:   "forktrust",
	Short: "Safe-by-default git worktree manager for parallel AI coding sessions",
	Long: `forktrust isolates parallel AI coding chats in their own git worktrees,
with refuse-on-conflict merges and a never-lose-WIP guarantee.

Each chat = one worktree at .forktrust/worktrees/<slug> on branch fork/<slug>.
Finish a chat with "forktrust finish <slug>" — it commits WIP, merges to main,
pushes, and removes the worktree. Refuses on merge conflicts (owner resolves
manually). Abandon a chat with "forktrust rm <slug>" — it snapshots WIP as
wip/<branch>-YYYYMMDD-HHMMSS-<sha7> before removing, so work is never lost.`,
	SilenceUsage: true,
	// Errors are printed once by main.go (with "error:" prefix); silence
	// cobra's own print to avoid the duplicate "Error: ... error: ..." pair.
	SilenceErrors: true,
}

// Execute runs the root command.
func Execute() error {
	rootCmd.Version = version
	rootCmd.AddCommand(newCmd, listCmd, finishCmd, rmCmd, configCmd, trustCmd, aiCmd, agentDocsCmd, execCmd, statusCmd, cdCmd, shellCmd, doctorCmd, scopeCmd)
	return rootCmd.Execute()
}
