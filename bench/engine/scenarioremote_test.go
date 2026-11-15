package engine

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestRemoteTaskErrs(t *testing.T) {
	suite := &schemas.Suite{ID: "s"}
	state := &schemas.Task{ID: "st", Output: &schemas.OutputSpec{Kind: schemas.OutputState},
		Services: []schemas.ServiceSpec{{ID: "notes", Role: schemas.ServiceRoleMCP, Port: 8080}}}
	bare := &schemas.Task{ID: "bare", Output: &schemas.OutputSpec{Kind: schemas.OutputState}}
	diff := &schemas.Task{ID: "df"}
	art := &schemas.Task{ID: "ar", Output: &schemas.OutputSpec{Kind: schemas.OutputArtifact}}
	ans := &schemas.Task{ID: "an", Output: &schemas.OutputSpec{Kind: schemas.OutputAnswer}}
	cfg := map[string]*schemas.RemoteAgent{
		"plain":  {URL: "https://a"},
		"tools":  {URL: "https://a", Tools: schemas.RemoteToolsExpose},
		"inline": {URL: "https://a", Artifacts: schemas.RemoteArtifactsInline},
		"bad":    {URL: "https://a", Tools: "maybe"},
	}
	cases := []struct {
		task *schemas.Task
		arm  string
		want string
	}{
		{ans, "plain", ""},
		{state, "plain", "remote.tools expose"},
		{state, "tools", ""},
		{bare, "tools", "no proxied service or external API"},
		{diff, "tools", "not beside the workspace"},
		{art, "plain", "remote.artifacts inline"},
		{art, "inline", ""},
		{ans, "bad", "remote.tools"},
	}
	for _, tc := range cases {
		errs := remoteTaskErrs(suite, tc.task, []string{tc.arm}, cfg)
		switch {
		case tc.want == "" && len(errs) > 0:
			t.Errorf("%s/%s: %v", tc.task.ID, tc.arm, errs)
		case tc.want != "" && (len(errs) != 1 || !strings.Contains(errs[0].Error(), tc.want)):
			t.Errorf("%s/%s: %v, want %q", tc.task.ID, tc.arm, errs, tc.want)
		}
	}
}

// TestRedactPlanRemoteSecrets checks that every secret the remote launcher
// gets is under a name plan.json redacts, and the tool URLs are not.
func TestRedactPlanRemoteSecrets(t *testing.T) {
	plan := &schemas.RunPlan{Env: map[string]string{
		"BENCH_REMOTE_TOOLS_TOKEN": "tok", "BENCH_REMOTE_SIGN_SECRET": "sec", "BENCH_REMOTE_TOKEN": "bearer", "BENCH_REMOTE_RUN_KEY": "key",
		"BENCH_REMOTE_TOOLS": `{"notes":{"url":"http://127.0.0.1:1/r/r1/s/notes/mcp"}}`,
	}}
	out := redactPlan(plan)
	for _, k := range []string{"BENCH_REMOTE_TOOLS_TOKEN", "BENCH_REMOTE_SIGN_SECRET", "BENCH_REMOTE_TOKEN", "BENCH_REMOTE_RUN_KEY"} {
		if out.Env[k] != "[redacted]" {
			t.Errorf("%s = %q, want redacted", k, out.Env[k])
		}
	}
	if out.Env["BENCH_REMOTE_TOOLS"] != plan.Env["BENCH_REMOTE_TOOLS"] {
		t.Error("tool URLs were redacted")
	}
}
