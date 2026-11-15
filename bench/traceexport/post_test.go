package traceexport

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTracesURL(t *testing.T) {
	cases := []struct {
		in, want string
		bad      bool
	}{
		{"http://localhost:4318", "http://localhost:4318/v1/traces", false},
		{"http://localhost:4318/", "http://localhost:4318/v1/traces", false},
		{"https://cloud.langfuse.com/api/public/otel/v1/traces", "https://cloud.langfuse.com/api/public/otel/v1/traces", false},
		{"https://api.getmaxim.ai/v1/otel", "https://api.getmaxim.ai/v1/otel", false},
		{"ftp://x", "", true},
		{"http://", "", true},
		{"https://user:pass@host/v1/traces", "", true},
		{"::", "", true},
	}
	for _, c := range cases {
		got, err := TracesURL(c.in)
		if (err != nil) != c.bad || got != c.want {
			t.Fatalf("TracesURL(%q) = %q, %v", c.in, got, err)
		}
	}
}

func TestParseHeaders(t *testing.T) {
	got, err := ParseHeaders("Authorization=Basic%20cGs6c2s=, x-langfuse-ingestion-version=4,,")
	if err != nil {
		t.Fatal(err)
	}
	if got["Authorization"] != "Basic cGs6c2s=" || got["x-langfuse-ingestion-version"] != "4" || len(got) != 2 {
		t.Fatalf("got %v", got)
	}
	for _, bad := range []string{"novalue", "=v", "k=%zz", "k=a%0Ab"} {
		_, err := ParseHeaders(bad)
		if err == nil {
			t.Fatalf("ParseHeaders(%q) accepted", bad)
		}
		if strings.Contains(err.Error(), "cGs6") {
			t.Fatal("an error carried a header value")
		}
	}
}

// TestPostFakeCollector posts an exported run to a local fake OTLP/HTTP
// collector and checks the request it received: method, path, content type,
// headers and a body that decodes and validates as a trace.
func TestPostFakeCollector(t *testing.T) {
	var gotPath, gotType, gotAuth string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		gotPath, gotType, gotAuth = r.URL.Path, r.Header.Get("Content-Type"), r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	td, err := Build(syntheticInput(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	body, err := Marshal(td)
	if err != nil {
		t.Fatal(err)
	}
	u, err := TracesURL(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := Post(context.Background(), srv.Client(), u, map[string]string{"Authorization": "Basic abc"}, body); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/traces" || gotType != "application/json" || gotAuth != "Basic abc" {
		t.Fatalf("collector saw path %q type %q auth %q", gotPath, gotType, gotAuth)
	}
	var back TracesData
	if err := json.Unmarshal(gotBody, &back); err != nil {
		t.Fatalf("collector body: %v", err)
	}
	if err := Validate(&back); err != nil {
		t.Fatalf("collector body: %v", err)
	}
}

func TestPostCollectorError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("bad key token=leaked-in-answer"))
	}))
	defer srv.Close()
	err := Post(context.Background(), srv.Client(), srv.URL+"/v1/traces?token=q", map[string]string{"x-maxim-api-key": "secret-header"}, []byte(`{}`))
	if err == nil {
		t.Fatal("a 401 was accepted")
	}
	msg := err.Error()
	if !strings.Contains(msg, "status 401") || strings.Contains(msg, "secret-header") || strings.Contains(msg, "leaked-in-answer") || strings.Contains(msg, "token=q") {
		t.Fatalf("error: %s", msg)
	}
}
