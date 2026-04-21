package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/SSanju/mcp-scope/internal/capture"
)

// requestPair holds a captured c2s request and the matching s2c response (if any).
type requestPair struct {
	ID       string
	Method   string
	Request  json.RawMessage
	Response json.RawMessage // nil when no response was captured
}

// loadRequestPairs scans r for JSON-RPC request/response pairs in capture order.
// If methodFilter is non-empty, only requests matching that method are included.
func loadRequestPairs(r io.Reader, methodFilter string) []requestPair {
	type pending struct {
		method  string
		payload json.RawMessage
		idx     int
	}
	pend := map[string]pending{}
	var pairs []requestPair

	sc := capture.NewRecordScanner(r)
	for sc.Scan() {
		rec := sc.Record()
		if rec.IsEvent() {
			continue
		}
		kind, id, method := classifyFrame(rec.Payload)

		if rec.Dir == capture.DirC2S && kind == "req" {
			if methodFilter != "" && method != methodFilter {
				continue
			}
			idx := len(pairs)
			pend[id] = pending{method: method, payload: rec.Payload, idx: idx}
			pairs = append(pairs, requestPair{
				ID: id, Method: method, Request: rec.Payload,
			})
		} else if rec.Dir == capture.DirS2C && (kind == "resp" || kind == "err") {
			if p, ok := pend[id]; ok {
				delete(pend, id)
				pairs[p.idx].Response = rec.Payload
			}
		}
	}
	return pairs
}

// loadPairsFromFile opens path, loads pairs, and groups them by method name.
func loadPairsFromFile(path, methodFilter string) (map[string][]requestPair, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	grouped := map[string][]requestPair{}
	for _, p := range loadRequestPairs(f, methodFilter) {
		grouped[p.Method] = append(grouped[p.Method], p)
	}
	return grouped, nil
}

// normalEqual compares two JSON values for semantic equality by normalising
// through unmarshal+marshal (ignores key order and whitespace differences).
func normalEqual(a, b json.RawMessage) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	var va, vb any
	if json.Unmarshal(a, &va) != nil || json.Unmarshal(b, &vb) != nil {
		return bytes.Equal(a, b)
	}
	aN, _ := json.Marshal(va)
	bN, _ := json.Marshal(vb)
	return bytes.Equal(aN, bN)
}

// compact returns a single-line JSON string, truncated to 120 runes.
func compact(v json.RawMessage) string {
	if len(v) == 0 {
		return "(none)"
	}
	var x any
	if json.Unmarshal(v, &x) != nil {
		return strings.TrimRight(string(v), "\n")
	}
	b, _ := json.Marshal(x)
	s := string(b)
	if len([]rune(s)) > 120 {
		runes := []rune(s)
		return string(runes[:119]) + "…"
	}
	return s
}
