package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMonitorCertifyArgs(t *testing.T) {
	argv, err := monitorCertifyArgs(json.RawMessage(`{"monitor":"monitors/guard.yaml","exp_ids":["e_1","e_2"],"fpr":"0.01","parallel":2}`))
	if err != nil {
		t.Fatal(err)
	}
	want := "monitor certify -config {{config}} -monitor monitors/guard.yaml -exp e_1 -exp e_2 -fpr 0.01 -parallel 2"
	if got := strings.Join(argv, " "); got != want {
		t.Fatalf("argv = %q, want %q", got, want)
	}
	for _, bad := range []string{
		`{"monitor":"/etc/passwd","exp_ids":["e_1"]}`,
		`{"monitor":"../x.yaml","exp_ids":["e_1"]}`,
		`{"monitor":"m.yaml","exp_ids":[]}`,
		`{"monitor":"m.yaml","exp_ids":["-config=x"]}`,
		`{"monitor":"m.yaml","exp_ids":["e_1"],"fpr":"1;rm"}`,
		`{"monitor":"m.yaml","exp_ids":["e_1"],"parallel":99}`,
	} {
		if _, err := monitorCertifyArgs(json.RawMessage(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}
