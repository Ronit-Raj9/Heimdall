package compat

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestCheckDefaults(t *testing.T) {
	codex := &schemas.AgentConfig{ID: "codex", Adapter: "command", Image: "bench/agent-codex:1"}
	remote := &schemas.AgentConfig{ID: "helpdesk-bot", Adapter: "remote_http"}
	coding := &schemas.Suite{ID: "hd-harbor-tb2", Track: "terminal"}
	memory := &schemas.Suite{ID: "longmemeval", Track: "memory"}
	support := &schemas.Suite{ID: "tau2", Track: "support"}

	if ok, missing, reason := Check(codex, coding); !ok || len(missing) != 0 || reason != "" {
		t.Fatalf("codex on terminal: %v %v %q", ok, missing, reason)
	}
	// CLI coding agents run memory benchmarks across sessions through the
	// per-session notes and memory folder, so they default to sessions.
	if ok, missing, _ := Check(codex, memory); !ok || len(missing) != 0 {
		t.Fatalf("codex on memory: %v %v", ok, missing)
	}
	plain := &schemas.AgentConfig{ID: "custom-python", Adapter: "command"}
	ok, missing, reason := Check(plain, memory)
	if ok || len(missing) != 1 || missing[0] != schemas.CapSessions {
		t.Fatalf("custom-python on memory: %v %v", ok, missing)
	}
	if reason != "agent custom-python cannot run benchmark longmemeval: needs sessions" {
		t.Fatalf("reason = %q", reason)
	}
	if ok, _, _ := Check(remote, support); !ok {
		t.Fatal("remote_http on support should be ok")
	}
	if ok, missing, _ := Check(remote, coding); ok || missing[0] != schemas.CapShell {
		t.Fatalf("remote_http on terminal: %v %v", ok, missing)
	}
}

func TestCheckExplicitAndRequires(t *testing.T) {
	a := &schemas.AgentConfig{ID: "cu", Adapter: "command", Capabilities: schemas.CapabilitySet{"desktop", "shell"}}
	s := &schemas.Suite{ID: "x", Track: "coding", Requires: schemas.CapabilitySet{"gpu", "shell"}}
	ok, missing, reason := Check(a, s)
	if ok || len(missing) != 1 || missing[0] != schemas.CapGPU || !strings.HasSuffix(reason, "needs gpu") {
		t.Fatalf("%v %v %q", ok, missing, reason)
	}
	if ok, _, _ := Check(nil, s); ok {
		t.Fatal("nil agent ok")
	}
	if ok, _, _ := Check(a, nil); ok {
		t.Fatal("nil suite ok")
	}
}

func TestScriptedRunsAnything(t *testing.T) {
	a := &schemas.AgentConfig{ID: "oracle", Adapter: "scripted"}
	for _, tr := range []string{"voice", "computer_use", "ml_engineering", "memory", "browser"} {
		if ok, m, _ := Check(a, &schemas.Suite{ID: tr, Track: tr}); !ok {
			t.Fatalf("scripted on %s: missing %v", tr, m)
		}
	}
}
