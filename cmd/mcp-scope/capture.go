package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/SSanju/mcp-scope/internal/capture"
)

func runCapture(args []string) int {
	fs := flag.NewFlagSet("capture", flag.ContinueOnError)
	out := fs.String("o", "capture.jsonl", "output capture file (JSONL)")
	queue := fs.Int("queue", 1024, "recorder queue depth before back-pressure")
	upstream := fs.String("upstream", "", "http mode: upstream MCP server URL (e.g. http://host:port/mcp)")
	listen := fs.String("listen", "127.0.0.1:9090", "http mode: address to listen on")
	redact := fs.Bool("redact", false, "scrub sensitive fields (tokens, passwords, secrets) from captured payloads")
	redactKeys := fs.String("redact-keys", "", "comma-separated extra field names to redact (implies --redact)")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `Usage:
    mcp-scope capture [flags] -- <server-command> [args...]       # stdio mode
    mcp-scope capture --upstream <url> [--listen <addr>] [flags]  # http mode

Stdio mode proxies stdio between the parent process and the server subcommand.
HTTP mode runs a reverse proxy forwarding to upstream, handling both
application/json and text/event-stream responses.

Every JSON-RPC frame (both directions) is written to the capture file as JSONL.

Flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()

	httpMode := *upstream != ""
	if httpMode && len(rest) > 0 {
		fmt.Fprintln(os.Stderr, "error: --upstream and a subcommand are mutually exclusive")
		fs.Usage()
		return 2
	}
	if !httpMode && len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "error: supply either --upstream or a server command after --")
		fs.Usage()
		return 2
	}

	f, err := os.Create(*out)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: create %s: %v\n", *out, err)
		return 1
	}

	rec := capture.NewRecorder(f, *queue)
	if *redact || *redactKeys != "" {
		var extra []string
		if *redactKeys != "" {
			extra = strings.Split(*redactKeys, ",")
		}
		rec.Redactor = capture.NewRedactor(extra)
	}
	var proxyErr error
	if httpMode {
		fmt.Fprintf(os.Stderr, "mcp-scope: http mode — listening on %s, forwarding to %s\n", *listen, *upstream)
		proxyErr = capture.ProxyHTTP(*upstream, *listen, rec)
	} else {
		proxyErr = capture.ProxyStdio(rest[0], rest[1:], rec)
	}
	if cerr := rec.Close(); cerr != nil {
		fmt.Fprintf(os.Stderr, "warning: close capture: %v\n", cerr)
	}
	if proxyErr != nil {
		fmt.Fprintf(os.Stderr, "mcp-scope: %v\n", proxyErr)
		return 1
	}
	return 0
}
