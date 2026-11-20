package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/faultproxy"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/metrics"
	"github.com/maximhq/bifrost/bench/reporters"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/maximhq/bifrost/bench/verdict"
)

func TestProxyUpstreams(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		unsupported bool
		wantErr     bool
		want        []engine.UpstreamStatus
	}{
		{name: "report mapped", status: http.StatusOK, body: `{"checked_at":"2026-11-14T10:00:00Z","upstreams":[` +
			`{"provider":"anthropic","host":"api.anthropic.com:443","ok":true,"latency_ms":40},` +
			`{"provider":"openai","host":"api.openai.com:443","ok":false,"error":"dial tcp: lookup api.openai.com: no such host"},` +
			`{"provider":"groq","host":"api.groq.com:443","skipped":"through an HTTP proxy"}]}`,
			want: []engine.UpstreamStatus{
				{Provider: "anthropic", Host: "api.anthropic.com:443", OK: true},
				{Provider: "openai", Host: "api.openai.com:443", Error: "dial tcp: lookup api.openai.com: no such host"},
				{Provider: "groq", Host: "api.groq.com:443", Skipped: "through an HTTP proxy"},
			}},
		{name: "older proxy is the engine sentinel", status: http.StatusNotFound, body: "404 page not found",
			unsupported: true, wantErr: true},
		{name: "other errors pass through", status: http.StatusUnauthorized, body: `{"error":"control token required"}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				if _, err := w.Write([]byte(tt.body)); err != nil {
					t.Errorf("write: %v", err)
				}
			}))
			defer srv.Close()
			var c engine.UpstreamChecker = proxyUpstreams{faultproxy.NewClient(srv.URL, faultproxy.WithToken("tok"))}
			got, err := c.CheckUpstreams(context.Background())
			if errors.Is(err, engine.ErrUpstreamCheckUnsupported) != tt.unsupported || (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, want error %v, unsupported %v", err, tt.wantErr, tt.unsupported)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("statuses = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestLoadPrices(t *testing.T) {
	dir := t.TempDir()
	t.Run("missing file falls back with a warning", func(t *testing.T) {
		var log bytes.Buffer
		pt, err := loadPrices(filepath.Join(dir, "absent.json"), utils.NewStdLogger(&log, false))
		if err != nil {
			t.Fatal(err)
		}
		if pt.ID == "" || len(pt.Models) == 0 {
			t.Fatalf("table = %+v", pt)
		}
		if !strings.Contains(log.String(), "WARN") || !strings.Contains(log.String(), "using the embedded default") {
			t.Errorf("log = %q", log.String())
		}
	})
	t.Run("broken file is an error", func(t *testing.T) {
		p := filepath.Join(dir, "broken.json")
		if err := os.WriteFile(p, []byte("{"), 0o644); err != nil {
			t.Fatal(err)
		}
		var log bytes.Buffer
		if _, err := loadPrices(p, utils.NewStdLogger(&log, false)); err == nil {
			t.Fatal("a broken snapshot loaded")
		}
		if log.Len() != 0 {
			t.Errorf("log = %q", log.String())
		}
	})
}

// TestMetricDirectionTablesMatchDescriptors keeps the verdict's direction and
// rate lists, which the verdict package may not read from bench/metrics, in
// step with every registered metric descriptor.
func TestMetricDirectionTablesMatchDescriptors(t *testing.T) {
	var lower, rate []string
	for _, m := range metrics.Builtin() {
		d := m.Descriptor()
		t.Run(d.Name, func(t *testing.T) {
			if got := verdict.HigherIsBetter(d.Name); got != d.HigherIsBetter {
				t.Errorf("verdict.HigherIsBetter = %v, descriptor says %v", got, d.HigherIsBetter)
			}
			if got := verdict.RateMetric(d.Name); got != (d.Kind == schemas.KindRate) {
				t.Errorf("verdict.RateMetric = %v, descriptor kind %s", got, d.Kind)
			}
		})
		if !d.HigherIsBetter {
			lower = append(lower, d.Name)
		}
		if d.Kind == schemas.KindRate {
			rate = append(rate, d.Name)
		}
	}
	slices.Sort(lower)
	slices.Sort(rate)
	if !slices.Equal(lower, verdict.LowerIsBetterMetrics()) || !slices.Equal(rate, verdict.RateMetrics()) {
		t.Errorf("verdict lists %v / %v, descriptors %v / %v", verdict.LowerIsBetterMetrics(), verdict.RateMetrics(), lower, rate)
	}
}

// analysisFixture freezes an experiment of 2 tasks x arms A and B x 1 trial,
// every run sealed with a verified_success, and returns an app over it.
func analysisFixture(t *testing.T) (*app, *schemas.Manifest) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "runs")
	t.Cleanup(func() { writable(t, root) })
	st, err := store.NewFSStore(root, schemas.NoopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	m := &schemas.Manifest{ExpID: "e_an", Arms: map[string]schemas.ArmManifest{"A": {Label: "a"}, "B": {Label: "b"}},
		Experiment: schemas.Experiment{Primary: schemas.Endpoint{Condition: schemas.ConditionClean, Metric: verdict.MetricVerifiedSuccess}}}
	for _, task := range []string{"t01", "t02"} {
		for _, arm := range []string{"A", "B"} {
			m.Slots = append(m.Slots, schemas.Slot{SlotID: "s_" + task + "_" + strings.ToLower(arm), PairID: "p_" + task, Task: task,
				TaskVersion: "1.0.0", Arm: arm, Condition: schemas.ConditionClean, Trial: 1, Counted: true})
		}
	}
	if m.Hash, err = utils.ManifestHash(m); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := st.CreateExperiment(ctx, m); err != nil {
		t.Fatal(err)
	}
	for i := range m.Slots {
		sl := m.Slots[i]
		sl.ExpID = m.ExpID
		r := &schemas.Run{RunID: "r_" + strings.TrimPrefix(sl.SlotID, "s_"), ExpID: m.ExpID, SlotID: sl.SlotID, PairID: sl.PairID,
			Attempt: 1, Stage: schemas.StageSeal, ManifestHash: m.Hash, Outcome: schemas.OutcomePass}
		if _, err := st.NewRun(ctx, &sl, r); err != nil {
			t.Fatal(err)
		}
		if err := st.WriteRunFile(ctx, r, "metrics.json", []byte(`{"verified_success":{"state":"observed","v":1,"evidence":"outcome"}}`)); err != nil {
			t.Fatal(err)
		}
		if _, err := st.Seal(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	a := &app{cfg: &lib.Config{Root: t.TempDir()}, logger: schemas.NoopLogger{}, store: st,
		reg: &schemas.Registry{Policies: verdict.Builtin(), Reporters: reporters.Builtin()}}
	return a, m
}

// writable makes every folder and file under root writable again.
func writable(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.Chmod(p, 0o755)
		}
		return os.Chmod(p, 0o644)
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Logf("make writable %s: %v", root, err)
	}
}

func TestAnalyzeVerifiesSeals(t *testing.T) {
	runDir := func(a *app, slot, run string) string {
		return filepath.Join(a.store.Root(), "e_an", "slots", slot, "runs", run)
	}
	tests := []struct {
		name    string
		tamper  func(t *testing.T, a *app)
		invalid []string // substrings of one invalid reason; nil: no evidence reason
		missing bool     // slot_coverage warns
	}{
		{name: "untouched", tamper: func(*testing.T, *app) {}},
		{name: "metrics.json edited", invalid: []string{"r_t02_b", "metrics.json changed"}, tamper: func(t *testing.T, a *app) {
			dir := runDir(a, "s_t02_b", "r_t02_b")
			writable(t, dir)
			if err := os.WriteFile(filepath.Join(dir, "metrics.json"), []byte(`{"verified_success":{"state":"observed","v":0,"evidence":"outcome"}}`), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		// Audit 1-A-02: a manifest.json edited after freezing (here its
		// alpha) no longer matches its hash, so the analysis is INVALID
		// instead of re-deciding the sealed runs under the new settings.
		{name: "manifest.json edited", invalid: []string{"manifest e_an does not match its hash"}, tamper: func(t *testing.T, a *app) {
			p := filepath.Join(a.store.Root(), "e_an", "manifest.json")
			writable(t, filepath.Dir(p))
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			var raw map[string]any
			if err := json.Unmarshal(b, &raw); err != nil {
				t.Fatal(err)
			}
			raw["experiment"].(map[string]any)["verdict"] = map[string]any{"alpha": 0.5}
			if b, err = json.Marshal(raw); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, b, 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "counted run deleted", invalid: []string{"seals.log chain", "r_t01_a"}, missing: true, tamper: func(t *testing.T, a *app) {
			dir := filepath.Dir(runDir(a, "s_t01_a", "r_t01_a"))
			writable(t, dir)
			if err := os.RemoveAll(filepath.Join(dir, "r_t01_a")); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, m := analysisFixture(t)
			tt.tamper(t, a)
			an, report, err := a.analyze(context.Background(), m.ExpID, "")
			if err != nil {
				t.Fatalf("analyze: %v", err)
			}
			var evidence []string
			for _, r := range an.Verdict.InvalidReasons {
				if strings.Contains(r, "evidence") {
					evidence = append(evidence, r)
				}
			}
			if tt.invalid == nil {
				if len(evidence) != 0 {
					t.Fatalf("evidence reasons %q on untouched runs", evidence)
				}
			} else {
				if an.Verdict.Decision != schemas.DecisionInvalid || len(evidence) == 0 {
					t.Fatalf("decision %s, reasons %q; want INVALID with an evidence reason", an.Verdict.Decision, an.Verdict.InvalidReasons)
				}
				for _, w := range tt.invalid {
					if !strings.Contains(strings.Join(evidence, "\n"), w) || !strings.Contains(string(report), w) {
						t.Fatalf("reasons %q (and the report) do not name %q", evidence, w)
					}
				}
			}
			var cov *schemas.InstrumentCheck
			for i := range an.Verdict.Instruments {
				if an.Verdict.Instruments[i].Name == verdict.InstrumentSlotCoverage {
					cov = &an.Verdict.Instruments[i]
				}
			}
			if cov == nil || (cov.Status == schemas.InstrumentWarn) != tt.missing {
				t.Fatalf("slot coverage %+v, want warn %v", cov, tt.missing)
			}
		})
	}
}
