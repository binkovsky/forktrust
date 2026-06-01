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
	"regexp"
	"strings"
	"text/template"

	"github.com/binkovsky/forktrust/internal/config"
	"github.com/binkovsky/forktrust/internal/pathsafe"
)

// envVarNameRE matches a valid POSIX environment variable name.
// Used by parseEnvFile to reject injection-prone lines (names with
// newlines, '=', spaces, or shell metacharacters).
var envVarNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

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
	if reason := protectedDst(ctx.Path, dst); reason != "" {
		e := fmt.Errorf("copy to %q refused: %s", h.To, reason)
		return Result{Type: h.Type, Summary: summary, Err: e}, e
	}
	info, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return Result{Type: h.Type, Summary: summary + " (skipped: source missing)", Skipped: true}, nil
		}
		return Result{Type: h.Type, Summary: summary, Err: err}, err
	}
	if info.IsDir() {
		if err := copyDir(src, dst, ctx.Path); err != nil {
			return Result{Type: h.Type, Summary: summary, Err: err}, err
		}
	} else {
		if err := copyFile(src, dst, ctx.Path); err != nil {
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
	if reason := protectedDst(ctx.Path, dst); reason != "" {
		e := fmt.Errorf("symlink to %q refused: %s", h.To, reason)
		return Result{Type: h.Type, Summary: summary, Err: e}, e
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
	// REFUSE to replace an existing regular file: that would silently
	// overwrite a tracked file and, for a self-referential hook like
	// from=README.md to=README.md, turn every write in the worktree into a
	// write to the main checkout — breaking the core worktree isolation.
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
			// dst is a regular file — refuse silently replacing it.
			e := fmt.Errorf("symlink to %q refused: destination is an existing regular file (use 'copy' hook to copy files, or remove the file first)", h.To)
			return Result{Type: h.Type, Summary: summary, Err: e}, e
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

	// Inject .env.local vars into the command's environment WITHOUT shell-eval.
	// The old approach (`set -a; . .env.local`) executed the file as shell code,
	// which let a committed or hook-written .env.local bypass the trust gate
	// (e.g. `touch /tmp/pwned` on its own line in .env.local would run).
	// We now parse .env.local with a strict KEY=VALUE reader — no shell
	// evaluation occurs, so only the actual variable bindings reach cmd.Env.
	envLocalVars := parseEnvFile(filepath.Join(ctx.Path, ".env.local"))

	cmd := exec.Command("sh", "-c", expanded)
	cmd.Dir = workDir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = append(os.Environ(), envLocalVars...)
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

// copyFile reads src and writes to dst with leaf-level O_NOFOLLOW. Both src
// and dst MUST already be secureJoin-validated by the caller.
//
// dst-side protection (the security guarantees this function actually offers):
//
//   - REFUSE if any ancestor directory between dstRoot and dst is a symlink.
//     This closes the R4 'intra-worktree ancestor symlink → write into
//     node_modules/.bin' attack: SafeJoin accepts ancestor symlinks pointing
//     inside the worktree (legitimate refactor pattern for reads), but writes
//     through such an ancestor would let a copy hook with dst='bin/cleanup'
//     land an executable in node_modules/.bin/cleanup because `bin` resolves
//     to .bin. We refuse all ancestor symlinks for writes — strictly safer
//     than the R4 'follow internal symlinks' permissiveness.
//
//   - REFUSE leaf-level symlink (O_NOFOLLOW) if dst exists as a symlink.
//     A worktree-internal leaf symlink at dst is rare (post_create symlink
//     hook followed by copy hook on the same path is the only known pattern,
//     and that pattern can be expressed as just-symlink-hook). Refusing is
//     simpler than the R4 broken 'allow if internal' branch.
//
// dstRoot is the worktree root used to evaluate ancestor symlinks. Callers
// pass ctx.Path.
func copyFile(src, dst, dstRoot string) error {
	if err := refuseAncestorSymlinks(dstRoot, dst); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	srcInfo, _ := os.Stat(src)
	mode := os.FileMode(0o644)
	if srcInfo != nil {
		mode = srcInfo.Mode().Perm()
	}
	flag := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	f, err := pathsafe.OpenLeafNoFollow(dst, flag, mode)
	if err != nil {
		return fmt.Errorf("safe copy open %s: %w", dst, err)
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

// refuseAncestorSymlinks walks every directory component between root and
// fullPath (exclusive of the leaf), Lstat-ing each. If any is a symlink we
// refuse — writes through ancestor symlinks let a benign-looking dst escape
// to anywhere the symlink points, even when the target is inside the
// worktree (e.g. node_modules/.bin is "inside" but on the executable PATH).
func refuseAncestorSymlinks(root, fullPath string) error {
	rel, err := filepath.Rel(root, fullPath)
	if err != nil {
		return err
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) <= 1 {
		return nil // file directly in root, no ancestors to check
	}
	cur := root
	for _, p := range parts[:len(parts)-1] {
		cur = filepath.Join(cur, p)
		info, err := os.Lstat(cur)
		if err != nil {
			// component doesn't exist yet — that's fine, MkdirAll will
			// create real dirs (not symlinks) below it.
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("write to %s refused: ancestor %s is a symlink (would escape via link target)", fullPath, cur)
		}
	}
	return nil
}

func copyDir(src, dst, dstRoot string) error {
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
			// Guard MkdirAll with the same ancestor-symlink check used by
			// copyFile: a prior symlink hook could have planted a symlink at
			// a parent component of `target` pointing outside the worktree
			// (e.g. bin -> /etc/). Without this guard MkdirAll follows it
			// and creates directories under the attacker-controlled target.
			if err := refuseAncestorSymlinks(dstRoot, target); err != nil {
				return err
			}
			return os.MkdirAll(target, info.Mode().Perm())
		}
		return copyFile(p, target, dstRoot)
	})
}

// protectedDst returns a non-empty refusal reason if dst is a path that hooks
// must never write to, regardless of hook type. Applies to both copy and
// symlink hooks. Returns "" when dst is safe to write.
//
// Protected paths:
//   - <worktree>/.git  (the gitdir file; overwriting it corrupts the worktree)
//   - <worktree>/.git/ (any file under the gitdir hierarchy)
//   - <worktree>/.forktrust (forktrust's own internal state dir)
func protectedDst(worktreePath, dst string) string {
	gitFile := filepath.Join(worktreePath, ".git")
	gitDir := gitFile + string(filepath.Separator)
	ftDir := filepath.Join(worktreePath, ".forktrust")
	ftDirSlash := ftDir + string(filepath.Separator)
	switch {
	case dst == gitFile:
		return "destination .git is protected (overwriting it corrupts the worktree)"
	case strings.HasPrefix(dst, gitDir):
		return "destination is inside .git/ which is protected"
	case dst == ftDir:
		return "destination .forktrust is protected (forktrust internal state)"
	case strings.HasPrefix(dst, ftDirSlash):
		return "destination is inside .forktrust/ which is protected"
	}
	return ""
}

// parseEnvFile reads a KEY=VALUE file (e.g. .env.local) and returns each
// valid binding as "KEY=VALUE" suitable for appending to exec.Cmd.Env.
//
// Security contract: NO shell evaluation occurs. Lines are matched with a
// strict regexp (POSIX env-var name followed by '='); anything else (shell
// commands, subshell expansions, multi-line values, comments without '#') is
// silently skipped. This prevents a committed or hook-written .env.local from
// executing arbitrary code when a command hook runs — unlike the old
// `set -a; . .env.local; set +a` preamble which ran the file as shell code.
func parseEnvFile(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil // missing or unreadable — not an error
	}
	var result []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		// Skip empty lines and comments.
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx <= 0 {
			continue
		}
		key := line[:idx]
		// Only accept POSIX env var names: letter/underscore then word chars.
		if !envVarNameRE.MatchString(key) {
			continue
		}
		result = append(result, line) // KEY=VALUE verbatim — no eval
	}
	return result
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
