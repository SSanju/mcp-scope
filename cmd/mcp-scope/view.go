package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"time"

	"github.com/SSanju/mcp-scope/internal/capture"
)

// tailReader wraps an *os.File and blocks on EOF instead of returning it,
// enabling live-follow behaviour similar to `tail -f`.
type tailReader struct{ f *os.File }

func (t *tailReader) Read(p []byte) (int, error) {
	for {
		n, err := t.f.Read(p)
		if n > 0 {
			return n, nil
		}
		if err == io.EOF {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		return n, err
	}
}

func runView(args []string) int {
	fs := flag.NewFlagSet("view", flag.ContinueOnError)
	verbose := fs.Bool("v", false, "print full JSON payload after each frame")
	follow := fs.Bool("follow", false, "watch file for new frames (like tail -f)")
	filter := &filterArgs{}
	filter.RegisterFlags(fs)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `Usage: mcp-scope view [flags] <capture.jsonl>

Pretty-prints a capture to stdout, one line per frame or event. Filter flags
are composable; all specified filters must match for a record to be shown.

Flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	if err := filter.Compile(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}

	f, err := os.Open(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer f.Close()

	var sc *capture.RecordScanner
	if *follow {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt)
		go func() { <-sig; os.Exit(0) }()
		sc = capture.NewRecordScanner(&tailReader{f})
	} else {
		sc = capture.NewRecordScanner(f)
	}

	for sc.Scan() {
		rec := sc.Record()
		if !filter.Allow(rec) {
			continue
		}
		printRecord(os.Stdout, rec, *verbose)
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func printRecord(w io.Writer, rec capture.Record, verbose bool) {
	ts := rec.TS.Format("15:04:05.000")
	if rec.IsEvent() {
		fmt.Fprintf(w, "• %s  %s", ts, rec.Event)
		if len(rec.Meta) > 0 {
			fmt.Fprintf(w, "  %s", formatMeta(rec.Meta))
		}
		fmt.Fprintln(w)
		return
	}
	arrow := "->"
	if rec.Dir == capture.DirS2C {
		arrow = "<-"
	}
	kind, id, method := classifyFrame(rec.Payload)
	summary := kind
	if method != "" {
		summary += " " + method
	}
	if id != "" {
		summary += " id=" + id
	}
	fmt.Fprintf(w, "%s %s  %-4s  %s\n", arrow, ts, string(rec.Transport), summary)
	if verbose && len(rec.Payload) > 0 {
		fmt.Fprintf(w, "    %s\n", rec.Payload)
	}
}

// classifyFrame returns (kind, id, method) by peeking at JSON-RPC fields.
// Kind is one of: "req", "resp", "err", "notif", "invalid", "?".
func classifyFrame(payload json.RawMessage) (kind, id, method string) {
	var msg struct {
		ID     json.RawMessage `json:"id,omitempty"`
		Method string          `json:"method,omitempty"`
		Error  json.RawMessage `json:"error,omitempty"`
		Result json.RawMessage `json:"result,omitempty"`
	}
	if err := json.Unmarshal(payload, &msg); err != nil {
		return "invalid", "", ""
	}
	idStr := ""
	if len(msg.ID) > 0 && string(msg.ID) != "null" {
		idStr = strings.Trim(string(msg.ID), `"`)
	}
	switch {
	case msg.Method != "" && idStr != "":
		return "req", idStr, msg.Method
	case msg.Method != "":
		return "notif", "", msg.Method
	case len(msg.Error) > 0:
		return "err", idStr, ""
	case len(msg.Result) > 0:
		return "resp", idStr, ""
	}
	return "?", idStr, ""
}

func formatMeta(meta map[string]string) string {
	parts := make([]string, 0, len(meta))
	for k, v := range meta {
		parts = append(parts, k+"="+v)
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
}
