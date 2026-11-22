package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// liveEnv is a fake environment whose one service shows a screen and takes
// input (Z03, livescreen.go).
type liveEnv struct {
	fakeEnv
	mu       sync.Mutex
	takeover bool
	frames   int
	inputs   []schemas.HumanAction
}

func (e *liveEnv) LiveServices(*schemas.RunContext) []schemas.LiveService {
	return []schemas.LiveService{{ID: "desktop", Takeover: e.takeover}}
}

func (e *liveEnv) LiveFrame(context.Context, *schemas.RunContext, string) ([]byte, string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.frames++
	return []byte{0xFF, 0xD8, 0xFF}, "image/jpeg", nil
}

func (e *liveEnv) LiveInput(_ context.Context, _ *schemas.RunContext, _ string, a schemas.HumanAction) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.inputs = append(e.inputs, a)
	return nil
}

// queueSink hands back its queued inputs on the first publish.
type queueSink struct {
	mu        sync.Mutex
	published []schemas.LiveFrame
	queue     []schemas.HumanAction
	ended     bool
}

func (s *queueSink) Publish(_ context.Context, f schemas.LiveFrame) []schemas.HumanAction {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.published = append(s.published, f)
	q := s.queue
	s.queue = nil
	return q
}

func (s *queueSink) End(string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ended = true
}

func liveState(t *testing.T, env *liveEnv, sink LiveSink, debug bool) (*Pipeline, *runState, string) {
	t.Helper()
	store := newFakeStore(nil, t.TempDir())
	run := &schemas.Run{RunID: "r_live", ExpID: "e_live"}
	dir, err := store.NewRun(context.Background(), &schemas.Slot{ExpID: "e_live", SlotID: "s_live"}, run)
	if err != nil {
		t.Fatal(err)
	}
	m := &schemas.Manifest{}
	if debug {
		m.Experiment.Debug = &schemas.DebugSpec{Mode: schemas.DebugReplay, FromRun: "r_src"}
	}
	p := NewPipeline(&schemas.Registry{Store: store}, "", nil)
	st := &runState{in: SlotInput{Live: sink}, env: env, rc: &schemas.RunContext{Run: run, Manifest: m}}
	return p, st, dir
}

func waitLive(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestLiveTakeoverInDebugRun(t *testing.T) {
	old := liveEvery
	liveEvery = 20 * time.Millisecond
	t.Cleanup(func() { liveEvery = old })
	env := &liveEnv{takeover: true}
	sink := &queueSink{queue: []schemas.HumanAction{{Seq: 1, Action: schemas.HumanClick, X: 3, Y: 4, By: "ana@example.com"}}}
	p, st, dir := liveState(t, env, sink, true)
	stop := p.startLive(context.Background(), st)
	waitLive(t, "two frames", func() bool { sink.mu.Lock(); defer sink.mu.Unlock(); return len(sink.published) >= 2 })
	stop()
	stop() // idempotent
	if !sink.ended {
		t.Error("sink not told the run ended")
	}
	f := sink.published[0]
	if f.RunID != "r_live" || f.Service != "desktop" || !f.Debug || !f.TakeoverOK || f.Mime != "image/jpeg" {
		t.Errorf("frame %+v", f)
	}
	if len(env.inputs) != 1 || env.inputs[0].X != 3 || env.inputs[0].Service != "desktop" {
		t.Fatalf("inputs performed %+v", env.inputs)
	}
	if !st.rc.Run.HumanAssisted {
		t.Error("run not marked human_assisted")
	}
	b, err := os.ReadFile(filepath.Join(dir, schemas.HumanActionsFile))
	if err != nil || !strings.Contains(string(b), `"by":"ana@example.com"`) {
		t.Fatalf("human_actions.jsonl %q %v", b, err)
	}
}

func TestLiveNoTakeoverOutsideDebug(t *testing.T) {
	old := liveEvery
	liveEvery = 20 * time.Millisecond
	t.Cleanup(func() { liveEvery = old })
	env := &liveEnv{takeover: true}
	sink := &queueSink{queue: []schemas.HumanAction{{Seq: 1, Action: schemas.HumanType, Text: "x"}}}
	p, st, dir := liveState(t, env, sink, false)
	stop := p.startLive(context.Background(), st)
	waitLive(t, "a frame", func() bool { sink.mu.Lock(); defer sink.mu.Unlock(); return len(sink.published) >= 2 })
	stop()
	if f := sink.published[0]; f.Debug || f.TakeoverOK {
		t.Errorf("a counted run offered takeover: %+v", f)
	}
	if len(env.inputs) != 0 || st.rc.Run.HumanAssisted {
		t.Fatalf("input performed in a counted run: %+v", env.inputs)
	}
	if _, err := os.Stat(filepath.Join(dir, schemas.HumanActionsFile)); err == nil {
		t.Fatal("human_actions.jsonl written for a counted run")
	}
}

func TestLiveOffWithoutLiveServices(t *testing.T) {
	p, st, _ := liveState(t, nil, nil, false)
	st.env = fakeEnv{part: part{"plain"}}
	p.startLive(context.Background(), st)() // no sampler, no panic
}
