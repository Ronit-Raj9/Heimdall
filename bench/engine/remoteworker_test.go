package engine

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestNewWorkerValidates(t *testing.T) {
	b := newBench(t)
	plan := NewPlanner(b.reg, b.root, nil).Plan
	run := NewPipeline(b.reg, b.root, nil)
	cases := []struct {
		name string
		opts WorkerOptions
		want string
	}{
		{"no server", WorkerOptions{Token: "t"}, "not an http(s) URL"},
		{"file url", WorkerOptions{Server: "file:///x", Token: "t"}, "not an http(s) URL"},
		{"no token", WorkerOptions{Server: "http://127.0.0.1:1"}, "empty token"},
		{"bad id", WorkerOptions{Server: "http://127.0.0.1:1", Token: "t", WorkerID: "a/b"}, "want 1 to 64"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewWorker(b.reg, run, plan, tc.opts); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
	w, err := NewWorker(b.reg, run, plan, WorkerOptions{Server: "http://127.0.0.1:1/", Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if w.opts.Server != "http://127.0.0.1:1" || w.opts.Slots != 1 || !validWorkerID(w.ID()) {
		t.Fatalf("defaults %+v", w.opts)
	}
}

func TestAPIError(t *testing.T) {
	body := func(msg string) []byte { return []byte(`{"mode":"live","error":{"message":"` + msg + `","type":"x"}}`) }
	cases := []struct {
		status int
		msg    string
		want   error
	}{
		{http.StatusUnauthorized, "worker token required", schemas.ErrWorkerAuth},
		{http.StatusNotFound, "lease: worker w1: worker not registered", schemas.ErrWorkerUnknown},
		{http.StatusNotFound, "manifest: experiment e is not open to workers: not found", schemas.ErrNotFound},
		{http.StatusGone, "lease gone", schemas.ErrLeaseGone},
		{http.StatusConflict, "offset", schemas.ErrUploadOffset},
		{http.StatusRequestEntityTooLarge, "too large", schemas.ErrUploadTooLarge},
		{http.StatusBadRequest, "bad", schemas.ErrWorkerRequest},
	}
	for _, tc := range cases {
		err := apiError(tc.status, body(tc.msg))
		if !errors.Is(err, tc.want) || !strings.Contains(err.Error(), tc.msg) {
			t.Fatalf("%d: %v, want %v", tc.status, err, tc.want)
		}
	}
	if err := apiError(http.StatusInternalServerError, []byte("boom")); err == nil || !strings.Contains(err.Error(), "500: boom") {
		t.Fatalf("500: %v", err)
	}
}

func TestMapDiff(t *testing.T) {
	if d := mapDiff(map[string]string{"a": "1"}, map[string]string{"a": "1"}); d != "" {
		t.Fatalf("equal maps: %q", d)
	}
	d := mapDiff(map[string]string{"a": "1", "c": "3"}, map[string]string{"a": "2", "b": "1"})
	for _, want := range []string{"a 1 here, 2 in the manifest", "b missing here", "c only here"} {
		if !strings.Contains(d, want) {
			t.Fatalf("diff %q lacks %q", d, want)
		}
	}
}

func TestAdoptUnrepeatable(t *testing.T) {
	b := newBench(t)
	remote := b.plan(t, testExperiment())
	e := remote.Experiment
	local := b.plan(t, &e)
	if local.Hash == remote.Hash {
		t.Fatal("two plans hash the same before adopting; the test proves nothing")
	}
	adoptUnrepeatable(local, remote)
	h, err := ManifestHash(local)
	if err != nil {
		t.Fatal(err)
	}
	if h != remote.Hash {
		t.Fatalf("replanned manifest differs in %s", manifestDiff(local, remote))
	}
	local.Experiment.Seed++
	if d := manifestDiff(local, remote); !strings.Contains(d, "experiment") {
		t.Fatalf("diff %q", d)
	}
}

func TestSleepCtx(t *testing.T) {
	if !sleepCtx(t.Context(), time.Millisecond) {
		t.Fatal("live context reported done")
	}
}

// TestAdoptCompatibilityMemberImages is QC2-09-01 / QC2-11-02: a
// compatibility team member's image id the coordinator froze (or deferred
// as empty) is adopted like an ordinary arm member's, so the replanned
// manifest still matches.
func TestAdoptCompatibilityMemberImages(t *testing.T) {
	arm := func(member string) *schemas.ArmManifest {
		return &schemas.ArmManifest{ImageID: "sha256:arm", Members: map[string]schemas.MemberManifest{"w1": {ImageID: member}}}
	}
	local := &schemas.Manifest{Compatibility: arm("sha256:local-build")}
	remote := &schemas.Manifest{Compatibility: arm("")}
	adoptUnrepeatable(local, remote)
	if got := local.Compatibility.Members["w1"].ImageID; got != "" {
		t.Fatalf("compatibility member image %q, want the coordinator's", got)
	}
}
