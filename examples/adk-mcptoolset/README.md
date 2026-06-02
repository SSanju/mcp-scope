# Example: Diagnosing a Google ADK MCPToolset agent

**The scenario:** You're using [Google ADK](https://github.com/google/adk-python)'s `MCPToolset`
to give an AI agent access to your MCP server's tools. The agent is calling tools incorrectly,
getting unexpected results, or hanging. ADK's own logs don't show the raw JSON-RPC frames.

**What this example shows:**
- Capturing traffic between ADK's `MCPToolset` and your MCP server
- Seeing exactly which tools the agent called and with what arguments
- Diagnosing "why did the agent call that tool three times?" with `stats`

---

## How ADK MCPToolset connects to MCP servers

ADK's `MCPToolset` wraps an MCP server as a set of agent-callable tools:

```python
from google.adk.tools.mcp_tool.mcp_toolset import MCPToolset, StdioServerParameters

toolset = MCPToolset(
    connection_params=StdioServerParameters(
        command="node",
        args=["build/index.js"],
    )
)
```

ADK manages the subprocess over stdio. There is no built-in way to see the raw JSON-RPC
traffic. `mcp-scope capture --` inserts a transparent recording layer.

---

## Setup

### Option A: Stdio server (most common)

Replace the `command` in `StdioServerParameters` so mcp-scope sits between ADK and your server:

```python
from google.adk.tools.mcp_tool.mcp_toolset import MCPToolset, StdioServerParameters

toolset = MCPToolset(
    connection_params=StdioServerParameters(
        command="mcp-scope",
        args=[
            "capture",
            "-o", "/tmp/adk-session.jsonl",
            "--",
            "node", "build/index.js",   # your real server command here
        ],
    )
)
```

No other changes needed. ADK sees the same MCP interface. Every frame is captured.

### Option B: HTTP/SSE server

If your MCP server runs as an HTTP server, start mcp-scope as an HTTP proxy:

```bash
# Terminal 1: your MCP server
node build/index.js --port 5000

# Terminal 2: mcp-scope proxy
mcp-scope capture \
  --upstream http://127.0.0.1:5000/mcp \
  --listen 127.0.0.1:9090 \
  -o /tmp/adk-session.jsonl
```

Then point MCPToolset at the proxy:

```python
from google.adk.tools.mcp_tool.mcp_toolset import MCPToolset, SseServerParams

toolset = MCPToolset(
    connection_params=SseServerParams(url="http://127.0.0.1:9090/sse")
)
```

---

## Run your agent

```python
import asyncio
from google.adk.agents import LlmAgent
from google.adk.runners import Runner
from google.adk.sessions import InMemorySessionService
from google.adk.tools.mcp_tool.mcp_toolset import MCPToolset, StdioServerParameters

async def main():
    toolset = MCPToolset(
        connection_params=StdioServerParameters(
            command="mcp-scope",
            args=["capture", "-o", "/tmp/adk-session.jsonl", "--", "node", "build/index.js"],
        )
    )

    agent = LlmAgent(
        model="gemini-2.0-flash",
        name="my_agent",
        tools=[toolset],
    )

    session_service = InMemorySessionService()
    runner = Runner(agent=agent, app_name="debug_session", session_service=session_service)

    session = await session_service.create_session(app_name="debug_session", user_id="user")
    async for event in runner.run_async(
        user_id="user",
        session_id=session.id,
        new_message={"role": "user", "parts": [{"text": "List available tools and call the first one"}]},
    ):
        if event.is_final_response():
            print(event.content)

asyncio.run(main())
```

---

## Analyse the capture

```bash
# What tools did the agent call, and in what order?
mcp-scope view --method tools/call /tmp/adk-session.jsonl

# See the exact arguments the agent passed to each tool call
mcp-scope view --method tools/call --kind req -v /tmp/adk-session.jsonl

# Did any tool calls fail?
mcp-scope view --kind err /tmp/adk-session.jsonl

# How many times did the agent call each tool? Any retries?
mcp-scope stats /tmp/adk-session.jsonl

# Protocol violations (important for custom MCP servers)
mcp-scope check --strict /tmp/adk-session.jsonl
```

---

## Common findings

### "The agent called the same tool 5 times"

```bash
mcp-scope stats /tmp/adk-session.jsonl
```

High call count on a single tool usually means the agent is in a retry loop. Look at the
error responses:

```bash
mcp-scope view --method tools/call --kind err -v /tmp/adk-session.jsonl
```

The error message in the response tells you why the agent kept retrying.

### "MCPToolset hung and the agent never finished"

```bash
mcp-scope view /tmp/adk-session.jsonl
```

Look for requests with no matching response. A `tools/call` with no `← resp` line means the
server never replied. `mcp-scope check` will flag these as unmatched requests.

### "The agent passed wrong arguments to my tool"

```bash
mcp-scope view --method tools/call -v /tmp/adk-session.jsonl | grep arguments
```

Compare the `arguments` in the request against your tool's `inputSchema`. The model derives
argument names from descriptions — if your schema description is ambiguous, the model guesses wrong.

### "My tool schema changed and now the agent breaks"

Capture a session before the schema change (`baseline.jsonl`) and after (`candidate.jsonl`):

```bash
mcp-scope diff baseline.jsonl candidate.jsonl
```

`BREAKING` lines show exactly what changed in a way that clients would notice.
