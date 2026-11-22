package utils

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestRemoteEgressTarget(t *testing.T) {
	cases := map[string]string{
		"https://agent.example.com/v1":       "agent.example.com:443",
		"http://agent.example.com/v1":        "agent.example.com:80",
		"https://agent.example.com:8443/x?y": "agent.example.com:8443",
		"http://[::1]:9000/":                 "[::1]:9000",
	}
	for in, want := range cases {
		got, err := RemoteEgressTarget(in)
		if err != nil || got != want {
			t.Fatalf("%s: %q %v, want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "ftp://x/", "https:///path", "https://u:p@x/", "::"} {
		if _, err := RemoteEgressTarget(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

// fakeDocker writes a docker stand-in that records its arguments, one call
// per line, and fails when its first argument is fail.
func fakeDocker(t *testing.T, fail string) (Docker, string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := "#!/bin/sh\nprintf '%s' \"$*\" | tr '\\n' ' ' >> " + log + "\necho >> " + log + "\n"
	if fail != "" {
		script += "case \"$1 $2\" in \"" + fail + "\"*) echo boom >&2; exit 1;; esac\n"
	}
	bin := filepath.Join(dir, "docker")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return Docker{Binary: bin}, log
}

func calls(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func TestStartRemoteEgress(t *testing.T) {
	ctx := context.Background()
	rc := &schemas.RunContext{Run: &schemas.Run{RunID: "r_1", ExpID: "e_1"}, Dir: t.TempDir()}
	d, log := fakeDocker(t, "")
	if e, err := StartRemoteEgress(ctx, d, rc, &schemas.RunPlan{Image: "img"}, "eval"); e != nil || err != nil || calls(t, log) != nil {
		t.Fatalf("plan without egress started something: %v %v", e, err)
	}
	plan := &schemas.RunPlan{Image: "img", Egress: []string{"agent.example.com:443"}}
	e, err := StartRemoteEgress(ctx, d, rc, plan, "eval")
	if err != nil {
		t.Fatal(err)
	}
	got := calls(t, log)
	if len(got) != 4 || !strings.HasPrefix(got[0], "network create") || !strings.HasSuffix(got[0], "bench-eg-r_1") ||
		!strings.Contains(got[1], "--cap-drop ALL") || !strings.Contains(got[1], "--network bench-eg-r_1") ||
		!strings.Contains(got[1], "BENCH_EGRESS_TARGET=agent.example.com:443") || !strings.Contains(got[1], "--read-only") ||
		!strings.Contains(got[1], "bifrost-bench.run=r_1") || got[2] != "network connect eval bench-eg-r_1" || got[3] != "start bench-eg-r_1" {
		t.Fatalf("docker calls:\n%s", strings.Join(got, "\n"))
	}
	var rec EgressRecord
	b, err := os.ReadFile(filepath.Join(rc.Dir, RemoteEgressFile))
	if err == nil {
		err = json.Unmarshal(b, &rec)
	}
	if err != nil || len(rec.Allowed) != 1 || rec.Allowed[0] != "agent.example.com:443" || rec.Joined != "eval" {
		t.Fatalf("record %+v %v", rec, err)
	}
	e.Stop(ctx)
	got = calls(t, log)
	if got[len(got)-2] != "rm -f bench-eg-r_1" || got[len(got)-1] != "network rm bench-eg-r_1" {
		t.Fatalf("stop calls %v", got[4:])
	}
	var none *RemoteEgress
	none.Stop(ctx)

	bad := []struct {
		plan    *schemas.RunPlan
		network string
		want    string
	}{
		{&schemas.RunPlan{Egress: []string{"a:1", "b:2", "c:3"}}, "eval", "at most 2"},
		{&schemas.RunPlan{Egress: []string{"nohost"}}, "eval", "destination"},
		{&schemas.RunPlan{Egress: []string{"a:99999"}}, "eval", "not host:port"},
		{&schemas.RunPlan{Egress: []string{"a:1"}}, "none", "cannot be joined"},
		{&schemas.RunPlan{Egress: []string{"a:1"}}, "host", "cannot be joined"},
	}
	for _, tc := range bad {
		if _, err := StartRemoteEgress(ctx, d, rc, tc.plan, tc.network); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%v on %s: %v, want %q", tc.plan.Egress, tc.network, err, tc.want)
		}
	}
}

// Y11: a remote coding agent's run reaches its endpoint and Heimdall's pull
// endpoint, each through its own forwarder.
func TestStartRemoteEgressTwoDestinations(t *testing.T) {
	d, log := fakeDocker(t, "")
	rc := &schemas.RunContext{Run: &schemas.Run{RunID: "r_3", ExpID: "e_1"}, Dir: t.TempDir()}
	e, err := StartRemoteEgress(context.Background(), d, rc, &schemas.RunPlan{Image: "img", Egress: []string{"agent.example.com:443", "heimdall.example.com:443"}}, "eval")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(calls(t, log), "\n")
	for _, want := range []string{"BENCH_EGRESS_TARGET=agent.example.com:443", "BENCH_EGRESS_TARGET=heimdall.example.com:443", "start bench-eg-r_3\n", "start bench-eg-r_3-2"} {
		if !strings.Contains(got+"\n", want) {
			t.Fatalf("calls lack %q:\n%s", want, got)
		}
	}
	if RemoteEgressAddrN("r_3", 1) != "bench-eg-r_3-2:9300" || RemoteEgressAddrN("r_3", 0) != RemoteEgressAddr("r_3") {
		t.Fatal("forwarder addresses")
	}
	var rec EgressRecord
	b, _ := os.ReadFile(filepath.Join(rc.Dir, RemoteEgressFile))
	if err := json.Unmarshal(b, &rec); err != nil || len(rec.Allowed) != 2 {
		t.Fatalf("record %s %v", b, err)
	}
	e.Stop(context.Background())
	got = strings.Join(calls(t, log), "\n")
	if !strings.Contains(got, "rm -f bench-eg-r_3-2") || !strings.Contains(got, "network rm bench-eg-r_3\n") {
		t.Fatalf("stop calls %s", got)
	}
}

func TestStartRemoteEgressCleansUpOnFailure(t *testing.T) {
	d, log := fakeDocker(t, "network connect")
	rc := &schemas.RunContext{Run: &schemas.Run{RunID: "r_2", ExpID: "e_1"}, Dir: t.TempDir()}
	_, err := StartRemoteEgress(context.Background(), d, rc, &schemas.RunPlan{Image: "img", Egress: []string{"a:1"}}, "eval")
	if err == nil {
		t.Fatal("failed start reported success")
	}
	got := calls(t, log)
	if got[len(got)-2] != "rm -f bench-eg-r_2" || got[len(got)-1] != "network rm bench-eg-r_2" {
		t.Fatalf("no cleanup after a failed start: %v", got)
	}
	if _, err := os.Stat(filepath.Join(rc.Dir, RemoteEgressFile)); err == nil {
		t.Fatal("a failed start was recorded as allowed egress")
	}
}

func TestRemoteEgressNoteAndExternalTraffic(t *testing.T) {
	if RemoteEgressNote(nil) != "" || RemoteEgressNote(&schemas.RunContext{Agent: &schemas.AgentConfig{}}) != "" {
		t.Fatal("note for a run without a remote agent")
	}
	remote := &schemas.RunContext{Agent: &schemas.AgentConfig{Remote: &schemas.RemoteAgent{URL: "https://agent.example.com/run"}}}
	if n := RemoteEgressNote(remote); !strings.Contains(n, "exactly agent.example.com:443") {
		t.Fatalf("note %q", n)
	}
	for _, tc := range []struct {
		rc       *schemas.RunContext
		complete bool
	}{
		{&schemas.RunContext{Agent: &schemas.AgentConfig{}}, true},
		{remote, false},
		{&schemas.RunContext{Agent: &schemas.AgentConfig{Remote: &schemas.RemoteAgent{ModelTraffic: "external"}}}, false},
		{&schemas.RunContext{Agent: &schemas.AgentConfig{Remote: &schemas.RemoteAgent{ModelTraffic: "gateway"}}}, true},
	} {
		log := &schemas.RequestLog{Complete: true}
		MarkExternalTraffic(tc.rc, log)
		if log.Complete != tc.complete || (!tc.complete && log.Reason != ExternalTrafficReason) {
			t.Fatalf("remote %+v: log %+v", tc.rc.Agent.Remote, log)
		}
	}
	MarkExternalTraffic(remote, nil)
}
