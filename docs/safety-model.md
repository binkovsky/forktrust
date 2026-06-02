# Safety model

This document is the authoritative description of forktrust's safety guarantees. If you're an AI agent reasoning about whether an operation is safe, read this first.

There are five named guarantees:

1. [Pre-flight refusal](#1-pre-flight-refusal)
2. [Dry-run parity](#2-dry-run-parity)
3. [Never-lose-WIP](#3-never-lose-wip)
4. [Refuse-on-conflict](#4-refuse-on-conflict)
5. [Refuse-on-ignored-loss](#5-refuse-on-ignored-loss)

Plus three structural protections:

6. [`.env.local` ownership rules](#6-envlocal-ownership-rules)
7. [Path safety in hooks](#7-path-safety-in-hooks)
8. [Trust gate](#8-trust-gate)

## 1. Pre-flight refusal

**Guarantee:** every refusal that `finish` or `rm` may produce happens BEFORE any git mutation. If the command exits non-zero, no commit, merge, push, branch-create, branch-delete, or worktree-remove happened.

### Why

The original bug: `finish` used to commit WIP first, then check whether main was on the right branch. If main was on `dev`, it exited 10 — but left a phantom `WIP: <slug>` commit on the worktree branch. Dry-run (pure reads) said "would refuse" but real `finish` had already mutated state. Owner had to clean up by hand.

Fix: restructure as a pure pre-flight phase followed by a mutation phase.

### How

`runFinish`:
1. Resolve `aheadRef` (pure read).
2. **PRE-FLIGHT — pure reads:**
   - `aheadRef` resolution failed → exit 12
   - Ignored-file check → exit 14
   - `git.CurrentBranch(main)` → wrong branch → exit 10
   - `git.DirtyCount(main)` → dirty → exit 3
3. Auto-commit WIP, count ahead, merge, push, remove worktree, release ports, delete branch.

`runRm`:
1. Resolve branch (pure read).
2. **PRE-FLIGHT — pure reads** (skipped under `--force`):
   - `aheadKnown == false` → exit 12
   - Ignored files → exit 14
   - Work + no origin → exit 9
3. Snapshot (WIP commit + push wip/*), remove worktree, release ports, delete branch.

### Verifying

The pre-flight property is tested with regression suites that:
- Add ignored files, then verify `finish` refuses with no new commits.
- Make main checkout dirty, then verify `finish` refuses with no WIP commit on the worktree branch.
- Remove `origin/main`, then verify both refuse without pushing.

## 2. Dry-run parity

**Guarantee:** `<cmd> --dry-run --json`'s `would_refuse` field and exit code exactly match what the real command would do (in the same instant).

A non-empty `would_refuse` ends with `(exit N)`. Match the order:

| Command | Order |
|---|---|
| `finish --dry-run` | `aheadKnown` → ignored → wrong-branch → dirty-main |
| `rm --dry-run` | `aheadKnown` → ignored → no-origin |

The order matches the real-command code path one-for-one.

### Why

Agents (and CI scripts) need to ask "would this succeed?" without making any mutation. Without parity, dry-run lies, and agents make decisions on stale information.

### How

`previewFinish` and `previewRm` are kept structurally aligned with `runFinish` / `runRm` via shared helpers and matched-order switch statements. Changes to either side must be applied to both in the same commit.

### Caveats

- Parity is "in the same instant." If main is clean at dry-run but someone makes it dirty 5 seconds later, the real command will then exit 3. Re-check immediately before commit if you care about TOCTOU.
- Dry-run does not consult `--force` for behavior changes — it shows what would happen without `--force`. Always re-run dry-run with `--force` if that's what you'd actually invoke.

## 3. Never-lose-WIP

**Guarantee:** `rm` will push any unpushed work to a uniquely-named `wip/*` branch on origin BEFORE removing the worktree, unless the user passes `--force`.

### Definition of "unpushed work"

- `dirty > 0` — any uncommitted change in the worktree (tracked or untracked, NOT ignored).
- OR `commits_ahead > 0` — local commits not on `<aheadRef>`.

### wip/* name

```
wip/<branch-without-fork-prefix>-YYYYMMDD-HHMMSS-<sha7>
```

- `<sha7>` is the short SHA of the tip commit at push time (after any auto-WIP commit).
- Two `rm` runs on different branches in the same second always produce different SHAs ⇒ no collision.
- If two `rm` runs back up identical content (same tip SHA), the push is a no-op ("Everything up-to-date") and exits 0 — content already safe.

### Failure mode

If the push fails (auth, network, non-fast-forward), `rm` exits 5 **with the worktree still in place**. Work is preserved, you can inspect or retry.

### `--force` semantics

`--force` skips:
- The ignored-files guard
- The `aheadKnown` guard
- The no-origin refuse
- The wip/* snapshot entirely

It does NOT skip the worktree-remove (worktree always goes). The local branch is **kept** if there was work (so commits remain reachable), **deleted** if clean.

`--force` is the only way to lose work. Never use it from an agent without explicit user confirmation.

## 4. Refuse-on-conflict

**Guarantee:** `finish` will not auto-resolve a merge conflict. Ever. No `--strategy ours`. No `--strategy theirs`. No `-X` flags.

### How

`git merge --no-ff --no-edit <branch>` is the only merge command. If it returns non-zero (which it does on conflict), `finish` immediately runs `git merge --abort` to restore main to its pre-merge state, then exits 2.

### What you can do

After exit 2:
1. Open the conflict yourself: `cd <main-repo> && git merge fork/<slug>`
2. Resolve, commit, push.
3. Clean up: `forktrust rm <slug>` (will detect 0 ahead and just remove).

## 5. Refuse-on-ignored-loss

**Guarantee:** `git worktree remove` silently deletes ignored files (files matched by `.gitignore` but not tracked). forktrust refuses with exit 14 if any are present, unless `--force` is passed.

### What counts as "safe to delete"

ONLY the root-level `.env.local` IFF both:
- It is a **regular file** (not a symlink).
- It begins with the exact `ManagedHeader` line written by forktrust.

Any other ignored file — `secret.log`, `dist/`, `node_modules/.cache`, nested `foo/.env.local`, or a symlink at `.env.local` — counts.

### How to bypass

```bash
forktrust rm <slug> --force
forktrust finish <slug>     # finish has no --force; move ignored files out first
```

For `finish`, the agent should NEVER bypass — finish + ignored loss = data is gone without a wip/* snapshot.

For `rm`, `--force` skips both the snapshot and the ignored guard. Use only when the ignored files are truly disposable.

### Verifying

The bypass surface is closed by tightening both:
- The path check: `filepath.Clean(line) == ".env.local"` (matches only root path, not nested).
- The content check: `os.Lstat` first refuses symlinks; then read N bytes and compare against the full `ManagedHeader` (not a prefix — so `# Managed by forktrust but actually mine` is correctly counted).

## 6. `.env.local` ownership rules

forktrust is the only file it owns. Detection: BOTH conditions must hold for forktrust to consider a `.env.local` "ours":

1. Path equals exactly `.env.local` at the worktree root (not nested, not `subdir/.env.local`).
2. File is a regular file (`os.Lstat`), not a symlink.
3. Content's first bytes equal the exact `ManagedHeader` string (including trailing newline).

The `ManagedHeader` constant:
```
# Managed by forktrust. Do not edit; values are overwritten on each `forktrust new`.\n
```

Single source of truth: `ports.ManagedHeader`. Duplicated as `git.envLocalManagedHeader` only to avoid an import cycle; both must change together.

If your user-authored `.env.local` (no marker) gets ignored by `.gitignore`, it shows up as a normal ignored file and is protected by [refuse-on-ignored-loss](#5-refuse-on-ignored-loss).

If you author `.env.local` and want to keep using it across worktrees:
- Put it in `.forktrustconfig` as a `copy` hook, OR
- Disable port allocation (don't set `[ports]`) and forktrust won't touch it at all.

## 7. Path safety in hooks

All paths in `copy` / `symlink` hooks go through `pathsafe.SafeJoin` which:

1. **Lexical**: rejects absolute paths and `..`-prefixed paths.
2. **Runtime**: walks every existing path component with `os.Lstat`. If any is a symlink, resolves it and verifies the resolved target is inside the root via `filepath.EvalSymlinks`.

Plus three writer-specific protections:

- **`OpenLeafNoFollow`**: the actual file write opens with `O_NOFOLLOW`, so a leaf-symlink swap between the `Lstat` sweep and the write fails with `ELOOP`. Closes the leaf TOCTOU window.
- **`refuseAncestorSymlinks`**: in `copyFile` and `copyDir`'s `MkdirAll`, walks every ancestor of the destination and refuses if any is a symlink. Closes "prior symlink hook plants `bin -> /etc/`, later copy hook lands under `/etc/`" attacks.
- **`protectedDst`**: refuses `to = ".git"`, `to = ".git/<anything>"`, `to = ".forktrust"`, `to = ".forktrust/<anything>"` in both `copy` and `symlink` hooks.

Plus one for `symlink` specifically: refuses to replace an existing regular file at `to` (otherwise `from=README.md to=README.md` turns every worktree write into a main-checkout write).

Tested with regression cases for each guard. See `internal/pathsafe/pathsafe_test.go`, `internal/hooks/`, and `internal/git/worktree_test.go`.

## 8. Trust gate

`command` hooks REFUSE to run until the user explicitly trusts `.forktrustconfig`:

```bash
forktrust trust
```

Trust is **SHA-pinned**: the trust store records the SHA-256 of `.forktrustconfig` at trust time. Any edit changes the SHA, auto-revoking trust. A malicious commit cannot silently add a `command` hook — the user is forced through `forktrust trust` again, which is when they can see the diff.

`copy` and `symlink` hooks don't require trust: they cannot execute arbitrary code and they cannot escape the worktree (see [path safety](#7-path-safety-in-hooks)).

The trust check runs BEFORE worktree creation. If hooks are untrusted, no worktree, no port allocation, no exclude write. Exit 8.

`--no-hooks` on `forktrust new` skips command hooks (and the trust check) entirely. `copy` and `symlink` still run.

Trust store location:
- macOS: `~/Library/Application Support/forktrust/trust.toml`
- Linux/other: `~/.config/forktrust/trust.toml`

## Threat model summary

forktrust defends against:

- **Accidental data loss** from `git worktree remove` silently dropping ignored files.
- **Phantom commits** left behind when a refusal happens mid-pipeline.
- **Dry-run lying** about what the real command would do.
- **Symlink-confinement escapes** from `copy`/`symlink` hooks (lexical, runtime, leaf TOCTOU, ancestor TOCTOU all covered).
- **Protected-path overwrites** of `.git/` (would corrupt the worktree).
- **`symlink` hook hijacking** tracked files by replacing them with links to main.
- **Silent shell injection** via `.env.local` content (no shell eval; strict KEY=VALUE parser).
- **Same-second wip/* collision** when two `rm` runs land in the same wall-clock second.
- **Marker-bypass** by user files crafted to mimic forktrust's `.env.local`.
- **Tampered `.forktrustconfig`** silently gaining `command` hook execution (SHA pin auto-revokes).

forktrust does NOT defend against:

- A user who passes `--force` and then loses work — that is what `--force` means.
- A user who runs `forktrust trust` on a malicious config and then `forktrust new`. Once trusted, command hooks execute. Read the diff.
- Concurrent git operations from outside forktrust (e.g. someone running `git push --force origin main` while you `finish`). Out of scope.
- Filesystem-level malice (root-equivalent attacker). Out of scope.
