package metrics

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestPatternUnverifiedFinish(t *testing.T) {
	gw, native := schemas.EvidenceGateway, schemas.EvidenceNative
	made := claim(true, native)
	tests := []struct {
		name string
		ev   *schemas.Evidence
		want schemas.Value
	}{
		{name: "nil evidence", ev: nil, want: schemas.Unknown(reasonNoEvents, gw)},
		{name: "no events", ev: &schemas.Evidence{Claim: made}, want: schemas.Unknown(reasonNoEvents, gw)},
		{name: "no claim evidence", ev: &schemas.Evidence{Events: gwTrace().tool("Edit", "x.py").ev()}, want: schemas.Unknown(reasonNoClaim, gw)},
		{name: "no success claim", ev: &schemas.Evidence{Events: gwTrace().tool("Edit", "x.py").ev(), Claim: claim(false, native)}, want: absent(gw)},
		{
			name: "edit then a test run",
			ev:   &schemas.Evidence{Events: gwTrace().tool("Edit", "x.py").tool("Bash", "pytest -q").model().ev(), Claim: made},
			want: absent(gw),
		},
		{
			name: "test run before the last edit only",
			ev:   &schemas.Evidence{Events: gwTrace().tool("Bash", "pytest -q").tool("Edit", "x.py").model().ev(), Claim: made},
			want: seen(gw, `events: r:gateway:2,r:gateway:3; success claimed with no test command after the last edit, Edit "x.py"`),
		},
		{
			name: "no edit and no test run",
			ev:   &schemas.Evidence{Events: gwTrace().tool("Read", "x.py").model().ev(), Claim: made},
			want: seen(gw, "events: r:gateway:2; success claimed with no test command in the run"),
		},
		{
			name: "no edit, tests ran",
			ev:   &schemas.Evidence{Events: gwTrace().tool("Bash", "go test ./...").model().ev(), Claim: made},
			want: absent(gw),
		},
		{
			name: "a todo update after the tests is not an edit",
			ev:   &schemas.Evidence{Events: gwTrace().tool("Edit", "x.py").tool("Bash", "python -m pytest tests").tool("TodoWrite", "").ev(), Claim: made},
			want: absent(gw),
		},
		{
			name: "codex stream: patch then npm test",
			ev: &schemas.Evidence{Events: streamTrace().tool("apply_patch", "src/a.ts").result(false, "completed").
				tool("exec_command", "npm test").result(false, "exit 0: ok").ev(), Claim: made},
			want: absent(native),
		},
		{
			name: "a tool named for tests counts",
			ev:   &schemas.Evidence{Events: gwTrace().tool("Edit", "x.py").tool("run_tests", "").ev(), Claim: made},
			want: absent(gw),
		},
		{
			name: "reading a test file is not running tests",
			ev:   &schemas.Evidence{Events: gwTrace().tool("Edit", "x.py").tool("Read", "tests/test_pytest.py").ev(), Claim: made},
			want: seen(gw, "events: r:gateway:1,r:gateway:2;"),
		},
		{
			name: "claim read from stdout tail still applies",
			ev:   &schemas.Evidence{Events: gwTrace().tool("Edit", "x.py").ev(), Claim: claim(true, schemas.EvidenceOutcome)},
			want: seen(gw, "events: r:gateway:1;"),
		},
		{
			name: "incomplete gateway cannot rule out a test run",
			ev:   &schemas.Evidence{Events: gwTrace().tool("Edit", "x.py").incomplete().ev(), Claim: made},
			want: schemas.Unknown(gatewayIncomplete, gw),
		},
	}
	m := newPatternUnverifiedMetric()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkValue(t, compute(t, m, nil, tt.ev), tt.want)
		})
	}
}
