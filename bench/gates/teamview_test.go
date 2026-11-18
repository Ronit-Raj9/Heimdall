package gates

import (
	"reflect"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Shared fixtures for the multi-agent gate tests: a lead, two workers and a
// reviewer, each on its own gateway key, talking through the mailbox.
var teamT0 = time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)

// at returns teamT0 plus s seconds.
func at(s int) time.Time { return teamT0.Add(time.Duration(s) * time.Second) }

// teamTree returns the team's agent tree.
func teamTree() *schemas.AgentTree {
	node := func(id, role, parent string, depth, start, end int) schemas.AgentNode {
		return schemas.AgentNode{ID: id, Role: role, ParentID: parent, Depth: depth, Started: at(start), Ended: at(end),
			Source: schemas.AgentSourceGatewayKey, Strength: schemas.AgentStrengthProven}
	}
	return &schemas.AgentTree{Version: schemas.AgentTreeVersion, Shape: schemas.AgentShapeTeam, Agents: []schemas.AgentNode{
		node("lead", "lead", "", 0, 0, 100),
		node("worker-a", "worker", "", 0, 0, 90),
		node("worker-b", "worker", "", 0, 0, 90),
		node("reviewer", "reviewer", "", 0, 0, 95),
	}}
}

// teamEvidence returns evidence of a team run with msgs and no other
// activity.
func teamEvidence(msgs ...schemas.AgentMessage) *schemas.Evidence {
	if msgs == nil {
		msgs = []schemas.AgentMessage{}
	}
	return &schemas.Evidence{Agents: teamTree(), Messages: msgs, Requests: &schemas.RequestLog{Complete: true}}
}

// msg returns a message sent at second s and delivered a second later.
func msg(seq int, from, to, body string, s int) schemas.AgentMessage {
	return schemas.AgentMessage{Seq: seq, From: from, To: to, Body: body, SenderAgentID: from, At: at(s),
		Delivered: true, DeliveredTo: []string{to}, DeliveredAt: at(s + 1)}
}

// teamRC returns a run context under probe id aimed at agent.
func teamRC(id, agent string, markers ...string) *schemas.RunContext {
	return &schemas.RunContext{
		Task:   &schemas.Task{ID: "t01", Project: "acme"},
		Agent:  &schemas.AgentConfig{},
		Probe:  &schemas.ProbeSpec{ID: id, Apply: schemas.ProbeApply{Agent: agent, Markers: markers}},
		Handle: &schemas.EnvironmentHandle{Workdir: "/workspace"},
	}
}

func TestTargetAgents(t *testing.T) {
	ev := teamEvidence()
	cases := []struct {
		agent string
		want  []string
	}{
		{"worker-a", []string{"worker-a"}},
		{"worker", []string{"worker-a", "worker-b"}},
		{"nobody", nil},
		{"", nil},
	}
	for _, tc := range cases {
		t.Run(tc.agent, func(t *testing.T) {
			if got := targetAgents(teamRC("p", tc.agent), ev); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMessageHelpers(t *testing.T) {
	m := schemas.AgentMessage{Seq: 1, From: "lead", To: "*", Body: "hi", Fields: map[string]string{"z": "2", "a": "1"}, Received: "cut", Altered: true}
	t.Run("sender falls back to from", func(t *testing.T) {
		if messageSender(m) != "lead" {
			t.Fatal(messageSender(m))
		}
		m2 := m
		m2.SenderAgentID = "worker-a"
		if messageSender(m2) != "worker-a" {
			t.Fatal("proven sender must win over the from field")
		}
	})
	t.Run("broadcast without delivery has no readers", func(t *testing.T) {
		if r := messageReaders(m); r != nil {
			t.Fatal(r)
		}
	})
	t.Run("received text is the altered one", func(t *testing.T) {
		if receivedText(m) != "cut" {
			t.Fatal(receivedText(m))
		}
	})
	t.Run("sent text has fields in key order", func(t *testing.T) {
		if got := sentText(m); got != "hi\na: 1\nz: 2" {
			t.Fatalf("%q", got)
		}
	})
}

func TestActions(t *testing.T) {
	ev := teamEvidence(msg(1, "lead", "worker-a", "go", 1))
	ev.ToolCalls = []schemas.ToolCall{
		{Seq: 1, Server: "desk", Tool: "refund", Arguments: `{"x":1}`, AgentID: "worker-a", Started: at(5)},
		{Seq: 2, Server: schemas.MailboxServer, Tool: schemas.MailboxSend, Arguments: "go", AgentID: "lead", Started: at(1)},
		{Seq: 3, Server: "desk", Tool: "read", AgentID: "worker-b", Started: at(6)},
	}
	ev.Steps = []schemas.StepRecord{{Seq: 7, Kind: schemas.StepCommand, Summary: "rm -rf data", AgentID: "worker-b", TS: at(8)}}
	got := actions(ev)
	var refs []string
	for _, a := range got {
		refs = append(refs, a.Agent+" "+a.Ref)
	}
	want := []string{"lead message:1", "worker-a tool_call:1", "worker-b step:7"}
	if !reflect.DeepEqual(refs, want) {
		t.Fatalf("got %v, want %v (mailbox ledger rows and read-only tools are left out)", refs, want)
	}
}

func TestPathAgent(t *testing.T) {
	ev := teamEvidence()
	ev.Steps = []schemas.StepRecord{
		{Seq: 1, Kind: schemas.StepFileChange, Path: "src/a.py", TS: at(3)},
		{Seq: 2, Kind: schemas.StepFileChange, Path: "src/a.py", AgentID: "worker-b", TS: at(9)},
		{Seq: 3, Kind: schemas.StepFileChange, Path: "./src/a.py", AgentID: "worker-a", TS: at(4)},
	}
	if got := pathAgent(ev, "src/a.py"); got != "worker-a" {
		t.Fatalf("got %q, want the first placed writer worker-a", got)
	}
	if got := pathAgent(ev, "src/b.py"); got != "" {
		t.Fatalf("got %q for an untouched path", got)
	}
}
