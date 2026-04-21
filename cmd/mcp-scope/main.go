package main

import (
	"fmt"
	"os"
)

const usage = `mcp-scope — transparent JSON-RPC capture for MCP

Usage:
    mcp-scope capture [flags] -- <server-command> [args...]
    mcp-scope capture --upstream <url> [--listen <addr>] [flags]
    mcp-scope view    [-v] [filter flags] <capture.jsonl>
    mcp-scope stats   [--json] <capture.jsonl>
    mcp-scope check   [--json] [--strict] <capture.jsonl>
    mcp-scope replay  [flags] --upstream <url> <capture.jsonl>
    mcp-scope replay  [flags] -- <server-command> [args...] <capture.jsonl>
    mcp-scope diff    [flags] <capture-a.jsonl> <capture-b.jsonl>
    mcp-scope tui     <capture.jsonl>

Run 'mcp-scope <subcommand> -h' for subcommand flags.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "capture":
		os.Exit(runCapture(os.Args[2:]))
	case "view":
		os.Exit(runView(os.Args[2:]))
	case "stats":
		os.Exit(runStats(os.Args[2:]))
	case "check":
		os.Exit(runCheck(os.Args[2:]))
	case "replay":
		os.Exit(runReplay(os.Args[2:]))
	case "diff":
		os.Exit(runDiff(os.Args[2:]))
	case "tui":
		os.Exit(runTUI(os.Args[2:]))
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}
