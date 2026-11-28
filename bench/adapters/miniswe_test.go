package adapters

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// The sample testdata/mini-swe-agent-2.4.6-sample.stdout is hand-made, since
// no real run's trajectory was kept: the stdout that the launch wrapper in
// agents/mini-swe-agent.yaml prints. The trajectory holds
// only fields the mini-swe-agent 2.4.6 source writes (see the field list
// above miniTrajectory in miniswe.go: agents/default.py serialize,
// models/litellm_model.py query and serialize,
// models/utils/actions_toolcall.py, environments/local.py). Three model
// calls: pytest (exit 1), a format error, sed plus pytest (exit 0), then the
// submit command, ending with exit_status Submitted.
func miniSample(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "mini-swe-agent-2.4.6-sample.stdout"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func parseMini(t *testing.T, raw string) *schemas.ParseResult {
	t.Helper()
	agent := commandAgent()
	agent.Model = "gpt-6-luna"
	agent.Gateway.Provider = "openai"
	rc, _ := fixture(t, agent)
	res, err := newMiniSWEParser().Parse(context.Background(), rc, []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestMiniSWEParseSample(t *testing.T) {
	res := parseMini(t, miniSample(t))
	d := res.Diagnostics
	if d.Quality != schemas.TraceQualityComplete || d.Source != schemas.TraceSourceNative || d.Parser != ParserMiniSWE || d.Truncated || d.Reason != "" {
		t.Fatalf("diagnostics = %+v", d)
	}
	if d.Lines != 9 || d.Recognized != 9 || d.Unrecognized != 0 || d.Malformed != 0 || d.Emitted != len(res.Events) {
		t.Fatalf("counts = %+v", d)
	}
	for _, tc := range []struct {
		typ  schemas.EventType
		want int
	}{
		{schemas.EventModelResponse, 3},
		{schemas.EventToolCall, 3},
		{schemas.EventToolResult, 2},
		{schemas.EventError, 1}, // the format error; Submitted is not an error
	} {
		if got := len(eventsOf(res.Events, tc.typ)); got != tc.want {
			t.Errorf("%s events = %d, want %d", tc.typ, got, tc.want)
		}
	}
	calls, results, turns := eventsOf(res.Events, schemas.EventToolCall), eventsOf(res.Events, schemas.EventToolResult), eventsOf(res.Events, schemas.EventModelResponse)
	if calls[0].ToolName != "bash" || calls[0].Target != "python -m pytest -q" || calls[0].ParentEventID != turns[0].EventID || calls[0].ArgsHash == "" {
		t.Errorf("first call = %+v", calls[0])
	}
	if calls[2].Target != "echo COMPLETE_TASK_AND_SUBMIT_FINAL_OUTPUT" {
		t.Errorf("submit call = %+v", calls[2])
	}
	if r := results[0]; r.ParentEventID != calls[0].EventID || r.ExitStatus == nil || *r.ExitStatus != 1 || !r.IsError || r.Summary != "exit 1: 1 failed in 0.01s" {
		t.Errorf("first result = %+v", r)
	}
	if r := results[1]; r.ExitStatus == nil || *r.ExitStatus != 0 || r.IsError || r.Target != calls[1].Target {
		t.Errorf("second result = %+v", r)
	}
	var fresh, cached, out int64
	for _, e := range turns {
		if e.Tokens == nil || e.ModelServed != "gpt-6-luna" || e.USD != nil {
			t.Fatalf("turn = %+v", e)
		}
		fresh, cached, out = fresh+e.Tokens.Fresh, cached+e.Tokens.CacheRead, out+e.Tokens.Out
	}
	if fresh != 1200+476+292 || cached != 1024+1408 || out != 120 {
		t.Errorf("tokens fresh %d cached %d out %d", fresh, cached, out)
	}
	if turns[0].TS.IsZero() || turns[0].Summary != "Run the tests first." {
		t.Errorf("turn 1 = %+v", turns[0])
	}
	if res.Claim == nil || !res.Claim.Made || res.Claim.Source != schemas.EvidenceNative {
		t.Errorf("claim = %+v", res.Claim)
	}
}

func TestMiniSWEParseBlockStates(t *testing.T) {
	sample := miniSample(t)
	begin := strings.Index(sample, MiniSWETrajectoryBegin)
	end := strings.Index(sample, MiniSWETrajectoryEnd)
	cut := begin + (end-begin)/2 // inside the messages array
	for _, tc := range []struct {
		name      string
		raw       string
		quality   schemas.TraceQuality
		truncated bool
		minEvents int
	}{
		{"whole", sample, schemas.TraceQualityComplete, false, 9},
		{"cut by the output cap", sample[:cut], schemas.TraceQualityPartial, true, 1},
		{"end marker but short body", strings.Replace(sample, "bytes=", "bytes=9", 1), schemas.TraceQualityPartial, true, 9},
		{"no block", "\n", schemas.TraceQualityDegraded, false, 0},
		{"stray line beside a whole block", "stray\n" + sample, schemas.TraceQualityPartial, false, 9},
		{"block not json", MiniSWETrajectoryBegin + "3\nxyz\n" + MiniSWETrajectoryEnd + "\n", schemas.TraceQualityDegraded, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := parseMini(t, tc.raw)
			d := res.Diagnostics
			if d.Quality != tc.quality || d.Truncated != tc.truncated || len(res.Events) < tc.minEvents {
				t.Fatalf("quality %s truncated %v events %d: %+v", d.Quality, d.Truncated, len(res.Events), d)
			}
			if tc.quality != schemas.TraceQualityComplete && d.Reason == "" {
				t.Errorf("no reason for %s", d.Quality)
			}
		})
	}
}

// TestMiniSWEExitStatusClaim: only Submitted is a claim; other exits are an
// error event and a negative claim.
func TestMiniSWEExitStatusClaim(t *testing.T) {
	sample := miniSample(t)
	for status, made := range map[string]bool{"Submitted": true, "LimitsExceeded": false, "RepeatedFormatError": false} {
		raw := strings.ReplaceAll(sample, `"exit_status": "Submitted"`, `"exit_status": "`+status+`"`)
		raw = fixMiniSize(raw)
		res := parseMini(t, raw)
		if res.Claim == nil || res.Claim.Made != made || res.Diagnostics.Quality != schemas.TraceQualityComplete {
			t.Errorf("%s: claim %+v quality %s", status, res.Claim, res.Diagnostics.Quality)
		}
		if errs := len(eventsOf(res.Events, schemas.EventError)); (errs == 2) == made {
			t.Errorf("%s: %d error events", status, errs)
		}
	}
}

// fixMiniSize rewrites the begin line's size after an edit of the body.
func fixMiniSize(raw string) string {
	b := strings.Index(raw, MiniSWETrajectoryBegin)
	nl := b + strings.Index(raw[b:], "\n")
	e := strings.Index(raw, "\n"+MiniSWETrajectoryEnd)
	return raw[:b] + MiniSWETrajectoryBegin + strconv.Itoa(e-(nl+1)) + raw[nl:]
}
