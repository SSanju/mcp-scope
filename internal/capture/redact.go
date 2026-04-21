package capture

import (
	"encoding/json"
	"strings"
)

var defaultSensitiveKeys = map[string]bool{
	"authorization":  true,
	"api_key":        true,
	"apikey":         true,
	"api-key":        true,
	"token":          true,
	"access_token":   true,
	"refresh_token":  true,
	"id_token":       true,
	"password":       true,
	"passwd":         true,
	"secret":         true,
	"client_secret":  true,
	"x-api-key":      true,
}

// Redactor scrubs sensitive key/value pairs from JSON payloads and meta maps.
type Redactor struct {
	keys map[string]bool
}

// NewRedactor returns a Redactor using the built-in sensitive key list plus
// any extra keys the caller supplies (case-insensitive).
func NewRedactor(extra []string) *Redactor {
	keys := make(map[string]bool, len(defaultSensitiveKeys)+len(extra))
	for k := range defaultSensitiveKeys {
		keys[k] = true
	}
	for _, k := range extra {
		keys[strings.ToLower(strings.TrimSpace(k))] = true
	}
	return &Redactor{keys: keys}
}

func (r *Redactor) sensitive(key string) bool {
	return r.keys[strings.ToLower(key)]
}

// Payload walks the JSON tree and replaces values of sensitive keys with
// "[REDACTED]". Returns the original bytes on any parse/marshal error.
func (r *Redactor) Payload(payload json.RawMessage) json.RawMessage {
	var v any
	if err := json.Unmarshal(payload, &v); err != nil {
		return payload
	}
	r.walk(&v)
	out, err := json.Marshal(v)
	if err != nil {
		return payload
	}
	return out
}

func (r *Redactor) walk(v *any) {
	switch val := (*v).(type) {
	case map[string]any:
		for k, child := range val {
			if r.sensitive(k) {
				val[k] = "[REDACTED]"
			} else {
				r.walk(&child)
				val[k] = child
			}
		}
	case []any:
		for i := range val {
			r.walk(&val[i])
		}
	}
}

// Meta returns a copy of the meta map with sensitive values replaced.
func (r *Redactor) Meta(meta map[string]string) map[string]string {
	if len(meta) == 0 {
		return meta
	}
	out := make(map[string]string, len(meta))
	for k, v := range meta {
		if r.sensitive(k) {
			out[k] = "[REDACTED]"
		} else {
			out[k] = v
		}
	}
	return out
}
