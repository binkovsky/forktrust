# Shell integration

forktrust is a CLI. With three small additions to your shell config, it feels like a native part of your workflow.

## 1. `ft` — cd to any worktree

Drop into `~/.zshrc` or `~/.bashrc`:

```bash
ft() {
  local p
  p="$(forktrust cd "$1" 2>/dev/null)" || { echo "forktrust: no worktree '$1'" >&2; return 1; }
  cd "$p" || return 1
}
```

After `source ~/.zshrc`:

```bash
ft fix-payment
# now you're in .forktrust/worktrees/fix-payment
```

How it works:
- `forktrust cd <slug>` prints ONLY the absolute path (no decoration).
- The function captures it and runs `cd` in the current shell (which is why it has to be a function, not a script).
- Errors from forktrust (exit 6/7) are caught and re-stated in the function's own format.

Fish equivalent (`~/.config/fish/functions/ft.fish`):

```fish
function ft
    set -l p (forktrust cd $argv[1] 2>/dev/null)
    if test $status -ne 0
        echo "forktrust: no worktree '$argv[1]'" >&2
        return 1
    end
    cd $p
end
```

## 2. `forktrust shell` — subshell inside a worktree

Sometimes you want a temporary subshell rather than changing your current shell's cwd:

```bash
forktrust shell fix-payment
# you're now in a subshell with cwd at the worktree
# when you exit, you're back where you started
```

The subshell inherits your environment plus:
- `FORKTRUST_SLUG=fix-payment` — useful for prompt customization

Default shell is `$SHELL`. If unset, `/bin/sh`.

## 3. Prompt customization

Detect the active worktree in your prompt by checking `FORKTRUST_SLUG`. For zsh:

```bash
# in ~/.zshrc, after your prompt setup
forktrust_prompt() {
    [ -n "$FORKTRUST_SLUG" ] && echo " (ft:$FORKTRUST_SLUG)"
}

# add $(forktrust_prompt) to your PS1 / PROMPT
PROMPT='%n@%m %~$(forktrust_prompt) $ '
```

For bash:

```bash
forktrust_ps1() {
    [ -n "$FORKTRUST_SLUG" ] && echo " (ft:$FORKTRUST_SLUG)"
}
PS1='\u@\h \w$(forktrust_ps1) $ '
```

For starship (`~/.config/starship.toml`):

```toml
[env_var.FORKTRUST_SLUG]
format = "[(ft:$env_value)](bold yellow) "
```

## 4. Tab completion

forktrust uses cobra, which can generate completions:

### zsh

```bash
# One-time setup
forktrust completion zsh > "${fpath[1]}/_forktrust"
# restart shell or:
autoload -U compinit && compinit
```

Now `forktrust f<TAB>` expands to commands, `forktrust finish <TAB>` lists slugs (in newer versions).

### bash

```bash
forktrust completion bash > /etc/bash_completion.d/forktrust   # may need sudo
# or for user-local:
forktrust completion bash > ~/.local/share/bash-completion/completions/forktrust
```

### fish

```bash
forktrust completion fish > ~/.config/fish/completions/forktrust.fish
```

## 5. fzf integration (optional)

If you have `fzf`, an interactive worktree picker:

```bash
fts() {
    local slug
    slug=$(forktrust list --json | jq -r '.worktrees[] | select(.is_main == false) | "\(.project)/\(.path | split("/") | .[-1])"' | fzf) || return 1
    local picked_slug="${slug##*/}"
    local picked_project="${slug%%/*}"
    cd "$(forktrust cd "$picked_slug" -p "$picked_project")" || return 1
}
```

Now `fts` is a fuzzy picker across all worktrees in all repos.

## 6. Aliases

Compact aliases for the most-used commands:

```bash
alias fn='forktrust new'
alias fl='forktrust list'
alias fs='forktrust status'
alias fst='forktrust status --watch'
alias ff='forktrust finish'
alias frm='forktrust rm'
alias fd='forktrust doctor'
```

Then:
```bash
fn fix-bug                    # forktrust new
ft fix-bug                    # cd (from the ft() function)
ff fix-bug --dry-run --json   # finish preview
ff fix-bug                    # finish for real
```

## 7. Per-project hooks

If you want to run code automatically when you `cd` into a worktree (e.g. activate a virtualenv, load env vars), use `direnv` with `.envrc` in the worktree:

```bash
# .forktrust/worktrees/fix-bug/.envrc — gitignored
dotenv .env.local
layout python
```

Or copy a template `.envrc` via a hook:

```toml
[[hooks.post_create]]
type = "copy"
from = ".envrc.template"
to   = ".envrc"
```

`direnv allow` once per worktree to enable.

## 8. tmux / window manager integration

Each worktree is a real directory — open it in a new tmux window:

```bash
# In your tmux config or ~/.bin/ft-window
ft_window() {
    local slug=$1
    local path=$(forktrust cd "$slug") || return 1
    tmux new-window -c "$path" -n "$slug"
}
```

Now `ft_window fix-payment` opens a new tmux window already in the worktree.

## Summary

The minimum set, in order of value:

1. **`ft` function** (10 lines) — essential. Adds `ft <slug>` to your daily flow.
2. **Tab completion** — autocomplete commands and slugs.
3. **Prompt** — visible reminder of which worktree you're in.
4. **fzf picker** — for when you have many worktrees.

Everything else is optional.
