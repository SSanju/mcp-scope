package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
)

// ── MCP schema types ──────────────────────────────────────────────────────────

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type mcpResource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MimeType    string `json:"mimeType"`
}

type mcpPrompt struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Arguments   []promptArg `json:"arguments"`
}

type promptArg struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

type captureSchema struct {
	Tools     []mcpTool
	Resources []mcpResource
	Prompts   []mcpPrompt
}

// ── Schema change types ───────────────────────────────────────────────────────

type schemaChange struct {
	Category string `json:"category"` // BREAKING | SAFE | INFO
	Item     string `json:"item"`
	Change   string `json:"change"`
}

type schemaDiffReport struct {
	FileA    string         `json:"file_a"`
	FileB    string         `json:"file_b"`
	Changes  []schemaChange `json:"changes"`
	Breaking int            `json:"breaking"`
	Safe     int            `json:"safe"`
	Info     int            `json:"info"`
	Clean    bool           `json:"clean"`
	NoSchema bool           `json:"no_schema,omitempty"`
}

// ── Frame diff types (--frames mode) ─────────────────────────────────────────

type diffEntry struct {
	Method   string        `json:"method"`
	Index    int           `json:"index"`
	Status   string        `json:"status"` // match|differ|only_a|only_b
	ReqDiff  bool          `json:"req_diff,omitempty"`
	RespDiff bool          `json:"resp_diff,omitempty"`
	A        *pairSnapshot `json:"a,omitempty"`
	B        *pairSnapshot `json:"b,omitempty"`
}

type pairSnapshot struct {
	Request  json.RawMessage `json:"request,omitempty"`
	Response json.RawMessage `json:"response,omitempty"`
}

type frameDiffReport struct {
	FileA   string      `json:"file_a"`
	FileB   string      `json:"file_b"`
	Entries []diffEntry `json:"entries"`
	Match   int         `json:"match"`
	Differ  int         `json:"differ"`
	OnlyA   int         `json:"only_a"`
	OnlyB   int         `json:"only_b"`
}

// ── Entry point ───────────────────────────────────────────────────────────────

func runDiff(args []string) int {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON report")
	verbose := fs.Bool("v", false, "show payload details (frame mode) or full schema (schema mode)")
	framesMode := fs.Bool("frames", false, "compare raw frame sequences instead of schemas")
	method := fs.String("method", "", "restrict frame diff to this method (implies --frames)")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `Usage: mcp-scope diff [flags] <capture-a.jsonl> <capture-b.jsonl>

Schema mode (default): extracts tools/list, resources/list, and prompts/list
declarations from each capture and classifies every change as BREAKING, SAFE,
or INFO. Exits 1 if any breaking change is found.

Frame mode (--frames): compares raw request/response pairs by (method, index).

Flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return 2
	}

	pairsA, err := loadPairsFromFile(fs.Arg(0), *method)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	pairsB, err := loadPairsFromFile(fs.Arg(1), *method)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	if *framesMode || *method != "" {
		rep := buildFrameDiff(fs.Arg(0), fs.Arg(1), pairsA, pairsB)
		if *jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			enc.Encode(rep)
		} else {
			printFrameDiff(os.Stdout, rep, *verbose)
		}
		if rep.Differ > 0 || rep.OnlyA > 0 || rep.OnlyB > 0 {
			return 1
		}
		return 0
	}

	schemaA := extractSchema(pairsA)
	schemaB := extractSchema(pairsB)
	rep := buildSchemaDiff(fs.Arg(0), fs.Arg(1), schemaA, schemaB)

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(rep)
	} else {
		printSchemaDiff(os.Stdout, rep, *verbose)
	}
	if !rep.Clean {
		return 1
	}
	return 0
}

// ── Schema extraction ─────────────────────────────────────────────────────────

func extractSchema(grouped map[string][]requestPair) captureSchema {
	var s captureSchema

	// Use the last occurrence of each list response — covers reconnect scenarios
	// where tools/list is called multiple times and only the final state matters.
	if pairs := grouped["tools/list"]; len(pairs) > 0 {
		last := pairs[len(pairs)-1]
		var resp struct {
			Result struct {
				Tools []mcpTool `json:"tools"`
			} `json:"result"`
		}
		if len(last.Response) > 0 && json.Unmarshal(last.Response, &resp) == nil {
			s.Tools = resp.Result.Tools
		}
	}

	if pairs := grouped["resources/list"]; len(pairs) > 0 {
		last := pairs[len(pairs)-1]
		var resp struct {
			Result struct {
				Resources []mcpResource `json:"resources"`
			} `json:"result"`
		}
		if len(last.Response) > 0 && json.Unmarshal(last.Response, &resp) == nil {
			s.Resources = resp.Result.Resources
		}
	}

	if pairs := grouped["prompts/list"]; len(pairs) > 0 {
		last := pairs[len(pairs)-1]
		var resp struct {
			Result struct {
				Prompts []mcpPrompt `json:"prompts"`
			} `json:"result"`
		}
		if len(last.Response) > 0 && json.Unmarshal(last.Response, &resp) == nil {
			s.Prompts = resp.Result.Prompts
		}
	}

	return s
}

// ── Schema diff ───────────────────────────────────────────────────────────────

func buildSchemaDiff(pathA, pathB string, a, b captureSchema) schemaDiffReport {
	hasSchema := len(a.Tools)+len(a.Resources)+len(a.Prompts)+
		len(b.Tools)+len(b.Resources)+len(b.Prompts) > 0
	rep := schemaDiffReport{FileA: pathA, FileB: pathB, Changes: []schemaChange{}, NoSchema: !hasSchema}

	rep.Changes = append(rep.Changes, diffTools(a.Tools, b.Tools)...)
	rep.Changes = append(rep.Changes, diffResources(a.Resources, b.Resources)...)
	rep.Changes = append(rep.Changes, diffPrompts(a.Prompts, b.Prompts)...)

	sort.Slice(rep.Changes, func(i, j int) bool {
		order := map[string]int{"BREAKING": 0, "SAFE": 1, "INFO": 2}
		if rep.Changes[i].Category != rep.Changes[j].Category {
			return order[rep.Changes[i].Category] < order[rep.Changes[j].Category]
		}
		return rep.Changes[i].Item < rep.Changes[j].Item
	})

	for _, c := range rep.Changes {
		switch c.Category {
		case "BREAKING":
			rep.Breaking++
		case "SAFE":
			rep.Safe++
		case "INFO":
			rep.Info++
		}
	}
	rep.Clean = rep.Breaking == 0
	return rep
}

func diffTools(a, b []mcpTool) []schemaChange {
	var changes []schemaChange
	aMap := indexTools(a)
	bMap := indexTools(b)

	for name := range aMap {
		if _, ok := bMap[name]; !ok {
			changes = append(changes, schemaChange{"BREAKING", "tools/call:" + name, "tool removed"})
		}
	}
	for name := range bMap {
		if _, ok := aMap[name]; !ok {
			changes = append(changes, schemaChange{"SAFE", "tools/call:" + name, "new tool"})
		}
	}
	for name, at := range aMap {
		bt, ok := bMap[name]
		if !ok {
			continue
		}
		changes = append(changes, diffToolSchema("tools/call:"+name, at, bt)...)
	}
	return changes
}

func diffToolSchema(item string, a, b mcpTool) []schemaChange {
	var changes []schemaChange
	if a.Description != b.Description {
		changes = append(changes, schemaChange{"INFO", item, "description changed"})
	}
	changes = append(changes, diffInputSchema(item, a.InputSchema, b.InputSchema)...)
	return changes
}

func diffInputSchema(item string, a, b map[string]any) []schemaChange {
	var changes []schemaChange
	aProps := schemaProps(a)
	bProps := schemaProps(b)
	aReq := schemaRequired(a)
	bReq := schemaRequired(b)

	for prop := range aProps {
		if _, ok := bProps[prop]; !ok {
			cat := "SAFE"
			if aReq[prop] {
				cat = "BREAKING"
			}
			changes = append(changes, schemaChange{cat, item + "." + prop, "param removed"})
		}
	}
	for prop := range bProps {
		if _, ok := aProps[prop]; !ok {
			cat := "SAFE"
			if bReq[prop] {
				cat = "BREAKING"
			}
			label := "optional param added"
			if bReq[prop] {
				label = "required param added"
			}
			changes = append(changes, schemaChange{cat, item + "." + prop, label})
		}
	}
	for prop, ap := range aProps {
		bp, ok := bProps[prop]
		if !ok {
			continue
		}
		if at, bt := propType(ap), propType(bp); at != bt && at != "" && bt != "" {
			changes = append(changes, schemaChange{
				"BREAKING", item + "." + prop,
				fmt.Sprintf("type changed %s→%s", at, bt),
			})
		}
		switch {
		case !aReq[prop] && bReq[prop]:
			changes = append(changes, schemaChange{"BREAKING", item + "." + prop, "param became required"})
		case aReq[prop] && !bReq[prop]:
			changes = append(changes, schemaChange{"SAFE", item + "." + prop, "param became optional"})
		}
		if propDesc(ap) != propDesc(bp) {
			changes = append(changes, schemaChange{"INFO", item + "." + prop, "param description changed"})
		}
	}
	return changes
}

func diffResources(a, b []mcpResource) []schemaChange {
	var changes []schemaChange
	aMap := map[string]mcpResource{}
	for _, r := range a {
		aMap[r.URI] = r
	}
	bMap := map[string]mcpResource{}
	for _, r := range b {
		bMap[r.URI] = r
	}
	for uri := range aMap {
		if _, ok := bMap[uri]; !ok {
			changes = append(changes, schemaChange{"BREAKING", "resource:" + uri, "resource removed"})
		}
	}
	for uri := range bMap {
		if _, ok := aMap[uri]; !ok {
			changes = append(changes, schemaChange{"SAFE", "resource:" + uri, "new resource"})
		}
	}
	for uri, ar := range aMap {
		br, ok := bMap[uri]
		if !ok {
			continue
		}
		if ar.MimeType != br.MimeType && ar.MimeType != "" && br.MimeType != "" {
			changes = append(changes, schemaChange{
				"BREAKING", "resource:" + uri,
				fmt.Sprintf("mimeType changed %s→%s", ar.MimeType, br.MimeType),
			})
		}
		if ar.Description != br.Description {
			changes = append(changes, schemaChange{"INFO", "resource:" + uri, "description changed"})
		}
	}
	return changes
}

func diffPrompts(a, b []mcpPrompt) []schemaChange {
	var changes []schemaChange
	aMap := map[string]mcpPrompt{}
	for _, p := range a {
		aMap[p.Name] = p
	}
	bMap := map[string]mcpPrompt{}
	for _, p := range b {
		bMap[p.Name] = p
	}
	for name := range aMap {
		if _, ok := bMap[name]; !ok {
			changes = append(changes, schemaChange{"BREAKING", "prompt:" + name, "prompt removed"})
		}
	}
	for name := range bMap {
		if _, ok := aMap[name]; !ok {
			changes = append(changes, schemaChange{"SAFE", "prompt:" + name, "new prompt"})
		}
	}
	for name, ap := range aMap {
		bp, ok := bMap[name]
		if !ok {
			continue
		}
		if ap.Description != bp.Description {
			changes = append(changes, schemaChange{"INFO", "prompt:" + name, "description changed"})
		}
		changes = append(changes, diffPromptArgs(name, ap.Arguments, bp.Arguments)...)
	}
	return changes
}

func diffPromptArgs(prompt string, a, b []promptArg) []schemaChange {
	var changes []schemaChange
	aMap := map[string]promptArg{}
	for _, arg := range a {
		aMap[arg.Name] = arg
	}
	bMap := map[string]promptArg{}
	for _, arg := range b {
		bMap[arg.Name] = arg
	}
	for name := range aMap {
		if _, ok := bMap[name]; !ok {
			changes = append(changes, schemaChange{"BREAKING", "prompt:" + prompt + "." + name, "argument removed"})
		}
	}
	for name, bArg := range bMap {
		if _, ok := aMap[name]; !ok {
			cat, label := "SAFE", "optional argument added"
			if bArg.Required {
				cat, label = "BREAKING", "required argument added"
			}
			changes = append(changes, schemaChange{cat, "prompt:" + prompt + "." + name, label})
		}
	}
	for name, aArg := range aMap {
		bArg, ok := bMap[name]
		if !ok {
			continue
		}
		switch {
		case !aArg.Required && bArg.Required:
			changes = append(changes, schemaChange{"BREAKING", "prompt:" + prompt + "." + name, "argument became required"})
		case aArg.Required && !bArg.Required:
			changes = append(changes, schemaChange{"SAFE", "prompt:" + prompt + "." + name, "argument became optional"})
		}
	}
	return changes
}

// ── Schema output ─────────────────────────────────────────────────────────────

func printSchemaDiff(w io.Writer, rep schemaDiffReport, verbose bool) {
	fmt.Fprintf(w, "Schema diff  A: %s\n             B: %s\n\n", rep.FileA, rep.FileB)

	if len(rep.Changes) == 0 {
		if rep.NoSchema {
			fmt.Fprintln(w, "  (no tools/resources/prompts declarations found in either capture)")
			fmt.Fprintln(w, "  Tip: run capture while the client calls tools/list, resources/list, or prompts/list")
			fmt.Fprintln(w, "  Use --frames to compare raw frame sequences instead")
		} else {
			fmt.Fprintln(w, "  No changes.")
		}
	} else {
		for _, c := range rep.Changes {
			fmt.Fprintf(w, "  %-8s  %-45s  %s\n", c.Category, truncStr(c.Item, 45), c.Change)
		}
	}

	fmt.Fprintf(w, "\n%d breaking, %d safe, %d info", rep.Breaking, rep.Safe, rep.Info)
	if rep.Clean {
		fmt.Fprintln(w, " — no breaking changes")
	} else {
		fmt.Fprintln(w, " — BREAKING CHANGES DETECTED")
	}

	if verbose && len(rep.Changes) > 0 {
		fmt.Fprintln(w, "\nNote: use --frames to compare raw request/response payloads")
	}
}

// ── Frame diff (--frames mode) ────────────────────────────────────────────────

func buildFrameDiff(pathA, pathB string, pairsA, pairsB map[string][]requestPair) frameDiffReport {
	rep := frameDiffReport{FileA: pathA, FileB: pathB, Entries: []diffEntry{}}

	methods := map[string]bool{}
	for m := range pairsA {
		methods[m] = true
	}
	for m := range pairsB {
		methods[m] = true
	}
	sorted := make([]string, 0, len(methods))
	for m := range methods {
		sorted = append(sorted, m)
	}
	sort.Strings(sorted)

	for _, method := range sorted {
		as := pairsA[method]
		bs := pairsB[method]
		n := max(len(as), len(bs))
		for i := range n {
			entry := diffEntry{Method: method, Index: i}
			hasA, hasB := i < len(as), i < len(bs)
			switch {
			case hasA && hasB:
				rq := !normalEqual(as[i].Request, bs[i].Request)
				rs := !normalEqual(as[i].Response, bs[i].Response)
				entry.ReqDiff, entry.RespDiff = rq, rs
				if !rq && !rs {
					entry.Status = "match"
					rep.Match++
				} else {
					entry.Status = "differ"
					entry.A = &pairSnapshot{as[i].Request, as[i].Response}
					entry.B = &pairSnapshot{bs[i].Request, bs[i].Response}
					rep.Differ++
				}
			case hasA:
				entry.Status = "only_a"
				entry.A = &pairSnapshot{as[i].Request, as[i].Response}
				rep.OnlyA++
			default:
				entry.Status = "only_b"
				entry.B = &pairSnapshot{bs[i].Request, bs[i].Response}
				rep.OnlyB++
			}
			rep.Entries = append(rep.Entries, entry)
		}
	}
	return rep
}

func printFrameDiff(w io.Writer, rep frameDiffReport, verbose bool) {
	fmt.Fprintf(w, "Frame diff  A: %s\n            B: %s\n\n", rep.FileA, rep.FileB)

	for _, e := range rep.Entries {
		label := e.Method
		if e.Index > 0 {
			label = fmt.Sprintf("%s [%d]", e.Method, e.Index+1)
		}
		marker := map[string]string{
			"match": "  MATCH ", "differ": "  DIFFER",
			"only_a": "  ONLY_A", "only_b": "  ONLY_B",
		}[e.Status]
		extra := ""
		if e.ReqDiff {
			extra += " req↕"
		}
		if e.RespDiff {
			extra += " resp↕"
		}
		fmt.Fprintf(w, "%s  %s%s\n", marker, label, extra)

		if verbose && e.Status == "differ" && e.A != nil && e.B != nil {
			if e.ReqDiff {
				fmt.Fprintf(w, "          A req:  %s\n", compact(e.A.Request))
				fmt.Fprintf(w, "          B req:  %s\n", compact(e.B.Request))
			}
			if e.RespDiff {
				fmt.Fprintf(w, "          A resp: %s\n", compact(e.A.Response))
				fmt.Fprintf(w, "          B resp: %s\n", compact(e.B.Response))
			}
		} else if verbose && (e.Status == "only_a" || e.Status == "only_b") {
			snap := e.A
			if snap == nil {
				snap = e.B
			}
			fmt.Fprintf(w, "          req: %s\n", compact(snap.Request))
		}
	}
	fmt.Fprintf(w, "\n%d match, %d differ, %d only in A, %d only in B\n",
		rep.Match, rep.Differ, rep.OnlyA, rep.OnlyB)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func indexTools(tools []mcpTool) map[string]mcpTool {
	m := make(map[string]mcpTool, len(tools))
	for _, t := range tools {
		m[t.Name] = t
	}
	return m
}

func schemaProps(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	props, _ := schema["properties"].(map[string]any)
	return props
}

func schemaRequired(schema map[string]any) map[string]bool {
	out := map[string]bool{}
	if schema == nil {
		return out
	}
	req, _ := schema["required"].([]any)
	for _, r := range req {
		if s, ok := r.(string); ok {
			out[s] = true
		}
	}
	return out
}

func propType(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	t, _ := m["type"].(string)
	return t
}

func propDesc(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	d, _ := m["description"].(string)
	return d
}
