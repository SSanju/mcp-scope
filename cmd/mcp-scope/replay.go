package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type replayResult struct {
	Method    string          `json:"method"`
	ID        string          `json:"id"`
	LatencyMS float64         `json:"latency_ms"`
	Status    string          `json:"status"` // "ok"|"differ"|"fail"
	Error     string          `json:"error,omitempty"`
	OrigResp  json.RawMessage `json:"orig_resp,omitempty"`
	NewResp   json.RawMessage `json:"new_resp,omitempty"`
}

type replaySummary struct {
	Total   int            `json:"total"`
	OK      int            `json:"ok"`
	Differ  int            `json:"differ"`
	Failed  int            `json:"failed"`
	Results []replayResult `json:"results"`
}

func runReplay(args []string) int {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	upstream := fs.String("upstream", "", "HTTP mode: upstream MCP server URL")
	timeout := fs.Duration("timeout", 10*time.Second, "per-request timeout")
	delay := fs.Duration("delay", 0, "delay between requests")
	compare := fs.Bool("compare", false, "compare response payloads to originals")
	jsonOut := fs.Bool("json", false, "emit JSON summary")
	method := fs.String("method", "", "only replay requests matching this method")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `Usage:
    mcp-scope replay [flags] --upstream <url> <capture.jsonl>
    mcp-scope replay [flags] -- <server-command> [args...] <capture.jsonl>

Re-sends captured client→server requests to a live server.
With --compare, response payloads are checked against the original capture.

Exits 0 when all requests succeed (and match, if --compare).

Flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	rest := fs.Args()
	var captureFile string
	var serverCmd []string

	if *upstream != "" {
		if len(rest) != 1 {
			fs.Usage()
			return 2
		}
		captureFile = rest[0]
	} else {
		if len(rest) < 2 {
			fmt.Fprintln(os.Stderr, "error: supply --upstream <url> or -- <server-command> <capture.jsonl>")
			fs.Usage()
			return 2
		}
		captureFile = rest[len(rest)-1]
		serverCmd = rest[:len(rest)-1]
	}

	f, err := os.Open(captureFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer f.Close()

	pairs := loadRequestPairs(f, *method)
	if len(pairs) == 0 {
		fmt.Fprintln(os.Stderr, "no requests found in capture")
		return 0
	}

	var sum replaySummary
	if *upstream != "" {
		sum = replayHTTP(pairs, *upstream, *timeout, *delay, *compare)
	} else {
		// For stdio, always perform the MCP initialize handshake before replaying.
		var initPair *requestPair
		f2, err2 := os.Open(captureFile)
		if err2 == nil {
			if ip := loadRequestPairs(f2, "initialize"); len(ip) > 0 {
				cp := ip[0]
				initPair = &cp
			}
			f2.Close()
		}
		if initPair == nil {
			cp := requestPair{
				ID:     "0",
				Method: "initialize",
				Request: json.RawMessage(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":` +
					`{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"mcp-scope","version":"0"}}}`),
			}
			initPair = &cp
		}
		sum = replayStdio(pairs, initPair, serverCmd, *timeout, *delay, *compare)
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(sum)
	} else {
		printReplaySummary(os.Stdout, sum)
	}

	if sum.Failed > 0 || (*compare && sum.Differ > 0) {
		return 1
	}
	return 0
}

func replayHTTP(pairs []requestPair, upstream string, timeout, delay time.Duration, compare bool) replaySummary {
	client := &http.Client{Timeout: timeout}
	sum := replaySummary{Total: len(pairs)}
	var sessionID string // preserved across requests (Mcp-Session-Id)

	for _, pair := range pairs {
		if delay > 0 {
			time.Sleep(delay)
		}
		res := replayResult{Method: pair.Method, ID: pair.ID}
		start := time.Now()

		req, err := http.NewRequest("POST", upstream, bytes.NewReader(pair.Request))
		if err != nil {
			res.Status = "fail"
			res.Error = err.Error()
			sum.Failed++
			sum.Results = append(sum.Results, res)
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		if sessionID != "" {
			req.Header.Set("Mcp-Session-Id", sessionID)
		}

		resp, err := client.Do(req)
		if err != nil {
			res.Status = "fail"
			res.Error = err.Error()
			sum.Failed++
			sum.Results = append(sum.Results, res)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		// Capture session ID from the first response that sets it.
		if sessionID == "" {
			if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
				sessionID = sid
			}
		}

		res.LatencyMS = float64(time.Since(start).Nanoseconds()) / 1e6
		res.NewResp = json.RawMessage(bytes.TrimRight(body, "\n"))

		if compare && len(pair.Response) > 0 {
			res.OrigResp = pair.Response
			if normalEqual(pair.Response, res.NewResp) {
				res.Status = "ok"
				sum.OK++
			} else {
				res.Status = "differ"
				sum.Differ++
			}
		} else {
			res.Status = "ok"
			sum.OK++
		}
		sum.Results = append(sum.Results, res)
	}
	return sum
}

func replayStdio(pairs []requestPair, initPair *requestPair, cmd []string, timeout, delay time.Duration, compare bool) replaySummary {
	sum := replaySummary{Total: len(pairs)}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	stdin, err := c.StdinPipe()
	if err != nil {
		return allFailed(pairs, "stdin pipe: "+err.Error())
	}
	stdout, err := c.StdoutPipe()
	if err != nil {
		return allFailed(pairs, "stdout pipe: "+err.Error())
	}
	c.Stderr = os.Stderr
	if err := c.Start(); err != nil {
		return allFailed(pairs, "start: "+err.Error())
	}

	type incoming struct {
		payload []byte
		err     error
	}
	// Dispatcher: one goroutine reads server stdout and routes by JSON-RPC id.
	dispatch := map[string]chan incoming{}
	var mu sync.Mutex
	notifDrop := make(chan incoming, 64) // absorbs notifications

	go func() {
		br := bufio.NewReaderSize(stdout, 64*1024)
		for {
			line, err := br.ReadBytes('\n')
			if len(line) > 0 {
				var hdr struct {
					ID json.RawMessage `json:"id"`
				}
				json.Unmarshal(line, &hdr)
				idStr := ""
				if len(hdr.ID) > 0 && string(hdr.ID) != "null" {
					idStr = strings.Trim(string(hdr.ID), `"`)
				}
				mu.Lock()
				ch, ok := dispatch[idStr]
				mu.Unlock()
				if ok {
					ch <- incoming{payload: line}
				} else {
					select {
					case notifDrop <- incoming{payload: line}:
					default:
					}
				}
			}
			if err != nil {
				mu.Lock()
				for _, ch := range dispatch {
					ch <- incoming{err: err}
				}
				mu.Unlock()
				return
			}
		}
	}()

	// MCP handshake: send initialize + notifications/initialized before replaying.
	// Skip if the first pair to replay is already initialize (full capture replay).
	firstIsInit := len(pairs) > 0 && pairs[0].Method == "initialize"
	if initPair != nil && !firstIsInit {
		initCh := make(chan incoming, 1)
		mu.Lock()
		dispatch[initPair.ID] = initCh
		mu.Unlock()
		if _, err := stdin.Write(append(initPair.Request, '\n')); err == nil {
			initTimer := time.NewTimer(timeout)
			select {
			case <-initCh:
				initTimer.Stop()
				_, _ = stdin.Write(append(
					json.RawMessage(`{"jsonrpc":"2.0","method":"notifications/initialized"}`),
					'\n',
				))
			case <-initTimer.C:
			}
		}
		mu.Lock()
		delete(dispatch, initPair.ID)
		mu.Unlock()
	}

	for _, pair := range pairs {
		if delay > 0 {
			time.Sleep(delay)
		}
		res := replayResult{Method: pair.Method, ID: pair.ID}
		start := time.Now()

		ch := make(chan incoming, 1)
		mu.Lock()
		dispatch[pair.ID] = ch
		mu.Unlock()

		if _, err := stdin.Write(append(pair.Request, '\n')); err != nil {
			res.Status = "fail"
			res.Error = "write: " + err.Error()
			sum.Failed++
			mu.Lock()
			delete(dispatch, pair.ID)
			mu.Unlock()
			sum.Results = append(sum.Results, res)
			continue
		}

		timer := time.NewTimer(timeout)
		select {
		case inc := <-ch:
			timer.Stop()
			res.LatencyMS = float64(time.Since(start).Nanoseconds()) / 1e6
			mu.Lock()
			delete(dispatch, pair.ID)
			mu.Unlock()

			if inc.err != nil {
				res.Status = "fail"
				res.Error = inc.err.Error()
				sum.Failed++
			} else {
				res.NewResp = json.RawMessage(bytes.TrimRight(inc.payload, "\n"))
				if compare && len(pair.Response) > 0 {
					res.OrigResp = pair.Response
					if normalEqual(pair.Response, res.NewResp) {
						res.Status = "ok"
						sum.OK++
					} else {
						res.Status = "differ"
						sum.Differ++
					}
				} else {
					res.Status = "ok"
					sum.OK++
				}
			}
		case <-timer.C:
			mu.Lock()
			delete(dispatch, pair.ID)
			mu.Unlock()
			res.Status = "fail"
			res.Error = "timeout"
			sum.Failed++
		}
		sum.Results = append(sum.Results, res)
	}

	stdin.Close()
	c.Wait()
	return sum
}

func allFailed(pairs []requestPair, msg string) replaySummary {
	results := make([]replayResult, len(pairs))
	for i, p := range pairs {
		results[i] = replayResult{Method: p.Method, ID: p.ID, Status: "fail", Error: msg}
	}
	return replaySummary{Total: len(pairs), Failed: len(pairs), Results: results}
}

func printReplaySummary(w io.Writer, sum replaySummary) {
	fmt.Fprintf(w, "Replaying %d request(s)...\n", sum.Total)
	for _, r := range sum.Results {
		mark := "✓"
		detail := ""
		switch r.Status {
		case "differ":
			mark = "≠"
			detail = " (result differs)"
		case "fail":
			mark = "✗"
			detail = " (" + r.Error + ")"
		}
		fmt.Fprintf(w, "  %s %-30s id=%-6s %8.1fms%s\n",
			mark, truncStr(r.Method, 30), r.ID, r.LatencyMS, detail)
	}
	fmt.Fprintf(w, "\n%d/%d ok", sum.OK, sum.Total)
	if sum.Differ > 0 {
		fmt.Fprintf(w, ", %d differ", sum.Differ)
	}
	if sum.Failed > 0 {
		fmt.Fprintf(w, ", %d failed", sum.Failed)
	}
	fmt.Fprintln(w)
}
