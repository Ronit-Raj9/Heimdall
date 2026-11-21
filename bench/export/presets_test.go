package export

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/agentevents"
	"github.com/maximhq/bifrost/bench/traceexport"
)

// recordedRun is a sealed run recorded by a $0 smoke experiment (scripted
// agent and model through the real pipeline), shared with traceexport.
const recordedRun = "../testdata/recorded/e_smoke_sdoj/slots/s_glz13mrnep/runs/r_ioap8m36je"

func recordedTrace(t *testing.T) *traceexport.TracesData {
	t.Helper()
	in, err := traceexport.LoadRun(filepath.FromSlash(recordedRun))
	if err != nil {
		t.Fatal(err)
	}
	td, err := traceexport.Build(in, traceexport.Options{ServiceVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return td
}

// spanSet reads a decoded export as span id -> name and sorted attribute keys.
func spanSet(recs []agentevents.Record) map[string]string {
	out := map[string]string{}
	for _, r := range recs {
		keys := make([]string, 0, len(r.Attributes))
		for k := range r.Attributes {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out[r.ID] = r.Name + "|" + strings.Join(keys, ",")
	}
	return out
}

// TestProtobufMatchesJSON encodes one trace both ways and decodes both with
// agentevents' independent reader: the same spans and attributes come back.
func TestProtobufMatchesJSON(t *testing.T) {
	td := recordedTrace(t)
	pb, err := MarshalOTLPProto(td)
	if err != nil {
		t.Fatal(err)
	}
	js, err := traceexport.Marshal(td)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1763460000, 0)
	fromPB, badPB, err := agentevents.ParseOTLP(pb, agentevents.ContentTypeOTLPProto, agentevents.Limits{}, now)
	if err != nil || badPB != 0 {
		t.Fatalf("decode protobuf: %v (%d refused)", err, badPB)
	}
	fromJS, badJS, err := agentevents.ParseOTLP(js, agentevents.ContentTypeOTLPJSON, agentevents.Limits{}, now)
	if err != nil || badJS != 0 {
		t.Fatalf("decode json: %v (%d refused)", err, badJS)
	}
	a, b := spanSet(fromPB), spanSet(fromJS)
	if len(a) == 0 || len(a) != len(b) {
		t.Fatalf("protobuf %d spans, json %d", len(a), len(b))
	}
	for id, v := range b {
		if a[id] != v {
			t.Errorf("span %s: protobuf %q, json %q", id, a[id], v)
		}
	}
	pb2, _ := MarshalOTLPProto(td)
	if string(pb) != string(pb2) {
		t.Fatal("protobuf encoding is not deterministic")
	}
}

// fakeCollector records one request.
type fakeCollector struct {
	mu      sync.Mutex
	path    string
	headers http.Header
	body    []byte
}

func (f *fakeCollector) handler(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.path, f.headers, f.body = r.URL.Path, r.Header.Clone(), b
	f.mu.Unlock()
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{}`))
}

// TestPresetsAgainstFakeCollectors sends a recorded run through every preset
// to a local fake collector and checks the path, the headers (built from
// environment variables), the content type, that the body decodes, and the
// attributes the backend's documents say it reads. No real backend is
// called.
func TestPresetsAgainstFakeCollectors(t *testing.T) {
	env := map[string]string{
		"LANGFUSE_PUBLIC_KEY": "pk-lf-test", "LANGFUSE_SECRET_KEY": "sk-lf-test",
		"LANGSMITH_API_KEY": "ls-test", "BRAINTRUST_API_KEY": "bt-test", "BRAINTRUST_PROJECT_ID": "proj1",
		"DD_API_KEY": "dd-test", "MAXIM_API_KEY": "mx-test", "MAXIM_LOG_REPO_ID": "repo1",
	}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	cases := map[string]struct {
		path       string
		headers    map[string]string
		attrs      []string // attributes some span must carry
		resource   []string
		contentTyp string
	}{
		"langfuse": {"/api/public/otel/v1/traces", map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte("pk-lf-test:sk-lf-test")), "X-Langfuse-Ingestion-Version": "4"},
			[]string{"langfuse.observation.type", "langfuse.trace.name", "langfuse.trace.metadata.experiment"}, nil, "application/json"},
		"langsmith": {"/otel/v1/traces", map[string]string{"X-Api-Key": "ls-test"}, []string{"langsmith.span.kind", "langsmith.metadata.task"}, nil, "application/x-protobuf"},
		"braintrust": {"/otel/v1/traces", map[string]string{"Authorization": "Bearer bt-test", "X-Bt-Parent": "project_id:proj1"},
			[]string{"braintrust.span_attributes.type", "braintrust.metadata.outcome", "gen_ai.operation.name"}, nil, "application/x-protobuf"},
		"datadog": {"/v1/traces", map[string]string{"Dd-Api-Key": "dd-test", "Dd-Otlp-Source": "llmobs", "Dd-Llmobs-Enabled": "true"},
			[]string{"gen_ai.operation.name"}, []string{"dd_llmobs_enabled"}, "application/x-protobuf"},
		"maxim":   {"/v1/otel", map[string]string{"X-Maxim-Api-Key": "mx-test", "X-Maxim-Repo-Id": "repo1"}, []string{"maxim.metadata"}, nil, "application/json"},
		"phoenix": {"/v1/traces", nil, []string{"openinference.span.kind"}, []string{"openinference.project.name"}, "application/x-protobuf"},
		"jaeger":  {"/v1/traces", nil, nil, nil, "application/json"},
	}
	presets := Presets()
	if len(presets) != len(cases) {
		t.Fatalf("%d presets, %d cases", len(presets), len(cases))
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			p := presets[name]
			fc := &fakeCollector{}
			srv := httptest.NewServer(http.HandlerFunc(fc.handler))
			defer srv.Close()
			endpoint := srv.URL + c.path
			if p.Endpoint != "" && !strings.HasSuffix(p.Endpoint, c.path) {
				t.Fatalf("default endpoint %s does not end in %s", p.Endpoint, c.path)
			}
			h, err := p.PresetHeaders(lookup)
			if err != nil {
				t.Fatal(err)
			}
			td := recordedTrace(t)
			p.Apply(td)
			body, ctype, err := p.Encode(td)
			if err != nil {
				t.Fatal(err)
			}
			if ctype != c.contentTyp {
				t.Fatalf("content type %s, want %s", ctype, c.contentTyp)
			}
			if err := PostTrace(t.Context(), nil, endpoint, h, body, ctype); err != nil {
				t.Fatal(err)
			}
			fc.mu.Lock()
			defer fc.mu.Unlock()
			if fc.path != c.path || fc.headers.Get("Content-Type") != ctype {
				t.Fatalf("path %s content type %s", fc.path, fc.headers.Get("Content-Type"))
			}
			for k, v := range c.headers {
				if got := fc.headers.Get(k); got != v {
					t.Errorf("header %s = %q, want %q", k, got, v)
				}
			}
			recs, bad, err := agentevents.ParseOTLP(fc.body, ctype, agentevents.Limits{}, time.Unix(1763460000, 0))
			if err != nil || bad != 0 || len(recs) == 0 {
				t.Fatalf("collector body does not decode: %v (%d refused, %d spans)", err, bad, len(recs))
			}
			keys := bodyAttrKeys(t, fc.body, ctype)
			for _, a := range c.attrs {
				if !keys[a] {
					t.Errorf("no span in the body carries %s", a)
				}
			}
			for _, a := range c.resource {
				if _, ok := traceexport.Attr(td.ResourceSpans[0].Resource.Attributes, a); !ok {
					t.Errorf("resource has no %s", a)
				}
			}
		})
	}
}

func TestPresetHeadersNeedTheirVariables(t *testing.T) {
	p := Presets()["langfuse"]
	_, err := p.PresetHeaders(func(k string) (string, bool) {
		if k == "LANGFUSE_PUBLIC_KEY" {
			return "pk", true
		}
		return "", false
	})
	if !errors.Is(err, ErrMissingCredential) || !strings.Contains(err.Error(), "LANGFUSE_SECRET_KEY") || strings.Contains(err.Error(), "pk") {
		t.Fatalf("err = %v", err)
	}
	// Optional headers are left out when their variable is unset.
	h, err := Presets()["phoenix"].PresetHeaders(func(string) (string, bool) { return "", false })
	if err != nil || len(h) != 0 {
		t.Fatalf("phoenix without a key: %v %v", h, err)
	}
}

func TestMaximMetadataIsJSON(t *testing.T) {
	td := recordedTrace(t)
	Presets()["maxim"].Apply(td)
	root := td.ResourceSpans[0].ScopeSpans[0].Spans[0]
	var meta map[string]any
	if err := json.Unmarshal([]byte(attrStr(root.Attributes, "maxim.metadata")), &meta); err != nil {
		t.Fatalf("maxim.metadata: %v", err)
	}
	if meta["maxim-trace-name"] == "" || meta["maxim-trace-tags"] == nil {
		t.Fatalf("meta = %v", meta)
	}
	if exp := attrStr(root.Attributes, "heimdall.experiment"); exp != "" && meta["maxim-session-id"] != "heimdall-"+exp {
		t.Fatalf("session id = %v for experiment %s", meta["maxim-session-id"], exp)
	}
}

// bodyAttrKeys returns every span attribute key in a collector body, read
// with encoding/json or a minimal protobuf field walker written for this
// test (ExportTraceServiceRequest 1 > ResourceSpans 2 > ScopeSpans 2 > Span 9
// > KeyValue 1).
func bodyAttrKeys(t *testing.T, body []byte, ctype string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	if ctype == "application/json" {
		var td traceexport.TracesData
		if err := json.Unmarshal(body, &td); err != nil {
			t.Fatal(err)
		}
		eachSpan(&td, func(sp *traceexport.Span) {
			for _, kv := range sp.Attributes {
				out[kv.Key] = true
			}
		})
		return out
	}
	fields := func(b []byte, want int) [][]byte {
		var res [][]byte
		for len(b) > 0 {
			key, n := binary.Uvarint(b)
			if n <= 0 {
				t.Fatal("bad varint")
			}
			b = b[n:]
			switch key & 7 {
			case 0:
				_, m := binary.Uvarint(b)
				b = b[m:]
			case 1:
				b = b[8:]
			case 5:
				b = b[4:]
			case 2:
				l, m := binary.Uvarint(b)
				b = b[m:]
				if int(key>>3) == want {
					res = append(res, b[:l])
				}
				b = b[l:]
			default:
				t.Fatalf("wire type %d", key&7)
			}
		}
		return res
	}
	for _, rs := range fields(body, 1) {
		for _, ss := range fields(rs, 2) {
			for _, sp := range fields(ss, 2) {
				for _, kv := range fields(sp, 9) {
					for _, k := range fields(kv, 1) {
						out[string(k)] = true
					}
				}
			}
		}
	}
	return out
}
