package handlers

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/schemas"
)

func TestEventRecorderSince(t *testing.T) {
	l := &fakeLauncher{events: []schemas.LiveEvent{{Kind: "slot_started", SlotID: "s1"}, {Kind: "slot_sealed", SlotID: "s1"}}}
	r := newEventRecorder(l)
	defer r.close()
	r.follow(testExp)
	deadline := time.Now().Add(2 * time.Second)
	for {
		events, next, _ := r.since(testExp, 0)
		if len(events) == 2 {
			if events[0].Seq != 1 || events[1].Seq != 2 || next != 2 {
				t.Fatalf("seqs %d %d next %d", events[0].Seq, events[1].Seq, next)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("recorder got %d events", len(events))
		}
		time.Sleep(5 * time.Millisecond)
	}
	if events, next, truncated := r.since(testExp, 1); len(events) != 1 || next != 2 || truncated {
		t.Fatalf("since 1 = %v %d %v", events, next, truncated)
	}
	if events, _, _ := r.since("e_other", 0); len(events) != 0 {
		t.Fatalf("other experiment has events: %v", events)
	}
}

func TestStreamSSE(t *testing.T) {
	ch := make(chan schemas.LiveEvent, 2)
	ch <- schemas.LiveEvent{Seq: 7, Kind: "stage", RunID: "r1", Stage: schemas.StageRun}
	close(ch)
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	cancelled := false
	streamSSE(w, testMode, ch, func() { cancelled = true }, make(chan struct{}), func() bool { return true })
	out := buf.String()
	for _, want := range []string{"retry: 2000\n\n", "id: 7\ndata: {\"mode\":\"recorded\",\"seq\":7,\"kind\":\"stage\"", "event: end\ndata: {\"mode\":\"recorded\""} {
		if !strings.Contains(out, want) {
			t.Errorf("stream missing %q:\n%s", want, out)
		}
	}
	if !cancelled {
		t.Error("subscription not cancelled")
	}
}

func TestEventsEndpointServesSSE(t *testing.T) {
	d := fullDeps()
	d.launcher = &fakeLauncher{events: []schemas.LiveEvent{{Seq: 1, Kind: "slot_started", SlotID: "s_t01_A"}}}
	resp := newTestClient(t, d).do(t, "GET", "/api/bench/experiments/e_test/events", nil, map[string]string{"Accept": "text/event-stream"})
	if ct := string(resp.Header.ContentType()); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content type = %q", ct)
	}
	lines := readSSE(resp.Body())
	found := false
	for _, l := range lines {
		if strings.HasPrefix(l, "data: ") && strings.Contains(l, `"kind":"slot_started"`) && strings.Contains(l, `"mode":"recorded"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no slot_started data line in %q", lines)
	}
}

// TestStreamsEndWhenAccessRevoked is audit qc2-17-04: an open event or job
// log stream stops sending once the request's stream guard refuses.
func TestStreamsEndWhenAccessRevoked(t *testing.T) {
	ch := make(chan schemas.LiveEvent, 2)
	ch <- schemas.LiveEvent{Seq: 1, Kind: "stage", RunID: "r_secret"}
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	done := make(chan struct{})
	go func() {
		streamSSE(w, testMode, ch, nil, make(chan struct{}), func() bool { return false })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("event stream kept running after access was revoked")
	}
	if strings.Contains(buf.String(), "r_secret") {
		t.Fatalf("event sent after access was revoked:\n%s", buf.String())
	}
	page := &jobs.LogPage{JobID: "j_1", Status: jobs.StatusRunning}
	buf.Reset()
	jdone := make(chan struct{})
	go func() {
		streamJobLogs(bufio.NewWriter(&buf), testMode, nil, "org_a", "j_1", page, make(chan struct{}), func() bool { return false })
		close(jdone)
	}()
	select {
	case <-jdone:
	case <-time.After(5 * time.Second):
		t.Fatal("job log stream kept running after access was revoked")
	}
}
