package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/binkovsky/forktrust/internal/config"
	"github.com/binkovsky/forktrust/internal/git"
	"github.com/binkovsky/forktrust/internal/hooks"
)

var (
	newInstall bool
	newProject string
	newJSON    bool
)

var newCmd = &cobra.Command{
	Use:   "new <slug>",
	Short: "Create an isolated worktree for a new task",
	Long: `Create a new git worktree at .forktrust/worktrees/<slug> on branch fork/<slug>.

The worktree is isolated from the main checkout, so parallel AI sessions can
each have their own without stepping on each other. By default copies any
.env / .env.local / .env.development / .env.production files from the main
checkout. For declarative copy/symlink/command hooks, use a .forktrustconfig
file at the repo root (see "forktrust config schema" for details).

Auto-adds .forktrust/ to .git/info/exclude (local-only, never touches the
project's tracked .gitignore).`,
	Args: cobra.ExactArgs(1),
	RunE: runNew,
}

func init() {
	newCmd.Flags().BoolVar(&newInstall, "install", false, "run the project's install command after creating the worktree")
	newCmd.Flags().StringVarP(&newProject, "project", "p", "", "target project name (required if more than one is registered)")
	newCmd.Flags().BoolVar(&newJSON, "json", false, "emit a structured JSON result on stdout (one object)")
}

func runNew(_ *cobra.Command, args []string) error {
	slug := args[0]

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	proj, err := selectProject(cfg, newProject)
	if err != nil {
		return err
	}

	wtPath := filepath.Join(proj.Path, ".forktrust", "worktrees", slug)
	branch := "fork/" + slug

	if _, err := os.Stat(wtPath); err == nil {
		return fmt.Errorf("worktree already exists: %s", wtPath)
	}

	// Make sure .forktrust/ stays out of `git status` for the main checkout.
	// Uses .git/info/exclude (local-only, never committed).
	if err := git.EnsureLocalExclude(proj.Path, ".forktrust/"); err != nil {
		if !newJSON {
			fmt.Fprintf(os.Stderr, "warn: could not update .git/info/exclude: %v\n", err)
		}
	}

	r := newResult{
		Project:      proj.Name,
		Slug:         slug,
		WorktreePath: wtPath,
		Branch:       branch,
	}

	if git.HasBranch(proj.Path, branch) {
		newf("branch %s exists, reusing", branch)
		r.BranchReused = true
		if err := addWorktreeExisting(newJSON, proj.Path, wtPath, branch); err != nil {
			return fmt.Errorf("worktree add: %w", err)
		}
	} else {
		newf("creating worktree %s on new branch %s (from current HEAD)", wtPath, branch)
		if err := addWorktreeNew(newJSON, proj.Path, wtPath, branch); err != nil {
			return fmt.Errorf("worktree add: %w", err)
		}
	}

	// Legacy fallback: copy bare .env* files only when there is no
	// .forktrustconfig. Once the repo declares hooks, the user is expected
	// to express .env handling via a copy hook explicitly.
	repoCfg, err := config.LoadRepoConfig(proj.Path)
	if err != nil {
		return err
	}
	if repoCfg == nil {
		copied := copyDotEnvFiles(proj.Path, wtPath)
		r.EnvFilesCopied = copied
		if copied > 0 {
			newf("copied %d .env file(s) into worktree (no .forktrustconfig)", copied)
		}
	} else {
		newf("loaded .forktrustconfig (%d post_create hook(s))", len(repoCfg.Hooks.PostCreate))
		if repoCfg.HasCommandHooks() {
			store, err := config.LoadTrust()
			if err != nil {
				return err
			}
			trusted, reason := store.Check(proj.Path)
			if !trusted {
				fmt.Fprintln(os.Stderr)
				fmt.Fprintf(os.Stderr, "REFUSE: .forktrustconfig has command hooks but this repo is not trusted (%s).\n", reason)
				fmt.Fprintln(os.Stderr, "Inspect the config, then approve with:")
				fmt.Fprintf(os.Stderr, "  forktrust trust %s\n", proj.Path)
				fmt.Fprintln(os.Stderr, "Or skip command hooks for this run with --no-hooks (copy/symlink still run).")
				return coded(ExitHookFailed, fmt.Errorf("untrusted .forktrustconfig: %s", reason))
			}
		}
		hctx := hooks.Context{
			Branch:   branch,
			Slug:     slug,
			Path:     wtPath,
			MainPath: proj.Path,
			Project:  proj.Name,
		}
		stdout := io.Writer(os.Stdout)
		if newJSON {
			stdout = os.Stderr
		}
		results, hookErr := hooks.Run(repoCfg, hctx, stdout, os.Stderr)
		for _, hr := range results {
			r.HooksRun = append(r.HooksRun, hr.Summary)
			if !newJSON {
				mark := "ok"
				if hr.Err != nil {
					mark = "FAIL"
				} else if hr.Skipped {
					mark = "skip"
				}
				fmt.Fprintf(os.Stderr, "  [%s] %s\n", mark, hr.Summary)
			}
		}
		if hookErr != nil {
			return coded(ExitHookFailed, hookErr)
		}
	}

	if newInstall {
		installCmd := proj.InstallCmd
		if installCmd == "" {
			installCmd = "npm install"
		}
		newf("running install: %s", installCmd)
		if err := runShell(wtPath, installCmd); err != nil {
			return fmt.Errorf("install failed: %w", err)
		}
		r.HooksRun = append(r.HooksRun, "install:"+installCmd)
	} else if proj.InstallCmd != "" && repoCfg == nil {
		newf("skipping install (use --install to run: %s)", proj.InstallCmd)
	}

	if !newJSON {
		fmt.Println()
		fmt.Println("Worktree ready:")
		fmt.Printf("  path:   %s\n", wtPath)
		fmt.Printf("  branch: %s\n", branch)
		fmt.Printf("  cd %s\n", wtPath)
	}
	return emitNew(r)
}

func newf(format string, args ...interface{}) {
	if newJSON {
		return
	}
	fmt.Printf("==> "+format+"\n", args...)
}

// selectProject picks the project to operate on:
//   - if --project given, must match a registered name
//   - if exactly one project registered (or auto-discovered from cwd), use it
//   - otherwise error and list candidates.
func selectProject(cfg *config.Config, name string) (*config.Project, error) {
	projects := cfg.AllProjects()
	if len(projects) == 0 {
		return nil, fmt.Errorf("no projects registered and cwd is not a git repo (run: forktrust config add <path>)")
	}
	if name != "" {
		for i := range projects {
			if projects[i].Name == name {
				return &projects[i], nil
			}
		}
		return nil, fmt.Errorf("project %q not registered (run: forktrust config add <path>)", name)
	}
	if len(projects) == 1 {
		return &projects[0], nil
	}
	names := make([]string, len(projects))
	for i := range projects {
		names[i] = projects[i].Name
	}
	return nil, fmt.Errorf("multiple projects registered, specify --project (one of: %v)", names)
}

func copyDotEnvFiles(src, dst string) int {
	candidates := []string{".env", ".env.local", ".env.development", ".env.production"}
	copied := 0
	for _, name := range candidates {
		data, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			continue
		}
		if err := os.WriteFile(filepath.Join(dst, name), data, 0o600); err == nil {
			copied++
		}
	}
	return copied
}

func runShell(dir, command string) error {
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}
