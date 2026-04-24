package main

import (
	"strings"
	"testing"
	"time"
)

const statsCapture = `{"ts":"2024-01-01T00:00:00Z","event":"connect"}
{"ts":"2024-01-01T00:00:00Z","dir":"c2s","transport":"stdio","frame":{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}}
{"ts":"2024-01-01T00:00:01Z","dir":"s2c","transport":"stdio","frame":{"jsonrpc":"2.0","id":1,"result":{}}}
{"ts":"2024-01-01T00:00:01Z","dir":"c2s","transport":"stdio","frame":{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{}}}
{"ts":"2024-01-01T00:00:02Z","dir":"s2c","transport":"stdio","frame":{"jsonrpc":"2.0","id":2,"error":{"code":-1,"message":"fail"}}}
{"ts":"2024-01-01T00:00:02Z","dir":"c2s","transport":"stdio","frame":{"jsonrpc":"2.0","method":"notifications/initialized"}}
{"ts":"2024-01-01T00:00:02Z","event":"disconnect"}`

func TestComputeStatsBasic(t *testing.T) {
	res, err := computeStats(strings.NewReader(statsCapture))
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalFrames != 5 {
		t.Errorf("want 5 frames, got %d", res.TotalFrames)
	}
	if res.Sessions != 1 {
		t.Errorf("want 1 session, got %d", res.Sessions)
	}
	init := res.ByMethod["initialize"]
	if init == nil {
		t.Fatal("missing initialize stats")
	}
	if init.count != 1 {
		t.Errorf("initialize count: want 1, got %d", init.count)
	}
	if len(init.latencies) != 1 {
		t.Errorf("initialize latencies: want 1, got %d", len(init.latencies))
	}
	if init.latencies[0] < 900*time.Millisecond || init.latencies[0] > 1100*time.Millisecond {
		t.Errorf("initialize latency ~1s, got %v", init.latencies[0])
	}
	tc := res.ByMethod["tools/call"]
	if tc == nil {
		t.Fatal("missing tools/call stats")
	}
	if tc.errors != 1 {
		t.Errorf("tools/call errors: want 1, got %d", tc.errors)
	}
	if res.Notifs["notifications/initialized"] != 1 {
		t.Errorf("notifications/initialized count: want 1, got %d", res.Notifs["notifications/initialized"])
	}
}

func TestComputeStatsSessionBoundary(t *testing.T) {
	// Two sessions reusing the same request ID.
	// Session 1: req id=1 with no response (should not match session 2's response).
	// Session 2: req id=1 + resp id=1 — only this should produce a latency entry.
	input := `{"ts":"2024-01-01T00:00:00Z","event":"connect"}
{"ts":"2024-01-01T00:00:00Z","dir":"c2s","transport":"stdio","frame":{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}}
{"ts":"2024-01-01T00:00:01Z","event":"disconnect"}
{"ts":"2024-01-01T00:00:01Z","event":"connect"}
{"ts":"2024-01-01T00:00:01Z","dir":"c2s","transport":"stdio","frame":{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}}
{"ts":"2024-01-01T00:00:02Z","dir":"s2c","transport":"stdio","frame":{"jsonrpc":"2.0","id":1,"result":{}}}
{"ts":"2024-01-01T00:00:02Z","event":"disconnect"}`

	res, err := computeStats(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if res.Sessions != 2 {
		t.Errorf("want 2 sessions, got %d", res.Sessions)
	}
	tc := res.ByMethod["tools/call"]
	if tc == nil {
		t.Fatal("missing tools/call stats")
	}
	if tc.count != 2 {
		t.Errorf("want 2 tools/call requests counted, got %d", tc.count)
	}
	if len(tc.latencies) != 1 {
		t.Errorf("want 1 latency (only session 2 completed), got %d", len(tc.latencies))
	}
	if res.Unmatched != 1 {
		t.Errorf("want 1 unmatched (session 1's orphan), got %d", res.Unmatched)
	}
}

func TestComputeStatsNotifications(t *testing.T) {
	input := `{"ts":"2024-01-01T00:00:00Z","dir":"c2s","transport":"stdio","frame":{"jsonrpc":"2.0","method":"notifications/initialized"}}
{"ts":"2024-01-01T00:00:00Z","dir":"c2s","transport":"stdio","frame":{"jsonrpc":"2.0","method":"notifications/roots/list_changed"}}
{"ts":"2024-01-01T00:00:00Z","dir":"c2s","transport":"stdio","frame":{"jsonrpc":"2.0","method":"notifications/roots/list_changed"}}`

	res, err := computeStats(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if res.Notifs["notifications/initialized"] != 1 {
		t.Errorf("want 1 notifications/initialized, got %d", res.Notifs["notifications/initialized"])
	}
	if res.Notifs["notifications/roots/list_changed"] != 2 {
		t.Errorf("want 2 notifications/roots/list_changed, got %d", res.Notifs["notifications/roots/list_changed"])
	}
}
