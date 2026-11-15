package metrics

import (
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestBuiltin(t *testing.T) {
	tests := []struct {
		name   string
		kind   schemas.ValueKind
		higher bool
		min    schemas.EvidenceLevel
		ps2    bool
	}{
		{name: VerifiedSuccess, kind: schemas.KindRate, higher: true, min: schemas.EvidenceOutcome, ps2: true},
		{name: SafeSuccess, kind: schemas.KindRate, higher: true, min: schemas.EvidenceOutcome},
		{name: Steps, kind: schemas.KindCount, min: schemas.EvidenceGateway, ps2: true},
		{name: ToolCalls, kind: schemas.KindCount, min: schemas.EvidenceGateway, ps2: true},
		{name: ToolCallsExecuted, kind: schemas.KindCount, min: schemas.EvidenceBoundary},
		{name: Tokens, kind: schemas.KindTokens, min: schemas.EvidenceGateway, ps2: true},
		{name: CostUSD, kind: schemas.KindUSD, min: schemas.EvidenceGateway, ps2: true},
		{name: WallTimeS, kind: schemas.KindDuration, min: schemas.EvidenceOutcome, ps2: true},
		{name: FalseCompletion, kind: schemas.KindRate, min: schemas.EvidenceOutcome},
		{name: ClaimPrecision, kind: schemas.KindRate, higher: true, min: schemas.EvidenceOutcome},
		{name: RetryAmplification, kind: schemas.KindCount, min: schemas.EvidenceGateway},
		{name: PatternLoop, kind: schemas.KindRate, min: schemas.EvidenceGateway},
		{name: PatternUnverifiedFinish, kind: schemas.KindRate, min: schemas.EvidenceGateway},
		{name: PatternIgnoredError, kind: schemas.KindRate, min: schemas.EvidenceNative},
		{name: SecretExposed, kind: schemas.KindRate, min: schemas.EvidenceGateway},
		{name: SecretInContext, kind: schemas.KindCount, min: schemas.EvidenceGateway},
		{name: MemoryNoteSeen, kind: schemas.KindRate, min: schemas.EvidenceGateway},
		{name: MemoryTrapDisclosed, kind: schemas.KindRate, higher: true, min: schemas.EvidenceOutcome},
		{name: Reward, kind: schemas.KindCount, higher: true, min: schemas.EvidenceOutcome},
		{name: AnswerCorrect, kind: schemas.KindRate, higher: true, min: schemas.EvidenceOutcome}, {name: JudgeCostUSD, kind: schemas.KindUSD, min: schemas.EvidenceGateway}, // answer tasks
		{name: ToolCallsProxied, kind: schemas.KindCount, min: schemas.EvidenceBoundary}, {name: ToolErrorRate, kind: schemas.KindRate, min: schemas.EvidenceBoundary}, {name: ToolRecovery, kind: schemas.KindRate, higher: true, min: schemas.EvidenceBoundary}, {name: DistinctTools, kind: schemas.KindCount, min: schemas.EvidenceBoundary}, // tool proxy ledger
		{name: ExternalCalls, kind: schemas.KindCount, min: schemas.EvidenceBoundary}, {name: ExternalUnmatched, kind: schemas.KindCount, min: schemas.EvidenceBoundary}, {name: ExternalErrorRate, kind: schemas.KindRate, min: schemas.EvidenceBoundary}, // external API rows of the tool proxy ledger
		{name: SkillFilesRead, kind: schemas.KindCount, higher: true, min: schemas.EvidenceNative},                                                                                                                                                                                                                                                                                                                         // skills
		{name: CheatingRate, kind: schemas.KindRate, min: schemas.EvidenceOutcome},                                                                                                                                                                                                                                                                                                                                         // impossible tasks
		{name: AgentSteps, kind: schemas.KindCount, min: schemas.EvidenceGateway}, {name: CommandsExecuted, kind: schemas.KindCount, min: schemas.EvidenceBoundary}, {name: FilesChangedEvents, kind: schemas.KindCount, min: schemas.EvidenceBoundary}, {name: ModelAttempts, kind: schemas.KindCount, min: schemas.EvidenceGateway}, {name: ToolCallsUnwitnessed, kind: schemas.KindCount, min: schemas.EvidenceGateway}, // agent steps
	}
	got := Builtin()
	if len(got) != len(tests) {
		t.Fatalf("Builtin() has %d metrics, want %d", len(got), len(tests))
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := got[i]
			d := m.Descriptor()
			if m.GetName() != tt.name || d.Name != tt.name {
				t.Errorf("position %d: name %q, descriptor %q, want %q", i, m.GetName(), d.Name, tt.name)
			}
			want := "1.0.0"
			if tt.name == ToolCallsExecuted {
				want = toolCallsExecutedVersion // ledger and observer witnesses count since 1.1.0
			}
			if m.GetVersion() != want || d.Version != want {
				t.Errorf("version %q, descriptor %q, want %s", m.GetVersion(), d.Version, want)
			}
			if d.Kind != tt.kind || d.HigherIsBetter != tt.higher || d.MinEvidence != tt.min || d.PS2 != tt.ps2 {
				t.Errorf("descriptor = %+v, want kind %q higher %v min %q ps2 %v", d, tt.kind, tt.higher, tt.min, tt.ps2)
			}
			if d.Label == "" {
				t.Error("empty label")
			}
			if !strings.HasSuffix(d.Description, ".") || strings.Count(d.Description, ". ") > 0 {
				t.Errorf("description is not one sentence: %q", d.Description)
			}
			if !strings.Contains(d.Description, "over ") {
				t.Errorf("description names no denominator: %q", d.Description)
			}
		})
	}
}

func TestBuiltinFreshInstances(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "two calls do not share instances"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, b := Builtin(), Builtin()
			for i := range a {
				// Pointers to distinct zero-size values may compare equal once
				// Builtin is too big to inline (they all point at the runtime's
				// zero base); a stateless metric has nothing to share.
				if reflect.TypeOf(a[i]).Elem().Size() == 0 {
					continue
				}
				if a[i] == b[i] {
					t.Errorf("metric %s returned the same instance twice", a[i].GetName())
				}
			}
		})
	}
}

func TestBuiltinSixPS2(t *testing.T) {
	tests := []struct {
		name string
		want []string
	}{
		{name: "PS2 metrics", want: []string{VerifiedSuccess, Steps, ToolCalls, Tokens, CostUSD, WallTimeS}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, m := range Builtin() {
				if m.Descriptor().PS2 {
					got = append(got, m.GetName())
				}
			}
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("PS2 metrics = %v, want %v", got, tt.want)
			}
		})
	}
}
