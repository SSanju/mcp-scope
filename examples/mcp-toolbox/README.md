# Example: Capturing and validating a Google mcp-toolbox session

**The scenario:** You're running [googleapis/mcp-toolbox](https://github.com/googleapis/mcp-toolbox)
as the MCP backend for your AI agent. A client reports intermittent failures. You want to see exactly
what's on the wire and whether the server speaks valid JSON-RPC.

**What this example shows:**
- Capturing HTTP/SSE traffic from a real mcp-toolbox instance
- Running `mcp-scope check --strict` to detect protocol violations
- Real bugs found in mcp-toolbox v1.3.0 using this exact method

---

## Setup

### 1. Download mcp-toolbox

```bash
# Linux amd64
curl -L -o toolbox \
  https://storage.googleapis.com/mcp-toolbox-for-databases/v1.3.0/linux/amd64/toolbox
chmod +x ./toolbox

# macOS arm64
curl -L -o toolbox \
  https://storage.googleapis.com/mcp-toolbox-for-databases/v1.3.0/darwin/arm64/toolbox
chmod +x ./toolbox
```

### 2. Start mcp-toolbox with a SQLite database

```bash
SQLITE_DATABASE="/tmp/test.db" ./toolbox --prebuilt sqlite --port 5001
# Server ready to serve on 127.0.0.1:5001
```

### 3. Start mcp-scope in front of it

```bash
mcp-scope capture \
  --upstream http://127.0.0.1:5001/mcp \
  --listen 127.0.0.1:9090 \
  -o capture.jsonl
# mcp-scope: http mode — listening on 127.0.0.1:9090, forwarding to http://127.0.0.1:5001/mcp
```

Now point your client (or curl) at `127.0.0.1:9090` instead of `5001`. Every frame is recorded.

### 4. Run a session through the proxy

```bash
# Initialize
curl -s -X POST http://127.0.0.1:9090 -H "Content-Type: application/json" -d '{
  "jsonrpc":"2.0","id":1,"method":"initialize",
  "params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1"}}
}'

# List tools
curl -s -X POST http://127.0.0.1:9090 -H "Content-Type: application/json" -d '{
  "jsonrpc":"2.0","id":2,"method":"tools/list","params":{}
}'

# Call a tool
curl -s -X POST http://127.0.0.1:9090 -H "Content-Type: application/json" -d '{
  "jsonrpc":"2.0","id":3,"method":"tools/call",
  "params":{"name":"execute_sql","arguments":{"sql":"SELECT sqlite_version()"}}
}'
```

### 5. Stop the proxy (Ctrl-C) and analyse

```bash
# See all frames
mcp-scope view capture.jsonl

# Check for protocol violations
mcp-scope check --strict capture.jsonl

# Performance summary
mcp-scope stats capture.jsonl
```

---

## Real bugs found in mcp-toolbox v1.3.0

Running `mcp-scope check --strict` against mcp-toolbox v1.3.0 with the full protocol test
(see [`capture.jsonl`](capture.jsonl)) produced:

```
Checking capture.jsonl ...
  [ERROR] line 25: response for unknown request id=a0240994-6225-49be-a8c6-440eccce2942
  [WARN ] line 23: unmatched request id=11 (no response in capture)
  [WARN ] line 24: unmatched request id=12 (no response in capture)
FAIL — 1 error(s), 2 warning(s)
```

### Bug 1: Batch rejection response uses a server-generated UUID

When toolbox rejects a batch request, it generates a fresh UUID as the response `id` instead of
using the id from the request. JSON-RPC 2.0 §5 requires the response id to match the request id.

```
→ [{"jsonrpc":"2.0","id":11,"method":"tools/list"},
   {"jsonrpc":"2.0","id":12,"method":"initialize",...}]

← {"jsonrpc":"2.0","id":"a0240994-6225-49be-a8c6-440eccce2942",
   "error":{"code":-32600,"message":"not supporting batch requests"}}
```

The ids `11` and `12` receive no response. Any client tracking pending requests by id will hang.
Filed as: https://github.com/googleapis/mcp-toolbox/issues/XXXX

### Bug 2: Request with `id: null` receives no response

```
→ {"jsonrpc":"2.0","id":null,"method":"tools/list","params":{}}
← [empty — no response]
```

A request with `id: null` is still a request per the spec and requires a response.

### Bug 3: `--prebuilt sqlite` broken with `:memory:`

```bash
$ SQLITE_DATABASE=":memory:" toolbox --prebuilt sqlite
ERROR: database: :memory:   ← YAML parsing fails, colon-prefixed value
```

The prebuilt template emits `database: :memory:` which is invalid YAML. Use a file path instead.

---

## What `mcp-scope view` shows

```
• connect    listen=127.0.0.1:9090 mode=http upstream=http://127.0.0.1:5001/mcp
→ req        initialize id=1
← resp       id=1
→ notif      notifications/initialized
→ req        tools/list id=2
← resp       id=2
→ req        tools/call id=3
← resp       id=3
• disconnect mode=http
```

## What `mcp-scope stats` shows

```
Requests:
  method          count  errors   p50    p95    p99    max
  tools/call          3       0  596µs  788µs  788µs   2ms
  tools/list          1       0  601µs  601µs  601µs  636µs
  initialize          1       0    1ms    1ms    1ms   1ms
```
