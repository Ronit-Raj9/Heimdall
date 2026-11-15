package main

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestAgentEventsRow(t *testing.T) {
	cases := []struct {
		st     *schemas.AgentEventStatus
		status string
		detail string
	}{
		{nil, checkSkip, "switched off"},
		{&schemas.AgentEventStatus{Collector: "on"}, checkWarn, "the agent reported nothing (gateway and observer evidence only)"},
		{&schemas.AgentEventStatus{Collector: "missing", Reason: "probe failed"}, checkWarn, "did not start (probe failed)"},
		{&schemas.AgentEventStatus{Collector: "on", Kept: 3, Channels: map[string]int{"otlp": 2, "file": 1}, Kinds: map[string]int{"plan": 1, "tool_call": 2}}, checkPass, "3 events received (otlp 2, file 1; kinds tool_call 2, plan 1)"},
		{&schemas.AgentEventStatus{Collector: "on", Kept: 1, Dropped: 4, Channels: map[string]int{"native": 1}}, checkWarn, "4 dropped"},
	}
	for _, c := range cases {
		in := goodCheckInput()
		in.ev.AgentEvents = c.st
		r := agentEventsRow(in)
		if r.Status != c.status || !strings.Contains(r.Detail, c.detail) || r.Status == checkFail {
			t.Errorf("%+v: %s %q", c.st, r.Status, r.Detail)
		}
	}
	if got := reportsEvents(&schemas.AgentEventStatus{Kept: 2, Channels: map[string]int{"otlp": 1, "stdout": 1}}); got != "reports events (OTLP | native)" {
		t.Errorf("reportsEvents = %q", got)
	}
}
