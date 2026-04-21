package capture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const maxBodySize = 16 << 20 // 16 MB

// ProxyHTTP runs a reverse proxy on listen, forwarding to upstream, recording
// every JSON-RPC frame in both directions. Handles both application/json
// responses and text/event-stream responses transparently.
func ProxyHTTP(upstream, listen string, rec *Recorder) error {
	u, err := url.Parse(upstream)
	if err != nil {
		return fmt.Errorf("invalid upstream: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("upstream must include scheme and host, got %q", upstream)
	}

	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = u.Scheme
			pr.Out.URL.Host = u.Host
			if u.Path != "" && u.Path != "/" {
				pr.Out.URL.Path = u.Path
				pr.Out.URL.RawPath = u.RawPath
			}
			pr.Out.Host = u.Host
		},
		ModifyResponse: func(resp *http.Response) error {
			wrapResponseForCapture(resp, rec)
			return nil
		},
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captureRequest(r, rec)
		rp.ServeHTTP(w, r)
	})

	srv := &http.Server{Addr: listen, Handler: handler}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	rec.Event("connect", map[string]string{
		"mode":     "http",
		"listen":   listen,
		"upstream": upstream,
	})
	defer rec.Event("disconnect", map[string]string{"mode": "http"})

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func captureRequest(r *http.Request, rec *Recorder) {
	meta := requestMeta(r)
	if r.Method != http.MethodPost || r.Body == nil {
		rec.Event("http_request", meta)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodySize+1))
	r.Body.Close()
	if err != nil {
		rec.Event("error", map[string]string{"dir": "c2s", "err": err.Error()})
		r.Body = io.NopCloser(bytes.NewReader(body))
		return
	}
	if len(body) > maxBodySize {
		rec.Event("body_truncated", meta)
		body = body[:maxBodySize]
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	recordJSONBody(DirC2S, TransportHTTP, body, meta, rec)
}

func requestMeta(r *http.Request) map[string]string {
	m := map[string]string{
		"method": r.Method,
		"path":   r.URL.Path,
	}
	if sid := r.Header.Get("Mcp-Session-Id"); sid != "" {
		m["session_id"] = sid
	}
	return m
}

func wrapResponseForCapture(resp *http.Response, rec *Recorder) {
	ct := resp.Header.Get("Content-Type")
	meta := map[string]string{
		"http_status": strconv.Itoa(resp.StatusCode),
	}
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		meta["session_id"] = sid
	}
	switch {
	case strings.HasPrefix(ct, "text/event-stream"):
		resp.Body = newSSETee(resp.Body, DirS2C, TransportSSE, meta, rec)
	case strings.HasPrefix(ct, "application/json"):
		resp.Body = newJSONTee(resp.Body, DirS2C, TransportHTTP, meta, rec)
	default:
		rec.Event("non_mcp_response", map[string]string{
			"content_type": ct,
			"http_status":  strconv.Itoa(resp.StatusCode),
		})
	}
}

func recordJSONBody(dir Direction, transport Transport, body []byte, baseMeta map[string]string, rec *Recorder) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return
	}
	if body[0] == '[' {
		var batch []json.RawMessage
		if err := json.Unmarshal(body, &batch); err != nil {
			rec.Frame(dir, transport, body, baseMeta)
			return
		}
		batchID := strconv.FormatInt(time.Now().UnixNano(), 10)
		for i, elem := range batch {
			m := map[string]string{"batch_id": batchID, "batch_index": strconv.Itoa(i)}
			for k, v := range baseMeta {
				m[k] = v
			}
			rec.Frame(dir, transport, elem, m)
		}
		return
	}
	rec.Frame(dir, transport, body, baseMeta)
}

type jsonTee struct {
	r         io.ReadCloser
	buf       bytes.Buffer
	dir       Direction
	transport Transport
	meta      map[string]string
	rec       *Recorder
	recorded  bool
}

func newJSONTee(r io.ReadCloser, dir Direction, transport Transport, meta map[string]string, rec *Recorder) *jsonTee {
	return &jsonTee{r: r, dir: dir, transport: transport, meta: meta, rec: rec}
}

func (t *jsonTee) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if n > 0 {
		t.buf.Write(p[:n])
	}
	if err != nil {
		t.flush()
	}
	return n, err
}

func (t *jsonTee) Close() error {
	t.flush()
	return t.r.Close()
}

func (t *jsonTee) flush() {
	if t.recorded {
		return
	}
	t.recorded = true
	recordJSONBody(t.dir, t.transport, t.buf.Bytes(), t.meta, t.rec)
}

type sseTee struct {
	r      io.ReadCloser
	parser *sseParser
	rec    *Recorder
	meta   map[string]string
	closed bool
}

func newSSETee(r io.ReadCloser, dir Direction, transport Transport, meta map[string]string, rec *Recorder) *sseTee {
	t := &sseTee{r: r, rec: rec, meta: meta}
	t.parser = &sseParser{
		onEvent: func(eventName string, data []byte) {
			m := map[string]string{}
			for k, v := range meta {
				m[k] = v
			}
			if eventName != "" {
				m["sse_event"] = eventName
			}
			rec.Frame(dir, transport, data, m)
		},
	}
	return t
}

func (t *sseTee) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if n > 0 {
		t.parser.Feed(p[:n])
	}
	return n, err
}

func (t *sseTee) Close() error {
	if !t.closed {
		t.closed = true
		if partial := t.parser.Flush(); len(partial) > 0 {
			raw := partial
			if len(raw) > 256 {
				raw = raw[:256]
			}
			m := map[string]string{"partial_sse": string(raw)}
			for k, v := range t.meta {
				m[k] = v
			}
			t.rec.Event("disconnect_partial", m)
		}
	}
	return t.r.Close()
}

// sseParser implements a minimal W3C EventSource line parser.
// Only event and data fields are consumed; id and retry are ignored.
type sseParser struct {
	buf       []byte
	data      []byte
	eventName string
	onEvent   func(eventName string, data []byte)
}

func (p *sseParser) Feed(b []byte) {
	p.buf = append(p.buf, b...)
	for {
		i := bytes.IndexByte(p.buf, '\n')
		if i < 0 {
			return
		}
		line := p.buf[:i]
		p.buf = p.buf[i+1:]
		line = bytes.TrimRight(line, "\r")
		p.processLine(line)
	}
}

func (p *sseParser) processLine(line []byte) {
	if len(line) == 0 {
		if len(p.data) > 0 {
			data := make([]byte, len(p.data))
			copy(data, p.data)
			p.onEvent(p.eventName, data)
		}
		p.data = p.data[:0]
		p.eventName = ""
		return
	}
	if line[0] == ':' {
		return
	}
	colon := bytes.IndexByte(line, ':')
	var field, value []byte
	if colon < 0 {
		field = line
	} else {
		field = line[:colon]
		value = line[colon+1:]
		if len(value) > 0 && value[0] == ' ' {
			value = value[1:]
		}
	}
	switch string(field) {
	case "data":
		if len(p.data) > 0 {
			p.data = append(p.data, '\n')
		}
		p.data = append(p.data, value...)
	case "event":
		p.eventName = string(value)
	}
}

func (p *sseParser) Flush() []byte {
	out := p.buf
	p.buf = nil
	return out
}
