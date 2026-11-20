package toolproxy

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// scriptedMCP answers each request with the function for its method (or
// for "GET" and the HTTP method of a non-POST), so a test can script exactly
// what the server sends.
type scriptedMCP struct {
	t      *testing.T
	byRPC  map[string]http.HandlerFunc
	byHTTP map[string]http.HandlerFunc
	// gotBody and gotEncoding are the last POST body and Content-Encoding
	// the server received, as sent.
	gotBody     []byte
	gotEncoding string
}

func (s *scriptedMCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h, ok := s.byHTTP[r.Method]; ok {
		h(w, r)
		return
	}
	raw, _ := io.ReadAll(r.Body)
	s.gotBody, s.gotEncoding = raw, r.Header.Get("Content-Encoding")
	body := raw
	switch r.Header.Get("Content-Encoding") {
	case "gzip":
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			http.Error(w, "bad gzip", 400)
			return
		}
		body, _ = io.ReadAll(zr)
	}
	items, _, ok := parseRPC(body)
	if !ok || len(items) == 0 {
		http.Error(w, "parse error", http.StatusBadRequest)
		return
	}
	if h, ok := s.byRPC[items[0].msg.Method]; ok {
		r.Body = io.NopCloser(bytes.NewReader(body))
		h(w, r)
		return
	}
	if items[0].msg.Method == "" || idKey(items[0].msg.ID) == "" {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"ok":true}}`, items[0].msg.ID)
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// rawPost posts body with headers and returns the status, headers and the
// raw answer bytes (compressed answers are not undone).
func rawPost(t *testing.T, url string, body io.Reader, size int64, hdr map[string]string) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, body)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = size
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	tr := &http.Transport{DisableCompression: true}
	defer tr.CloseIdleConnections()
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, b
}

// TestRecordEveryMCPMessageKind checks that the handshake, listings,
// resources, prompts, ping, notifications, answers to server requests, and
// the server's own requests and notifications each get a row of their own
// kind, and that only tools/call is a tool call.
func TestRecordEveryMCPMessageKind(t *testing.T) {
	up := &scriptedMCP{t: t, byRPC: map[string]http.HandlerFunc{
		"tools/call": func(w http.ResponseWriter, r *http.Request) {
			// While the call runs the server asks the client for a sampling
			// and reports progress, then answers.
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":\"s1\",\"method\":\"sampling/createMessage\",\"params\":{\"messages\":[]}}\n\n")
			fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{\"progress\":1}}\n\n")
			fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":9,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"done\"}]}}\n\n")
		},
	}, byHTTP: map[string]http.HandlerFunc{
		http.MethodGet: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, ": keep-alive\n\n")
			fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"id\":\"e1\",\"method\":\"elicitation/create\",\"params\":{\"message\":\"name?\"}}\n\n")
			fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/tools/list_changed\"}\n\n")
			fmt.Fprint(w, "data: not json at all\n\n")
		},
		http.MethodDelete: func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) },
	}}
	upstream := httptest.NewServer(up)
	defer upstream.Close()
	tp := newTestProxy(t, upstream.URL, ProtocolMCP)
	mcp := tp.front.URL + "/s/fs/mcp"
	for _, b := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"resources/list"}`,
		`{"jsonrpc":"2.0","id":4,"method":"resources/read","params":{"uri":"file:///a"}}`,
		`{"jsonrpc":"2.0","id":5,"method":"prompts/get","params":{"name":"p"}}`,
		`{"jsonrpc":"2.0","id":6,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"echo","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":"s1","result":{"role":"assistant","content":{"type":"text","text":"ok"}}}`,
	} {
		post(t, tp.front.URL, "/s/fs/mcp", b, nil)
	}
	resp, err := http.Get(mcp)
	if err != nil {
		t.Fatal(err)
	}
	stream, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(stream), "elicitation/create") || !strings.Contains(string(stream), "not json at all") {
		t.Fatalf("GET stream not relayed whole: %q", stream)
	}
	req, _ := http.NewRequest(http.MethodDelete, mcp, nil)
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
	}

	rows := tp.waitRows(t, 16)
	type want struct{ kind, tool string }
	wants := []want{
		{schemas.ToolRowMCPMethod, "initialize"},
		{schemas.ToolRowNotification, "notifications/initialized"},
		{schemas.ToolRowMCPMethod, "tools/list"},
		{schemas.ToolRowMCPMethod, "resources/list"},
		{schemas.ToolRowMCPMethod, "resources/read"},
		{schemas.ToolRowMCPMethod, "prompts/get"},
		{schemas.ToolRowMCPMethod, "ping"},
		{schemas.ToolRowServerRequest, "sampling/createMessage"},
		{schemas.ToolRowNotification, "notifications/progress"},
		{"", "echo"},
		{schemas.ToolRowResponse, "response"},
		{schemas.ToolRowServerRequest, "elicitation/create"},
		{schemas.ToolRowNotification, "notifications/tools/list_changed"},
		{schemas.ToolRowTransport, "GET /mcp"},
		{schemas.ToolRowTransport, "DELETE /mcp"},
	}
	if len(rows) != len(wants) {
		for _, r := range rows {
			t.Logf("row %d %s %s", r.Seq, r.Kind, r.Tool)
		}
		t.Fatalf("got %d rows, want %d", len(rows), len(wants))
	}
	calls := 0
	for i, w := range wants {
		r := rows[i]
		if r.Kind != w.kind || r.Tool != w.tool || r.Server != "fs" || r.Protocol != ProtocolMCP {
			t.Errorf("row %d = %s %s, want %s %s", i, r.Kind, r.Tool, w.kind, w.tool)
		}
		if r.IsToolCall() {
			calls++
		}
	}
	if calls != 1 {
		t.Errorf("%d rows count as tool calls, want only tools/call", calls)
	}
	if !strings.Contains(rows[4].Arguments, "file:///a") || !strings.Contains(rows[0].Result, "ok") {
		t.Errorf("resources/read or initialize row lost its content: %+v %+v", rows[4], rows[0])
	}
	if !strings.Contains(rows[9].Result, "done") || rows[9].IsError {
		t.Errorf("tool call row = %+v", rows[9])
	}
	if g := rows[13]; !g.Unparsed || !strings.Contains(g.UnparsedReason, "1 events") || g.ResultSHA256 != sum(stream) || g.ResponseBytes != int64(len(stream)) {
		t.Errorf("GET stream row = %+v", g)
	}
	if rows[1].Status != http.StatusAccepted || rows[1].IsError {
		t.Errorf("notification row = %+v", rows[1])
	}
}

// patternReader yields n bytes of a fixed pattern without holding them, and
// hashes what it yields.
type patternReader struct {
	n, off int64
	h      hash.Hash
}

func (p *patternReader) Read(b []byte) (int, error) {
	if p.off >= p.n {
		return 0, io.EOF
	}
	if int64(len(b)) > p.n-p.off {
		b = b[:p.n-p.off]
	}
	for i := range b {
		b[i] = byte('a' + (p.off+int64(i))%26)
	}
	p.off += int64(len(b))
	p.h.Write(b)
	return len(b), nil
}

// TestRecordStreamedHugeBody sends a 50 MiB request body through the proxy,
// once with a Content-Length and once chunked, and checks that it reaches
// the server whole, that the opaque row's hash and count cover every byte,
// and that the proxy's memory stays far below the body's size.
func TestRecordStreamedHugeBody(t *testing.T) {
	const size = 50 << 20
	for _, chunked := range []bool{false, true} {
		t.Run(fmt.Sprintf("chunked=%v", chunked), func(t *testing.T) {
			var gotN int64
			gotH := sha256.New()
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotN, _ = io.Copy(gotH, r.Body)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"ok":true}`)
			}))
			defer upstream.Close()
			tp := newTestProxy(t, upstream.URL, ProtocolMCP)
			tp.srv.parseCap = 1 << 20
			src := &patternReader{n: size, h: sha256.New()}
			length := int64(size)
			if chunked {
				length = -1
			}
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			status, _, _ := rawPost(t, tp.front.URL+"/s/fs/mcp", src, length, map[string]string{"Content-Type": "application/json"})
			runtime.ReadMemStats(&after)
			if status != 200 || gotN != size {
				t.Fatalf("status %d, server got %d bytes", status, gotN)
			}
			// Everything allocated during the request, client and server
			// sides included, stays far below the body: it streamed.
			alloc := after.TotalAlloc - before.TotalAlloc
			t.Logf("allocated %.1f MiB while relaying %d MiB", float64(alloc)/(1<<20), size>>20)
			if alloc > 16<<20 {
				t.Errorf("allocated %d MiB for a %d MiB body", alloc>>20, size>>20)
			}
			want := hex.EncodeToString(src.h.Sum(nil))
			if hex.EncodeToString(gotH.Sum(nil)) != want {
				t.Fatal("server got different bytes")
			}
			rows := tp.waitRows(t, 1)
			if len(rows) != 1 {
				t.Fatalf("rows = %+v", rows)
			}
			r := rows[0]
			if r.Kind != schemas.ToolRowOpaque || !r.Unparsed || !strings.Contains(r.UnparsedReason, "parse cap") || r.ArgsSHA256 != want || r.RequestBytes != size || r.Status != 200 || r.ResponseBytes != 11 || r.IsToolCall() {
				t.Errorf("row = %+v", r)
			}
		})
	}
}

// TestRecordChunkedSmallBody checks that a chunked request under the cap is
// parsed like any other.
func TestRecordChunkedSmallBody(t *testing.T) {
	up := &fakeMCP{}
	upstream := httptest.NewServer(up)
	defer upstream.Close()
	tp := newTestProxy(t, upstream.URL, ProtocolMCP)
	status, _, b := rawPost(t, tp.front.URL+"/s/fs/mcp", io.MultiReader(strings.NewReader(callBody(1, "echo", "chunk"))), -1, map[string]string{"Content-Type": "application/json"})
	if status != 200 || !strings.Contains(string(b), "echo: chunk") {
		t.Fatalf("status %d body %q", status, b)
	}
	rows := tp.rows(t)
	if len(rows) != 1 || rows[0].Tool != "echo" || rows[0].Unparsed || !rows[0].IsToolCall() {
		t.Fatalf("rows = %+v", rows)
	}
}

// TestRecordHugeAnswers checks answers over the parse cap: a plain JSON
// answer, and an event of an answer stream, are relayed whole, hashed and
// counted, and the call's row says its result was not parsed instead of
// claiming the call failed. A huge event that answers nothing gets an opaque
// row of its own.
func TestRecordHugeAnswers(t *testing.T) {
	big := strings.Repeat("x", 3<<20)
	bigAnswer := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":%q}]}}`, big)
	tests := []struct {
		name    string
		handler http.HandlerFunc
		check   func(t *testing.T, rows []schemas.ToolCall, got []byte)
	}{
		{name: "json answer", handler: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, bigAnswer)
		}, check: func(t *testing.T, rows []schemas.ToolCall, got []byte) {
			if string(got) != bigAnswer {
				t.Fatal("answer changed on the way")
			}
			if len(rows) != 1 || !rows[0].Unparsed || rows[0].IsError || rows[0].ResultSHA256 != sum([]byte(bigAnswer)) || rows[0].ResponseBytes != int64(len(bigAnswer)) || !rows[0].IsToolCall() {
				t.Fatalf("rows = %+v", rows)
			}
		}},
		{name: "huge answer event", handler: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", bigAnswer)
		}, check: func(t *testing.T, rows []schemas.ToolCall, got []byte) {
			ev := "event: message\ndata: " + bigAnswer + "\n\n"
			if string(got) != ev {
				t.Fatalf("stream changed on the way: %d bytes, want %d", len(got), len(ev))
			}
			if len(rows) != 2 {
				t.Fatalf("rows = %d", len(rows))
			}
			op, call := rows[0], rows[1]
			if op.Kind != schemas.ToolRowOpaque || !op.Unparsed || op.ResultSHA256 != sum([]byte(ev)) || op.ResponseBytes != int64(len(ev)) {
				t.Errorf("opaque row = %+v", op)
			}
			if call.Tool != "echo" || !call.Unparsed || call.IsError || call.ResultSHA256 != op.ResultSHA256 {
				t.Errorf("call row = %+v", call)
			}
		}},
		{name: "huge stray event then answer", handler: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: %s\n\n", big)
			fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"content\":[]}}\n\n")
		}, check: func(t *testing.T, rows []schemas.ToolCall, got []byte) {
			if len(got) != len(big)+8+len("data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"content\":[]}}\n\n") {
				t.Fatalf("stream changed on the way: %d bytes", len(got))
			}
			if len(rows) != 2 || rows[0].Kind != schemas.ToolRowOpaque || rows[1].Unparsed || rows[1].IsError {
				t.Fatalf("rows = %+v", rows)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(tt.handler)
			defer upstream.Close()
			tp := newTestProxy(t, upstream.URL, ProtocolMCP)
			tp.srv.parseCap = 1 << 20
			_, _, got := rawPost(t, tp.front.URL+"/s/fs/mcp", strings.NewReader(callBody(1, "echo", "x")), -1, map[string]string{"Content-Type": "application/json"})
			tt.check(t, tp.waitRows(t, 1), got)
		})
	}
}

// TestRecordBinaryAndUnknownBodies checks bodies the proxy does not parse:
// each is forwarded unchanged and recorded as an opaque row with a reason.
func TestRecordBinaryAndUnknownBodies(t *testing.T) {
	binary := []byte{0x00, 0xff, 0x10, 0x80, 0x7f, 0x00}
	tests := []struct {
		name   string
		body   []byte
		hdr    map[string]string
		reason string
	}{
		{name: "binary octet stream", body: binary, hdr: map[string]string{"Content-Type": "application/octet-stream"}, reason: "content type application/octet-stream"},
		{name: "binary, no type", body: binary, reason: "not JSON-RPC"},
		{name: "plain text", body: []byte("tools/call echo"), hdr: map[string]string{"Content-Type": "text/plain"}, reason: "content type text/plain"},
		{name: "brotli", body: []byte("\x1b\x00"), hdr: map[string]string{"Content-Type": "application/json", "Content-Encoding": "br"}, reason: "content encoding br"},
		{name: "broken gzip", body: []byte("not gzip"), hdr: map[string]string{"Content-Type": "application/json", "Content-Encoding": "gzip"}, reason: "content encoding gzip"},
		{name: "empty batch", body: []byte("[]"), hdr: map[string]string{"Content-Type": "application/json"}, reason: "empty JSON-RPC batch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []byte
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got, _ = io.ReadAll(r.Body)
				w.WriteHeader(http.StatusUnsupportedMediaType)
			}))
			defer upstream.Close()
			tp := newTestProxy(t, upstream.URL, ProtocolMCP)
			status, _, _ := rawPost(t, tp.front.URL+"/s/fs/mcp", bytes.NewReader(tt.body), int64(len(tt.body)), tt.hdr)
			if status != http.StatusUnsupportedMediaType || !bytes.Equal(got, tt.body) {
				t.Fatalf("status %d, server got %q", status, got)
			}
			rows := tp.waitRows(t, 1)
			if len(rows) != 1 {
				t.Fatalf("rows = %+v", rows)
			}
			r := rows[0]
			if r.Kind != schemas.ToolRowOpaque || r.Tool != schemas.ToolRowUnparsedTool || !r.Unparsed || !strings.Contains(r.UnparsedReason, tt.reason) || r.ArgsSHA256 != sum(tt.body) || r.RequestBytes != int64(len(tt.body)) || r.Status != http.StatusUnsupportedMediaType || !r.IsError {
				t.Errorf("row = %+v", r)
			}
		})
	}
}

// TestRecordCompressedBodies checks gzip and deflate: a compressed request
// is parsed and forwarded as sent; a compressed answer is parsed and relayed
// as sent; a fault still matches a compressed call.
func TestRecordCompressedBodies(t *testing.T) {
	gz := func(s string) []byte {
		var b bytes.Buffer
		zw := gzip.NewWriter(&b)
		_, _ = zw.Write([]byte(s))
		_ = zw.Close()
		return b.Bytes()
	}
	deflated := func(s string) []byte {
		var b bytes.Buffer
		zw := zlib.NewWriter(&b)
		_, _ = zw.Write([]byte(s))
		_ = zw.Close()
		return b.Bytes()
	}
	t.Run("gzip request", func(t *testing.T) {
		up := &scriptedMCP{t: t}
		upstream := httptest.NewServer(up)
		defer upstream.Close()
		tp := newTestProxy(t, upstream.URL, ProtocolMCP)
		body := gz(callBody(7, "echo", "zipped"))
		status, _, _ := rawPost(t, tp.front.URL+"/s/fs/mcp", bytes.NewReader(body), int64(len(body)), map[string]string{"Content-Type": "application/json", "Content-Encoding": "gzip"})
		if status != 200 || !bytes.Equal(up.gotBody, body) || up.gotEncoding != "gzip" {
			t.Fatalf("status %d, server got %d bytes encoded %q", status, len(up.gotBody), up.gotEncoding)
		}
		rows := tp.rows(t)
		if len(rows) != 1 || rows[0].Tool != "echo" || rows[0].Arguments != `{"text":"zipped"}` || rows[0].Unparsed {
			t.Fatalf("rows = %+v", rows)
		}
	})
	t.Run("deflate answer", func(t *testing.T) {
		answer := deflated(`{"jsonrpc":"2.0","id":7,"result":{"content":[{"type":"text","text":"squeezed"}]}}`)
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Encoding", "deflate")
			_, _ = w.Write(answer)
		}))
		defer upstream.Close()
		tp := newTestProxy(t, upstream.URL, ProtocolMCP)
		status, hdr, got := rawPost(t, tp.front.URL+"/s/fs/mcp", strings.NewReader(callBody(7, "echo", "x")), -1, map[string]string{"Content-Type": "application/json"})
		if status != 200 || !bytes.Equal(got, answer) || hdr.Get("Content-Encoding") != "deflate" {
			t.Fatalf("status %d, answer changed: %q %q", status, got, hdr.Get("Content-Encoding"))
		}
		rows := tp.rows(t)
		if len(rows) != 1 || !strings.Contains(rows[0].Result, "squeezed") || rows[0].Unparsed || rows[0].IsError {
			t.Fatalf("rows = %+v", rows)
		}
	})
	t.Run("gzip answer", func(t *testing.T) {
		answer := gz(`{"jsonrpc":"2.0","id":7,"result":{"content":[{"type":"text","text":"zipped answer"}]}}`)
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Encoding", "gzip")
			_, _ = w.Write(answer)
		}))
		defer upstream.Close()
		tp := newTestProxy(t, upstream.URL, ProtocolMCP)
		_, _, body := post(t, tp.front.URL, "/s/fs/mcp", callBody(7, "echo", "x"), nil)
		if !strings.Contains(body, "zipped answer") {
			t.Fatalf("body %q", body)
		}
		rows := tp.rows(t)
		if len(rows) != 1 || !strings.Contains(rows[0].Result, "zipped answer") || rows[0].Unparsed {
			t.Fatalf("rows = %+v", rows)
		}
	})
	// HTTP deflate on an event stream is zlib-framed (RFC 9110); raw
	// deflate is the tolerated fallback. Both must decode end to end.
	for _, framing := range []string{"zlib", "raw"} {
		t.Run("deflate event stream "+framing, func(t *testing.T) {
			ev := "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":7,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"streamed\"}]}}\n\n"
			var b bytes.Buffer
			if framing == "zlib" {
				b.Write(deflated(ev))
			} else {
				fw, _ := flate.NewWriter(&b, flate.DefaultCompression)
				_, _ = fw.Write([]byte(ev))
				_ = fw.Close()
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Content-Encoding", "deflate")
				_, _ = w.Write(b.Bytes())
			}))
			defer upstream.Close()
			tp := newTestProxy(t, upstream.URL, ProtocolMCP)
			status, _, got := rawPost(t, tp.front.URL+"/s/fs/mcp", strings.NewReader(callBody(7, "echo", "x")), -1, map[string]string{"Content-Type": "application/json"})
			if status != 200 || string(got) != ev {
				t.Fatalf("status %d, stream %q", status, got)
			}
			rows := tp.waitRows(t, 1)
			if len(rows) != 1 || !strings.Contains(rows[0].Result, "streamed") || rows[0].Unparsed || rows[0].IsError {
				t.Fatalf("rows = %+v", rows)
			}
		})
	}
	t.Run("fault on a gzip batch", func(t *testing.T) {
		up := &scriptedMCP{t: t}
		upstream := httptest.NewServer(up)
		defer upstream.Close()
		tp := newTestProxy(t, upstream.URL, ProtocolMCP)
		tp.setFault(t, `{"mode":"error","tool":"fail","nonce":"n_z"}`)
		body := gz("[" + callBody(1, "fail", "") + "," + callBody(2, "echo", "x") + "]")
		_, _, got := rawPost(t, tp.front.URL+"/s/fs/mcp", bytes.NewReader(body), int64(len(body)), map[string]string{"Content-Type": "application/json", "Content-Encoding": "gzip"})
		if !strings.Contains(string(got), "n_z") {
			t.Fatalf("answer %q", got)
		}
		// The rebuilt batch reaches the server plain, without the faulted call.
		if up.gotEncoding != "" || strings.Contains(string(up.gotBody), `"fail"`) || !strings.Contains(string(up.gotBody), `"echo"`) {
			t.Fatalf("server got %q encoded %q", up.gotBody, up.gotEncoding)
		}
		rows := tp.rows(t)
		if len(rows) != 2 || rows[0].Fault != FaultError || rows[0].Nonce != "n_z" || rows[1].Tool != "echo" {
			t.Fatalf("rows = %+v", rows)
		}
	})
}

// TestRecordBatchElements checks that a batch element that is not a
// JSON-RPC message gets an opaque row while the others are recorded.
func TestRecordBatchElements(t *testing.T) {
	up := &fakeMCP{}
	upstream := httptest.NewServer(up)
	defer upstream.Close()
	tp := newTestProxy(t, upstream.URL, ProtocolMCP)
	post(t, tp.front.URL, "/s/fs/mcp", "["+callBody(1, "echo", "a")+`,42,{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":0}}]`, nil)
	rows := tp.rows(t)
	if len(rows) != 3 {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].Tool != "echo" || rows[1].Kind != schemas.ToolRowOpaque || !rows[1].Unparsed || rows[1].Arguments != "42" || rows[2].Kind != schemas.ToolRowNotification {
		t.Fatalf("rows = %+v", rows)
	}
}

// TestSSESplitter feeds a stream to the splitter in awkward pieces and
// checks the events and the oversized one.
func TestSSESplitter(t *testing.T) {
	big := strings.Repeat("y", 100)
	stream := "data: one\n\n: comment\n\ndata: " + big + "\ndata: more\n\nevent: x\r\ndata: two\r\n\r\n"
	var events []string
	var overSHA string
	var overN int64
	sp := &sseSplitter{limit: 50, onEvent: func(b []byte) {
		ev := eventOf(b)
		d, _ := ev.data()
		events = append(events, string(d))
	}, onOver: func(sha string, n int64) { overSHA, overN = sha, n }}
	for i := 0; i < len(stream); i += 7 {
		_, _ = sp.Write([]byte(stream[i:min(i+7, len(stream))]))
	}
	if got, _ := json.Marshal(events); string(got) != `["one","","two"]` {
		t.Errorf("events = %s", got)
	}
	ev := "data: " + big + "\ndata: more\n\n"
	if overN != int64(len(ev)) || overSHA != sum([]byte(ev)) {
		t.Errorf("oversized event: %d bytes %s, want %d", overN, overSHA, len(ev))
	}
}
