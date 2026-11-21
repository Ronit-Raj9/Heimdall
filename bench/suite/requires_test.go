package suite

import (
	"reflect"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestRequires(t *testing.T) {
	S := func(c ...schemas.Capability) schemas.CapabilitySet { return append(schemas.CapabilitySet{}, c...) }
	cases := []struct {
		s    schemas.Suite
		want schemas.CapabilitySet
	}{
		{schemas.Suite{Environment: "docker_workspace"}, S(schemas.CapShell)},
		{schemas.Suite{Track: "terminal"}, S(schemas.CapShell)},
		{schemas.Suite{Track: "memory", Environment: "docker_workspace"}, S(schemas.CapSessions)},
		{schemas.Suite{Track: "support"}, S(schemas.CapTools)},
		{schemas.Suite{Track: "mcp"}, S(schemas.CapTools)},
		{schemas.Suite{Track: "tool_use"}, S(schemas.CapTools)},
		{schemas.Suite{Track: "research"}, S(schemas.CapTools)},
		{schemas.Suite{Track: "research", Environment: "browser_sandbox"}, S(schemas.CapTools, schemas.CapBrowser)},
		{schemas.Suite{Track: "computer_use"}, S(schemas.CapDesktop)},
		{schemas.Suite{Track: "browser"}, S(schemas.CapBrowser)},
		{schemas.Suite{Track: "voice"}, S(schemas.CapVoice)},
		{schemas.Suite{Track: "ml_engineering"}, S(schemas.CapShell, schemas.CapGPU)},
		{schemas.Suite{Track: "qa"}, S()},
		{schemas.Suite{Track: "coding", Tasks: []*schemas.Task{{Resources: &schemas.Resources{GPU: &schemas.GPURequest{Count: 1}}}}}, S(schemas.CapShell)},
		{schemas.Suite{Track: "memory", Requires: S("tools", "voice")}, S(schemas.CapTools, schemas.CapVoice)},
	}
	for i, c := range cases {
		if got := Requires(&c.s); !reflect.DeepEqual(got, c.want) {
			t.Errorf("case %d (%s): got %v want %v", i, c.s.Track, got, c.want)
		}
	}
	if got := Requires(nil); len(got) != 0 {
		t.Fatalf("nil suite: %v", got)
	}
}
