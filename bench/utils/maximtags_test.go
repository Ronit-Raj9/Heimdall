package utils

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestMaximHeaders(t *testing.T) {
	if MaximHeaders(nil, MaximRoleJudge) != nil || MaximHeaders(&schemas.RunContext{}, MaximRoleJudge) != nil {
		t.Fatal("headers without a run")
	}
	rc := &schemas.RunContext{Run: &schemas.Run{RunID: "r_1", ExpID: "e_1"}, Slot: &schemas.Slot{Task: "fix bug\r\nX-Evil: 1", Arm: "B"}}
	h := MaximHeaders(rc, MaximRoleSimUser)
	want := map[string]string{
		"x-bf-maxim-session-id": "heimdall-e_1", "x-bf-maxim-heimdall-run": "r_1", "x-bf-maxim-heimdall-experiment": "e_1",
		"x-bf-maxim-heimdall-task": "fix_bugX-Evil:_1", "x-bf-maxim-heimdall-arm": "B", "x-bf-maxim-heimdall-role": "simulated_user",
		"x-bf-maxim-generation-name": "heimdall simulated_user", "x-bf-maxim-source": "heimdall",
	}
	for k, v := range want {
		if h[k] != v {
			t.Fatalf("%s = %q, want %q (all %v)", k, h[k], v, h)
		}
	}
	ctx := WithMaximHeaders(context.Background(), map[string]string{"x-bf-maxim-a": "1", "x-bf-vk": "leak", "x-bf-maxim-b": ""})
	got := MaximHeadersFrom(ctx)
	if len(got) != 1 || got[0] != [2]string{"x-bf-maxim-a", "1"} {
		t.Fatalf("from ctx %v", got)
	}
	if len(MaximHeadersFrom(context.Background())) != 0 {
		t.Fatal("headers from an empty context")
	}
}
