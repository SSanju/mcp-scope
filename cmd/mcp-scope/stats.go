package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/SSanju/mcp-scope/internal/capture"
)

func runStats(args []string) int {
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit machine-readable JSON instead of a human table")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `Usage: mcp-scope stats [--json] <capture.jsonl>

Prints per-method call counts, error counts, latency (p50/p95/max),
notification counts, and a transport breakdown.

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

	res, err := computeStats(f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if *jsonOut {
		if err := writeStatsJSON(os.Stdout, res); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		return 0
	}
	writeStatsText(os.Stdout, res)
	return 0
}

type methodStats struct {
	count     int
	errors    int
	latencies []time.Duration
}

type pendingReq struct {
	ts     time.Time
	method string
}

type statsResult struct {
	ByMethod        map[string]*methodStats
	Notifs          map[string]int
	TransportCounts map[capture.Transport]int
	FirstTS, LastTS time.Time
	Connects        int
	Disconnects     int
	TotalFrames     int
	Unmatched       int
}

func computeStats(r io.Reader) (*statsResult, error) {
	sc := capture.NewRecordScanner(r)
	res := &statsResult{
		ByMethod:        map[string]*methodStats{},
		Notifs:          map[string]int{},
		TransportCounts: map[capture.Transport]int{},
	}
	pend := map[string]pendingReq{}

	for sc.Scan() {
		rec := sc.Record()
		if res.FirstTS.IsZero() && !rec.TS.IsZero() {
			res.FirstTS = rec.TS
		}
		if !rec.TS.IsZero() {
			res.LastTS = rec.TS
		}

		if rec.IsEvent() {
			switch rec.Event {
			case "connect":
				res.Connects++
			case "disconnect":
				res.Disconnects++
			}
			continue
		}

		res.TotalFrames++
		res.TransportCounts[rec.Transport]++

		kind, id, method := classifyFrame(rec.Payload)
		switch kind {
		case "req":
			pend[id] = pendingReq{ts: rec.TS, method: method}
			bucket(res.ByMethod, method).count++
		case "notif":
			res.Notifs[method]++
		case "resp", "err":
			p, ok := pend[id]
			if !ok {
				continue
			}
			delete(pend, id)
			s := bucket(res.ByMethod, p.method)
			s.latencies = append(s.latencies, rec.TS.Sub(p.ts))
			if kind == "err" {
				s.errors++
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	res.Unmatched = len(pend)
	return res, nil
}

func bucket(m map[string]*methodStats, name string) *methodStats {
	s, ok := m[name]
	if !ok {
		s = &methodStats{}
		m[name] = s
	}
	return s
}

func writeStatsText(w io.Writer, res *statsResult) {
	type row struct {
		method            string
		count             int
		errors            int
		p50, p95, p99, mx time.Duration
		hasLatency        bool
	}
	rows := make([]row, 0, len(res.ByMethod))
	for name, s := range res.ByMethod {
		sort.Slice(s.latencies, func(i, j int) bool { return s.latencies[i] < s.latencies[j] })
		rows = append(rows, row{
			method:     name,
			count:      s.count,
			errors:     s.errors,
			p50:        pct(s.latencies, 0.50),
			p95:        pct(s.latencies, 0.95),
			p99:        pct(s.latencies, 0.99),
			mx:         pct(s.latencies, 1.0),
			hasLatency: len(s.latencies) > 0,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].count > rows[j].count })

	fmt.Fprintln(w, "Requests:")
	fmt.Fprintf(w, "  %-40s %6s %8s %8s %8s %8s %8s\n", "method", "count", "errors", "p50", "p95", "p99", "max")
	if len(rows) == 0 {
		fmt.Fprintln(w, "  (none)")
	}
	for _, r := range rows {
		fmt.Fprintf(w, "  %-40s %6d %8d %8s %8s %8s %8s\n",
			truncStr(r.method, 40), r.count, r.errors,
			fmtDur(r.p50), fmtDur(r.p95), fmtDur(r.p99), fmtDur(r.mx))
	}

	if len(res.Notifs) > 0 {
		names := make([]string, 0, len(res.Notifs))
		for n := range res.Notifs {
			names = append(names, n)
		}
		sort.Slice(names, func(i, j int) bool { return res.Notifs[names[i]] > res.Notifs[names[j]] })
		fmt.Fprintln(w, "\nNotifications:")
		fmt.Fprintf(w, "  %-40s %6s\n", "method", "count")
		for _, n := range names {
			fmt.Fprintf(w, "  %-40s %6d\n", truncStr(n, 40), res.Notifs[n])
		}
	}

	fmt.Fprintln(w, "\nSummary:")
	fmt.Fprintf(w, "  frames:       %d\n", res.TotalFrames)
	fmt.Fprintf(w, "  unmatched:    %d (requests without a response in capture)\n", res.Unmatched)
	fmt.Fprintf(w, "  connects:     %d\n", res.Connects)
	fmt.Fprintf(w, "  disconnects:  %d\n", res.Disconnects)
	fmt.Fprintf(w, "  duration:     %s\n", fmtDur(res.LastTS.Sub(res.FirstTS)))
	fmt.Fprint(w, "  transports:  ")
	tNames := make([]capture.Transport, 0, len(res.TransportCounts))
	for t := range res.TransportCounts {
		tNames = append(tNames, t)
	}
	sort.Slice(tNames, func(i, j int) bool { return tNames[i] < tNames[j] })
	for _, t := range tNames {
		fmt.Fprintf(w, " %s=%d", t, res.TransportCounts[t])
	}
	fmt.Fprintln(w)
}

func writeStatsJSON(w io.Writer, res *statsResult) error {
	type methodRow struct {
		Method string   `json:"method"`
		Count  int      `json:"count"`
		Errors int      `json:"errors"`
		P50    *float64 `json:"p50_ms,omitempty"`
		P95    *float64 `json:"p95_ms,omitempty"`
		P99    *float64 `json:"p99_ms,omitempty"`
		Max    *float64 `json:"max_ms,omitempty"`
	}
	type notifRow struct {
		Method string `json:"method"`
		Count  int    `json:"count"`
	}
	type summary struct {
		Frames      int            `json:"frames"`
		Unmatched   int            `json:"unmatched"`
		Connects    int            `json:"connects"`
		Disconnects int            `json:"disconnects"`
		DurationMS  float64        `json:"duration_ms"`
		Transports  map[string]int `json:"transports"`
	}
	type out struct {
		Requests      []methodRow `json:"requests"`
		Notifications []notifRow  `json:"notifications,omitempty"`
		Summary       summary     `json:"summary"`
	}

	rows := make([]methodRow, 0, len(res.ByMethod))
	for name, s := range res.ByMethod {
		sort.Slice(s.latencies, func(i, j int) bool { return s.latencies[i] < s.latencies[j] })
		row := methodRow{Method: name, Count: s.count, Errors: s.errors}
		if len(s.latencies) > 0 {
			p50 := durToMS(pct(s.latencies, 0.50))
			p95 := durToMS(pct(s.latencies, 0.95))
			p99 := durToMS(pct(s.latencies, 0.99))
			mx := durToMS(pct(s.latencies, 1.0))
			row.P50, row.P95, row.P99, row.Max = &p50, &p95, &p99, &mx
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Count > rows[j].Count })

	notifs := make([]notifRow, 0, len(res.Notifs))
	for n, c := range res.Notifs {
		notifs = append(notifs, notifRow{Method: n, Count: c})
	}
	sort.Slice(notifs, func(i, j int) bool { return notifs[i].Count > notifs[j].Count })

	transports := map[string]int{}
	for t, c := range res.TransportCounts {
		transports[string(t)] = c
	}

	result := out{
		Requests:      rows,
		Notifications: notifs,
		Summary: summary{
			Frames:      res.TotalFrames,
			Unmatched:   res.Unmatched,
			Connects:    res.Connects,
			Disconnects: res.Disconnects,
			DurationMS:  durToMS(res.LastTS.Sub(res.FirstTS)),
			Transports:  transports,
		},
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

func durToMS(d time.Duration) float64 {
	return float64(d.Nanoseconds()) / 1e6
}

func pct(d []time.Duration, p float64) time.Duration {
	if len(d) == 0 {
		return 0
	}
	idx := int(float64(len(d)-1) * p)
	return d[idx]
}

func fmtDur(d time.Duration) string {
	switch {
	case d == 0:
		return "-"
	case d < time.Microsecond:
		return fmt.Sprintf("%dns", d.Nanoseconds())
	case d < time.Millisecond:
		return fmt.Sprintf("%dµs", d.Microseconds())
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	default:
		return d.Round(time.Millisecond).String()
	}
}

func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
