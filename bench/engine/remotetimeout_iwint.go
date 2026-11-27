package engine

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// A remote agent that does not finish in time (D1 F6.2, decision 13): the
// launcher (adapters/remotehttp.py) never writes a half answer; it prints
// one {"type":"error"} line whose message starts with "incomplete answer:"
// (an async answer still running when the polls ran out) or "timed out
// after" (a multi-turn run cut short), and exits 1. The run stays the
// agent's failure and is graded as one, but its outcome is timeout and its
// first failure says so in plain words, so the run page does not show only
// the grader's "no answer" line.

// remoteTimeoutExit is the launcher's exit code for its own failures.
const remoteTimeoutExit = 1

// remoteTimeoutPart is the part a remote timeout is recorded under.
const remoteTimeoutPart = "agent"

var remoteTimeoutPrefixes = []string{"incomplete answer:", "timed out after"}

// remoteTimeout reads the remote launcher's last error line when the run is
// a remote agent's and its launcher exited remoteTimeoutExit, and answers
// its message when it is one of the launcher's timeouts.
func remoteTimeout(rc *schemas.RunContext) (string, bool) {
	if rc == nil || rc.Agent == nil || rc.Agent.Remote == nil || rc.Exec == nil || rc.Exec.ExitCode != remoteTimeoutExit || rc.Exec.StdoutPath == "" {
		return "", false
	}
	f, err := os.Open(rc.Exec.StdoutPath)
	if err != nil {
		return "", false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	last := ""
	for sc.Scan() {
		line := sc.Bytes()
		if !strings.Contains(string(line), `"error"`) {
			continue
		}
		var l struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		}
		if json.Unmarshal(line, &l) == nil && l.Type == "error" {
			last = strings.TrimSpace(l.Message)
		}
	}
	for _, p := range remoteTimeoutPrefixes {
		if strings.HasPrefix(last, p) {
			return last, true
		}
	}
	return "", false
}

// remoteTimeoutOutcome turns a remote agent's fail or crash that its
// launcher reported as a timeout into a timeout with a plain failure. A pass,
// a fired veto, an infrastructure outcome or an evaluator problem stays.
func remoteTimeoutOutcome(rc *schemas.RunContext, o schemas.Outcome) (schemas.Outcome, *schemas.PartFailure) {
	switch o {
	case schemas.OutcomeFail, schemas.OutcomeCrash, schemas.OutcomeTimeout:
	default:
		return o, nil
	}
	msg, ok := remoteTimeout(rc)
	if !ok {
		return o, nil
	}
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return schemas.OutcomeTimeout, &schemas.PartFailure{Stage: schemas.StageRun, Part: remoteTimeoutPart,
		Error: "The agent did not finish in time (" + msg + "), so it has no answer and counts as a fail."}
}
