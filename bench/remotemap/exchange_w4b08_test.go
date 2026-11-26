package remotemap

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// w4b08PollAgent answers the main request with a job id and every poll
// with the body that answer(n) gives for the n-th poll.
func w4b08PollAgent(t *testing.T, answer func(n int) string) (*fakePlatform, *schemas.RemoteAgent) {
	n := 0
	f := newFake(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any, _ []byte) {
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"id":"job1"}`))
			return
		}
		n++
		_, _ = w.Write([]byte(answer(n)))
	})
	ra := &schemas.RemoteAgent{URL: f.srv.URL + "/run", Mapping: &schemas.RemoteMapping{
		Body: `{"q":"{{instruction}}"}`,
		Poll: &schemas.RemotePoll{URL: f.srv.URL + "/job", AnswerPath: "text"}}}
	return f, ra
}

func TestPollTimeoutIsIncomplete(t *testing.T) {
	f, ra := w4b08PollAgent(t, func(n int) string { return fmt.Sprintf(`{"text":"part %d"}`, n) })
	ex, err := f.caller(nil, "", "").Call(context.Background(), ra, vars())
	if err == nil || !strings.Contains(err.Error(), "incomplete answer: the agent did not finish within the time limit") {
		t.Fatalf("err %v answer %q", err, ex.Answer)
	}
	if ex.Answer != "" {
		t.Fatalf("a half answer was kept: %q", ex.Answer)
	}
}

func TestPollCompletePath(t *testing.T) {
	f, ra := w4b08PollAgent(t, func(n int) string {
		if n < 3 {
			return `{"text":"READ","is_final":false}`
		}
		return `{"text":"READY","is_final":true}`
	})
	ra.Mapping.Poll.CompletePath = "is_final"
	ex, err := f.caller(nil, "", "").Call(context.Background(), ra, vars())
	if err != nil || ex.Answer != "READY" || ex.Polls != 3 {
		t.Fatalf("err %v answer %q polls %d", err, ex.Answer, ex.Polls)
	}
}

func TestPollDoneAndFailStates(t *testing.T) {
	f, ra := w4b08PollAgent(t, func(n int) string {
		if n < 4 {
			return `{"text":"READY","status":"running"}`
		}
		return `{"text":"READY","status":"Succeeded"}`
	})
	ra.Mapping.Poll.StatusPath = "status"
	ra.Mapping.Poll.DoneStates = []string{"succeeded"}
	ex, err := f.caller(nil, "", "").Call(context.Background(), ra, vars())
	if err != nil || ex.Answer != "READY" || ex.Polls != 4 {
		t.Fatalf("err %v answer %q polls %d", err, ex.Answer, ex.Polls)
	}

	f2, ra2 := w4b08PollAgent(t, func(int) string { return `{"text":"half","status":"failed"}` })
	ra2.Mapping.Poll.StatusPath = "status"
	ra2.Mapping.Poll.DoneStates = []string{"succeeded"}
	ra2.Mapping.Poll.FailStates = []string{"failed"}
	if _, err := f2.caller(nil, "", "").Call(context.Background(), ra2, vars()); err == nil || err.Error() != "the agent said failed" {
		t.Fatalf("err %v", err)
	}
}
