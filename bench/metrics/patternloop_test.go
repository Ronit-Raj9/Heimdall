package metrics

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestPatternLoop(t *testing.T) {
	gw, native := schemas.EvidenceGateway, schemas.EvidenceNative
	many := gwTrace()
	for range 12 {
		many.tool("Read", "src/x.py")
	}
	tests := []struct {
		name string
		ev   *schemas.Evidence
		want schemas.Value
	}{
		{name: "nil evidence", ev: nil, want: schemas.Unknown(reasonNoEvents, gw)},
		{name: "no events", ev: &schemas.Evidence{}, want: schemas.Unknown(reasonNoEvents, gw)},
		{name: "model turns but no tool calls", ev: &schemas.Evidence{Events: gwTrace().model().model().ev()}, want: absent(gw)},
		{
			name: "same command 3 times in a row",
			ev:   &schemas.Evidence{Events: gwTrace().model().tool("Bash", "pytest -q").tool("Bash", "pytest -q").tool("Bash", "pytest -q").ev()},
			want: seen(gw, `events: r:gateway:2,r:gateway:3,r:gateway:4; Bash "pytest -q" repeated 3 times in a row`),
		},
		{
			name: "same command text with other arguments still loops",
			ev: &schemas.Evidence{Events: gwTrace().toolArgs("Bash", "pytest -q", "a").toolArgs("Bash", "pytest -q", "b").
				toolArgs("Bash", "pytest -q", "c").ev()},
			want: seen(gw, "repeated 3 times in a row"),
		},
		{name: "twice in a row is not a loop", ev: &schemas.Evidence{Events: gwTrace().tool("Bash", "pytest -q").tool("Bash", "pytest -q").ev()}, want: absent(gw)},
		{
			name: "same read 4 times spread out with no edit between",
			ev: &schemas.Evidence{Events: gwTrace().tool("Read", "src/x.py").tool("Bash", "ls").tool("Read", "src/x.py").tool("Grep", "parse").
				tool("Read", "src/x.py").tool("Bash", "pwd").tool("Read", "src/x.py").ev()},
			want: seen(gw, `events: r:gateway:1,r:gateway:3,r:gateway:5,r:gateway:7; Read "src/x.py" repeated 4 times with no edit between`),
		},
		{
			name: "same read 4 times with edits between is not a loop",
			ev: &schemas.Evidence{Events: gwTrace().tool("Read", "src/x.py").tool("Edit", "src/x.py").tool("Read", "src/x.py").
				toolArgs("Edit", "src/x.py", "2").tool("Read", "src/x.py").toolArgs("Edit", "src/x.py", "3").tool("Read", "src/x.py").ev()},
			want: absent(gw),
		},
		{
			name: "the same edit 3 times in a row loops",
			ev:   &schemas.Evidence{Events: gwTrace().tool("Edit", "src/x.py").tool("Edit", "src/x.py").tool("Edit", "src/x.py").ev()},
			want: seen(gw, `Edit "src/x.py" repeated 3 times in a row`),
		},
		{
			name: "two looping calls are both counted",
			ev: &schemas.Evidence{Events: gwTrace().tool("Bash", "ls").tool("Bash", "ls").tool("Bash", "ls").
				tool("Read", "a.py").tool("Read", "a.py").tool("Read", "a.py").ev()},
			want: seen(gw, "repeated 3 times in a row; 1 more repeated calls"),
		},
		{
			name: "reason lists at most 10 events",
			ev:   &schemas.Evidence{Events: many.ev()},
			want: seen(gw, "r:gateway:10; Read"),
		},
		{
			name: "agent stream when the gateway is absent",
			ev:   &schemas.Evidence{Events: streamTrace().tool("Bash", "ls").result(false, "").tool("Bash", "ls").result(false, "").tool("Bash", "ls").ev()},
			want: seen(native, "events: r:agent_stream:1,r:agent_stream:3,r:agent_stream:5;"),
		},
		{
			name: "incomplete gateway still shows a loop",
			ev:   &schemas.Evidence{Events: gwTrace().tool("Bash", "ls").tool("Bash", "ls").tool("Bash", "ls").incomplete().ev()},
			want: seen(gw, "in a row"),
		},
		{
			name: "incomplete gateway cannot rule a loop out",
			ev:   &schemas.Evidence{Events: gwTrace().tool("Bash", "ls").incomplete().ev()},
			want: schemas.Unknown(gatewayIncomplete, gw),
		},
	}
	m := newPatternLoopMetric()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compute(t, m, nil, tt.ev)
			checkValue(t, got, tt.want)
			if v, _ := got.Float(); v == 1 && strings.Count(strings.TrimPrefix(strings.SplitN(got.Reason, ";", 2)[0], "events: "), ",") > maxReasonEvents-1 {
				t.Errorf("reason lists more than %d events: %q", maxReasonEvents, got.Reason)
			}
		})
	}
}
