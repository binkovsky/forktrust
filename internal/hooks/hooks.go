// Package hooks executes post_create hooks declared in .forktrustconfig.
// Hooks run in declared order; if one fails, subsequent hooks are skipped.
package hooks

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/binkovsky/forktrust/internal/config"
)

// Context is the template/runtime context passed to every hook.
type Context struct {
	Branch   string // e.g. "fork/fix-bug"
	Slug     string // e.g. "fix-bug"
	Path     string // absolute path of the new worktree
	MainPath string // absolute path of the main checkout
	Project  string // registered project name
}

// Result describes one hook's outcome.
type Result struct {
	Type    string // copy | symlink | command
	Summary string // short human-readable description, e.g. "copy .env -> .env"
	Skipped bool   // true if input file/dir was missing (copy/symlink)
	Err     error  // nil on success
}

// Run executes all post_create hooks in order. Stops at the first error.
// stdoutStream and stderrStream control where command hook output goes
// (use os.Stderr for both in JSON mode to keep stdout clean).
func Run(cfg *config.RepoConfig, ctx Context, stdoutStream, stderrStream io.Writer) ([]Result, error) {
	var results []Result
	if cfg == nil {
		return results, nil
	}
	for i, h := range cfg.Hooks.PostCreate {
		r, err := runOne(h, ctx, stdoutStream, stderrStream)
		results = append(results, r)
		if err != nil {
			return results, fmt.Errorf("hook %d (%s) failed: %w", i, h.Type, err)
		}
	}
	return results, nil
}

func runOne(h config.Hook, ctx Context, stdout, stderr io.Writer) (Result, error) {
	switch h.Type {
	case config.HookCopy:
		return doCopy(h, ctx)
	case config.HookSymlink:
		return doSymlink(h, ctx)
	case config.HookCommand:
		return doCommand(h, ctx, stdout, stderr)
	}
	return Result{Type: h.Type}, fmt.Errorf("unknown hook type %q", h.Type)
}

func doCopy(h config.Hook, ctx Context) (Result, error) {
	summary := fmt.Sprintf("copy %s -> %s", h.From, h.To)
	src, err := secureJoin(ctx.MainPath, h.From)
	if err != nil {
		return Result{Type: h.Type, Summary: summary, Err: err}, fmt.Errorf("copy from %q: %w", h.From, err)
	}
	dst, err := secureJoin(ctx.Path, h.To)
	if err != nil {
		return Result{Type: h.Type, Summary: summary, Err: err}, fmt.Errorf("copy to %q: %w", h.To, err)
	}
	info, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return Result{Type: h.Type, Summary: summary + " (skipped: source missing)", Skipped: true}, nil
		}
		return Result{Type: h.Type, Summary: summary, Err: err}, err
	}
	if info.IsDir() {
		if err := copyDir(src, dst); err != nil {
			return Result{Type: h.Type, Summary: summary, Err: err}, err
		}
	} else {
		if err := copyFile(src, dst); err != nil {
			return Result{Type: h.Type, Summary: summary, Err: err}, err
		}
	}
	return Result{Type: h.Type, Summary: summary}, nil
}

func doSymlink(h config.Hook, ctx Context) (Result, error) {
	summary := fmt.Sprintf("symlink %s -> %s", h.To, h.From)
	src, err := secureJoin(ctx.MainPath, h.From)
	if err != nil {
		return Result{Type: h.Type, Summary: summary, Err: err}, fmt.Errorf("symlink from %q: %w", h.From, err)
	}
	dst, err := secureJoin(ctx.Path, h.To)
	if err != nil {
		return Result{Type: h.Type, Summary: summary, Err: err}, fmt.Errorf("symlink to %q: %w", h.To, err)
	}
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			return Result{Type: h.Type, Summary: summary + " (skipped: source missing)", Skipped: true}, nil
		}
		return Result{Type: h.Type, Summary: summary, Err: err}, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return Result{Type: h.Type, Summary: summary, Err: err}, err
	}
	// Replace existing symlink or empty dir so re-runs are idempotent.
	// For non-empty directories (typically tracked by git — symlink hook
	// is meant for gitignored dirs like node_modules), skip with a clear
	// message rather than destroying user data.
	if info, err := os.Lstat(dst); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			_ = os.Remove(dst)
		} else if info.IsDir() {
			entries, _ := os.ReadDir(dst)
			if len(entries) > 0 {
				return Result{Type: h.Type, Summary: summary + " (skipped: target is a non-empty tracked dir)", Skipped: true}, nil
			}
			_ = os.Remove(dst)
		} else {
			_ = os.Remove(dst)
		}
	}
	if err := os.Symlink(src, dst); err != nil {
		return Result{Type: h.Type, Summary: summary, Err: err}, err
	}
	return Result{Type: h.Type, Summary: summary}, nil
}

func doCommand(h config.Hook, ctx Context, stdout, stderr io.Writer) (Result, error) {
	expanded, err := expand(h.Run, ctx)
	if err != nil {
		return Result{Type: h.Type, Summary: "command: <template error>", Err: err}, err
	}
	summary := fmt.Sprintf("command: %s", truncate(expanded, 60))

	workDir := ctx.Path
	if h.WorkDir != "" {
		workDir = filepath.Join(ctx.Path, h.WorkDir)
	}

	// Auto-source the worktree's .env.local (if any) so command hooks see
	// PORT and friends without needing to write `source .env.local`
	// themselves. The `set -a; ... set +a` pattern exports each variable.
	// Errors sourcing are swallowed (2>/dev/null) so a missing file is fine.
	envLocal := filepath.Join(ctx.Path, ".env.local")
	preamble := ""
	if _, err := os.Stat(envLocal); err == nil {
		// Quote the path so spaces / specials in worktree paths are safe.
		preamble = "set -a; . " + shellQuote(envLocal) + " >/dev/null 2>&1; set +a; "
	}

	cmd := exec.Command("sh", "-c", preamble+expanded)
	cmd.Dir = workDir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = os.Environ()
	for k, v := range h.Env {
		ev, err := expand(v, ctx)
		if err != nil {
			return Result{Type: h.Type, Summary: summary, Err: err}, err
		}
		cmd.Env = append(cmd.Env, k+"="+ev)
	}
	if err := cmd.Run(); err != nil {
		return Result{Type: h.Type, Summary: summary, Err: err}, err
	}
	return Result{Type: h.Type, Summary: summary}, nil
}

// shellQuote wraps s in single quotes, escaping any embedded single quote so
// the result is safe to paste into a POSIX sh command line.
func shellQuote(s string) string {
	if !strings.ContainsAny(s, " \t\n'\"\\$`!*?[](){}<>|&;#~=") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// expand applies text/template with strict missingkey behavior — a template
// referring to a field that doesn't exist on Context errors out, rather than
// silently writing "<no value>".
func expand(s string, ctx Context) (string, error) {
	t, err := template.New("hook").Option("missingkey=error").Parse(s)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, ctx); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	info, _ := os.Stat(src)
	mode := os.FileMode(0o644)
	if info != nil {
		mode = info.Mode().Perm()
	}
	return os.WriteFile(dst, data, mode)
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		// If this entry is a symlink, refuse to follow it if it escapes the
		// source root. This protects against tracked symlinks inside the
		// source directory that point outside (e.g. dir/inner -> ../secret).
		// Internal symlinks (pointing to another file within src) are still
		// honored — we copy the resolved content.
		if info.Mode()&os.ModeSymlink != 0 {
			if !withinRoot(src, p) {
				// Skip silently — the entry would have leaked outside src.
				return nil
			}
		}
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		return copyFile(p, target)
	})
}

// Summary collapses results into a short multi-line string for human output.
func Summary(results []Result) string {
	if len(results) == 0 {
		return ""
	}
	var b strings.Builder
	for _, r := range results {
		mark := "ok"
		if r.Err != nil {
			mark = "FAIL"
		} else if r.Skipped {
			mark = "skip"
		}
		fmt.Fprintf(&b, "  [%s] %s\n", mark, r.Summary)
	}
	return b.String()
}
