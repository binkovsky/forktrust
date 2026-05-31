package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/binkovsky/forktrust/internal/config"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage the project registry",
}

var configAddCmd = &cobra.Command{
	Use:   "add <path> [name]",
	Short: "Register a git repository (defaults to using the dir name)",
	Args:  cobra.RangeArgs(1, 2),
	RunE:  runConfigAdd,
}

var configListCmd = &cobra.Command{
	Use:   "list",
	Short: "Show registered projects",
	RunE:  runConfigList,
}

var configRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Drop a project from the registry",
	Args:  cobra.ExactArgs(1),
	RunE:  runConfigRemove,
}

var configPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Print the config file path",
	RunE: func(_ *cobra.Command, _ []string) error {
		p, err := config.Path()
		if err != nil {
			return err
		}
		fmt.Println(p)
		return nil
	},
}

func init() {
	configCmd.AddCommand(configAddCmd, configListCmd, configRemoveCmd, configPathCmd)
}

func runConfigAdd(_ *cobra.Command, args []string) error {
	absPath, err := filepath.Abs(args[0])
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(absPath, ".git")); err != nil {
		return fmt.Errorf("%s is not a git repository", absPath)
	}
	name := filepath.Base(absPath)
	if len(args) == 2 {
		name = args[1]
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := cfg.Add(config.Project{Name: name, Path: absPath}); err != nil {
		return err
	}
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Printf("registered %s -> %s\n", name, absPath)
	return nil
}

func runConfigList(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if len(cfg.Projects) == 0 {
		fmt.Println("no projects registered (run: forktrust config add <path>)")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tPATH\tMAIN-BRANCH")
	for _, p := range cfg.Projects {
		mb := p.MainBranch
		if mb == "" {
			mb = "main"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", p.Name, p.Path, mb)
	}
	return w.Flush()
}

func runConfigRemove(_ *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if !cfg.Remove(args[0]) {
		return fmt.Errorf("project %q not found", args[0])
	}
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Printf("removed %s\n", args[0])
	return nil
}
