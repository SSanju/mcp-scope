package main

import (
	"strings"
	"testing"
)

func schemaFromJSONL(s string) captureSchema {
	grouped := map[string][]requestPair{}
	for _, p := range loadRequestPairs(strings.NewReader(s), "") {
		grouped[p.Method] = append(grouped[p.Method], p)
	}
	return extractSchema(grouped)
}

const captureToolA = `{"ts":"2024-01-01T00:00:00Z","dir":"c2s","transport":"stdio","frame":{"jsonrpc":"2.0","id":1,"method":"tools/list"}}
{"ts":"2024-01-01T00:00:01Z","dir":"s2c","transport":"stdio","frame":{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"search","description":"Search","inputSchema":{"type":"object","properties":{"q":{"type":"string","description":"query"}},"required":["q"]}}]}}}`

const captureToolB_ExtraRequired = `{"ts":"2024-01-01T00:00:00Z","dir":"c2s","transport":"stdio","frame":{"jsonrpc":"2.0","id":1,"method":"tools/list"}}
{"ts":"2024-01-01T00:00:01Z","dir":"s2c","transport":"stdio","frame":{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"search","description":"Search","inputSchema":{"type":"object","properties":{"q":{"type":"string","description":"query"},"mode":{"type":"string","description":"mode"}},"required":["q","mode"]}}]}}}`

const captureToolB_Removed = `{"ts":"2024-01-01T00:00:00Z","dir":"c2s","transport":"stdio","frame":{"jsonrpc":"2.0","id":1,"method":"tools/list"}}
{"ts":"2024-01-01T00:00:01Z","dir":"s2c","transport":"stdio","frame":{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}}`

const captureToolB_NewTool = `{"ts":"2024-01-01T00:00:00Z","dir":"c2s","transport":"stdio","frame":{"jsonrpc":"2.0","id":1,"method":"tools/list"}}
{"ts":"2024-01-01T00:00:01Z","dir":"s2c","transport":"stdio","frame":{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"search","description":"Search","inputSchema":{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}},{"name":"fetch","description":"Fetch a URL","inputSchema":{"type":"object","properties":{"url":{"type":"string"}},"required":["url"]}}]}}}`

const captureToolB_DescChanged = `{"ts":"2024-01-01T00:00:00Z","dir":"c2s","transport":"stdio","frame":{"jsonrpc":"2.0","id":1,"method":"tools/list"}}
{"ts":"2024-01-01T00:00:01Z","dir":"s2c","transport":"stdio","frame":{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"search","description":"Search the web","inputSchema":{"type":"object","properties":{"q":{"type":"string","description":"query"}},"required":["q"]}}]}}}`

func TestBuildSchemaDiff_Clean(t *testing.T) {
	a := schemaFromJSONL(captureToolA)
	rep := buildSchemaDiff("a", "b", a, a)
	if !rep.Clean {
		t.Errorf("identical schemas should be clean, got %d breaking", rep.Breaking)
	}
	if rep.Breaking+rep.Safe+rep.Info != 0 {
		t.Errorf("expected zero changes, got breaking=%d safe=%d info=%d", rep.Breaking, rep.Safe, rep.Info)
	}
}

func TestBuildSchemaDiff_RequiredParamAdded(t *testing.T) {
	a := schemaFromJSONL(captureToolA)
	b := schemaFromJSONL(captureToolB_ExtraRequired)
	rep := buildSchemaDiff("a", "b", a, b)
	if rep.Breaking == 0 {
		t.Error("expected BREAKING change: required param added")
	}
	if rep.Clean {
		t.Error("expected not clean when breaking changes exist")
	}
}

func TestBuildSchemaDiff_ToolRemoved(t *testing.T) {
	a := schemaFromJSONL(captureToolA)
	b := schemaFromJSONL(captureToolB_Removed)
	rep := buildSchemaDiff("a", "b", a, b)
	if rep.Breaking == 0 {
		t.Error("expected BREAKING change: tool removed")
	}
}

func TestBuildSchemaDiff_NewToolIsSafe(t *testing.T) {
	a := schemaFromJSONL(captureToolA)
	b := schemaFromJSONL(captureToolB_NewTool)
	rep := buildSchemaDiff("a", "b", a, b)
	if rep.Breaking != 0 {
		t.Errorf("new tool should not be breaking, got %d breaking", rep.Breaking)
	}
	if rep.Safe == 0 {
		t.Error("expected SAFE change: new tool added")
	}
	if !rep.Clean {
		t.Error("expected clean (no breaking changes)")
	}
}

func TestBuildSchemaDiff_DescriptionIsInfo(t *testing.T) {
	a := schemaFromJSONL(captureToolA)
	b := schemaFromJSONL(captureToolB_DescChanged)
	rep := buildSchemaDiff("a", "b", a, b)
	if rep.Breaking != 0 {
		t.Errorf("description change should not be breaking, got %d", rep.Breaking)
	}
	if rep.Info == 0 {
		t.Error("expected INFO change: description changed")
	}
}

func TestExtractSchemaLastOccurrence(t *testing.T) {
	// Two tools/list responses in one capture — extractSchema must use the last one.
	capture := `{"ts":"2024-01-01T00:00:00Z","dir":"c2s","transport":"stdio","frame":{"jsonrpc":"2.0","id":1,"method":"tools/list"}}
{"ts":"2024-01-01T00:00:01Z","dir":"s2c","transport":"stdio","frame":{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"old_tool","description":"Old"}]}}}
{"ts":"2024-01-01T00:00:02Z","dir":"c2s","transport":"stdio","frame":{"jsonrpc":"2.0","id":2,"method":"tools/list"}}
{"ts":"2024-01-01T00:00:03Z","dir":"s2c","transport":"stdio","frame":{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"new_tool","description":"New"}]}}}`

	schema := schemaFromJSONL(capture)
	if len(schema.Tools) != 1 {
		t.Fatalf("want 1 tool, got %d", len(schema.Tools))
	}
	if schema.Tools[0].Name != "new_tool" {
		t.Errorf("want new_tool (last occurrence), got %s", schema.Tools[0].Name)
	}
}
