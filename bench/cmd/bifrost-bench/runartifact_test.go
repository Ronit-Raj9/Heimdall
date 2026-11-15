package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/valyala/fasthttp"
)

var _ handlers.ArtifactReader = indexAdapter{}

// TestRunArtifactOnDisk serves an artifact file from a real sealed run
// folder through the store's index, then refuses it once the file changes
// after the seal.
func TestRunArtifactOnDisk(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Cleanup(func() { makeWritable(t, root) })
	st, err := store.NewFSStore(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := &schemas.Manifest{ExpID: "e_art", SuiteID: "s", SuiteVersion: "1", CreatedAt: time.Date(2026, 11, 15, 1, 0, 0, 0, time.UTC),
		Arms:  map[string]schemas.ArmManifest{"A": {Label: "a", ConfigHash: "sha256:aaa", Config: schemas.AgentConfig{ID: "codex", Adapter: "command"}}},
		Slots: []schemas.Slot{{SlotID: "s_t1", PairID: "p_t1", Task: "t1", Arm: "A", Condition: "clean", Trial: 1, Counted: true}}}
	if err := st.CreateExperiment(ctx, m); err != nil {
		t.Fatal(err)
	}
	sl := m.Slots[0]
	sl.ExpID = m.ExpID
	r := &schemas.Run{RunID: "r_art", Attempt: 1, Stage: schemas.StageCleanup, Outcome: schemas.OutcomePass}
	if _, err := st.NewRun(ctx, &sl, r); err != nil {
		t.Fatal(err)
	}
	r.ExpID, r.SlotID, r.PairID = m.ExpID, sl.SlotID, sl.PairID
	payload := []byte("solid ascii\x00\xff mesh")
	path := utils.ArtifactRunPath("mesh", "part.stl")
	ev, err := json.Marshal(schemas.Evidence{Artifacts: []schemas.ArtifactFile{
		{ID: "mesh", Name: "part.stl", Path: path, Source: "out/part.stl", SHA256: utils.SHA256Bytes(payload), Bytes: int64(len(payload))},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"evidence.json": ev, path: payload} {
		if err := st.WriteRunFile(ctx, r, name, data); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.Seal(ctx, r); err != nil {
		t.Fatal(err)
	}
	ix := store.NewIndex(st)
	if err := ix.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	rt := router.New()
	handlers.NewRunsHandler(schemas.ModeLive, indexAdapter{ix: ix, root: st.Root()}).RegisterRoutes(rt, lib.SecurityHeaders())
	get := func() *fasthttp.RequestCtx {
		var c fasthttp.RequestCtx
		c.Request.SetRequestURI(handlers.APIPrefix + "/runs/r_art/artifacts/mesh/part.stl")
		c.Request.Header.SetMethod("GET")
		rt.Handler(&c)
		return &c
	}

	c := get()
	if c.Response.StatusCode() != 200 || !bytes.Equal(c.Response.Body(), payload) {
		t.Fatalf("status = %d, body = %q", c.Response.StatusCode(), c.Response.Body())
	}
	for k, v := range map[string]string{
		"Content-Type":           "application/octet-stream",
		"Content-Disposition":    `attachment; filename="part.stl"`,
		"X-Content-Type-Options": "nosniff",
	} {
		if got := string(c.Response.Header.Peek(k)); got != v {
			t.Errorf("header %s = %q, want %q", k, got, v)
		}
	}
	if csp := string(c.Response.Header.Peek("Content-Security-Policy")); !strings.HasSuffix(csp, "; sandbox") {
		t.Errorf("csp = %q, want the global policy plus sandbox", csp)
	}

	// A file changed after the seal is never served.
	e, ok := ix.Run("r_art")
	if !ok {
		t.Fatal("run r_art not indexed")
	}
	dir := e.Dir
	target := filepath.Join(dir, filepath.FromSlash(path))
	makeWritable(t, dir)
	if err := os.WriteFile(target, []byte("swapped"), 0o644); err != nil {
		t.Fatal(err)
	}
	c = get()
	if c.Response.StatusCode() != 409 || bytes.Contains(c.Response.Body(), []byte("swapped")) {
		t.Fatalf("tampered: status = %d, body = %q", c.Response.StatusCode(), c.Response.Body())
	}
}
