package handlers

import (
	"testing"
)

func TestRunDetailScenario(t *testing.T) {
	idx := testIndex()
	idx.files["r_t01_A"][FileEvidence] = []byte(`{"requests":{"complete":true},
"sessions":[{"id":"s1","index":1,"exit_code":0,"timed_out":false,"started":"2026-11-15T00:00:00Z","ended":"2026-11-15T00:00:01Z","stdout_path":"agent.stream.1.jsonl"}],
"answer":{"text":"7","source":"file","sha256":"abc"},
"state":[{"service":"db","id":"dump","path":"state/db/dump","sha256":"s","bytes":4}],
"services":[{"id":"db","image":"db:1","ready":true}]}`)
	idx.files["r_t01_A"][FileToolLedger] = []byte(`{"seq":1,"server":"db","tool":"query","protocol":"mcp","args_sha256":"a","result_sha256":"b","is_error":false,"started":"2026-11-15T00:00:00Z","latency_ms":3}` + "\n")
	d := fullDeps()
	d.index = idx
	c := newTestClient(t, d)
	body := decodeBody(t, c.do(t, "GET", "/api/bench/runs/r_t01_A", nil, nil))
	for _, k := range []string{"tool_calls", "sessions", "state", "services"} {
		if l, _ := body[k].([]any); len(l) != 1 {
			t.Errorf("%s = %v", k, body[k])
		}
	}
	if a, _ := body["answer"].(map[string]any); a["text"] != "7" || a["source"] != "file" {
		t.Errorf("answer = %v", body["answer"])
	}

	plain := decodeBody(t, c.do(t, "GET", "/api/bench/runs/r_t02_A", nil, nil))
	if _, ok := plain["run"]; !ok {
		t.Fatalf("plain run not found: %v", plain)
	}
	for _, k := range []string{"tool_calls", "sessions", "answer", "state", "services"} {
		if _, ok := plain[k]; ok {
			t.Errorf("plain run has %s", k)
		}
	}
}
