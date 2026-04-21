package capture

import (
	"bufio"
	"encoding/json"
	"io"
	"time"
)

// Record is one line of a capture file, parsed. Exactly one of IsFrame() or
// IsEvent() is true for a valid record.
type Record struct {
	TS        time.Time         `json:"ts"`
	Dir       Direction         `json:"dir,omitempty"`
	Transport Transport         `json:"transport,omitempty"`
	Payload   json.RawMessage   `json:"frame,omitempty"`
	Event     string            `json:"event,omitempty"`
	Meta      map[string]string `json:"meta,omitempty"`
}

func (r *Record) IsFrame() bool { return len(r.Payload) > 0 }
func (r *Record) IsEvent() bool { return r.Event != "" }

// RecordScanner streams records from a JSONL capture file.
type RecordScanner struct {
	sc  *bufio.Scanner
	rec Record
}

func NewRecordScanner(r io.Reader) *RecordScanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	return &RecordScanner{sc: sc}
}

func (s *RecordScanner) Scan() bool {
	s.rec = Record{}
	if !s.sc.Scan() {
		return false
	}
	if err := json.Unmarshal(s.sc.Bytes(), &s.rec); err != nil {
		s.rec = Record{
			Event: "invalid_line",
			Meta:  map[string]string{"err": err.Error()},
		}
	}
	return true
}

func (s *RecordScanner) Record() Record { return s.rec }
func (s *RecordScanner) Err() error     { return s.sc.Err() }
