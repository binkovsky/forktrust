# MCP server (`forktrust mcp`)

Run forktrust as a Model Context Protocol server. AI agents that speak MCP (Claude Code, Cursor, etc.) call forktrust operations as native typed tools instead of shelling out and parsing CLI output themselves.

Shipped in v0.7.6.

## Quick start

Add to Claude Code's `settings.json`:

```json
{
  "mcpServers": {
    "forktrust": {
      "command": "forktrust",
      "args": ["mcp"]
    }
  }
}
```

Or to Cursor's `mcp.json`, or any other MCP-compliant client.

After restart, the model sees 13 typed tools (`forktrust_list`, `forktrust_new`, `forktrust_init`, etc.) and calls them directly. No more `Bash("forktrust list --json | jq ...")` boilerplate.

## What it does

`forktrust mcp` is a stdio server that:
- Reads newline-delimited JSON-RPC 2.0 requests from stdin.
- Writes newline-delimited JSON-RPC 2.0 responses to stdout.
- Speaks the [MCP 2024-11-05](https://spec.modelcontextprotocol.io/specification/2024-11-05/) protocol (initialize, tools/list, tools/call).
- Exposes 13 tools, each a thin wrapper around the corresponding `forktrust <cmd> --json`.

It exits cleanly when stdin EOFs (i.e. when the client disconnects) or when it receives SIGINT/SIGTERM.

## Tools exposed

| Tool | Wraps | Required args | Optional args |
|---|---|---|---|
| `forktrust_list` | `list --json` | — | — |
| `forktrust_status` | `status --json` | — | `project` |
| `forktrust_new` | `new --json` | `slug` | `project`, `scope`, `from` |
| `forktrust_cd` | `cd` | `slug` | `project` |
| `forktrust_finish` | `finish --json` | `slug` | `project`, `message`, `dry_run`, `no_verify`, `no_scope`, `no_summary` |
| `forktrust_rm` | `rm --json` | `slug` | `project`, `force`, `dry_run` |
| `forktrust_scope` | `scope --json` | `slug` | `project`, `set`, `clear`, `check` (mutually exclusive) |
| `forktrust_summary` | `summary --json` | `slug` | `project`, `check` |
| `forktrust_pr` | `pr --json` | `slug` | `project`, `title`, `body`, `base`, `draft`, `no_verify`, `no_scope`, `no_summary`, `dry_run` |
| `forktrust_pr_status` | `pr-status --json` | `slug` | `project` |
| `forktrust_init` | `init --json` | — | `template`, `force`, `show` |
| `forktrust_template_list` | `template list --json` | — | — |
| `forktrust_doctor` | `doctor --json` | — | `project` |

Each tool returns a single `text` content block whose body is the forktrust JSON envelope. Tool errors (non-zero exit) set `isError: true` so the model sees the failure prominently; the JSON envelope is still parseable for programmatic consumers.

## Why use MCP over shell?

Compared with the shell approach (model runs `Bash("forktrust list --json")` and parses):

| | Bash | MCP |
|---|---|---|
| Tool discovery | model must remember commands | `tools/list` enumerates all |
| Argument schemas | model improvises | client validates against JSON Schema |
| Error surfacing | parse stderr text | structured `isError` + content |
| Quoting / escaping | model must escape every arg | client handles JSON encoding |
| Network round-trips | one process per call (slow) | one persistent process |
| Cross-session state | reset per invocation | persistent — single binary |

For Claude Code specifically, MCP tools appear in the IDE picker with descriptions and schemas. Models call them by name with typed arguments instead of crafting shell commands.

## Wire format

Stdio: each message is one line of JSON, terminated by `\n`. The frame size is bounded at 16 MiB per line.

Standard JSON-RPC 2.0 codes:
- `-32700` parse error (malformed JSON in request)
- `-32600` invalid request (e.g. wrong `jsonrpc` version)
- `-32601` method not found
- `-32602` invalid params (e.g. unknown tool name, missing `name`)
- `-32603` internal error

Tool-level errors do NOT become JSON-RPC errors. Instead `tools/call` returns `{"result": {"content": [...], "isError": true}}` — JSON-RPC errors are reserved for protocol-level failures.

### Example session

```
> {"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","clientInfo":{"name":"my-client","version":"1.0"},"capabilities":{}}}
< {"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{"listChanged":false}},"serverInfo":{"name":"forktrust-mcp","version":"0.7.6"}}}

> {"jsonrpc":"2.0","method":"notifications/initialized"}
(no response — notification)

> {"jsonrpc":"2.0","id":2,"method":"tools/list"}
< {"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"forktrust_list","description":"List all forktrust worktrees ...","inputSchema":{...}}, ...]}}

> {"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"forktrust_new","arguments":{"slug":"fix-auth","scope":"internal/auth/**"}}}
< {"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"{\"project\":\"...\", \"slug\":\"fix-auth\", ...}"}]}}
```

## Client integration recipes

### Claude Code

`~/.config/claude-code/settings.json` (or via `claude code config`):

```json
{
  "mcpServers": {
    "forktrust": {
      "command": "forktrust",
      "args": ["mcp"]
    }
  }
}
```

Then in a session, the model can use `forktrust_new`, `forktrust_finish`, etc. with typed arguments. The Claude Code UI shows the tool list with descriptions.

### Cursor

Cursor's MCP support is in `~/.cursor/mcp.json`:

```json
{
  "mcpServers": {
    "forktrust": {
      "command": "forktrust",
      "args": ["mcp"]
    }
  }
}
```

### Any MCP-compatible agent framework

The launch command is always the same: `forktrust mcp`. The server discovers its own binary via `os.Executable()`, so it self-invokes correctly for each tool call even when launched from a non-standard path.

### Direct stdio testing

You can drive the server by hand:

```bash
( echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","clientInfo":{"name":"manual","version":"1"},"capabilities":{}}}'
  echo '{"jsonrpc":"2.0","method":"notifications/initialized"}'
  echo '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
) | forktrust mcp | jq -c .
```

Useful for debugging tool schemas or seeing what the model sees.

## Safety preservation

Every safety guarantee documented in [docs/safety-model.md](./safety-model.md) applies to MCP tool calls as well, because they invoke the same `forktrust <cmd> --json` subprocess:

- **Pre-flight refusal**: `forktrust_finish` runs verify + scope gates BEFORE any git mutation, same as the CLI.
- **Never-lose-WIP**: `forktrust_rm` pushes to `wip/*` before removing.
- **Refuse-on-conflict**: `forktrust_finish` aborts the merge on conflict (exit 2 surfaces in the JSON envelope).
- **Refuse-on-ignored-loss / scope / verify**: all exit 14/16/15 paths produce JSON envelopes the model can parse.
- **Bypass flags** (`--no-verify`, `--no-scope`, `--force`) are exposed as named arguments (`no_verify`, `no_scope`, `force`) so the model has to explicitly pass them — never silently bypassed by argument-string mangling.

Agent integration guides ([docs/ai-integration.md](./ai-integration.md)) MUST tell the model not to set `no_verify`, `no_scope`, or `force` without explicit user consent. The MCP layer cannot enforce intent — only mechanics.

## Comparison with shell-based integration

For projects already using `forktrust agent-docs` snippets (shell-based), the MCP server is purely additive. You can run both at once:

```json
{
  "mcpServers": {
    "forktrust": {"command": "forktrust", "args": ["mcp"]}
  }
}
```
…and still have `forktrust agent-docs >> AGENTS.md` for the human-readable safety/exit-code documentation. The model reads the AGENTS.md text and calls the MCP tools — they're complementary, not redundant.

## Limitations

- **No streaming output**: each `tools/call` blocks until forktrust exits, then returns the full content. For `forktrust finish` with a slow `[verify]` gate this can be minutes. Clients that want progress should poll `pr_status` or `status` separately.
- **No subscription / change notification**: `tools/listChanged` is `false`; the tool set is static per binary version.
- **Single concurrent call per server**: the server serializes requests by design. If you need parallelism, launch multiple `forktrust mcp` processes — they don't share state beyond the on-disk forktrust config, which is `flock`-protected.
- **No resource/prompt/sampling primitives** (yet): only the `tools/*` MCP capability is implemented. Sampling and resources are not relevant for forktrust's use case.

## Versioning

The MCP server's protocol version (`2024-11-05`) is independent of the forktrust binary's version. Tool names and schemas are **stable across forktrust releases**: existing tool args stay backwards compatible, new tools are added at the end of the list.

If the MCP spec itself bumps and we adopt it, the protocolVersion advertised at initialize will change. Clients should sanity-check the version field.

## Implementation

The MCP server lives at `internal/mcp/` (package mcp). Key files:

- `internal/mcp/protocol.go` — JSON-RPC + MCP type definitions
- `internal/mcp/server.go` — read loop, dispatcher
- `internal/mcp/tools.go` — tool catalog, schemas, handlers (each shells out to the forktrust binary)
- `internal/cli/mcp.go` — cobra command wiring (`forktrust mcp`)

Tests:
- 12 unit tests in `internal/mcp/server_test.go` (initialize, tools/list completeness, unknown method, notifications, parse errors, scope mutex, ID round-trip)
- 7 E2E tests covering live subprocess invocation

## Troubleshooting

**MCP client says "server didn't respond"**: ensure `forktrust` is on PATH for the client's process (not just your interactive shell). Check `forktrust doctor` reports `brew-version: ok`.

**Tools return empty content**: the underlying forktrust command may be writing to stderr only. Check by piping a tool call manually and inspecting `stderr` (you can split via shell redirection).

**Tool says "missing required argument: slug"**: the client sent a `tools/call` without `arguments.slug` (or with `slug: null`). Check the client's argument serialization.

**`forktrust_pr` returns exit 17**: `gh` is not available or not authenticated. Run `gh auth login` and `forktrust doctor` to confirm.
