package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/binkovsky/forktrust/internal/mcp"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Run as an MCP (Model Context Protocol) server over stdio",
	Long: `Run forktrust as an MCP server that exposes its commands as typed tools to
MCP-speaking AI agents (Claude Code, Cursor, etc.).

Wire format: newline-delimited JSON-RPC 2.0 over stdio (MCP 2024-11-05 spec).
Reads requests from stdin, writes responses to stdout. Designed to be spawned
by an MCP client; not interactive.

Configure in Claude Code's settings.json:

  {
    "mcpServers": {
      "forktrust": {
        "command": "forktrust",
        "args": ["mcp"]
      }
    }
  }

Tools exposed (each is a thin wrapper around the corresponding forktrust
command with --json):
  forktrust_list        — list all worktrees across projects
  forktrust_status      — per-worktree dashboard
  forktrust_new         — create isolated worktree (supports --scope)
  forktrust_cd          — get worktree path
  forktrust_finish      — ship: commit + merge + push + cleanup
  forktrust_rm          — abandon (snapshots WIP first)
  forktrust_scope       — show / set / clear / check change contract
  forktrust_pr          — open GitHub PR via gh
  forktrust_pr_status   — PR CI / approvals / mergeable
  forktrust_doctor      — health check

See docs/mcp.md for the full protocol and client integration recipes.`,
	Args: cobra.NoArgs,
	RunE: runMCP,
}

func runMCP(_ *cobra.Command, _ []string) error {
	// Resolve the running binary's path so child commands self-invoke
	// reliably even when forktrust is launched via $PATH lookup or symlink.
	binary, err := os.Executable()
	if err != nil {
		// Fall back to argv[0]; works in most setups but not guaranteed.
		binary = os.Args[0]
	}

	// Honor SIGINT/SIGTERM so the client can shut us down cleanly.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	s := mcp.New(binary, version)
	if err := s.Serve(ctx, os.Stdin, os.Stdout); err != nil {
		// Don't echo errors to stdout — that would corrupt the JSON-RPC
		// stream the client is still reading. Log to stderr.
		fmt.Fprintf(os.Stderr, "forktrust mcp: %v\n", err)
		return err
	}
	return nil
}
