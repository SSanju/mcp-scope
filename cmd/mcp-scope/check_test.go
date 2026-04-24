package main

import (
	"strings"
	"testing"
)

func TestCheckScanClean(t *testing.T) {
	input := `{"ts":"2024-01-01T00:00:00Z","event":"connect"}
{"ts":"2024-01-01T00:00:00Z","dir":"c2s","transport":"stdio","frame":{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}}
{"ts":"2024-01-01T00:00:01Z","dir":"s2c","transport":"stdio","frame":{"jsonrpc":"2.0","id":1,"result":{}}}
{"ts":"2024-01-01T00:00:01Z","event":"disconnect"}`

	rep := checkScan("test", strings.NewReader(input), false)
	if !rep.OK {
		t.Errorf("expected clean, got errors=%d warns=%d", rep.Errors, rep.Warns)
		for _, i := range rep.Issues {
			t.Logf("  [%s] %s", i.Severity, i.Msg)
		}
	}
}

func TestCheckScanDuplicateID(t *testing.T) {
	input := `{"ts":"2024-01-01T00:00:00Z","dir":"c2s","transport":"stdio","frame":{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}}
{"ts":"2024-01-01T00:00:01Z","dir":"c2s","transport":"stdio","frame":{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}}`

	rep := checkScan("test", strings.NewReader(input), false)
	if rep.Errors == 0 {
		t.Error("expected error for duplicate request id")
	}
}

func TestCheckScanOrphanResponse(t *testing.T) {
	input := `{"ts":"2024-01-01T00:00:00Z","dir":"s2c","transport":"stdio","frame":{"jsonrpc":"2.0","id":99,"result":{}}}`

	rep := checkScan("test", strings.NewReader(input), false)
	if rep.Errors == 0 {
		t.Error("expected error for response with no matching request")
	}
}

func TestCheckScanInvalidLine(t *testing.T) {
	rep := checkScan("test", strings.NewReader("not json at all"), false)
	if rep.Errors == 0 {
		t.Error("expected error for invalid JSON line")
	}
}

func TestCheckScanUnmatchedRequest(t *testing.T) {
	// Request with no response → warning (not error).
	input := `{"ts":"2024-01-01T00:00:00Z","dir":"c2s","transport":"stdio","frame":{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}}`
	rep := checkScan("test", strings.NewReader(input), false)
	if rep.Errors != 0 {
		t.Errorf("expected no errors, got %d", rep.Errors)
	}
	if rep.Warns == 0 {
		t.Error("expected warning for unmatched request")
	}
}

func TestCheckScanStrictMode(t *testing.T) {
	// Unmatched request is a warning; --strict promotes it to failure.
	input := `{"ts":"2024-01-01T00:00:00Z","dir":"c2s","transport":"stdio","frame":{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}}`
	repNormal := checkScan("test", strings.NewReader(input), false)
	repStrict := checkScan("test", strings.NewReader(input), true)
	if !repNormal.OK {
		t.Error("non-strict mode: expected OK with unmatched request")
	}
	if repStrict.OK {
		t.Error("strict mode: expected FAIL with unmatched request")
	}
}

func TestCheckScanSessionBoundaryResetsIDs(t *testing.T) {
	// Same ID reused across two sessions should NOT trigger a duplicate error.
	input := `{"ts":"2024-01-01T00:00:00Z","event":"connect"}
{"ts":"2024-01-01T00:00:00Z","dir":"c2s","transport":"stdio","frame":{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}}
{"ts":"2024-01-01T00:00:01Z","dir":"s2c","transport":"stdio","frame":{"jsonrpc":"2.0","id":1,"result":{}}}
{"ts":"2024-01-01T00:00:01Z","event":"disconnect"}
{"ts":"2024-01-01T00:00:01Z","event":"connect"}
{"ts":"2024-01-01T00:00:01Z","dir":"c2s","transport":"stdio","frame":{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}}
{"ts":"2024-01-01T00:00:02Z","dir":"s2c","transport":"stdio","frame":{"jsonrpc":"2.0","id":1,"result":{}}}
{"ts":"2024-01-01T00:00:02Z","event":"disconnect"}`

	rep := checkScan("test", strings.NewReader(input), false)
	if !rep.OK {
		t.Errorf("same IDs across sessions should be clean, got errors=%d warns=%d", rep.Errors, rep.Warns)
		for _, i := range rep.Issues {
			t.Logf("  [%s] %s", i.Severity, i.Msg)
		}
	}
}

func TestCheckScanSessionBoundaryOrphan(t *testing.T) {
	// Session 1 ends with an unmatched request. Session 2's response for same ID
	// should NOT match across the boundary — session 1's orphan becomes a warning.
	input := `{"ts":"2024-01-01T00:00:00Z","event":"connect"}
{"ts":"2024-01-01T00:00:00Z","dir":"c2s","transport":"stdio","frame":{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}}
{"ts":"2024-01-01T00:00:01Z","event":"disconnect"}
{"ts":"2024-01-01T00:00:01Z","event":"connect"}
{"ts":"2024-01-01T00:00:01Z","dir":"c2s","transport":"stdio","frame":{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}}
{"ts":"2024-01-01T00:00:02Z","dir":"s2c","transport":"stdio","frame":{"jsonrpc":"2.0","id":1,"result":{}}}
{"ts":"2024-01-01T00:00:02Z","event":"disconnect"}`

	rep := checkScan("test", strings.NewReader(input), false)
	if rep.Errors != 0 {
		t.Errorf("expected no errors, got %d", rep.Errors)
	}
	if rep.Warns == 0 {
		t.Error("expected warning for session 1's unmatched request at boundary")
	}
}
