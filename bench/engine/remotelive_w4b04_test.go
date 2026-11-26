package engine

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/plaintext"
	"github.com/maximhq/bifrost/bench/schemas"
)

// TestLiveEventMessageCleaned: on a hosted server a runner's live event
// message reaches the page with no server path or organization id (wave 4,
// F1.3).
func TestLiveEventMessageCleaned(t *testing.T) {
	saved := plaintext.DefaultRoots
	t.Cleanup(func() { plaintext.DefaultRoots = saved })
	plaintext.DefaultRoots = func() []string { return []string{"/var/lib/heimdall/runs"} }
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
	ev := make(chan Event, 16)
	rw.run.Events = ev
	done := rw.attach(t)
	defer done()
	w := rw.worker(t, "w1", newRemoteSide(t))
	l := lease(t, w)
	if l == nil {
		t.Fatal("no lease")
	}
	drainLive(ev, 50*time.Millisecond)
	msg := "slot org_org01m7yyh2wft8r2x5tjyxthksbj_openai_20: 503 from conversation call 2"
	if err := w.call(context.Background(), http.MethodPost, workerAPI+"/leases/"+l.LeaseID+"/heartbeat",
		schemas.WorkerHeartbeat{WorkerID: "w1", Events: []schemas.WorkerEvent{{Kind: EventStage, RunID: "r1", Stage: schemas.StageRun, Message: msg}}}, nil); err != nil {
		t.Fatal(err)
	}
	got := drainLive(ev, 100*time.Millisecond)
	if len(got) != 1 {
		t.Fatalf("events %+v", got)
	}
	if m := got[0].Message; strings.Contains(m, "org_") || m != "provider slot openai 20: 503 from conversation call 2" {
		t.Fatalf("message %q", m)
	}
}
