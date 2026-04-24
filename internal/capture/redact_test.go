package capture

import (
	"encoding/json"
	"testing"
)

func TestRedactorDefaults(t *testing.T) {
	r := NewRedactor(nil)
	payload := json.RawMessage(`{"authorization":"Bearer secret","data":"safe"}`)
	got := r.Payload(payload)
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatal(err)
	}
	if m["authorization"] != "[REDACTED]" {
		t.Errorf("authorization not redacted: %v", m["authorization"])
	}
	if m["data"] != "safe" {
		t.Errorf("data field changed: %v", m["data"])
	}
}

func TestRedactorCustomKeys(t *testing.T) {
	r := NewRedactor([]string{"my_secret"})
	payload := json.RawMessage(`{"my_secret":"value","other":"keep"}`)
	got := r.Payload(payload)
	var m map[string]any
	json.Unmarshal(got, &m)
	if m["my_secret"] != "[REDACTED]" {
		t.Errorf("custom key not redacted: %v", m["my_secret"])
	}
	if m["other"] != "keep" {
		t.Errorf("other field changed: %v", m["other"])
	}
}

func TestRedactorMeta(t *testing.T) {
	r := NewRedactor(nil)
	meta := map[string]string{"authorization": "Bearer secret", "host": "localhost"}
	got := r.Meta(meta)
	if got["authorization"] != "[REDACTED]" {
		t.Errorf("meta authorization not redacted: %v", got["authorization"])
	}
	if got["host"] != "localhost" {
		t.Errorf("host changed: %v", got["host"])
	}
}

func TestRedactorNestedFields(t *testing.T) {
	r := NewRedactor(nil)
	payload := json.RawMessage(`{"params":{"authorization":"secret","data":"safe"}}`)
	got := r.Payload(payload)
	var m map[string]any
	json.Unmarshal(got, &m)
	params := m["params"].(map[string]any)
	if params["authorization"] != "[REDACTED]" {
		t.Errorf("nested authorization not redacted: %v", params["authorization"])
	}
	if params["data"] != "safe" {
		t.Errorf("nested data changed: %v", params["data"])
	}
}

func TestRedactorCaseInsensitive(t *testing.T) {
	r := NewRedactor(nil)
	payload := json.RawMessage(`{"Authorization":"secret","API_KEY":"key"}`)
	got := r.Payload(payload)
	var m map[string]any
	json.Unmarshal(got, &m)
	if m["Authorization"] != "[REDACTED]" {
		t.Errorf("Authorization (mixed case) not redacted: %v", m["Authorization"])
	}
	if m["API_KEY"] != "[REDACTED]" {
		t.Errorf("API_KEY not redacted: %v", m["API_KEY"])
	}
}

func TestRedactorPreservesInvalidJSON(t *testing.T) {
	r := NewRedactor(nil)
	bad := json.RawMessage(`not json`)
	got := r.Payload(bad)
	if string(got) != "not json" {
		t.Errorf("invalid JSON should be returned as-is, got: %s", got)
	}
}
