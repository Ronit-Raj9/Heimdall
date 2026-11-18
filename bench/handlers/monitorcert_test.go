package handlers

// Eval integrity area (S2): monitor certification routes.

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// fakeMonitorCerts holds one certificate, mc_one.
type fakeMonitorCerts struct{}

func (fakeMonitorCerts) cert() *schemas.MonitorCertificate {
	rate, fpr, thr := 0.8, 0.0083, 0.7
	return &schemas.MonitorCertificate{
		CertID: "mc_one", CreatedAt: time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC),
		Monitor: schemas.MonitorCertMonitor{Name: "keyword", Kind: schemas.MonitorKindBuiltin, Mode: schemas.MonitorModeTranscript,
			ConfigHash: "sha256:abc", Network: schemas.MonitorNetworkNone},
		ExpIDs: []string{"e_x"},
		Labels: schemas.MonitorLabelSummary{Honest: 120, Attack: 10, Rule: "rule", ExcludedReasons: map[string]int{}},
		OperatingPoints: []schemas.MonitorOperatingPoint{{TargetFPR: 0.01, Supported: true, Threshold: &thr, FalsePositives: 1, Honest: 120,
			AchievedFPR: &fpr, FPRInterval: []float64{0.0002, 0.0456}, Caught: 8, Attacks: 10, CatchRate: &rate, CatchInterval: []float64{0.44, 0.97}}},
		Families: []schemas.MonitorFamily{}, Warnings: []string{}, Items: []schemas.MonitorItem{}, Excluded: []schemas.MonitorExcludedRun{},
		Headline: &schemas.MonitorCertHeadline{TargetFPR: 0.01, CatchRate: &rate, CatchInterval: []float64{0.44, 0.97}, Supported: true},
	}
}

func (f fakeMonitorCerts) List(context.Context) (*schemas.MonitorCertList, error) {
	c := f.cert()
	return &schemas.MonitorCertList{Certifications: []schemas.MonitorCertSummary{{CertID: c.CertID, CreatedAt: c.CreatedAt,
		MonitorName: c.Monitor.Name, MonitorKind: c.Monitor.Kind, Mode: c.Monitor.Mode, ExpIDs: c.ExpIDs,
		Honest: c.Labels.Honest, Attacks: c.Labels.Attack, Headline: c.Headline, Warnings: []string{}}}}, nil
}

func (f fakeMonitorCerts) Get(_ context.Context, id string) (*schemas.MonitorCertificate, error) {
	if id != "mc_one" {
		return nil, fmt.Errorf("monitor certificate %s: %w", id, schemas.ErrNotFound)
	}
	return f.cert(), nil
}

func (f fakeMonitorCerts) Report(ctx context.Context, id string) ([]byte, error) {
	if _, err := f.Get(ctx, id); err != nil {
		return nil, err
	}
	return []byte("# Monitor certificate mc_one\n"), nil
}

func newMonitorCertClient(t *testing.T, certs MonitorCertSource) *testClient {
	t.Helper()
	r := router.New()
	r.NotFound = func(ctx *fasthttp.RequestCtx) {
		lib.SendError(ctx, testMode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "route not found")
	}
	NewMonitorCertHandler(testMode, certs).RegisterRoutes(r, lib.BodyLimit(testMode, lib.MaxBodyBytes))
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: r.Handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		if err := srv.Shutdown(); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	return &testClient{client: &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}}
}

func TestMonitorCertRoutes(t *testing.T) {
	c := newMonitorCertClient(t, fakeMonitorCerts{})
	none := newMonitorCertClient(t, nil)
	p := APIPrefix + "/monitors/certifications"
	tests := []struct {
		name        string
		client      *testClient
		path        string
		status      int
		contains    string
		contentType string
	}{
		{"list", c, p, 200, `"certifications":[{"cert_id":"mc_one"`, "application/json"},
		{"list headline", c, p, 200, `"headline":{"target_fpr":0.01,"catch_rate":0.8,"catch_interval":[0.44,0.97],"supported":true}`, "application/json"},
		{"get", c, p + "/mc_one", 200, `"operating_points":[{"target_fpr":0.01,"supported":true,"threshold":0.7`, "application/json"},
		{"get unknown", c, p + "/mc_two", 404, "not_found", "application/json"},
		{"get bad id", c, p + "/..%2Fx", 404, "not_found", "application/json"},
		{"report", c, p + "/mc_one/report.md", 200, "# Monitor certificate mc_one", "text/markdown"},
		{"report unknown", c, p + "/mc_two/report.md", 404, "not_found", "application/json"},
		{"no source list", none, p, 503, "not available", "application/json"},
		{"no source get", none, p + "/mc_one", 503, "not available", "application/json"},
		{"no source report", none, p + "/mc_one/report.md", 503, "not available", "application/json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := tt.client.do(t, "GET", tt.path, nil, nil)
			if resp.StatusCode() != tt.status {
				t.Fatalf("status = %d, want %d: %s", resp.StatusCode(), tt.status, resp.Body())
			}
			if !strings.Contains(string(resp.Body()), tt.contains) {
				t.Errorf("body %s lacks %q", resp.Body(), tt.contains)
			}
			if ct := string(resp.Header.ContentType()); !strings.HasPrefix(ct, tt.contentType) {
				t.Errorf("content type %q, want %q", ct, tt.contentType)
			}
			if got := string(resp.Header.Peek(lib.ModeHeader)); got != string(testMode) {
				t.Errorf("mode header %q", got)
			}
		})
	}
}

// TestMonitorCertContractKeys pins the certificate JSON to the keys of the
// API contract (contract section 3), so a renamed field fails here.
func TestMonitorCertContractKeys(t *testing.T) {
	c := newMonitorCertClient(t, fakeMonitorCerts{})
	p := APIPrefix + "/monitors/certifications"
	tests := []struct {
		name string
		path string
		pick func(map[string]any) map[string]any
		keys []string
	}{
		{"list entry", p, func(m map[string]any) map[string]any {
			return m["certifications"].([]any)[0].(map[string]any)
		}, []string{"cert_id", "created_at", "monitor_name", "monitor_kind", "mode", "exp_ids", "honest", "attacks", "headline", "warnings"}},
		{"certificate", p + "/mc_one", func(m map[string]any) map[string]any { return m },
			[]string{"cert_id", "created_at", "monitor", "exp_ids", "labels", "operating_points", "families", "warnings", "items", "errors"}},
		{"monitor", p + "/mc_one", func(m map[string]any) map[string]any { return m["monitor"].(map[string]any) },
			[]string{"name", "kind", "mode", "config_hash", "network"}},
		{"labels", p + "/mc_one", func(m map[string]any) map[string]any { return m["labels"].(map[string]any) },
			[]string{"honest", "attack", "excluded", "rule"}},
		{"operating point", p + "/mc_one", func(m map[string]any) map[string]any {
			return m["operating_points"].([]any)[0].(map[string]any)
		}, []string{"target_fpr", "supported", "threshold", "false_positives", "honest", "achieved_fpr", "fpr_interval",
			"caught", "attacks", "catch_rate", "catch_interval", "note"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := c.do(t, "GET", tt.path, nil, nil)
			var m map[string]any
			if err := json.Unmarshal(resp.Body(), &m); err != nil {
				t.Fatalf("decode: %v", err)
			}
			obj := tt.pick(m)
			for _, k := range tt.keys {
				if _, ok := obj[k]; !ok {
					t.Errorf("%s lacks key %q: %v", tt.name, k, obj)
				}
			}
		})
	}
}
