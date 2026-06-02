# Example: Debugging a Gemini CLI MCP integration

**The scenario:** You've wired an MCP server into [Gemini CLI](https://github.com/google-gemini/gemini-cli)
via `settings.json`. Tool calls return wrong results or fail silently. Gemini CLI gives you no debug
output. You need to see what actually passed between Gemini CLI and your server.

**What this example shows:**
- Wrapping a stdio MCP server so Gemini CLI's traffic is captured
- Finding the exact frame where Gemini CLI sends unexpected parameters
- Using `view --follow` to watch a live session in real time

---

## How Gemini CLI connects to MCP servers

Gemini CLI reads `~/.gemini/settings.json` (or `$GEMINI_CONFIG_DIR/settings.json`).
Each MCP server entry specifies a command Gemini CLI runs as a subprocess over stdio:

```json
{
  "mcpServers": {
    "my-server": {
      "command": "node",
      "args": ["build/index.js"]
    }
  }
}
```

Gemini CLI manages the subprocess. You never see its stdout/stderr directly.
This is exactly the problem mcp-scope solves.

---

## Setup

### 1. Find your current MCP server command

Check `~/.gemini/settings.json`. Note the `command` and `args` for your server.

Example:
```json
"my-server": {
  "command": "node",
  "args": ["/home/user/my-mcp-server/build/index.js"]
}
```

### 2. Wrap the server command with mcp-scope

Replace the command in `settings.json` so mcp-scope sits between Gemini CLI and your server:

```json
"my-server": {
  "command": "mcp-scope",
  "args": [
    "capture",
    "-o", "/tmp/gemini-session.jsonl",
    "--",
    "node", "/home/user/my-mcp-server/build/index.js"
  ]
}
```

`mcp-scope capture --` transparently proxies stdio. Gemini CLI sees no difference.

### 3. Use Gemini CLI normally

```bash
gemini
> use my-server to list the available resources
> call the search tool with query "hello world"
```

Every JSON-RPC frame is written to `/tmp/gemini-session.jsonl` in real time.

### 4. Watch live (optional)

In a second terminal, while the session is active:

```bash
mcp-scope view --follow /tmp/gemini-session.jsonl
```

Output streams as Gemini CLI makes calls:

```
→ req  initialize id=1
← resp id=1
→ req  tools/list id=2
← resp id=2
→ req  tools/call id=3   ← watch what Gemini CLI actually sends
← resp id=3
```

### 5. Analyse after the session

```bash
# See all frames with full payloads
mcp-scope view -v /tmp/gemini-session.jsonl

# Find all tool calls that returned errors
mcp-scope view --kind err /tmp/gemini-session.jsonl

# Find the exact parameters Gemini CLI sent for a failing tool call
mcp-scope view --method tools/call --kind req -v /tmp/gemini-session.jsonl

# Check for protocol violations
mcp-scope check --strict /tmp/gemini-session.jsonl

# Latency breakdown
mcp-scope stats /tmp/gemini-session.jsonl
```

---

## Common findings

### "Gemini CLI sends different parameters than I expect"

```bash
mcp-scope view --method tools/call -v /tmp/gemini-session.jsonl
```

Look at the `arguments` field in each `tools/call` request. Compare against what your server
expects. Often the model sends camelCase keys when your server requires snake_case, or omits
optional parameters your server treats as required.

### "Gemini CLI makes repeated calls — is it retrying?"

```bash
mcp-scope stats /tmp/gemini-session.jsonl
```

High call counts on a single method with a non-zero error count usually means the client
is retrying on error. The `p99` latency tells you whether the server is timing out.

### "Gemini CLI worked yesterday, broke after I deployed a new server version"

Capture a session with the old version (`baseline.jsonl`), deploy the new version, capture
another session (`candidate.jsonl`), then:

```bash
mcp-scope diff baseline.jsonl candidate.jsonl
```

Any `BREAKING` line is the root cause.

---

## Restore the original config

After debugging, restore `settings.json` to the original command. Or keep the wrapper — the
overhead is negligible and having the capture available is useful.
