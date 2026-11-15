package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/clusters"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
)

// recordedRoot is the committed recorded bundle. Tests only read it.
const recordedRoot = "../../testdata/recorded"

// recordedSource opens the recorded bundle through the same adapter the
// server and the CLI use.
func recordedSource(t *testing.T) indexAdapter {
	t.Helper()
	st, err := store.NewFSStore(recordedRoot, schemas.NoopLogger{})
	if err != nil {
		t.Fatalf("open recorded store: %v", err)
	}
	ix := store.NewIndex(st)
	if err := ix.Rebuild(context.Background()); err != nil {
		t.Fatalf("index recorded store: %v", err)
	}
	return indexAdapter{ix: ix, root: st.Root()}
}

func TestClustersRecorded(t *testing.T) {
	src := recordedSource(t)
	ctx := context.Background()
	t.Run("live runs only by default", func(t *testing.T) {
		rep, err := clusters.Build(ctx, src, clusters.Query{})
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		if rep.Runs != 10 || rep.Failures != 3 {
			t.Fatalf("runs %d failures %d, want 10 and 3 (the pilot bundle)", rep.Runs, rep.Failures)
		}
		if len(rep.Experiments) != 1 || rep.Experiments[0] != "e_pilot_mixed_nc93" {
			t.Fatalf("experiments %v, want only the pilot", rep.Experiments)
		}
		if got := excludedRuns(rep, clusters.ReasonSynthetic); got != 8 {
			t.Fatalf("synthetic exclusions %d, want 8 (e_smoke_sdoj)", got)
		}
		total := 0
		for _, c := range rep.Clusters {
			total += c.Count
			if c.Outcome != "fail" || !strings.HasPrefix(c.Title, "fails ") {
				t.Errorf("cluster %s: outcome %s title %q", c.Signature, c.Outcome, c.Title)
			}
		}
		if total != 3 {
			t.Fatalf("clustered runs %d, want 3", total)
		}
		// The two model-outage runs fail the same 7 hidden tests but end on
		// different errors (Codex's own stream versus the gateway's 503), so
		// they are separate clusters that list each other as related.
		related := 0
		for _, c := range rep.Clusters {
			related += len(c.Related)
		}
		if related != 2 {
			t.Fatalf("related links %d, want 2", related)
		}
	})
	t.Run("synthetic only", func(t *testing.T) {
		rep, err := clusters.Build(ctx, src, clusters.Query{Synthetic: clusters.SyntheticOnly})
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		if rep.Runs != 8 || rep.Failures != 4 {
			t.Fatalf("runs %d failures %d, want 8 and 4 (the smoke bundle)", rep.Runs, rep.Failures)
		}
		// Two tasks, each failed by both arms with the same error: two
		// clusters of 2, each task-specific.
		if len(rep.Clusters) != 2 {
			t.Fatalf("clusters %d, want 2: %+v", len(rep.Clusters), rep.Clusters)
		}
		for _, c := range rep.Clusters {
			if c.Count != 2 || c.Scope != clusters.ScopeTask {
				t.Errorf("cluster %s count %d scope %s, want 2 and task", c.Title, c.Count, c.Scope)
			}
		}
	})
	t.Run("signatures are stable across filters", func(t *testing.T) {
		all, err := clusters.Build(ctx, src, clusters.Query{Synthetic: clusters.SyntheticInclude})
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		one, err := clusters.Build(ctx, src, clusters.Query{Synthetic: clusters.SyntheticInclude, Experiments: []string{"e_pilot_mixed_nc93"}, Agent: "codex"})
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		sigs := map[string]bool{}
		for _, c := range all.Clusters {
			sigs[c.Signature] = true
		}
		for _, c := range one.Clusters {
			if !sigs[c.Signature] {
				t.Errorf("signature %s of the filtered view is not in the full view", c.Signature)
			}
		}
	})
}

func TestPrintClusters(t *testing.T) {
	src := recordedSource(t)
	ctx := context.Background()
	t.Run("table", func(t *testing.T) {
		var out bytes.Buffer
		if code := printClusters(ctx, &out, src, clusters.Query{}, "", false); code != exitOK {
			t.Fatalf("exit %d", code)
		}
		if !strings.Contains(out.String(), "3 failures in 10 verified runs") || !strings.Contains(out.String(), "COUNT") {
			t.Fatalf("table:\n%s", out.String())
		}
		t.Logf("table:\n%s", out.String())
	})
	t.Run("json detail", func(t *testing.T) {
		rep, err := clusters.Build(ctx, src, clusters.Query{})
		if err != nil || len(rep.Clusters) == 0 {
			t.Fatalf("build: %v", err)
		}
		sig := rep.Clusters[0].Signature
		var out bytes.Buffer
		if code := printClusters(ctx, &out, src, clusters.Query{}, sig, true); code != exitOK {
			t.Fatalf("exit %d", code)
		}
		var d clusters.Detail
		if err := json.Unmarshal(out.Bytes(), &d); err != nil {
			t.Fatalf("decode: %v\n%s", err, out.String())
		}
		if d.Cluster.Signature != sig || len(d.Runs) != d.Cluster.Count {
			t.Fatalf("detail %s with %d runs, want %s with %d", d.Cluster.Signature, len(d.Runs), sig, d.Cluster.Count)
		}
	})
	t.Run("bad query is a usage error", func(t *testing.T) {
		var out bytes.Buffer
		if code := printClusters(ctx, &out, src, clusters.Query{Synthetic: "maybe"}, "", false); code != exitUsage {
			t.Fatalf("exit %d, want %d", code, exitUsage)
		}
	})
	t.Run("unknown signature fails", func(t *testing.T) {
		var out bytes.Buffer
		if code := printClusters(ctx, &out, src, clusters.Query{}, "c_0000000000000000", false); code != exitInfra {
			t.Fatalf("exit %d, want %d", code, exitInfra)
		}
	})
}

func TestRunEvidence(t *testing.T) {
	src := recordedSource(t)
	t.Run("serves evidence files", func(t *testing.T) {
		data, err := src.RunEvidence("r_gevv5gsi7b", clusters.FileStderr)
		if err != nil || len(data) == 0 {
			t.Fatalf("stderr: %d bytes, %v", len(data), err)
		}
	})
	t.Run("refuses other names", func(t *testing.T) {
		for _, name := range []string{"seal.json", "../manifest.json", "gateway.requests.jsonl"} {
			if _, err := src.RunEvidence("r_gevv5gsi7b", name); !errors.Is(err, schemas.ErrNotFound) {
				t.Errorf("%s: err %v, want not found", name, err)
			}
		}
	})
	t.Run("unknown run", func(t *testing.T) {
		if _, err := src.RunEvidence("r_missing", clusters.FileEvents); !errors.Is(err, schemas.ErrNotFound) {
			t.Fatalf("err %v, want not found", err)
		}
	})
}

// excludedRuns returns the runs a report excluded for one reason.
func excludedRuns(rep *clusters.Report, reason string) int {
	for _, x := range rep.Excluded {
		if x.Reason == reason {
			return x.Runs
		}
	}
	return 0
}
