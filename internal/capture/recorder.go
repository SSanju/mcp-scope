package capture

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Recorder serializes captured frames and events to a JSONL sink via a
// background goroutine. Sends block when the queue is full; the first time
// that happens, a slow_writer event is emitted so the gap is visible.
type Recorder struct {
	ch       chan []byte
	done     chan struct{}
	w        *bufio.Writer
	closer   io.Closer
	slowOnce sync.Once
	Redactor *Redactor // optional; scrubs sensitive fields before writing
}

func NewRecorder(w io.WriteCloser, queue int) *Recorder {
	if queue < 1 {
		queue = 1024
	}
	r := &Recorder{
		ch:     make(chan []byte, queue),
		done:   make(chan struct{}),
		w:      bufio.NewWriter(w),
		closer: w,
	}
	go r.run()
	return r
}

func (r *Recorder) run() {
	defer close(r.done)
	for line := range r.ch {
		if _, err := r.w.Write(line); err != nil {
			fmt.Fprintf(os.Stderr, "mcp-scope: recorder write failed: %v\n", err)
		}
	}
	r.w.Flush()
}

func (r *Recorder) emit(v any) {
	line, err := json.Marshal(v)
	if err != nil {
		return
	}
	line = append(line, '\n')
	select {
	case r.ch <- line:
		return
	default:
	}
	r.slowOnce.Do(func() {
		slow, _ := json.Marshal(Event{TS: time.Now().UTC(), Event: "slow_writer"})
		r.ch <- append(slow, '\n')
	})
	r.ch <- line
}

func (r *Recorder) Frame(dir Direction, transport Transport, payload []byte, meta map[string]string) {
	if !json.Valid(payload) {
		m := map[string]string{"dir": string(dir)}
		raw := payload
		if len(raw) > 256 {
			raw = raw[:256]
		}
		m["raw"] = string(raw)
		for k, v := range meta {
			m[k] = v
		}
		r.Event("invalid_json", m)
		return
	}
	if r.Redactor != nil {
		payload = r.Redactor.Payload(payload)
		meta = r.Redactor.Meta(meta)
	}
	r.emit(Frame{
		TS:        time.Now().UTC(),
		Dir:       dir,
		Transport: transport,
		Payload:   json.RawMessage(payload),
		Meta:      meta,
	})
}

func (r *Recorder) Event(name string, meta map[string]string) {
	r.emit(Event{TS: time.Now().UTC(), Event: name, Meta: meta})
}

func (r *Recorder) Close() error {
	close(r.ch)
	<-r.done
	return r.closer.Close()
}
