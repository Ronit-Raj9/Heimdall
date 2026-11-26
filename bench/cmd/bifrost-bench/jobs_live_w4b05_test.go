package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/maximhq/bifrost/bench/jobs"
)

// p13-9: the next step after set-up names the place a hosted user can act.
func TestLiveInitHostedNextNamesSecrets(t *testing.T) {
	config, _ := liveProject(t)
	ctx := context.Background()
	a, err := newApp(ctx, config, appOptions{})
	if err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(liveInitParams{BaselineKey: "vk-a", CandidateKey: "vk-b"})
	for _, c := range []struct {
		hosted bool
		want   string
	}{
		{true, "Add the gateway read key as an organization secret in Settings > Secrets, then press Pull now."},
		{false, "Set the gateway read key on this server, then press Pull now."},
	} {
		res, err := runLiveInit(ctx, jobEnv{App: a, Hosted: c.hosted}, &jobs.Job{Params: params}, &testJobLog{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := res.(liveInitResult).Next; got != c.want {
			t.Fatalf("hosted=%v next %q, want %q", c.hosted, got, c.want)
		}
		params, _ = json.Marshal(liveInitParams{BaselineKey: "vk-a", CandidateKey: "vk-b", Force: true})
	}
}
