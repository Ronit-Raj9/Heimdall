package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/jobs"
)

type ibNopLog struct{}

func (ibNopLog) Printf(string, ...any)       {}
func (ibNopLog) Warnf(string, ...any)        {}
func (ibNopLog) Errorf(string, ...any)       {}
func (ibNopLog) Write(p []byte) (int, error) { return len(p), nil }

// The server's pattern scrub of job results turned the test id
// test_url[http://user:pass@example.com] into [redacted]; the emitted task's
// reference answer then failed admission (real hosted run). An ExactResult
// kind keeps its result as the kind redacted it.
func TestRunRedactedExactResultKeepsTaskContent(t *testing.T) {
	id := "tests/test_validate.py::test_url_absolute_valid[http://user:pass@example.com]"
	run := func(context.Context, jobEnv, *jobs.Job, jobs.Logger, func(any)) (any, error) {
		return map[string]any{"pass_to_pass": []string{id}}, nil
	}
	for _, exact := range []bool{true, false} {
		res, err := runRedacted(context.Background(), jobKindSpec{Run: run, ExactResult: exact}, jobEnv{}, &jobs.Job{}, ibNopLog{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(res)
		if kept := strings.Contains(string(raw), "user:pass@"); kept != exact {
			t.Fatalf("exact=%v: result %s", exact, raw)
		}
	}
}
