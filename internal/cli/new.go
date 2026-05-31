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
	"github.com/binkovsky/forktrust/internal/ports"
	"github.com/binkovsky/forktrust/internal/predict"
)

var (
	newInstall bool
	newProject string
	newJSON    bool
	newNoHooks bool
)

var newCmd = &cobra.Command{
	Use:   "new <slug>",
	Short: "Create an isolated worktree for a new task",
	Long: `Create a new git worktree at .forktrust/worktrees/<slug> on branch fork/<slug>.

The worktree is isolated from the main checkout, so parallel AI sessions can
each have their own without stepping on each other. By default copies any
.env / .env.local / .env.development / .env.production files from the main
checkout when there is no .forktrustconfig. With a .forktrustconfig, declared
copy/symlink/command hooks run instead.

Auto-adds .forktrust/ to .git/info/exclude (local-only, never touches the
project's tracked .gitignore).

Use --no-hooks to skip only command hooks (copy/symlink still run).`,
	Args: cobra.ExactArgs(1),
	RunE: runNew,
}

func init() {
	newCmd.Flags().BoolVar(&newInstall, "install", false, "run the project's install command after creating the worktree")
	newCmd.Flags().StringVarP(&newProject, "project", "p", "", "target project name (required if more than one is registered)")
	newCmd.Flags().BoolVar(&newJSON, "json", false, "emit a structured JSON result on stdout (one object)")
	newCmd.Flags().BoolVar(&newNoHooks, "no-hooks", false, "skip command hooks (copy/symlink still run); also skips the trust gate")
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

	// PREFLIGHT — load .forktrustconfig and check trust BEFORE creating any
	// worktree state. This way an untrusted command-hook config refuses
	// cleanly with no orphaned worktree/port to clean up.
	repoCfg, err := config.LoadRepoConfig(proj.Path)
	if err != nil {
		return err
	}
	if repoCfg != nil && repoCfg.HasCommandHooks() && !newNoHooks {
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

	if repoCfg == nil {
		// Legacy fallback: copy bare .env* files when there is no
		// .forktrustconfig. Once the repo declares hooks, the user is
		// expected to express .env handling via a copy hook explicitly.
		copied := copyDotEnvFiles(proj.Path, wtPath)
		r.EnvFilesCopied = copied
		if copied > 0 {
			newf("copied %d .env file(s) into worktree (no .forktrustconfig)", copied)
		}
	} else {
		newf("loaded .forktrustconfig (%d post_create hook(s))", len(repoCfg.Hooks.PostCreate))

		// Port allocation runs BEFORE hooks so command hooks see PORT in env via .env.local.
		if repoCfg.Ports != nil {
			min, max, perr := ports.ParseRange(repoCfg.Ports.Range)
			if perr != nil {
				return fmt.Errorf("invalid [ports].range: %w", perr)
			}
			size := repoCfg.Ports.Size
			if size == 0 {
				size = 10
			}
			storePath, _ := ports.DefaultPath()
			blk, perr := ports.Allocate(storePath, ports.AllocOpts{
				Repo: proj.Path, Slug: slug, Min: min, Max: max, Size: size,
			})
			if perr != nil {
				return fmt.Errorf("port allocation: %w", perr)
			}
			if perr := ports.WriteEnv(wtPath, blk, repoCfg.Ports.Vars); perr != nil {
				_ = ports.Release(storePath, proj.Path, slug)
				return fmt.Errorf("port .env.local write: %w", perr)
			}
			for p := blk.Start; p <= blk.End(); p++ {
				r.Ports = append(r.Ports, p)
			}
			newf("allocated ports %d-%d (written to %s)", blk.Start, blk.End(), ports.EnvFileName)
		}

		// Build the set of hooks to actually run. --no-hooks filters out
		// command hooks while keeping copy/symlink (matches the docs).
		runCfg := *repoCfg
		if newNoHooks {
			var kept []config.Hook
			skipped := 0
			for _, h := range repoCfg.Hooks.PostCreate {
				if h.Type == config.HookCommand {
					skipped++
					continue
				}
				kept = append(kept, h)
			}
			runCfg.Hooks.PostCreate = kept
			if skipped > 0 {
				newf("--no-hooks: skipping %d command hook(s)", skipped)
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
		results, hookErr := hooks.Run(&runCfg, hctx, stdout, os.Stderr)
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

	// Cross-worktree edit prediction: warn if other active worktrees are
	// touching files that this one will likely touch too. Heuristic — does
	// NOT block creation; informational only.
	mainBranch := proj.MainBranch
	if mainBranch == "" {
		mainBranch = "main"
	}
	if overlaps, perr := predict.Active(proj.Path, mainBranch, slug); perr == nil && len(overlaps) > 0 {
		if !newJSON {
			fmt.Fprint(os.Stderr, predict.FormatWarning(overlaps, 5))
		}
		for _, o := range overlaps {
			r.PredictedOverlaps = append(r.PredictedOverlaps, o.File)
		}
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

// selectProject picks the project for this invocation. Logic:
//   - if --project given, must match a registered name
//   - if cwd is inside a registered project, prefer that one
//   - if cwd is in a git repo NOT registered, refuse (would surprise the user)
//   - if cwd is not in a git repo:
//   - exactly one registered project → use it
//   - zero registered projects → error
//   - many registered projects → require --project
func selectProject(cfg *config.Config, name string) (*config.Project, error) {
	if name != "" {
		for i := range cfg.Projects {
			if cfg.Projects[i].Name == name {
				return &cfg.Projects[i], nil
			}
		}
		return nil, fmt.Errorf("project %q not registered (run: forktrust config add <path> %s)", name, name)
	}

	cwd, _ := os.Getwd()
	cwdRepoRoot := findGitRoot(cwd)

	if cwdRepoRoot != "" {
		// We're inside a git repo. Find the registered entry that owns it.
		for i := range cfg.Projects {
			if samePath(cfg.Projects[i].Path, cwdRepoRoot) {
				return &cfg.Projects[i], nil
			}
		}
		// Not registered.
		if len(cfg.Projects) == 0 {
			// Zero-config flow: treat cwd as anonymous project.
			return &config.Project{Name: filepath.Base(cwdRepoRoot), Path: cwdRepoRoot}, nil
		}
		names := projectNames(cfg)
		return nil, coded(ExitCwdNotRegistered, fmt.Errorf(
			"cwd is in git repo %s which is not registered (registered: %v). Either:\n"+
				"  forktrust config add . <name>     # register this repo\n"+
				"  forktrust new --project <name> %s   # target a registered repo explicitly",
			cwdRepoRoot, names, "<slug>"))
	}

	// Not in a git repo. Fall back to registered-only logic.
	if len(cfg.Projects) == 0 {
		return nil, fmt.Errorf("no projects registered and cwd is not a git repo (run: forktrust config add <path>)")
	}
	if len(cfg.Projects) == 1 {
		return &cfg.Projects[0], nil
	}
	return nil, fmt.Errorf("multiple projects registered, specify --project (one of: %v)", projectNames(cfg))
}

func projectNames(cfg *config.Config) []string {
	out := make([]string, len(cfg.Projects))
	for i := range cfg.Projects {
		out[i] = cfg.Projects[i].Name
	}
	return out
}

// findGitRoot walks up from start until it finds a .git directory or hits the
// filesystem root. Returns "" if no git repo is found.
func findGitRoot(start string) string {
	dir, err := filepath.Abs(start)
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
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
