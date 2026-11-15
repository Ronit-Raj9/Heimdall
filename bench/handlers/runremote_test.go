package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRunRemote(t *testing.T) {
	d := fullDeps()
	idx := stepsFixture(t)
	dir := filepath.Join(idx.root, "r_t01_A")
	ev := `{"requests":{"complete":false},"remote":{"channels":{"model_traffic":"off","tools":"on","self_report":"on"},` +
		`"trusted":["outcome (verifier)","tool calls through the published tool proxy (tool ledger)"],"agent_reported":["reported_tokens"],` +
		`"unknown":["tokens"],"reported":{"input_tokens":10,"output_tokens":2,"cost_usd":0.1,"model":"m","version":"<b>v1</b>","build":"b",` +
		`"events":3,"events_dropped":1,"artifacts":["out/a.csv"],"async":true,"notes":["n"]}}}`
	if err := os.WriteFile(filepath.Join(dir, FileEvidence), []byte(ev), 0o644); err != nil {
		t.Fatal(err)
	}
	d.index = idx
	c := newTestClient(t, d)
	resp := c.do(t, "GET", APIPrefix+"/runs/r_t01_A/remote", nil, nil)
	if resp.StatusCode() != 200 {
		t.Fatalf("status = %d: %s", resp.StatusCode(), resp.Body())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body(), &body); err != nil {
		t.Fatal(err)
	}
	checkKeys(t, "RunRemote", body, loadKeys(t, "RunRemote"), true)
	for path, typ := range map[string]string{"remote": "RemoteEvidence", "remote.channels": "RemoteChannels", "remote.reported": "RemoteReport"} {
		for _, obj := range objectsAt(t, body, path) {
			checkKeys(t, "RunRemote."+path, obj, loadKeys(t, typ), false)
		}
	}
	var out RunRemote
	if err := json.Unmarshal(resp.Body(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Available || out.Remote.Channels.Tools != "on" || out.Remote.Reported.Version != "<b>v1</b>" {
		t.Fatalf("remote = %+v", out)
	}

	// A run without remote evidence: available false, remote null.
	resp = c.do(t, "GET", APIPrefix+"/runs/r_t02_A/remote", nil, nil)
	if resp.StatusCode() != 200 {
		t.Fatalf("status = %d: %s", resp.StatusCode(), resp.Body())
	}
	out = RunRemote{}
	if err := json.Unmarshal(resp.Body(), &out); err != nil || out.Available || out.Remote != nil {
		t.Fatalf("old run = %+v %v", out, err)
	}
}
