package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/SSanju/mcp-scope/internal/capture"
)

type checkIssue struct {
	Severity string `json:"severity"` // "error" | "warn"
	Line     int    `json:"line"`
	TS       string `json:"ts,omitempty"`
	Msg      string `json:"msg"`
}

type checkReport struct {
	File   string       `json:"file"`
	Frames int          `json:"frames"`
	Issues []checkIssue `json:"issues"`
	Errors int          `json:"errors"`
	Warns  int          `json:"warns"`
	OK     bool         `json:"ok"`
}

func runCheck(args []string) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit machine-readable JSON report")
	strict := fs.Bool("strict", false, "treat warnings as errors (affects exit code)")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `Usage: mcp-scope check [--json] [--strict] <capture.jsonl>

Validates a capture file for JSON-RPC protocol correctness:
  - invalid JSON lines or unparseable frames
  - ambiguous frames (no method, result, or error field)
  - duplicate request IDs
  - responses for unknown request IDs
  - requests without a response in the capture (warnings)

Exits 0 when clean, 1 when issues are found.

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

	f, err := os.Open(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer f.Close()

	rep := checkScan(fs.Arg(0), f, *strict)

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(rep)
	} else {
		printCheckReport(os.Stdout, rep)
	}
	if !rep.OK {
		return 1
	}
	return 0
}

func checkScan(path string, r io.Reader, strict bool) checkReport {
	rep := checkReport{File: path, Issues: []checkIssue{}}
	pending := map[string]int{} // id → line number of request

	sc := capture.NewRecordScanner(r)
	line := 0
	for sc.Scan() {
		line++
		rec := sc.Record()
		ts := ""
		if !rec.TS.IsZero() {
			ts = rec.TS.Format("15:04:05.000")
		}

		if rec.IsEvent() {
			switch rec.Event {
			case "invalid_line":
				rep.issue("error", line, ts, "invalid JSON: "+rec.Meta["err"])
			case "connect":
				// Flush unmatched requests from the previous session before resetting.
				for id, reqLine := range pending {
					rep.issue("warn", reqLine, "", fmt.Sprintf("session ended with unmatched request id=%s", id))
				}
				pending = map[string]int{}
			}
			continue
		}
		rep.Frames++

		kind, id, method := classifyFrame(rec.Payload)
		switch kind {
		case "invalid":
			rep.issue("error", line, ts, "unparseable JSON-RPC frame")
		case "?":
			rep.issue("warn", line, ts, "ambiguous frame: no method, result, or error field")
		case "req":
			if _, dup := pending[id]; dup {
				rep.issue("error", line, ts,
					fmt.Sprintf("duplicate request id=%s method=%s", id, method))
			} else {
				pending[id] = line
			}
		case "resp", "err":
			if _, ok := pending[id]; ok {
				delete(pending, id)
			} else {
				rep.issue("error", line, ts,
					fmt.Sprintf("response for unknown request id=%s", id))
			}
		}
	}

	// Remaining pending entries are unmatched requests.
	for id, reqLine := range pending {
		rep.issue("warn", reqLine, "",
			fmt.Sprintf("unmatched request id=%s (no response in capture)", id))
	}

	for _, iss := range rep.Issues {
		if iss.Severity == "error" {
			rep.Errors++
		} else {
			rep.Warns++
		}
	}
	rep.OK = rep.Errors == 0 && (!strict || rep.Warns == 0)
	return rep
}

func (r *checkReport) issue(sev string, line int, ts, msg string) {
	r.Issues = append(r.Issues, checkIssue{Severity: sev, Line: line, TS: ts, Msg: msg})
}

func printCheckReport(w io.Writer, rep checkReport) {
	fmt.Fprintf(w, "Checking %s ...\n", rep.File)
	for _, iss := range rep.Issues {
		lvl := "WARN "
		if iss.Severity == "error" {
			lvl = "ERROR"
		}
		loc := fmt.Sprintf("line %d", iss.Line)
		if iss.TS != "" {
			loc += " " + iss.TS
		}
		fmt.Fprintf(w, "  [%s] %s: %s\n", lvl, loc, iss.Msg)
	}
	if rep.OK {
		fmt.Fprintf(w, "OK (%d frames, 0 issues)\n", rep.Frames)
	} else {
		fmt.Fprintf(w, "FAIL — %d error(s), %d warning(s)\n", rep.Errors, rep.Warns)
	}
}
