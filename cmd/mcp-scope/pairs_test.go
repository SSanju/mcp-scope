package main

import (
	"encoding/json"
	"strings"
	"testing"
)

const pairsCapture = `{"ts":"2024-01-01T00:00:00Z","event":"connect"}
{"ts":"2024-01-01T00:00:00Z","dir":"c2s","transport":"stdio","frame":{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}}
{"ts":"2024-01-01T00:00:01Z","dir":"s2c","transport":"stdio","frame":{"jsonrpc":"2.0","id":1,"result":{"capabilities":{}}}}
{"ts":"2024-01-01T00:00:01Z","dir":"c2s","transport":"stdio","frame":{"jsonrpc":"2.0","method":"notifications/initialized"}}
{"ts":"2024-01-01T00:00:01Z","dir":"c2s","transport":"stdio","frame":{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search"}}}
{"ts":"2024-01-01T00:00:02Z","dir":"s2c","transport":"stdio","frame":{"jsonrpc":"2.0","id":2,"result":{"content":[]}}}
{"ts":"2024-01-01T00:00:02Z","event":"disconnect"}`

func TestLoadRequestPairs(t *testing.T) {
	pairs := loadRequestPairs(strings.NewReader(pairsCapture), "")
	if len(pairs) != 2 {
		t.Fatalf("want 2 pairs, got %d", len(pairs))
	}
	if pairs[0].Method != "initialize" {
		t.Errorf("pair[0]: want initialize, got %s", pairs[0].Method)
	}
	if pairs[1].Method != "tools/call" {
		t.Errorf("pair[1]: want tools/call, got %s", pairs[1].Method)
	}
	if len(pairs[0].Response) == 0 {
		t.Error("initialize response not matched")
	}
	if len(pairs[1].Response) == 0 {
		t.Error("tools/call response not matched")
	}
}

func TestLoadRequestPairsMethodFilter(t *testing.T) {
	pairs := loadRequestPairs(strings.NewReader(pairsCapture), "tools/call")
	if len(pairs) != 1 {
		t.Fatalf("want 1 pair, got %d", len(pairs))
	}
	if pairs[0].Method != "tools/call" {
		t.Errorf("want tools/call, got %s", pairs[0].Method)
	}
}

func TestLoadRequestPairsUnmatched(t *testing.T) {
	// Request with no matching response.
	input := `{"ts":"2024-01-01T00:00:00Z","dir":"c2s","transport":"stdio","frame":{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}}`
	pairs := loadRequestPairs(strings.NewReader(input), "")
	if len(pairs) != 1 {
		t.Fatalf("want 1 pair, got %d", len(pairs))
	}
	if len(pairs[0].Response) != 0 {
		t.Error("unmatched request should have nil response")
	}
}

func TestNormalEqual(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{`{"a":1,"b":2}`, `{"b":2,"a":1}`, true},
		{`{"a":1}`, `{"a":2}`, false},
		{`null`, `null`, true},
		{`{}`, `{}`, true},
		{`[1,2,3]`, `[1,2,3]`, true},
		{`[1,2,3]`, `[1,3,2]`, false},
		{`"hello"`, `"hello"`, true},
		{`"hello"`, `"world"`, false},
	}
	for _, c := range cases {
		got := normalEqual(json.RawMessage(c.a), json.RawMessage(c.b))
		if got != c.want {
			t.Errorf("normalEqual(%s, %s) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
