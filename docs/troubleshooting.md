# Troubleshooting

Every error you might see, with a fix. Indexed by exit code and by stderr text.

If your problem isn't here, run `forktrust doctor` first — it diagnoses the most common configurations.

## By exit code

For the full catalog see [exit-codes.md](./exit-codes.md). Quick fixes:

| Code | Quick fix |
|---|---|
| 2 | `cd <main-repo> && git merge fork/<slug>`, resolve manually |
| 3 | `cd <main-repo> && git stash push -u`, retry, `git stash pop` |
| 4 | Check origin auth (`git push -n origin main`), retry |
| 5 | Check origin auth, retry `forktrust rm <slug>` |
| 6 | Check spelling with `forktrust list` |
| 7 | Add `--project <name>` |
| 8 | `forktrust trust` (if message mentions trust); else fix the underlying hook |
| 9 | Add origin remote, or `--force` (loses snapshot) |
| 10 | `cd <main-repo> && git checkout main` |
| 11 | `forktrust config add .` |
| 12 | `git push -u origin main`, or create local `main` |
| 13 | `git worktree list`; find what's checked out, then `git branch -D` manually |
| 14 | Move ignored files out, or `--force` (rm only) |
| 15 | Fix the failing verify command, or `--no-verify` on `finish` (with explicit consent) |

## By stderr message

### `REFUSE: merge of fork/<slug> into main produced conflicts. Aborted to leave main clean.`

`finish` exit 2. Main is in its pre-merge state. Do the merge by hand:

```bash
cd <main-repo>
git merge fork/<slug>
# fix conflicts in your editor
git add .
git commit
git push
forktrust rm <slug>           # optional cleanup
```

### `REFUSE: main working tree in /path has N uncommitted change(s).`

`finish` exit 3. Stash or commit main's WIP first.

### `REFUSE: main checkout at /path is on branch "X", expected "main".`

`finish` exit 10. Switch main back:
```bash
cd <main-repo> && git checkout main
forktrust finish <slug>
```

### `REFUSE: push of WIP snapshot to origin/wip/* failed.`

`rm` exit 5. Reproduce the push to see the real git error:
```bash
cd <worktree> && git push origin HEAD:refs/heads/wip/<branch>-<stamp>-<sha7>
```

Then re-run `forktrust rm <slug>`. **DO NOT use `--force`** — that drops the WIP.

### `REFUSE: [verify] gate failed.`

`finish` exit 15. Stderr lists the failing command and reason. Real test output was already streamed live above the REFUSE line. JSON has `verify_failed_command` and a tail of `verify_output`.

Fix:
1. Run the failing command yourself in the worktree:
   ```bash
   forktrust exec <slug> -- <failing-command>
   ```
2. Fix what it complains about.
3. Re-run `forktrust finish <slug>`.

Bypass (only when you have verified manually):
```bash
forktrust finish <slug> --no-verify
```

`require_clean` failure (`verify_failed_command: "(require_clean)"`) means a verify command wrote files that weren't in `.gitignore`. Either `.gitignore` them or fix the command not to create them.

### `REFUSE: worktree has N ignored file(s)`

Exit 14. List them:
```bash
git -C <worktree> ls-files --others --ignored --exclude-standard
```

Move them, or `--force` (`rm` only).

### `REFUSE: could not determine if branch has unpushed work.`

`rm` exit 12. Cascade failed: neither `origin/<main>` nor local `<main>` exists. Fix one:
```bash
git push -u origin main          # push origin/main first
# OR create local main if you really have no origin
```

Then retry. `--force` works but skips the never-lose-WIP guarantee.

### `REFUSE: worktree at /path is detached (no branch).`

`rm` exit 1. forktrust doesn't manage detached worktrees. Clean up manually:
```bash
git -C <main-repo> worktree remove <worktree-path>
```

### `error: hook 0 (command) failed: ...`

A `[[hooks.post_create]] type = "command"` returned non-zero. Stderr contains the full output. Fix the command (e.g. install the missing tool, fix the script). Re-run `forktrust new`.

### `error: hook 0 (command) ... .forktrustconfig is not trusted`

Exit 8 — trust gate. The config has `command` hooks but trust is not pinned (or was auto-revoked because the SHA changed). Pin:
```bash
forktrust trust
```

Review the diff first:
```bash
git -C <main-repo> diff HEAD~ .forktrustconfig
```

### `error: copy to "X" refused: destination .git is protected`

A `[[hooks.post_create]] type = "copy"` (or symlink) tried to write into `.git`. Change the `to` path. forktrust will never let a hook write into `.git/`.

### `error: symlink to "X" refused: destination is an existing regular file`

A `symlink` hook would have replaced a tracked file with a symlink, which would silently route writes back into the main checkout. Either use a `copy` hook, or delete the file from the worktree first.

### `error: ancestor /X is a symlink (would escape via link target)`

A prior `symlink` hook planted a directory-symlink whose target is outside the worktree. A later hook tried to write under it. forktrust refuses to follow into the link's target.

Fix: remove the offending symlink hook, or change the `to` path so it doesn't traverse a symlinked ancestor.

### `error: invalid .forktrustconfig: ...`

The TOML failed validation. The hint after the colon is specific:
- `[ports].vars[i] = "X": must match ^[A-Za-z_][A-Za-z0-9_]*$` — invalid env var name.
- `hook N: unknown type "X"` — must be `copy`, `symlink`, or `command`.
- `hook N (copy): from and to are required`.

Fix and retry.

### `error: path "/X" resolves to the same directory as already-registered project "Y"`

`forktrust config add` refuses to register the same canonical directory twice (this previously broke `resolveWorktree`). To switch the name:
```bash
forktrust config remove <old-name>
forktrust config add /X <new-name>
```

## Common situations

### "My .env.local got deleted by forktrust rm"

This should be impossible as of v0.6.4. If it happened, file an issue with:
- forktrust version (`forktrust --version`)
- first line of the `.env.local` (was it the exact `ManagedHeader`?)
- was it a symlink? (`ls -la .env.local`)

The current rule: `.env.local` is treated as forktrust-managed ONLY IF it (a) is a regular file at the worktree root, (b) starts with the exact `ManagedHeader`. Anything else is treated as user data and triggers exit 14 unless `--force`.

### "forktrust new is slow"

Possible causes:
- A `[[hooks.post_create]] type = "command"` hook (e.g. `pnpm install`). Use `symlink` for `node_modules` instead.
- `[ports]` allocator running an orphan-prune sweep across many stale entries. One-time fix: `forktrust list` to see and `forktrust rm` orphans, then it stays fast.
- Network: `git fetch -q origin <mainBranch>` happens once at the start of `finish`. If origin is slow, this dominates. Doesn't affect `new`.

### "Two agents allocated the same port"

Shouldn't happen — the allocator uses `flock` and writes the store atomically. If you see overlapping `PORT=` values in two different `.env.local` files, file an issue. Check `~/Library/Application Support/forktrust/ports.json` to see the recorded blocks.

### "I can't `git push` from the worktree"

Same auth as the main checkout (worktrees share `.git`). If `git push` fails in the worktree but works from main, check what's different (env vars, agent forwarding).

### "forktrust finish hangs"

Usually the underlying `git push` hangs (auth prompt, slow origin). Cancel with Ctrl-C; the worktree state is preserved (`finish` has already committed the WIP and merged locally, just not pushed). Resume:

```bash
cd <main-repo>
git push origin main
forktrust rm <slug>     # clean up the worktree (which is now 0 ahead)
```

### "I want to undo a `forktrust finish`"

There's no `forktrust undo` yet (planned for v0.8.0). Manual procedure:

```bash
cd <main-repo>
git reset --hard HEAD~1                          # undo the local merge
git push --force-with-lease origin main          # ONLY if you're sure no one else pushed
```

The worktree is gone, the branch is gone, the port block is freed. To re-attempt: `forktrust new <new-slug> --from <branch-commit-sha>` if you remember the SHA. Otherwise the work is in the merge commit you just reset away from — check `git reflog`.

## Getting help

- `forktrust <command> --help` — full flag list for each command.
- `forktrust doctor` — health check.
- `forktrust agent-docs` — version-pinned AI-integration snippet.
- [GitHub issues](https://github.com/binkovsky/forktrust/issues) — bugs, feature requests.
