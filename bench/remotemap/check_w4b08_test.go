package remotemap

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestPollDoneStatesNeedStatusPath(t *testing.T) {
	agent := func(p schemas.RemotePoll) *schemas.RemoteAgent {
		p.URL, p.AnswerPath = "/p", "text"
		return &schemas.RemoteAgent{URL: "https://a.example.com/", Mapping: &schemas.RemoteMapping{Poll: &p}}
	}
	ok := []schemas.RemotePoll{
		{StatusPath: "status", DoneStates: []string{"done"}, FailStates: []string{"failed"}},
		{CompletePath: "is_final"},
	}
	for i, p := range ok {
		if err := Check(agent(p)); err != nil {
			t.Fatalf("ok %d: %v", i, err)
		}
	}
	bad := []schemas.RemotePoll{
		{DoneStates: []string{"done"}},
		{FailStates: []string{"failed"}},
		{StatusPath: "a[", DoneStates: []string{"done"}},
		{CompletePath: "a["},
		{StatusPath: "s", DoneStates: []string{""}},
		{StatusPath: "s", DoneStates: []string{strings.Repeat("x", 41)}},
		{StatusPath: "s", DoneStates: make([]string, 21)},
	}
	for i, p := range bad {
		if err := Check(agent(p)); err == nil {
			t.Fatalf("bad %d accepted: %+v", i, p)
		}
	}
}
