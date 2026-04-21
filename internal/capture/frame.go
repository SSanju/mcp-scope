package capture

import (
	"encoding/json"
	"time"
)

type Direction string

const (
	DirC2S Direction = "c2s"
	DirS2C Direction = "s2c"
)

type Transport string

const (
	TransportStdio Transport = "stdio"
	TransportHTTP  Transport = "http"
	TransportSSE   Transport = "sse"
)

type Frame struct {
	TS        time.Time         `json:"ts"`
	Dir       Direction         `json:"dir"`
	Transport Transport         `json:"transport"`
	Payload   json.RawMessage   `json:"frame"`
	Meta      map[string]string `json:"meta,omitempty"`
}

type Event struct {
	TS    time.Time         `json:"ts"`
	Event string            `json:"event"`
	Meta  map[string]string `json:"meta,omitempty"`
}
