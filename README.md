# mcp-scope

**tcpdump for MCP.** A transparent JSON-RPC capture, viewer, and diff tool for the Model Context Protocol.

> Status: pre-alpha. README-driven design — published to test the pitch before writing the code. Star or open an issue if you'd use this.

---

## What it does

`mcp-scope` sits between any MCP client (Claude Desktop, Cursor, Copilot, your own script) and any MCP server, silently records every JSON-RPC frame, and gives you the tools to inspect, summarise, and diff what happened.

You don't change your client. You don't change your server. You prepend `mcp-scope capture --` and keep going.

```
┌──────────────┐    JSON-RPC    ┌────────────┐    JSON-RPC    ┌──────────────┐
│  MCP client  │ ◄────────────► │ mcp-scope  │ ◄────────────► │  MCP server  │
└──────────────┘                └─────┬──────┘                └──────────────┘
                                      │
                                      ▼
                                session.jsonl
```

## Why it exists

The MCP debugging ecosystem is full of *interactive* tools — TUIs and web UIs you drive yourself to call tools and watch responses. None of them help when the bug only reproduces inside Claude Desktop, Cursor, or your CI pipeline. You need to see what *actually* flowed on the wire during a real session.

`mcp-scope` is passive. It captures real traffic from real clients. Then you analyse it offline.

## Install

```sh
# planned
brew install mcp-scope
go install github.com/<org>/mcp-scope@latest
```

Single static Go binary. No Node, no Python, no browser, no localhost port.

## Quick start

### Capture a session

Wrap a stdio server:
```sh
mcp-scope capture --upstream "node build/index.js" -o session.jsonl
```

Wrap an HTTP/SSE server:
```sh
mcp-scope capture --upstream https://my-server.internal/mcp --listen :8080 -o session.jsonl
# then point your client at http://localhost:8080
```

Every JSON-RPC frame in either direction is timestamped and written to `session.jsonl`.

### View a capture

```sh
mcp-scope view session.jsonl
```

Scrolling TUI. Filter by method, success/error, time range. Expand any frame to see the raw JSON-RPC.

### Get a summary

```sh
mcp-scope stats session.jsonl
```

```
METHOD                    COUNT   ERR%   p50      p95      p99
initialize                1       0%     12ms     12ms     12ms
tools/list                3       0%     8ms      14ms     14ms
tools/call:read_file      47      0%     22ms     89ms     210ms
tools/call:write_file     12      8%     45ms     180ms    340ms
notifications/cancelled   2       —      —        —        —
```

### Diff two captures (v0.2)

```sh
mcp-scope diff baseline.jsonl candidate.jsonl
```

Classifies every change between the two servers' declared capabilities:

```
BREAKING  tools/call:write_file  required param "mode" added
BREAKING  tools/call:delete_file removed
SAFE      tools/call:read_file   optional param "encoding" added
SAFE      tools/call:list_dir    new tool
INFO      tools/call:read_file   description changed
```

Exit code `1` if any breaking change is found — drop into CI to gate server upgrades:

```yaml
- run: mcp-scope diff baseline.jsonl <(mcp-scope capture-once --upstream ./server)
```

## How it compares

| Tool | Category | What it does |
|------|----------|--------------|
| **mcp-scope** | Passive proxy | Records real traffic from real clients. Diffs captures. |
| MCP Inspector (official) | Web debugger | You drive it to call tools. Browser-based. |
| mcp-tui | TUI debugger | You drive it interactively in the terminal. |
| par-mcp-inspector-tui | TUI debugger | You drive it; rich JSON-RPC pane. |
| mcp-probe | TUI debugger + CI | You drive it; built-in record/replay inside its own session. |
| mcptools (`f/mcptools`) | CLI client | One-shot tool calls from the shell. |

Every other tool in the ecosystem is *active* — you sit at the keyboard and drive it. `mcp-scope` is the only one that watches an existing client/server pair without disturbing either side.

## Roadmap

**MVP**
- [ ] `capture` — stdio + SSE + streamable HTTP proxy
- [ ] `view` — scrolling TUI viewer
- [ ] `stats` — per-method latency and error summary

**v0.2**
- [ ] `diff` — schema diff between captures, breaking-change classifier, CI exit codes

**v0.3+ (only if pulled)**
- [ ] `replay` — fire recorded calls at a different server for regression testing
- [ ] `check` — explicit baseline contract gate

## Explicitly not building

- An interactive debugger to drive tool calls (mcp-tui, par's, mcp-probe already do this)
- A web UI (official Inspector does this)
- LLM-in-the-loop testing (MCPJam does this)
- A YAML test DSL (the diff exit code is the contract gate)
- Schema-aware input forms (no input forms — this is a passive tool)

## Contributing

Pre-alpha. Open an issue describing your use case before sending a PR — the scope is deliberately narrow and the answer to most "could it also do X" questions is no.

## License

MIT (planned).
