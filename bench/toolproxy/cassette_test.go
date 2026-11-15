package toolproxy

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeQuery(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "keys sorted", in: "b=2&a=1", want: "a=1&b=2"},
		{name: "values keep order", in: "k=2&k=1", want: "k=2&k=1"},
		{name: "escaping canonical", in: "q=a+b&r=%41", want: "q=a+b&r=A"},
		{name: "unparsable kept", in: "a=%zz", want: "a=%zz"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeQuery(tt.in); got != tt.want {
				t.Fatalf("NormalizeQuery(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestReadCassette(t *testing.T) {
	good := `{"method":"GET","path":"/rates","query":"a=1","body_sha256":"x","status":200,"body":"{}"}` + "\n\n"
	tests := []struct {
		name    string
		in      string
		want    int
		wantErr string
	}{
		{name: "good with blank line", in: good, want: 1},
		{name: "empty", in: "", want: 0},
		{name: "torn line", in: good + `{"method":"GET"`, wantErr: "cassette line 3"},
		{name: "no status", in: `{"method":"GET","path":"/x","body":""}`, wantErr: "needs a method, a path and a status"},
		{name: "bad base64", in: `{"method":"GET","path":"/x","status":200,"body":"***","base64":true}`, wantErr: "decode cassette body"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ReadCassette(strings.NewReader(tt.in))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || len(got) != tt.want {
				t.Fatalf("got %d entries, err %v; want %d", len(got), err, tt.want)
			}
		})
	}
}

func TestCassetteMatch(t *testing.T) {
	h := http.Header{"Content-Type": {"application/json"}}
	c := newCassette([]CassetteEntry{
		NewCassetteEntry("GET", "/rates", "b=2&a=1", nil, 200, h, []byte(`{"n":1}`)),
		NewCassetteEntry("GET", "/rates", "a=1&b=2", nil, 200, h, []byte(`{"n":2}`)),
		NewCassetteEntry("POST", "/convert", "", []byte(`{"amount":5}`), 201, h, []byte(`{"ok":true}`)),
	})
	tests := []struct {
		name, method, path, query, body, want string
		ok                                    bool
	}{
		{name: "first of two", method: "GET", path: "/rates", query: "a=1&b=2", want: `{"n":1}`, ok: true},
		{name: "second of two, query order ignored", method: "GET", path: "/rates", query: "b=2&a=1", want: `{"n":2}`, ok: true},
		{name: "last repeats", method: "GET", path: "/rates", query: "a=1&b=2", want: `{"n":2}`, ok: true},
		{name: "body matches", method: "POST", path: "/convert", body: `{"amount":5}`, want: `{"ok":true}`, ok: true},
		{name: "other body", method: "POST", path: "/convert", body: `{"amount":6}`},
		{name: "other method", method: "DELETE", path: "/rates", query: "a=1&b=2"},
		{name: "other query", method: "GET", path: "/rates", query: "a=9"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, ok := c.match(tt.method, tt.path, tt.query, []byte(tt.body))
			if ok != tt.ok {
				t.Fatalf("match ok = %v, want %v", ok, tt.ok)
			}
			if ok && e.Body != tt.want {
				t.Fatalf("body = %q, want %q", e.Body, tt.want)
			}
		})
	}
}

func TestNewCassetteEntry(t *testing.T) {
	h := http.Header{"Content-Type": {"image/png"}, "Set-Cookie": {"s=1"}, "Date": {"today"}, "Location": {"/next"}}
	bin := []byte{0x89, 'P', 'N', 'G', 0xff, 0xfe}
	e := NewCassetteEntry("GET", "/logo", "", nil, 200, h, bin)
	if !e.Base64 {
		t.Fatal("binary body not base64")
	}
	got, err := e.BodyBytes()
	if err != nil || !bytes.Equal(got, bin) {
		t.Fatalf("round trip = %v, %v", got, err)
	}
	if len(e.Headers) != 2 || e.Headers["Content-Type"] != "image/png" || e.Headers["Location"] != "/next" {
		t.Fatalf("headers = %v, want only Content-Type and Location", e.Headers)
	}
	if e.BodySHA256 != sha256Hex(nil) {
		t.Fatalf("body hash = %s", e.BodySHA256)
	}
	text := NewCassetteEntry("GET", "/t", "", nil, 200, http.Header{}, []byte("héllo"))
	if text.Base64 || text.Body != "héllo" || text.Headers != nil {
		t.Fatalf("text entry = %+v", text)
	}
}

func TestCassetteWriter(t *testing.T) {
	p := filepath.Join(t.TempDir(), "external", "acme.cassette.jsonl")
	w, err := openCassetteWriter(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(p); err != nil || fi.Size() != 0 {
		t.Fatalf("cassette not created empty: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := w.Append(NewCassetteEntry("GET", "/rates", "", nil, 200, http.Header{}, []byte("{}"))); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	c, err := loadCassette(p)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(c.byKey); n != 1 {
		t.Fatalf("keys = %d, want 1", n)
	}
	if _, err := loadCassette(filepath.Join(t.TempDir(), "missing.jsonl")); err == nil {
		t.Fatal("missing cassette loaded")
	}
}
