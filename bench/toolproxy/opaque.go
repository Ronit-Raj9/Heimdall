package toolproxy

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"mime"
	"net/http"
	"net/http/httputil"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// defaultParseCap is the most the proxy keeps in memory to parse one
// message: one request body, one JSON answer, or one event of a stream. MCP
// messages are small except for images (a full-page screenshot as base64 is
// a few MiB), so 16 MiB leaves room for those while a handful of concurrent
// requests stays far below any sane container limit. Traffic over the cap is
// still relayed byte for byte, streamed, and recorded as an opaque row whose
// hash and byte count cover all of it.
const defaultParseCap = 16 << 20

// bodyWait bounds how long the proxy waits, after an answer was relayed,
// for the transport to finish reading the request body it forwarded, so
// the request's hash and byte count are complete. It only matters when a
// server answers before it has read the whole request.
const bodyWait = time.Second

// meter hashes and counts every byte written to it. The transport's body
// goroutine and the handler may use it at once.
type meter struct {
	mu sync.Mutex
	h  hash.Hash
	n  int64
}

func newMeter() *meter { return &meter{h: sha256.New()} }

// Write implements io.Writer; it never fails.
func (m *meter) Write(p []byte) (int, error) {
	m.mu.Lock()
	m.h.Write(p)
	m.n += int64(len(p))
	m.mu.Unlock()
	return len(p), nil
}

// sum returns the hex sha256 and the byte count so far.
func (m *meter) sum() (string, int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return hex.EncodeToString(m.h.Sum(nil)), m.n
}

// signalBody is a request body that says when the transport closed it,
// which it always does once it has sent the body or given up.
type signalBody struct {
	r    io.Reader
	once sync.Once
	done chan struct{}
}

func newSignalBody(r io.Reader) *signalBody { return &signalBody{r: r, done: make(chan struct{})} }

// Read implements io.Reader. The end of the body, or a read error, counts as
// done too: not every caller passes Close on.
func (b *signalBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if err != nil {
		b.Close()
	}
	return n, err
}

// Close implements io.Closer.
func (b *signalBody) Close() error {
	b.once.Do(func() { close(b.done) })
	return nil
}

// wait returns once the body was closed, or after d.
func (b *signalBody) wait(d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-b.done:
	case <-t.C:
	}
}

// teeBody is an answer body whose bytes also go to w as they are read.
type teeBody struct {
	io.Reader
	c io.Closer
}

func (t teeBody) Close() error { return t.c.Close() }

// trackWriter remembers the first error writing to the agent, so a row can
// say the agent did not get all of the answer.
type trackWriter struct {
	http.ResponseWriter
	err error
}

func (t *trackWriter) Write(p []byte) (int, error) {
	n, err := t.ResponseWriter.Write(p)
	if err != nil && t.err == nil {
		t.err = err
	}
	return n, err
}

// Flush implements http.Flusher.
func (t *trackWriter) Flush() {
	if f, ok := t.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the real writer.
func (t *trackWriter) Unwrap() http.ResponseWriter { return t.ResponseWriter }

// relayed is what a recorded relay learned about the answer.
type relayed struct {
	status      int
	contentType string
	sha         string
	n           int64
	undelivered bool
	err         string // the transport failed: no answer from the server
}

// relayRecorded forwards a request as it is and streams the answer back
// unchanged, metering the answer. body is the request body to send (nil for
// none). onAnswer, when set, sees the answer and returns a writer
// that receives every answer byte as it is relayed (an event stream
// splitter), or nil.
func (s *Server) relayRecorded(w http.ResponseWriter, r *http.Request, rt Route, path string, body io.Reader, onAnswer func(resp *http.Response) io.Writer) relayed {
	target := s.targets[rt.Service]
	out := relayed{}
	ans := newMeter()
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme, pr.Out.URL.Host = target.Scheme, target.Host
			pr.Out.URL.Path, pr.Out.URL.RawPath = strings.TrimRight(target.Path, "/")+path, ""
			pr.Out.Host = target.Host
		},
		Transport:     s.client.Transport,
		FlushInterval: -1,
		ModifyResponse: func(resp *http.Response) error {
			out.status, out.contentType = resp.StatusCode, resp.Header.Get("Content-Type")
			var sink io.Writer = ans
			if onAnswer != nil {
				if extra := onAnswer(resp); extra != nil {
					sink = io.MultiWriter(ans, extra)
				}
			}
			resp.Body = teeBody{Reader: io.TeeReader(resp.Body, sink), c: resp.Body}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			out.status, out.err = http.StatusBadGateway, "tool server unreachable: "+transportError(err)
			http.Error(w, out.err, http.StatusBadGateway)
		},
	}
	r2 := r.Clone(r.Context())
	var sb *signalBody
	if body != nil {
		sb = newSignalBody(body)
		r2.Body = sb
	} else {
		r2.Body = http.NoBody
		r2.ContentLength = 0
	}
	tw := &trackWriter{ResponseWriter: w}
	rp.ServeHTTP(tw, r2)
	if sb != nil {
		sb.wait(bodyWait)
	}
	out.sha, out.n = ans.sum()
	out.undelivered = tw.err != nil
	return out
}

// baseRow is the start of a row for a request that is not a parsed tool
// call: the route, the HTTP method and path, the request's content type.
func (s *Server) baseRow(rt Route, r *http.Request, path, kind, tool string) schemas.ToolCall {
	return schemas.ToolCall{
		Server: rt.Service, Tool: tool, Protocol: rt.Protocol, Kind: kind, Started: s.now(),
		HTTPMethod: r.Method, Path: path, ContentType: r.Header.Get("Content-Type"),
	}
}

// finishRow fills the request and answer side of a relayed row and writes it.
func (s *Server) finishRow(row *schemas.ToolCall, req *meter, res relayed) {
	if req != nil {
		row.ArgsSHA256, row.RequestBytes = req.sum()
	} else {
		row.ArgsSHA256 = emptySHA
	}
	row.ResultSHA256, row.ResponseBytes = res.sha, res.n
	row.Status, row.ResponseContentType = res.status, res.contentType
	row.IsError = res.status >= 400 || res.err != ""
	if res.err != "" {
		row.Result, row.ResultSHA256 = Capture([]byte(res.err))
	}
	row.Undelivered = res.undelivered
	row.LatencyMS = s.now().Sub(row.Started).Milliseconds()
	s.append(row)
}

// forwardOpaque relays a request the proxy could not parse, unchanged, and
// records it as one opaque row with its reason. body is the whole request
// body to send (what was already read, then the rest); req meters every
// byte of it as it is read.
func (s *Server) forwardOpaque(w http.ResponseWriter, r *http.Request, rt Route, path string, body io.Reader, req *meter, reason string) {
	s.logger.Debug("tool traffic relayed unparsed", "service", rt.Service, "reason", reason)
	row := s.baseRow(rt, r, path, schemas.ToolRowOpaque, schemas.ToolRowUnparsedTool)
	row.Unparsed, row.UnparsedReason = true, reason
	res := s.relayRecorded(w, r, rt, path, body, nil)
	s.finishRow(&row, req, res)
}

// emptySHA is the sha256 of no bytes.
var emptySHA = func() string { s := sha256.Sum256(nil); return hex.EncodeToString(s[:]) }()

// contentEncoding returns the body's content coding, lower case, or "" for
// identity.
func contentEncoding(h http.Header) string {
	enc := strings.ToLower(strings.TrimSpace(h.Get("Content-Encoding")))
	if enc == "identity" {
		return ""
	}
	return enc
}

var errDecodedTooLarge = errors.New("decoded body over the parse cap")

// decodeBody undoes a gzip or deflate content coding for parsing, reading
// at most limit decoded bytes (a small compressed body can expand without
// bound). Other codings are not read.
func decodeBody(raw []byte, enc string, limit int64) ([]byte, error) {
	var rd io.Reader
	switch enc {
	case "gzip", "x-gzip":
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		rd = zr
	case "deflate":
		// RFC 9110 deflate is zlib; some servers send raw deflate.
		if zr, err := zlib.NewReader(bytes.NewReader(raw)); err == nil {
			rd = zr
		} else {
			rd = flate.NewReader(bytes.NewReader(raw))
		}
	default:
		return nil, fmt.Errorf("content encoding %q is not decoded", enc)
	}
	b, err := readUpTo(rd, limit)
	if errors.Is(err, errTooLarge) {
		return nil, errDecodedTooLarge
	}
	return b, err
}

// decodeReader undoes a gzip or deflate content coding of a stream.
func decodeReader(r io.Reader, enc string) (io.Reader, error) {
	switch enc {
	case "gzip", "x-gzip":
		return gzip.NewReader(r)
	case "deflate":
		return flate.NewReader(r), nil
	}
	return nil, fmt.Errorf("content encoding %q is not decoded", enc)
}

// notJSONType returns the media type of a request whose declared content
// type is not JSON, or "" when it is JSON or not declared.
func notJSONType(contentType string) string {
	if strings.TrimSpace(contentType) == "" {
		return ""
	}
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return contentType
	}
	if mt == "application/json" || strings.HasSuffix(mt, "+json") || mt == "text/json" {
		return ""
	}
	return mt
}

// sseSplitter receives the bytes of an event stream as they are relayed and
// hands each complete event to onEvent, keeping at most limit bytes of one
// event. An event over the limit is not kept: onOver gets its hash and size
// once it ends. It never fails, so it can sit on a tee.
type sseSplitter struct {
	limit    int
	event    []byte // the current event, while under the limit
	line     []byte // the current line, while under the limit
	over     bool   // the current event went over the limit
	nonEmpty bool   // the current line has bytes (kept or not)
	big      *meter // meters the current event once it is over the limit
	onEvent  func(ev []byte)
	onOver   func(sha string, n int64)
}

// Write implements io.Writer.
func (s *sseSplitter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		chunk := p
		if i >= 0 {
			chunk = p[:i+1]
		}
		p = p[len(chunk):]
		s.take(chunk, i >= 0)
	}
	return n, nil
}

// take adds one piece of a line; ended says the piece ends the line.
func (s *sseSplitter) take(chunk []byte, ended bool) {
	blank := ended && !s.nonEmpty && len(bytes.TrimRight(chunk, "\r\n")) == 0
	if blank {
		s.endEvent(chunk)
		return
	}
	s.nonEmpty = true
	if s.over {
		s.big.Write(chunk)
	} else {
		s.line = append(s.line, chunk...)
		if len(s.event)+len(s.line) > s.limit {
			s.over = true
			s.big = newMeter()
			s.big.Write(s.event)
			s.big.Write(s.line)
			s.event, s.line = nil, nil
		}
	}
	if ended {
		if !s.over {
			s.event = append(s.event, s.line...)
		}
		s.line, s.nonEmpty = s.line[:0], false
	}
}

// endEvent closes the current event at its blank line.
func (s *sseSplitter) endEvent(end []byte) {
	switch {
	case s.over:
		s.big.Write(end)
		sha, n := s.big.sum()
		if s.onOver != nil {
			s.onOver(sha, n)
		}
	case len(s.event) > 0 && s.onEvent != nil:
		s.onEvent(s.event)
	}
	s.event, s.line, s.over, s.big, s.nonEmpty = nil, nil, false, nil, false
}

// eventOf splits the bytes of one event into the lines an sseEvent holds.
func eventOf(b []byte) sseEvent {
	var ev sseEvent
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			ev.lines = append(ev.lines, b)
			break
		}
		ev.lines = append(ev.lines, b[:i+1])
		b = b[i+1:]
	}
	return ev
}
