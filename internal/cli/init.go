package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/binkovsky/forktrust/internal/templates"
)

var (
	initTemplate string
	initForce    bool
	initJSON     bool
	initShow     bool // print to stdout instead of writing the file
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Scaffold a .forktrustconfig from a starter template",
	Long: `Write a starter .forktrustconfig in the current directory based on a known
template. If --template is omitted, auto-detect based on cwd files
(package.json with "next" dep → nextjs, go.mod → go-cli,
pyproject.toml with [tool.poetry] → python-poetry, else minimal).

Refuses if .forktrustconfig already exists, unless --force.

To inspect a template without writing it:
  forktrust init --template nextjs --show
  forktrust template show nextjs

To enumerate available templates:
  forktrust template list

Exit codes:
  0   wrote / printed
  1   template not found, or .forktrustconfig already exists (no --force)`,
	Args: cobra.NoArgs,
	RunE: runInit,
}

func init() {
	initCmd.Flags().StringVarP(&initTemplate, "template", "t", "", "template name (see `forktrust template list`); empty triggers auto-detect")
	initCmd.Flags().BoolVar(&initForce, "force", false, "overwrite an existing .forktrustconfig (refuses without this flag)")
	initCmd.Flags().BoolVar(&initJSON, "json", false, "emit a structured JSON object on stdout")
	initCmd.Flags().BoolVar(&initShow, "show", false, "print the chosen template to stdout instead of writing the file")
}

// initResult is the stable JSON schema for `forktrust init [--json]`.
type initResult struct {
	Template     string `json:"template"`
	Path         string `json:"path"`              // absolute path that was written (or would be)
	Wrote        bool   `json:"wrote"`             // true if the file was written
	Overwrote    bool   `json:"overwrote"`         // true if --force replaced an existing file
	Show         bool   `json:"show,omitempty"`    // --show: printed instead of wrote
	AutoDetected bool   `json:"auto_detected,omitempty"`
	Detection    string `json:"detection_reason,omitempty"` // human-readable why this template won the detect race
}

func runInit(_ *cobra.Command, _ []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	target := filepath.Join(cwd, ".forktrustconfig")

	name := initTemplate
	r := initResult{Path: target}
	if name == "" {
		picked, reason := detect(cwd)
		name = picked
		r.AutoDetected = true
		r.Detection = reason
	}
	t, ok := templates.Get(name)
	if !ok {
		return fmt.Errorf("template %q not found; run `forktrust template list` to see options", name)
	}
	r.Template = t.Name

	if initShow {
		r.Show = true
		if initJSON {
			return emitJSON(true, r) // metadata only
		}
		fmt.Print(t.Content)
		return nil
	}

	exists, err := fileExists(target)
	if err != nil {
		return err
	}
	if exists && !initForce {
		return fmt.Errorf("%s already exists (pass --force to overwrite, or --show to print the template to stdout)", target)
	}
	if err := os.WriteFile(target, []byte(t.Content), 0o644); err != nil {
		return err
	}
	r.Wrote = true
	r.Overwrote = exists

	if initJSON {
		return emitJSON(true, r)
	}
	if r.Overwrote {
		fmt.Printf("overwrote %s with template %q\n", target, t.Name)
	} else {
		fmt.Printf("wrote %s using template %q\n", target, t.Name)
	}
	if r.AutoDetected {
		fmt.Printf("  (auto-detected: %s)\n", r.Detection)
	}
	fmt.Println()
	fmt.Println("Next:")
	fmt.Println("  1. Review the file — every starter is a sensible default, not a perfect fit.")
	fmt.Println("  2. `forktrust doctor` to confirm the config is valid.")
	fmt.Println("  3. `forktrust trust` if you added any [[hooks.post_create]] with type=command.")
	fmt.Println("  4. `forktrust new <slug>` to use it.")
	return nil
}

// fileExists is os.Stat with a clean (exists?, error?) signature — distinguishes
// not-found from real errors (permission denied, EIO).
func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, err
}

// detect picks the best template for the cwd based on filesystem signals.
// Returns the chosen template name and a one-line reason for the JSON
// envelope / human output. Falls back to "minimal" when nothing matches.
func detect(cwd string) (name, reason string) {
	// Strongest signal first.
	pkgJSON := filepath.Join(cwd, "package.json")
	if data, err := os.ReadFile(pkgJSON); err == nil {
		// Cheap substring sniff for the "next" dependency — full JSON parse
		// is overkill for a hint and would error on comments / trailing
		// commas that pnpm/yarn sometimes accept.
		if hasDependency(data, "next") {
			return "nextjs", `package.json declares a "next" dependency`
		}
		// node project without next → still prefer minimal over python/go.
		// Future: add a "node" template; for now fall through.
	}
	pyproject := filepath.Join(cwd, "pyproject.toml")
	if data, err := os.ReadFile(pyproject); err == nil {
		if strings.Contains(string(data), "[tool.poetry]") {
			return "python-poetry", "pyproject.toml has [tool.poetry] section"
		}
		// non-poetry python: fall through; no python-pip template yet.
	}
	if exists, _ := fileExists(filepath.Join(cwd, "go.mod")); exists {
		return "go-cli", "go.mod present"
	}
	return "minimal", "no project-type signals detected (no package.json with next, no go.mod, no pyproject.toml [tool.poetry])"
}

// hasDependency checks whether a JSON object's "dependencies" or
// "devDependencies" map contains the given key. Implemented as a substring
// scan over the JSON-encoded form because we only need a hint, not a full
// dependency graph, and full json.Unmarshal here would crash on the trailing
// commas some tools emit. We look for `"<name>": ` (with the leading quote)
// to avoid matching `"@scope/<name>": `.
func hasDependency(packageJSON []byte, name string) bool {
	// Lowercase both haystack and needle so the user's casing in
	// package.json doesn't matter — npm dep names are case-insensitive at
	// resolution time.
	needle := `"` + strings.ToLower(name) + `"`
	hay := strings.ToLower(string(packageJSON))
	idx := 0
	for {
		i := strings.Index(hay[idx:], needle)
		if i < 0 {
			return false
		}
		// Anchor on a colon-space following the key to avoid hits in
		// `"description"` etc that contain the substring.
		end := idx + i + len(needle)
		// Trim ASCII whitespace until ":" — Go's strings.IndexFunc is overkill.
		j := end
		for j < len(hay) && (hay[j] == ' ' || hay[j] == '\t') {
			j++
		}
		if j < len(hay) && hay[j] == ':' {
			return true
		}
		idx = end
	}
}

// ---- template list/show subcommands ----

var (
	templateJSON bool
)

var templateCmd = &cobra.Command{
	Use:   "template",
	Short: "Inspect available .forktrustconfig starter templates",
	Long: `Templates are pre-baked .forktrustconfig starters shipped inside the
forktrust binary. ` + "`forktrust init`" + ` uses them to scaffold new repos.

  forktrust template list                 # all available templates
  forktrust template show <name>          # print a template to stdout

Each template is valid TOML and passes config validation; CI enforces this.`,
}

var templateListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available .forktrustconfig templates",
	Args:  cobra.NoArgs,
	RunE:  runTemplateList,
}

var templateShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Print a template's contents to stdout",
	Args:  cobra.ExactArgs(1),
	RunE:  runTemplateShow,
}

func init() {
	templateListCmd.Flags().BoolVar(&templateJSON, "json", false, "emit a JSON array of {name, description, detect}")
	templateCmd.AddCommand(templateListCmd, templateShowCmd)
}

// templateListEntry mirrors the public shape we expose to JSON consumers.
type templateListEntry struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Detect      []string `json:"detect,omitempty"`
}

func runTemplateList(_ *cobra.Command, _ []string) error {
	all := templates.All()
	if templateJSON {
		entries := make([]templateListEntry, 0, len(all))
		for _, t := range all {
			entries = append(entries, templateListEntry{Name: t.Name, Description: t.Description, Detect: t.Detect})
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(entries)
	}
	fmt.Printf("%d template(s):\n", len(all))
	for _, t := range all {
		fmt.Printf("\n  %s\n    %s\n", t.Name, t.Description)
		if len(t.Detect) > 0 {
			fmt.Printf("    auto-detect: %s\n", strings.Join(t.Detect, ", "))
		}
	}
	fmt.Println()
	fmt.Println("Use: forktrust init --template <name>")
	fmt.Println("     forktrust template show <name>")
	return nil
}

func runTemplateShow(_ *cobra.Command, args []string) error {
	name := args[0]
	t, ok := templates.Get(name)
	if !ok {
		return fmt.Errorf("template %q not found; run `forktrust template list` to see options", name)
	}
	fmt.Print(t.Content)
	return nil
}
