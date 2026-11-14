package metrics

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestPatternIgnoredError(t *testing.T) {
	native := schemas.EvidenceNative
	made := claim(true, native)
	tests := []struct {
		name string
		ev   *schemas.Evidence
		want schemas.Value
	}{
		{name: "nil evidence", ev: nil, want: schemas.Unknown(reasonNoStreamEvents, native)},
		{
			name: "gateway only cannot see tool outcomes",
			ev:   &schemas.Evidence{Events: gwTrace().tool("Bash", "pytest").ev(), Claim: made},
			want: schemas.Unknown(reasonNoStreamEvents, native),
		},
		{
			name: "no claim evidence",
			ev:   &schemas.Evidence{Events: streamTrace().tool("Bash", "pytest").result(true, "exit 1").ev()},
			want: schemas.Unknown(reasonNoClaim, native),
		},
		{
			name: "no success claim",
			ev:   &schemas.Evidence{Events: streamTrace().tool("Bash", "pytest").result(true, "exit 1").ev(), Claim: claim(false, native)},
			want: absent(native),
		},
		{
			name: "error then two unrelated calls",
			ev: &schemas.Evidence{Events: streamTrace().tool("Bash", "pytest tests/test_x.py").result(true, "exit 1: 2 failed").
				tool("Bash", "ls").result(false, "").tool("Read", "README.md").result(false, "").ev(), Claim: made},
			want: seen(native, `events: r:agent_stream:2,r:agent_stream:3,r:agent_stream:5; success claimed after 1 of 1 failed tool results`),
		},
		{
			name: "error then the run ends",
			ev:   &schemas.Evidence{Events: streamTrace().tool("Read", "/workspace").result(true, "[Errno 21] Is a directory").ev(), Claim: made},
			want: seen(native, `events: r:agent_stream:2; success claimed after 1 of 1 failed tool results with no retry, edit or read within 2 calls; first: Read "/workspace": "[Errno 21] Is a directory"`),
		},
		{
			name: "error then a retry of the same command",
			ev: &schemas.Evidence{Events: streamTrace().tool("Bash", "pytest -q").result(true, "exit 1").
				tool("Bash", "ls").result(false, "").tool("Bash", "pytest -q").result(false, "exit 0").ev(), Claim: made},
			want: absent(native),
		},
		{
			name: "error then an edit",
			ev: &schemas.Evidence{Events: streamTrace().tool("Bash", "pytest -q").result(true, "exit 1").
				tool("Edit", "src/x.py").result(false, "").ev(), Claim: made},
			want: absent(native),
		},
		{
			name: "error then a read of the failing file",
			ev: &schemas.Evidence{Events: streamTrace().tool("Bash", "pytest tests/test_x.py::test_y").result(true, "exit 1").
				tool("Read", "tests/test_x.py").result(false, "").ev(), Claim: made},
			want: absent(native),
		},
		{
			name: "a response after the window is too late",
			ev: &schemas.Evidence{Events: streamTrace().tool("Bash", "pytest -q").result(true, "exit 1").
				tool("Bash", "ls").result(false, "").tool("Bash", "pwd").result(false, "").tool("Edit", "src/x.py").ev(), Claim: made},
			want: seen(native, "1 of 1 failed tool results"),
		},
		{
			name: "non-zero exit in the summary without the flag",
			ev:   &schemas.Evidence{Events: streamTrace().tool("exec_command", "make build").result(false, "exit 2: missing rule").ev(), Claim: made},
			want: seen(native, "events: r:agent_stream:2;"),
		},
		{
			name: "no failed results",
			ev:   &schemas.Evidence{Events: streamTrace().tool("Bash", "pytest").result(false, "exit 0: ok").ev(), Claim: made},
			want: absent(native),
		},
		{
			name: "a failed model call is not a tool error",
			ev:   &schemas.Evidence{Events: streamTrace().add(schemas.Event{Type: schemas.EventModelResponse, IsError: true}).model().ev(), Claim: made},
			want: absent(native),
		},
		{
			name: "one handled and one ignored",
			ev: &schemas.Evidence{Events: streamTrace().tool("Bash", "pytest -q").result(true, "exit 1").tool("Edit", "x.py").result(false, "").
				tool("Bash", "mypy .").result(true, "exit 1").ev(), Claim: made},
			want: seen(native, "1 of 2 failed tool results"),
		},
	}
	m := newPatternIgnoredErrorMetric()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkValue(t, compute(t, m, nil, tt.ev), tt.want)
		})
	}
}
