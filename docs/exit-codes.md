# Exit codes catalog

forktrust uses stable exit codes so scripts and AI agents can branch on them programmatically. **Never parse stderr.** Always switch on the exit code, and read `--json` output's `would_refuse` field for the structured reason.

Codes are stable across releases. New codes are added at the high end; existing codes never change meaning.

## Quick reference

| Code | One-line meaning |
|---|---|
| 0 | success |
| 1 | generic error (cobra default) |
| 2 | merge conflict — refuse to auto-resolve |
| 3 | main worktree is dirty |
| 4 | push to origin failed |
| 5 | wip/* snapshot push failed (worktree NOT removed) |
| 6 | no worktree matching slug |
| 7 | slug matches worktrees in multiple projects |
| 8 | hook failed (or untrusted command hook) |
| 9 | no origin remote configured |
| 10 | main checkout on the wrong branch |
| 11 | cwd is in an unregistered git repo |
| 12 | could not determine ahead count (no main reference resolved) |
| 13 | rm/finish: worktree removed and ports released, but `git branch -D` failed |
| 14 | rm/finish: worktree has ignored files that would be permanently deleted |
| 15 | finish: `[verify]` gate failed (command non-zero, or `require_clean` worktree dirty) |
| 16 | finish/scope-check: diff touches files outside the declared `--scope` contract |

## Full catalog

Each entry covers: cause, what the user should do, what an AI agent should do.

---

### `0` — success

The command finished without error. JSON output (if requested) reflects the actual state changes.

---

### `1` — generic error

Fallback used when no more specific code applies. The stderr text describes what happened. Examples:
- detached HEAD worktree passed to `rm`
- I/O error reading config
- unknown command

**User:** read the error message. **Agent:** surface to the user; do not assume a fix.

---

### `2` — merge conflict

`finish` attempted `git merge --no-ff <branch> → main` and the merge had conflicts. The merge was **aborted** (`git merge --abort` was run), main is back to its pre-merge state, nothing was pushed.

**User:** resolve manually. The error message includes the exact reproduction:
```
cd <main-repo> && git merge fork/<slug>
# fix conflicts
git commit
git push
forktrust rm <slug>   # cleanup if you want
```

**Agent:** STOP. Do not auto-resolve. Surface the conflict to the user, list the conflicting files (`git -C <main-repo> diff --name-only --diff-filter=U`), and ask before doing anything else. Never use `--strategy ours/theirs`.

---

### `3` — main worktree is dirty

`finish` saw uncommitted changes in the main checkout and refused to merge into a dirty tree (would risk overwriting user work).

**User:** commit or stash the main checkout, then re-run `forktrust finish`.

**Agent:** tell the user, including which files are dirty (`git -C <main-repo> status --short`). Do not commit or stash on their behalf without asking.

---

### `4` — push to origin failed

`git push origin main` (after merge) or `git pull --ff-only origin main` (before merge) failed. Common causes: auth, network, non-fast-forward (someone pushed concurrently).

**User:** check origin reachable, auth ok. The main branch IS already merged locally — re-running `forktrust finish` will pick up where it left off (idempotent at this point because the worktree is still there).

**Agent:** retry once. If it fails again, surface to user with the exact `git push` command to inspect.

---

### `5` — wip/* snapshot push failed

`rm` tried to push the WIP snapshot to `wip/<branch>-<stamp>-<sha7>` on origin and the push failed. **The worktree was NOT removed** — work is still there, and you can investigate or retry.

**User:** the error message includes the exact reproduction:
```
cd <worktree> && git push origin HEAD:refs/heads/wip/<branch>-<stamp>-<sha7>
```
Once that succeeds, re-run `forktrust rm <slug>`.

If the WIP is junk, use `forktrust rm <slug> --force` (drops the snapshot).

**Agent:** retry once. If still failing, surface to user. Never use `--force` to "make it work" — that's exactly the data-loss path the guard exists to prevent.

---

### `6` — no worktree matching slug

The slug doesn't match any worktree under any registered project's `.forktrust/worktrees/`.

**User:** check the slug spelling. `forktrust list` shows everything.

**Agent:** suggest `forktrust list`. Don't auto-create.

---

### `7` — slug matches worktrees in multiple projects

The slug resolves in more than one registered project. Disambiguate with `--project <name>`.

**User:** add `-p <project-name>`. Or rename the slug in one of the projects.

**Agent:** re-run with `--project`. Use `forktrust list --json` to figure out which project to target.

---

### `8` — hook failed (or untrusted command hook)

Two distinct causes:
- A `[[hooks.post_create]]` hook returned non-zero (e.g. `pnpm install` failed, copy refused on protected path).
- A `command` hook was declared but the `.forktrustconfig` is not trusted (or trust was auto-revoked because the file changed).

**User:** look at the stderr — it names the hook (`hook 0 (command)`) and the underlying error. If it's trust:
```bash
forktrust trust              # pin current SHA
```
If it's a real failure, fix and retry.

**Agent:** read the error. For trust issues, ask the user before running `forktrust trust` (because trust grants shell execution). For real failures, surface to user.

---

### `9` — no origin remote configured

`rm` had work to snapshot but the repo has no `origin` remote, so there's nowhere to push the wip/* branch.

**User:** add an `origin` remote, or pass `--force` to drop the snapshot:
```bash
forktrust rm <slug> --force
```

**Agent:** ask user about adding remote. Never use `--force` unilaterally.

---

### `10` — main checkout on the wrong branch

`finish` checked the main checkout's current branch and it isn't `mainBranch`. Merging now would land your work on the wrong branch and lie about success.

**User:**
```bash
cd <main-repo>
git checkout main   # or whatever your mainBranch is
forktrust finish <slug>
```

**Agent:** surface to user. The error message tells them which branch is checked out vs expected. Never `git checkout` on their behalf — they may have unstaged work tied to that branch.

---

### `11` — cwd is in an unregistered git repo

A command that needs a project context was run inside a git repo that isn't registered with forktrust.

**User:**
```bash
forktrust config add .
# or pass --project for an already-registered one
```

**Agent:** register the repo only after confirming with the user (it's an explicit consent step).

---

### `12` — could not determine ahead count

`rm` or `finish` tried to count commits ahead of the cascade target (`origin/<mainBranch>` then local `<mainBranch>`) and **neither resolved**. Failing closed: refuse rather than potentially lose unpushed work.

**User:** either push the mainBranch to origin (so `origin/main` exists), create a local main branch, or use `--force` to bypass (for `rm` only). The error message includes the exact list of refs it tried.

**Agent:** never use `--force` to silence this. The whole point is "we can't tell what's safe." Ask user.

---

### `13` — branch -D failed after merge/cleanup

`finish` (or `rm`) succeeded in everything important: the merge/wip-push went through, the worktree was removed, the port block was released. But `git branch -D fork/<slug>` failed (typically because the branch is checked out in another worktree somewhere).

The branch lingers. Your work is safe — main is merged, or wip/* is pushed.

**User:**
```bash
git -C <main-repo> branch | grep <slug>
# find what's holding it, then:
git -C <main-repo> worktree list
git -C <main-repo> worktree remove <holder-path>
git -C <main-repo> branch -D <branch>
```

**Agent:** surface to user. Mention the worktree IS gone and the merge/wip DID happen — only the branch reference lingers.

---

### `16` — scope contract violated

`finish` (or `forktrust scope <slug> --check`) computed the diff vs the cascade target and found one or more changed files that do NOT match any glob in the worktree's declared scope (`<repo>/.forktrust/scopes/<slug>.toml`).

**No git mutation happened.** Pre-flight refusal — no WIP commit, no merge, no push.

**JSON consumers:** `scope_passed: false`, `scope_violation_count: N`, `scope_violations: [...]` (truncated to first 100; full count in `scope_violation_count`), `scope_allowed: [...]` (the declared globs).

**User:** three options:
1. **Revert the out-of-scope changes** (the most honest path): `cd <worktree> && git checkout HEAD -- <file>` for each.
2. **Widen the scope** if the change is genuinely needed: `forktrust scope <slug> --set "internal/auth/**, the/new/path/**"`.
3. **Bypass after manual review**: `forktrust finish <slug> --no-scope` (prints stderr warning, sets `no_scope: true` in JSON).

**Agent:** STOP. Surface the file list to the user. NEVER `--no-scope` without explicit user consent — the entire point of the gate is to catch scope creep.

---

### `15` — `[verify]` gate failed

`finish` ran the `[verify]` commands declared in `.forktrustconfig` and either:
- one of them exited non-zero (e.g. `go test` failed), or
- `require_clean = true` is set and the worktree is dirty after verify ran (a verify command wrote files that aren't in `.gitignore`).

**No git mutation happened.** This is a pre-flight refusal — no WIP commit, no merge, no push. The worktree is intact, exactly as before `finish` started.

**JSON consumers:** the failure is fully described in `verify_failed_command`, `verify_ran_commands`, and `verify_output` (the tail of the failing command's stdout+stderr, capped at 8 KiB).

**User:** the stderr names the failing command and reason. Fix the underlying problem (failing test, build error, leftover artifact), then re-run `forktrust finish <slug>`.

If you've already verified manually and just want to ship: `forktrust finish <slug> --no-verify` (prints a stderr warning, sets `no_verify: true` in JSON).

**Agent:** STOP. Surface the failure to the user, including the `verify_failed_command` and the last few lines of `verify_output`. Do NOT use `--no-verify` — the gate exists to prevent shipping broken code; only the user can decide it's safe.

---

### `14` — worktree has ignored files that would be lost

`rm` or `finish` detected ignored files (files matched by `.gitignore` but not actually tracked) in the worktree. `git worktree remove` would silently delete them.

**Exclusions from this check** (these are treated as safe to delete):
- the root-level `.env.local` IFF it is a regular file (not symlink) and starts with the exact `ManagedHeader` line written by forktrust.

Anything else triggers exit 14.

**User:** options:
1. Move the files out of the worktree first.
2. List what would be lost: `git -C <worktree> ls-files --others --ignored --exclude-standard`.
3. If you're sure they're disposable, pass `--force` to skip the guard.

**Agent:** surface to user with the file list. NEVER `--force` without asking — this guard exists because git's silent-delete on `worktree remove` has lost real secrets.

---

## Decision flow for an AI agent

```
exit code
├── 0      → proceed
├── 2      → STOP, conflict; ask user
├── 3      → tell user to clean main
├── 4      → retry once; if still failing, surface
├── 5      → check origin auth; retry rm
├── 6      → check slug; suggest forktrust list
├── 7      → re-run with --project
├── 8      → read message; surface (trust ⇒ ask before forktrust trust)
├── 9      → ask user about adding remote
├── 10     → tell user to git checkout mainBranch
├── 11     → ask user about forktrust config add .
├── 12     → ask user; never --force
├── 13     → tell user branch lingers; work is safe
├── 14     → list ignored files; ask user; never --force
├── 15     → surface verify_failed_command + tail of verify_output; ask user; never --no-verify
├── 16     → surface scope_violations list to user; ask user; never --no-scope
└── other  → surface raw error
```

When you have JSON output, prefer reading `would_refuse` over guessing from the exit code — the string includes both the reason and the `(exit N)` suffix.
