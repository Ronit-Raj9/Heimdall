package agentevents

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func w4b08Collector(t *testing.T, lim Limits) (*Collector, *httptest.Server) {
	t.Helper()
	c, err := NewCollector(filepath.Join(t.TempDir(), LedgerName), lim)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(c)
	t.Cleanup(func() { srv.Close(); c.Close() })
	return c, srv
}

func w4b08Post(t *testing.T, url, ct, enc string, body []byte) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	if enc != "" {
		req.Header.Set("Content-Encoding", enc)
	}
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

func w4b08Gzip(b []byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(b)
	_ = zw.Close()
	return buf.Bytes()
}

func TestCollectorAcceptsGzipOTLP(t *testing.T) {
	c, srv := w4b08Collector(t, Limits{})
	status, _ := w4b08Post(t, srv.URL+PathOTLP, "application/json", "gzip", w4b08Gzip([]byte(otlpJSON)))
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if got := c.Counts(); got.Kept != 5 || got.Dropped != 0 {
		t.Fatalf("counts %+v", got)
	}
}

func TestCollectorRejectsUnknownEncoding(t *testing.T) {
	_, srv := w4b08Collector(t, Limits{})
	status, raw := w4b08Post(t, srv.URL+PathOTLP, "application/json", "br", []byte(otlpJSON))
	if status != http.StatusUnsupportedMediaType || !strings.Contains(string(raw), "content encoding br is not supported") {
		t.Fatalf("status %d body %q", status, raw)
	}
}

func TestCollectorReportsPartialSuccess(t *testing.T) {
	_, srv := w4b08Collector(t, Limits{MaxEvents: 2})
	status, raw := w4b08Post(t, srv.URL+PathOTLP, "application/json", "", []byte(otlpJSON))
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	var js struct {
		PartialSuccess struct {
			RejectedSpans string `json:"rejectedSpans"`
			ErrorMessage  string `json:"errorMessage"`
		} `json:"partialSuccess"`
	}
	if err := json.Unmarshal(raw, &js); err != nil || js.PartialSuccess.RejectedSpans != "3" || js.PartialSuccess.ErrorMessage == "" {
		t.Fatalf("json answer %q", raw)
	}

	_, srv2 := w4b08Collector(t, Limits{MaxEvents: 2})
	status, raw = w4b08Post(t, srv2.URL+PathOTLP, "application/x-protobuf", "", protoRequest())
	if status != http.StatusOK {
		t.Fatalf("proto status %d", status)
	}
	top, err := pbFields(raw)
	if err != nil || len(top) != 1 || top[0].n != 1 || top[0].wire != wireBytes {
		t.Fatalf("proto answer %x %v", raw, err)
	}
	inner, err := pbFields(top[0].buf)
	if err != nil || len(inner) != 2 || inner[0].n != 1 || inner[0].num != 3 || inner[1].n != 2 || string(inner[1].buf) != js.PartialSuccess.ErrorMessage {
		t.Fatalf("proto inner %+v %v", inner, err)
	}
}

func TestCollectorGzipBombStops(t *testing.T) {
	_, srv := w4b08Collector(t, Limits{MaxBody: 4096})
	bomb := w4b08Gzip(bytes.Repeat([]byte(" "), 1<<20))
	if len(bomb) > 4096 {
		t.Fatalf("bomb too large to pass the raw cap: %d", len(bomb))
	}
	status, _ := w4b08Post(t, srv.URL+PathOTLP, "application/json", "gzip", bomb)
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d", status)
	}
}
