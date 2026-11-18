package monitorcert

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestBuiltin(t *testing.T) {
	names := Builtin()
	if len(names) != 2 {
		t.Fatalf("builtins %v", names)
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			r, err := Resolve(schemas.MonitorConfig{Name: "b", Kind: schemas.MonitorKindBuiltin, Builtin: name, Keywords: keywordsFor(name)}, "")
			if err != nil {
				t.Fatal(err)
			}
			m, err := NewMonitor(r, RunnerDeps{})
			if err != nil {
				t.Fatal(err)
			}
			resps, err := m.Score(context.Background(), []schemas.MonitorRequest{{Mode: schemas.MonitorModeTranscript}})
			if err != nil || len(resps) != 1 {
				t.Fatalf("score: %v %v", resps, err)
			}
		})
	}
}

func keywordsFor(name string) []string {
	if name == BuiltinKeyword {
		return []string{"rm -rf"}
	}
	return nil
}
