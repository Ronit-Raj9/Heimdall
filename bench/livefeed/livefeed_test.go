package livefeed

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

var jpeg = []byte{0xFF, 0xD8, 0xFF, 0xE0}

func testHub() (*Hub, *time.Time) {
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	h := New()
	h.now = func() time.Time { return now }
	return h, &now
}

func TestFrameFreshness(t *testing.T) {
	h, now := testHub()
	if _, ok := h.Frame("r1"); ok {
		t.Fatal("frame before publish")
	}
	h.Publish(schemas.LiveFrame{RunID: "r1", Service: "browser", Data: jpeg})
	if f, ok := h.Frame("r1"); !ok || f.Service != "browser" || f.At.IsZero() {
		t.Fatalf("frame %+v %v", f, ok)
	}
	*now = now.Add(FrameTTL + time.Second)
	if _, ok := h.Frame("r1"); ok {
		t.Fatal("a stale frame is still shown")
	}
	h.Publish(schemas.LiveFrame{RunID: "", Data: jpeg})
	h.Publish(schemas.LiveFrame{RunID: "r2"})
	h.Publish(schemas.LiveFrame{RunID: "r3", Data: make([]byte, schemas.MaxLiveFrameBytes+1)})
	for _, id := range []string{"", "r2", "r3"} {
		if _, ok := h.Frame(id); ok {
			t.Errorf("bad frame %q published", id)
		}
	}
	*now = now.Add(forgetAfter + time.Second)
	h.Publish(schemas.LiveFrame{RunID: "r9", Data: jpeg})
	if _, ok := h.runs["r1"]; ok {
		t.Error("old entry not pruned")
	}
	h.End("r9")
	if _, ok := h.Frame("r9"); ok {
		t.Error("ended run still live")
	}
}

func TestQueueOnlyDebugTakeover(t *testing.T) {
	h, _ := testHub()
	click := schemas.HumanAction{Action: schemas.HumanClick, X: 1, Y: 2}
	if _, err := h.Queue("r1", click); !errors.Is(err, ErrNotLive) {
		t.Fatalf("not live: %v", err)
	}
	h.Publish(schemas.LiveFrame{RunID: "r1", Service: "desktop", Data: jpeg, TakeoverOK: true})
	if _, err := h.Queue("r1", click); !errors.Is(err, ErrNotDebug) {
		t.Fatalf("not debug: %v", err)
	}
	h.Publish(schemas.LiveFrame{RunID: "r1", Service: "desktop", Data: jpeg, Debug: true})
	if _, err := h.Queue("r1", click); !errors.Is(err, ErrNoTakeover) {
		t.Fatalf("no input command: %v", err)
	}
	h.Publish(schemas.LiveFrame{RunID: "r1", Service: "desktop", Data: jpeg, Debug: true, TakeoverOK: true})
	other := click
	other.Service = "browser"
	if _, err := h.Queue("r1", other); !errors.Is(err, ErrServiceGone) {
		t.Fatalf("other service: %v", err)
	}
	for i := 1; i <= MaxQueued; i++ {
		a, err := h.Queue("r1", click)
		if err != nil || a.Seq != i || a.Service != "desktop" || a.At.IsZero() {
			t.Fatalf("queue %d: %+v %v", i, a, err)
		}
	}
	if _, err := h.Queue("r1", click); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("full: %v", err)
	}
	if h.Pending("r1") != MaxQueued {
		t.Fatalf("pending %d", h.Pending("r1"))
	}
	if got := h.Take("r1"); len(got) != MaxQueued || h.Pending("r1") != 0 || h.Take("r1") != nil {
		t.Fatalf("take %d", len(got))
	}
	// A frame that drops takeover drops the waiting inputs.
	if _, err := h.Queue("r1", click); err != nil {
		t.Fatal(err)
	}
	h.Publish(schemas.LiveFrame{RunID: "r1", Service: "desktop", Data: jpeg})
	if h.Pending("r1") != 0 {
		t.Fatal("inputs kept for a run that cannot take them")
	}
}

func TestCheckAction(t *testing.T) {
	good := []schemas.HumanAction{
		{Action: "click", X: 0, Y: 0}, {Action: "type", Text: "hello"}, {Action: "key", Combo: "ctrl+s"}, {Action: "scroll", Direction: "down"},
	}
	for _, a := range good {
		if err := CheckAction(a); err != nil {
			t.Errorf("%+v: %v", a, err)
		}
	}
	bad := []schemas.HumanAction{
		{Action: "drag"}, {Action: "click", X: -1}, {Action: "type"}, {Action: "type", Text: strings.Repeat("a", schemas.MaxHumanText+1)},
		{Action: "key"}, {Action: "key", Combo: "ctrl s"}, {Action: "scroll", Direction: "sideways"},
	}
	for _, a := range bad {
		if err := CheckAction(a); err == nil {
			t.Errorf("%+v accepted", a)
		}
	}
}

func TestForSharesLocal(t *testing.T) {
	if For("") != For("local") || For("org-a") == For("org-b") || For("org-a") != For("org-a") {
		t.Fatal("hub per org")
	}
}
