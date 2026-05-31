package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/binkovsky/forktrust/internal/config"
	"github.com/binkovsky/forktrust/internal/git"
)

var (
	newInstall bool
	newProject string
)

var newCmd = &cobra.Command{
	Use:   "new <slug>",
	Short: "Create an isolated worktree for a new task",
	Long: `Create a new git worktree at .forktrust/worktrees/<slug> on branch fork/<slug>.

The worktree is isolated from the main checkout, so parallel AI sessions can
each have their own without stepping on each other. Copies any .env / .env.local /
.env.development / .env.production from the main checkout if present.`,
	Args: cobra.ExactArgs(1),
	RunE: runNew,
}

func init() {
	newCmd.Flags().BoolVar(&newInstall, "install", false, "run the project's install command after creating the worktree")
	newCmd.Flags().StringVarP(&newProject, "project", "p", "", "target project name (required if more than one is registered)")
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
	// Uses .git/info/exclude (local-only, not committed).
	if err := git.EnsureLocalExclude(proj.Path, ".forktrust/"); err != nil {
		fmt.Fprintf(os.Stderr, "warn: could not update .git/info/exclude: %v\n", err)
	}

	if git.HasBranch(proj.Path, branch) {
		fmt.Printf("==> branch %s exists, reusing\n", branch)
		if err := git.AddWorktreeExistingBranch(proj.Path, wtPath, branch); err != nil {
			return fmt.Errorf("worktree add: %w", err)
		}
	} else {
		fmt.Printf("==> creating worktree %s on new branch %s (from current HEAD)\n", wtPath, branch)
		if err := git.AddWorktreeNewBranch(proj.Path, wtPath, branch); err != nil {
			return fmt.Errorf("worktree add: %w", err)
		}
	}

	copied := copyDotEnvFiles(proj.Path, wtPath)
	if copied > 0 {
		fmt.Printf("==> copied %d .env file(s) into worktree\n", copied)
	}

	if newInstall {
		installCmd := proj.InstallCmd
		if installCmd == "" {
			installCmd = "npm install"
		}
		fmt.Printf("==> running install: %s\n", installCmd)
		if err := runShell(wtPath, installCmd); err != nil {
			return fmt.Errorf("install failed: %w", err)
		}
	} else if proj.InstallCmd != "" {
		fmt.Printf("==> skipping install (use --install to run: %s)\n", proj.InstallCmd)
	}

	fmt.Println()
	fmt.Println("Worktree ready:")
	fmt.Printf("  path:   %s\n", wtPath)
	fmt.Printf("  branch: %s\n", branch)
	fmt.Printf("  cd %s\n", wtPath)
	return nil
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
