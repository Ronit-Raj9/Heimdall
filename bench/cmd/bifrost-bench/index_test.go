package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/resultsindex"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/trends"
)

func TestIndexUsage(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"no subcommand", nil},
		{"unknown subcommand", []string{"drop"}},
		{"bad format", []string{"query", "-format", "xml"}},
		{"bad filter", []string{"query", "color=red"}},
		{"rebuild takes no terms", []string{"rebuild", "agent=codex"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := runIndex(tt.args); got != exitUsage {
				t.Fatalf("runIndex(%v) = %d, want %d", tt.args, got, exitUsage)
			}
		})
	}
}

func TestPrintRunRows(t *testing.T) {
	ok := 1.0
	rows := []resultsindex.RunRow{{Experiment: "e_1", RunID: "r_1", Suite: "core@1", Task: "t1", Arm: "A", Agent: "codex",
		Model: "m1", Condition: "clean", Trial: 1, Outcome: "pass", Success: &ok, Started: time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)}}
	var table bytes.Buffer
	if code := printRunRows(&table, rows, "table"); code != exitOK {
		t.Fatalf("table exit %d", code)
	}
	if !strings.Contains(table.String(), "r_1") || !strings.Contains(table.String(), "2026-11-01 09:00Z") || !strings.Contains(table.String(), "1 runs") {
		t.Fatalf("table = %q", table.String())
	}
	var js bytes.Buffer
	if code := printRunRows(&js, nil, "json"); code != exitOK {
		t.Fatalf("json exit %d", code)
	}
	var body map[string][]any
	if err := json.Unmarshal(js.Bytes(), &body); err != nil || body["runs"] == nil || len(body["runs"]) != 0 {
		t.Fatalf("json = %q (%v): want an empty runs array", js.String(), err)
	}
	var an bytes.Buffer
	if code := printAnalysisRows(&an, []resultsindex.AnalysisRow{{Experiment: "e_1", AnalysisID: "a_1", Decision: "improved"}}, "table"); code != exitOK {
		t.Fatalf("analyses exit %d", code)
	}
	if !strings.Contains(an.String(), "a_1") || !strings.Contains(an.String(), "improved") {
		t.Fatalf("analyses = %q", an.String())
	}
}

func TestTrendSourceBuildsIndex(t *testing.T) {
	st, err := store.NewFSStore(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ix := resultsindex.New(st, nil)
	resp, err := trendSource{ix: ix, logger: schemas.NoopLogger{}}.Trends(context.Background(), trends.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Points != 0 || resp.Metric != trends.MetricSuccess {
		t.Fatalf("resp = %+v", resp)
	}
	snap, err := ix.Snapshot()
	if err != nil || !snap.Status().Exists {
		t.Fatalf("index not written by the first trends request: %v %+v", err, snap.Status())
	}
}

func TestIndexOnSealIgnoresOtherEvents(t *testing.T) {
	indexOnSeal(context.Background(), nil, "e_1", engine.Event{Kind: engine.EventStage})
	if indexRefresher.kick != nil {
		t.Fatal("a stage event started the index refresher")
	}
}
