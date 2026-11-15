package utils

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestRemoteOutputAllowed(t *testing.T) {
	plain := &schemas.RemoteAgent{URL: "https://a"}
	tools := &schemas.RemoteAgent{URL: "https://a", Tools: schemas.RemoteToolsExpose}
	inline := &schemas.RemoteAgent{URL: "https://a", Artifacts: schemas.RemoteArtifactsInline}
	cases := []struct {
		r    *schemas.RemoteAgent
		kind string
		ok   bool
		why  string
	}{
		{plain, schemas.OutputAnswer, true, ""},
		{plain, schemas.OutputState, false, "remote.tools expose"},
		{tools, schemas.OutputState, true, ""},
		{plain, schemas.OutputArtifact, false, "remote.artifacts inline"},
		{inline, schemas.OutputArtifact, true, ""},
		{tools, schemas.OutputDiff, false, "not beside the workspace"},
		{inline, schemas.OutputDiff, false, "not beside the workspace"},
	}
	for _, tc := range cases {
		ok, why := RemoteOutputAllowed(tc.r, tc.kind)
		if ok != tc.ok || !strings.Contains(why, tc.why) {
			t.Errorf("%+v %s: %v %q", tc.r, tc.kind, ok, why)
		}
	}
	if CheckRemoteAgent(&schemas.RemoteAgent{Tools: "yes"}) == nil || CheckRemoteAgent(&schemas.RemoteAgent{Artifacts: "zip"}) == nil {
		t.Fatal("unknown values accepted")
	}
	if CheckRemoteAgent(&schemas.RemoteAgent{Tools: "off", Artifacts: "none"}) != nil || CheckRemoteAgent(nil) != nil {
		t.Fatal("known values refused")
	}
}

func TestRemoteToolsListenAndBase(t *testing.T) {
	h, p, err := SplitRemoteToolsListen("")
	if err != nil || h != "127.0.0.1" || p != 0 {
		t.Fatalf("default: %s %d %v", h, p, err)
	}
	if h, p, err = SplitRemoteToolsListen("0.0.0.0:9400"); err != nil || h != "0.0.0.0" || p != 9400 {
		t.Fatalf("explicit: %s %d %v", h, p, err)
	}
	for _, bad := range []string{"localhost:1", "127.0.0.1", "127.0.0.1:99999", ":9400"} {
		if _, _, err := SplitRemoteToolsListen(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if b, _ := RemoteToolsBaseURL("", "127.0.0.1", 41000); b != "http://127.0.0.1:41000" {
		t.Fatalf("base %q", b)
	}
	if b, _ := RemoteToolsBaseURL("https://bench.example.com:{port}/", "127.0.0.1", 41000); b != "https://bench.example.com:41000" {
		t.Fatalf("public base %q", b)
	}
	if _, err := RemoteToolsBaseURL("https://u:p@bench.example.com", "127.0.0.1", 1); err == nil {
		t.Fatal("user info accepted")
	}
}

func TestRemoteToolsAccessFromAndURLs(t *testing.T) {
	handles := []schemas.ServiceHandle{
		{Spec: schemas.ServiceSpec{ID: "notes", Role: schemas.ServiceRoleMCP}, URL: "http://toolproxy:9200/s/notes"},
		{Spec: schemas.ServiceSpec{ID: "api"}, URL: "http://toolproxy:9200/s/api"},
		{Spec: schemas.ServiceSpec{ID: "db"}, URL: "http://db:5432"},
		{Spec: schemas.ServiceSpec{ID: "grader", Hidden: true}, URL: "http://toolproxy:9200/s/grader"},
		{Spec: schemas.ServiceSpec{ID: "weather", Role: schemas.ServiceRoleExternal}, URL: ExternalURL("weather")},
	}
	a := RemoteToolsAccessFrom("r1", handles)
	if strings.Join(a.Services, ",") != "api,notes" || strings.Join(a.MCP, ",") != "notes" || strings.Join(a.External, ",") != "weather" {
		t.Fatalf("access %+v", a)
	}
	if RemoteToolURLs(a) != nil {
		t.Fatal("urls without a base")
	}
	a.BaseURL = "http://127.0.0.1:41000/"
	urls := RemoteToolURLs(a)
	if urls["notes"].URL != "http://127.0.0.1:41000/r/r1/s/notes/mcp" || urls["api"].URL != "http://127.0.0.1:41000/r/r1/s/api" ||
		urls["weather"].URL != "http://127.0.0.1:41000/r/r1/x/weather" || len(urls) != 3 {
		t.Fatalf("urls %v", urls)
	}
	tok, err := NewRemoteToolsToken()
	tok2, _ := NewRemoteToolsToken()
	if err != nil || len(tok) != 64 || tok == tok2 {
		t.Fatalf("token %q %v", tok, err)
	}
}

func TestRemoteEgressNoteNamesPublishedGate(t *testing.T) {
	rc := &schemas.RunContext{Agent: &schemas.AgentConfig{Remote: &schemas.RemoteAgent{URL: "https://agent.example.com/run"}}}
	if strings.Contains(RemoteEgressNote(rc), "gate") {
		t.Fatal("gate named without tools expose")
	}
	rc.Agent.Remote.Tools = schemas.RemoteToolsExpose
	if n := RemoteEgressNote(rc); !strings.Contains(n, "agent.example.com:443") || !strings.Contains(n, schemas.RemoteToolsFile) {
		t.Fatalf("note %q", n)
	}
}
