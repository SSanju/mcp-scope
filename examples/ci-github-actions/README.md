# Example: MCP server CI with protocol validation and breaking-change gates

**The scenario:** You're shipping a new version of your MCP server. You want CI to:
1. Verify the server speaks valid JSON-RPC before the PR merges
2. Block the merge if any tool/resource/prompt schema changed in a breaking way
3. Surface the diff so reviewers can see exactly what changed

**What this example shows:**
- A complete GitHub Actions workflow for MCP server validation
- Recording a baseline capture during CI
- Blocking on `check --strict` errors and `diff` breaking changes

---

## The workflow

Add this to `.github/workflows/mcp-validate.yml` in your MCP server repo:

```yaml
name: MCP server validation

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  validate:
    runs-on: ubuntu-latest

    steps:
      - uses: actions/checkout@v4

      - name: Install mcp-scope
        run: |
          curl -L https://github.com/SSanju/mcp-scope/releases/latest/download/mcp-scope_linux_amd64.tar.gz \
            | tar xz
          sudo mv mcp-scope /usr/local/bin/

      # ── build / start your server here ──────────────────────────────────────
      - name: Build server
        run: npm ci && npm run build   # adjust to your stack

      - name: Start server
        run: node build/index.js &
        # For HTTP servers: add --port 5000 and wait for readiness below

      - name: Wait for server
        run: |
          for i in $(seq 1 10); do
            curl -sf http://127.0.0.1:5000/health && break || sleep 1
          done
      # ── end server setup ─────────────────────────────────────────────────────

      - name: Capture MCP session
        run: |
          mcp-scope capture \
            --upstream http://127.0.0.1:5000/mcp \
            --listen 127.0.0.1:9090 \
            -o candidate.jsonl &
          PROXY_PID=$!

          # Run the initialization sequence + tools/list
          curl -sf -X POST http://127.0.0.1:9090 \
            -H "Content-Type: application/json" \
            -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"ci","version":"1"}}}'

          curl -sf -X POST http://127.0.0.1:9090 \
            -H "Content-Type: application/json" \
            -d '{"jsonrpc":"2.0","method":"notifications/initialized"}'

          curl -sf -X POST http://127.0.0.1:9090 \
            -H "Content-Type: application/json" \
            -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'

          curl -sf -X POST http://127.0.0.1:9090 \
            -H "Content-Type: application/json" \
            -d '{"jsonrpc":"2.0","id":3,"method":"prompts/list","params":{}}'

          curl -sf -X POST http://127.0.0.1:9090 \
            -H "Content-Type: application/json" \
            -d '{"jsonrpc":"2.0","id":4,"method":"resources/list","params":{}}'

          kill $PROXY_PID

      - name: Validate protocol (check --strict)
        run: mcp-scope check --strict candidate.jsonl
        # Exits 1 on any JSON-RPC protocol violation → blocks merge

      - name: Download baseline capture
        uses: dawidd6/action-download-artifact@v3
        with:
          name: mcp-baseline
          if_no_artifact_found: warn   # first run has no baseline yet

      - name: Check for breaking schema changes
        run: |
          if [ -f baseline.jsonl ]; then
            mcp-scope diff baseline.jsonl candidate.jsonl
          else
            echo "No baseline yet — skipping diff (first run)"
          fi
        # Exits 1 if any BREAKING change detected → blocks merge

      - name: Show stats
        run: mcp-scope stats candidate.jsonl
        if: always()   # show even if earlier steps failed

      - name: Save candidate as new baseline (on main only)
        if: github.ref == 'refs/heads/main' && success()
        uses: actions/upload-artifact@v4
        with:
          name: mcp-baseline
          path: candidate.jsonl
          retention-days: 90
```

---

## What each step does

### `mcp-scope check --strict`

Validates every JSON-RPC frame in the capture against the spec:

```
Checking candidate.jsonl ...
  [ERROR] line 12: response for unknown request id=abc123
FAIL — 1 error(s), 0 warning(s)
```

Exit code 1 on any error. In `--strict` mode, warnings also become errors.

This catches:
- Invalid JSON lines
- Responses with ids that don't match any request
- Duplicate request ids
- Malformed frames (missing `method`, `result`, or `error`)

### `mcp-scope diff baseline.jsonl candidate.jsonl`

Schema-level diff against the last known-good capture:

```
BREAKING  tools/call:write_file    required param "encoding" added
BREAKING  tools/call:delete_file   tool removed
SAFE      tools/call:read_file     new optional param "offset"
INFO      tools/call:list_dir      description changed
```

Exit code 1 on any `BREAKING` change. `SAFE` and `INFO` changes pass.

This catches:
- Required parameters added (breaks existing callers)
- Tools or resources removed (breaks existing callers)
- Parameter type changes
- New optional parameters and new tools (safe, always pass)

### `mcp-scope stats`

Printed even on failure for diagnostics:

```
Requests:
  method          count  errors   p50    p95    p99    max
  tools/list          1       0   8ms   8ms    8ms    8ms
  initialize          1       0  12ms  12ms   12ms   12ms
```

---

## Stdio servers

For servers that use stdio instead of HTTP, replace the capture step:

```yaml
- name: Capture MCP session (stdio)
  run: |
    mcp-scope capture -o candidate.jsonl -- node build/index.js &
    sleep 2   # wait for server to start

    # Send frames via the stdio proxy
    # (wrap your test client to point at the mcp-scope subprocess)
```

Or use the replay command to replay a previously recorded session:

```yaml
- name: Replay baseline against new server (stdio)
  run: |
    mcp-scope replay \
      --compare \          # validate responses match baseline
      baseline.jsonl \
      -- node build/index.js
```

---

## Caching the baseline between runs

The workflow above stores `candidate.jsonl` as a GitHub Actions artifact after every
successful merge to `main`. Each PR downloads it as `baseline.jsonl` for the diff step.

The 90-day retention means you always compare against a recent known-good state rather
than a stale baseline from months ago.
