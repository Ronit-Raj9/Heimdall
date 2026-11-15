package main

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/evidence"
	"github.com/maximhq/bifrost/bench/schemas"
)

func TestRemoteRows(t *testing.T) {
	if remoteRows(&checkInput{agent: &schemas.AgentConfig{}}) != nil {
		t.Fatal("a local agent got a remote row")
	}
	remote := &schemas.RemoteAgent{URL: "https://a", Tools: schemas.RemoteToolsExpose, ModelTraffic: "gateway"}
	in := &checkInput{agent: &schemas.AgentConfig{Remote: remote}}
	if rows := remoteRows(in); len(rows) != 1 || rows[0].Status != checkWarn {
		t.Fatalf("no summary: %+v", rows)
	}
	in.ev.Remote = evidence.RemoteSummary(remote, true, &schemas.RemoteReport{Version: "v9"})
	rows := remoteRows(in)
	if rows[0].Status != checkPass || !strings.Contains(rows[0].Detail, "tool ledger") || !strings.Contains(rows[0].Detail, `"v9"`) {
		t.Fatalf("all channels: %+v", rows)
	}
	in.ev.Remote = evidence.RemoteSummary(remote, false, nil)
	if rows := remoteRows(in); rows[0].Status != checkFail {
		t.Fatalf("tools missing: %+v", rows)
	}
	remote.ModelTraffic = ""
	in.ev.Remote = evidence.RemoteSummary(remote, true, nil)
	if rows := remoteRows(in); rows[0].Status != checkWarn || !strings.Contains(rows[0].Detail, "unknown: tokens") {
		t.Fatalf("external traffic: %+v", rows)
	}
}

func TestRemoteToolsCheck(t *testing.T) {
	cases := []struct {
		listen, public string
		want           schemas.DoctorStatus
	}{
		{"", "", schemas.DoctorPass},
		{"127.0.0.1:9400", "http://127.0.0.1:19400", schemas.DoctorPass},
		{"10.0.0.5:0", "http://10.0.0.5:{port}", schemas.DoctorWarn},
		{"localhost:1", "", schemas.DoctorFail},
		{"", "ftp://x", schemas.DoctorFail},
	}
	for _, tc := range cases {
		if got := remoteToolsCheck(tc.listen, tc.public); got.Status != tc.want {
			t.Errorf("%q %q: %+v, want %s", tc.listen, tc.public, got, tc.want)
		}
	}
}
