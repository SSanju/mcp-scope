package main

import (
	"errors"
	"flag"
	"fmt"
	"regexp"
	"time"

	"github.com/SSanju/mcp-scope/internal/capture"
)

// filterArgs bundles record-filter flags. The workflow is:
// RegisterFlags on a FlagSet, Parse, then Compile, then Allow on each record.
type filterArgs struct {
	method, id, dir, kind, session string
	grep, since, until             string
	eventsOnly, framesOnly         bool

	grepRE           *regexp.Regexp
	sinceTS, untilTS time.Time
}

var validKinds = map[string]bool{"req": true, "resp": true, "err": true, "notif": true}

func (a *filterArgs) RegisterFlags(fs *flag.FlagSet) {
	fs.StringVar(&a.method, "method", "", "only frames whose JSON-RPC method equals this")
	fs.StringVar(&a.id, "id", "", "only frames with matching JSON-RPC id")
	fs.StringVar(&a.dir, "dir", "", "only frames in this direction (c2s|s2c)")
	fs.StringVar(&a.kind, "kind", "", "only frames of this kind (req|resp|err|notif)")
	fs.StringVar(&a.session, "session", "", "only records whose meta.session_id matches")
	fs.StringVar(&a.grep, "grep", "", "regex matched against the raw JSON-RPC payload")
	fs.StringVar(&a.since, "since", "", "RFC3339 lower bound on record timestamp")
	fs.StringVar(&a.until, "until", "", "RFC3339 upper bound on record timestamp")
	fs.BoolVar(&a.eventsOnly, "events-only", false, "show only boundary events")
	fs.BoolVar(&a.framesOnly, "frames-only", false, "show only JSON-RPC frames")
}

func (a *filterArgs) Compile() error {
	if a.eventsOnly && a.framesOnly {
		return errors.New("--events-only and --frames-only are mutually exclusive")
	}
	if a.dir != "" && a.dir != "c2s" && a.dir != "s2c" {
		return fmt.Errorf("--dir must be c2s or s2c, got %q", a.dir)
	}
	if a.kind != "" && !validKinds[a.kind] {
		return fmt.Errorf("--kind must be one of req, resp, err, notif; got %q", a.kind)
	}
	if a.grep != "" {
		re, err := regexp.Compile(a.grep)
		if err != nil {
			return fmt.Errorf("--grep: %w", err)
		}
		a.grepRE = re
	}
	if a.since != "" {
		t, err := time.Parse(time.RFC3339Nano, a.since)
		if err != nil {
			return fmt.Errorf("--since: %w", err)
		}
		a.sinceTS = t
	}
	if a.until != "" {
		t, err := time.Parse(time.RFC3339Nano, a.until)
		if err != nil {
			return fmt.Errorf("--until: %w", err)
		}
		a.untilTS = t
	}
	return nil
}

func (a *filterArgs) Allow(rec capture.Record) bool {
	if !a.sinceTS.IsZero() && rec.TS.Before(a.sinceTS) {
		return false
	}
	if !a.untilTS.IsZero() && rec.TS.After(a.untilTS) {
		return false
	}
	if rec.IsEvent() {
		if a.framesOnly {
			return false
		}
		// Frame-specific filters exclude events entirely when set.
		if a.method != "" || a.id != "" || a.kind != "" || a.dir != "" || a.grepRE != nil {
			return false
		}
		if a.session != "" && rec.Meta["session_id"] != a.session {
			return false
		}
		return true
	}
	if a.eventsOnly {
		return false
	}
	if a.dir != "" && string(rec.Dir) != a.dir {
		return false
	}
	if a.session != "" && rec.Meta["session_id"] != a.session {
		return false
	}
	kind, id, method := classifyFrame(rec.Payload)
	if a.kind != "" && kind != a.kind {
		return false
	}
	if a.id != "" && id != a.id {
		return false
	}
	if a.method != "" && method != a.method {
		return false
	}
	if a.grepRE != nil && !a.grepRE.Match(rec.Payload) {
		return false
	}
	return true
}
