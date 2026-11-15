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

func TestRunDetailArtifacts(t *testing.T) {
	idx := testIndex()
	idx.files["r_t01_A"][FileEvidence] = []byte(`{"requests":{"complete":true},
"artifacts":[{"id":"report","name":"report.csv","path":"artifacts/report/report.csv","source":"out/report.csv","sha256":"abc","bytes":12},
{"id":"chart","source":"out/*.svg","bytes":0,"error":"missing"}]}`)
	d := fullDeps()
	d.index = idx
	c := newTestClient(t, d)
	body := decodeBody(t, c.do(t, "GET", "/api/bench/runs/r_t01_A", nil, nil))
	files, _ := body["artifacts"].([]any)
	if len(files) != 2 {
		t.Fatalf("artifacts = %v", body["artifacts"])
	}
	first, _ := files[0].(map[string]any)
	if first["id"] != "report" || first["name"] != "report.csv" || first["path"] != "artifacts/report/report.csv" ||
		first["sha256"] != "abc" || first["bytes"] != float64(12) {
		t.Errorf("artifacts[0] = %v", first)
	}
	second, _ := files[1].(map[string]any)
	if second["error"] != "missing" {
		t.Errorf("artifacts[1] = %v", second)
	}
	if _, ok := second["path"]; ok {
		t.Errorf("failed artifact has a path: %v", second)
	}

	plain := decodeBody(t, c.do(t, "GET", "/api/bench/runs/r_t02_A", nil, nil))
	if _, ok := plain["artifacts"]; ok {
		t.Errorf("plain run has artifacts: %v", plain["artifacts"])
	}
}
