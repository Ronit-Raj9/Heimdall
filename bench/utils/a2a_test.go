package utils

import (
	"regexp"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestA2ARegistrationName(t *testing.T) {
	valid := regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	cases := map[[2]string]string{
		{"r_mxfbr3dqg3", "lead"}:                       "hb-r-mxfbr3dqg3-lead",
		{"R__X--y", "writer"}:                          "hb-r-x-y-writer",
		{"a_" + strings.Repeat("z", 80), "researcher"}: "",
	}
	for in, want := range cases {
		got := A2ARegistrationName(in[0], in[1])
		if want != "" && got != want {
			t.Errorf("A2ARegistrationName(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
		if len(got) > 63 || !valid.MatchString(got) || !strings.HasSuffix(got, "-"+in[1]) {
			t.Errorf("A2ARegistrationName(%q, %q) = %q: not a Bifrost agent name of at most 63 characters ending in the member", in[0], in[1], got)
		}
	}
}

func TestCheckA2AAgent(t *testing.T) {
	ok := []*schemas.A2AAgent{
		{CardURL: "http://a:9000/.well-known/agent-card.json"},
		{Registration: "support-bot", Streaming: true},
		{CardURL: "https://a/card", Credential: "none", Tools: "expose", Artifacts: "inline", Team: []schemas.A2AMember{{ID: "writer", CardURL: "http://w/card"}}},
	}
	for _, s := range ok {
		if err := CheckA2AAgent(s); err != nil {
			t.Errorf("CheckA2AAgent(%+v) = %v, want nil", s, err)
		}
	}
	bad := map[string]*schemas.A2AAgent{
		"nil":            nil,
		"no target":      {},
		"both":           {CardURL: "http://a/card", Registration: "x"},
		"not http":       {CardURL: "file:///card"},
		"bad name":       {Registration: "Support_Bot"},
		"credential":     {CardURL: "http://a/card", Credential: "steal"},
		"tools":          {CardURL: "http://a/card", Tools: "on"},
		"team on reg":    {Registration: "x", Team: []schemas.A2AMember{{ID: "w", CardURL: "http://w"}}},
		"member is lead": {CardURL: "http://a/card", Team: []schemas.A2AMember{{ID: "lead", CardURL: "http://w"}}},
		"member twice":   {CardURL: "http://a/card", Team: []schemas.A2AMember{{ID: "w", CardURL: "http://w"}, {ID: "w", CardURL: "http://w"}}},
		"member url":     {CardURL: "http://a/card", Team: []schemas.A2AMember{{ID: "w", CardURL: "w"}}},
	}
	for name, s := range bad {
		if err := CheckA2AAgent(s); err == nil {
			t.Errorf("%s: want a refusal", name)
		}
	}
}

func TestA2AOutputAllowed(t *testing.T) {
	if ok, _ := A2AOutputAllowed(&schemas.A2AAgent{}, schemas.OutputAnswer); !ok {
		t.Fatal("answer tasks always run")
	}
	if ok, why := A2AOutputAllowed(&schemas.A2AAgent{}, schemas.OutputState); ok || !strings.Contains(why, "a2a.tools") {
		t.Fatalf("a state task needs a2a.tools expose: %v %q", ok, why)
	}
	if ok, _ := A2AOutputAllowed(&schemas.A2AAgent{Artifacts: "inline"}, schemas.OutputArtifact); !ok {
		t.Fatal("an artifact task runs with artifacts inline")
	}
	if ok, _ := A2AOutputAllowed(&schemas.A2AAgent{Tools: "expose", Artifacts: "inline"}, schemas.OutputDiff); ok {
		t.Fatal("a diff task never runs")
	}
	if !RemoteToolsExposed(&schemas.AgentConfig{A2A: &schemas.A2AAgent{Tools: "expose"}}) || RemoteToolsExposed(&schemas.AgentConfig{A2A: &schemas.A2AAgent{}}) {
		t.Fatal("a2a.tools expose publishes the tool proxy")
	}
}
