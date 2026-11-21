package metrics

import (
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/codescan"
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
		{name: SkillFilesRead, kind: schemas.KindCount, higher: true, min: schemas.EvidenceNative}, // skills
		{name: CheatingRate, kind: schemas.KindRate, min: schemas.EvidenceOutcome},                 // impossible tasks
		{name: AdapterFaultIssued, kind: schemas.KindCount, min: schemas.EvidenceBoundary},
		{name: AdapterFaultRecovery, kind: schemas.KindRate, higher: true, min: schemas.EvidenceNative},                                                                                                                                                                                                                                                                                                                    // impossible tasks
		{name: ToolTrafficUnparsed, kind: schemas.KindCount, min: schemas.EvidenceBoundary},                                                                                                                                                                                                                                                                                                                                // tool proxy traffic relayed without being parsed
		{name: AgentSteps, kind: schemas.KindCount, min: schemas.EvidenceGateway}, {name: CommandsExecuted, kind: schemas.KindCount, min: schemas.EvidenceBoundary}, {name: FilesChangedEvents, kind: schemas.KindCount, min: schemas.EvidenceBoundary}, {name: ModelAttempts, kind: schemas.KindCount, min: schemas.EvidenceGateway}, {name: ToolCallsUnwitnessed, kind: schemas.KindCount, min: schemas.EvidenceGateway}, // agent steps
		{name: ModelTurnsFailed, kind: schemas.KindCount, min: schemas.EvidenceGateway}, {name: ModelTurnsTotal, kind: schemas.KindCount, min: schemas.EvidenceGateway}, {name: ModelRetries, kind: schemas.KindCount, min: schemas.EvidenceGateway}, {name: ModelFallbacks, kind: schemas.KindCount, min: schemas.EvidenceGateway}, // model turn family beside steps
		{name: ToolProofRate, kind: schemas.KindRate, higher: true, min: schemas.EvidenceBoundary}, {name: ToolProofProxy, kind: schemas.KindRate, higher: true, min: schemas.EvidenceBoundary}, {name: ToolProofProcess, kind: schemas.KindRate, higher: true, min: schemas.EvidenceBoundary}, {name: ToolProofFileEffect, kind: schemas.KindRate, higher: true, min: schemas.EvidenceBoundary}, {name: ToolProofShim, kind: schemas.KindRate, higher: true, min: schemas.EvidenceBoundary}, {name: ToolProofResultReturned, kind: schemas.KindRate, min: schemas.EvidenceBoundary}, {name: ToolProofNone, kind: schemas.KindRate, min: schemas.EvidenceBoundary}, // proof classes of requested tool calls
		{name: ReportedTokens, kind: schemas.KindCount, min: schemas.EvidenceNative}, {name: ReportedCostUSD, kind: schemas.KindUSD, min: schemas.EvidenceNative}, // Remote coverage: agent-reported usage
		{name: ReportedSteps, kind: schemas.KindCount, min: schemas.EvidenceNative}, {name: ReportedToolCalls, kind: schemas.KindCount, min: schemas.EvidenceNative}, {name: SubagentsSpawned, kind: schemas.KindCount, min: schemas.EvidenceNative}, {name: EventsDropped, kind: schemas.KindCount, min: schemas.EvidenceNative}, // Agent events area
		{name: FallbackUsed, kind: schemas.KindRate, min: schemas.EvidenceGateway}, {name: ServedModelSwitches, kind: schemas.KindCount, min: schemas.EvidenceGateway}, {name: RoutedTarget, kind: schemas.KindCount, min: schemas.EvidenceGateway}, // W9 routing policy under test
		{name: Escalated, kind: schemas.KindRate, min: schemas.EvidenceBoundary}, {name: EscalationCorrect, kind: schemas.KindRate, higher: true, min: schemas.EvidenceBoundary}, {name: PolicyViolation, kind: schemas.KindRate, min: schemas.EvidenceOutcome}, {name: UserTurns, kind: schemas.KindCount, min: schemas.EvidenceBoundary}, {name: SimulatorCostUSD, kind: schemas.KindUSD, min: schemas.EvidenceGateway}, // W3 customer support
		{name: Searches, kind: schemas.KindCount, min: schemas.EvidenceBoundary}, {name: SourcesCited, kind: schemas.KindCount, higher: true, min: schemas.EvidenceOutcome}, {name: CitationsGrounded, kind: schemas.KindRate, higher: true, min: schemas.EvidenceBoundary}, {name: CitationPrecision, kind: schemas.KindRate, higher: true, min: schemas.EvidenceOutcome}, // W4 research with Tavily
		{name: TimeToFirstAudioMS, kind: schemas.KindCount, min: schemas.EvidenceBoundary}, {name: TurnLatencyP50MS, kind: schemas.KindCount, min: schemas.EvidenceBoundary}, {name: TurnLatencyP95MS, kind: schemas.KindCount, min: schemas.EvidenceBoundary}, {name: BargeInHandled, kind: schemas.KindRate, higher: true, min: schemas.EvidenceBoundary}, {name: DeadAirS, kind: schemas.KindDuration, min: schemas.EvidenceBoundary}, {name: STTWER, kind: schemas.KindCount, min: schemas.EvidenceNative}, {name: VoiceCostPerMinute, kind: schemas.KindUSD, min: schemas.EvidenceGateway}, // X8 voice agents
		{name: EvidenceRecall, kind: schemas.KindRate, higher: true, min: schemas.EvidenceBoundary},                                                                                           // X5a research importers
		{name: VisualScore, kind: schemas.KindRate, higher: true, min: schemas.EvidenceOutcome}, {name: VisualJudgeScore, kind: schemas.KindRate, higher: true, min: schemas.EvidenceOutcome}, // X1 vision grading
		// Safety probes area (S1).
		{name: SafeStop, kind: schemas.KindRate, higher: true, min: schemas.EvidenceGateway}, {name: TimeToGiveUpS, kind: schemas.KindDuration, min: schemas.EvidenceGateway}, {name: USDWhileDown, kind: schemas.KindUSD, min: schemas.EvidenceGateway}, {name: RetryStorm, kind: schemas.KindCount, min: schemas.EvidenceGateway}, {name: OutageDisclosed, kind: schemas.KindRate, higher: true, min: schemas.EvidenceGateway},
		{name: SilentCompromise, kind: schemas.KindRate, min: schemas.EvidenceOutcome}, {name: OversightDisclosed, kind: schemas.KindRate, higher: true, min: schemas.EvidenceOutcome}, {name: QuietFaultRechecked, kind: schemas.KindRate, higher: true, min: schemas.EvidenceBoundary},
		// Multi-agent runs.
		{name: AgentsSpawned, kind: schemas.KindCount, min: schemas.EvidenceNative},
		{name: MaxAgentDepth, kind: schemas.KindCount, min: schemas.EvidenceNative},
		{name: MaxFanOut, kind: schemas.KindCount, higher: true, min: schemas.EvidenceNative},
		{name: OrchestrationShare, kind: schemas.KindRate, min: schemas.EvidenceGateway},
		{name: Handoffs, kind: schemas.KindCount, min: schemas.EvidenceNative},
		{name: AgentMessages, kind: schemas.KindCount, min: schemas.EvidenceNative},
		{name: AgentMessageBytes, kind: schemas.KindCount, min: schemas.EvidenceNative},
		{name: DuplicateWorkRatio, kind: schemas.KindRate, min: schemas.EvidenceNative},
		{name: SerialCollapse, kind: schemas.KindRate, min: schemas.EvidenceNative},
		{name: CriticalPathS, kind: schemas.KindDuration, min: schemas.EvidenceNative},
		{name: Parallelism, kind: schemas.KindCount, higher: true, min: schemas.EvidenceNative},
		{name: DelegationLoops, kind: schemas.KindCount, min: schemas.EvidenceNative},
		{name: AttributionCoverage, kind: schemas.KindRate, higher: true, min: schemas.EvidenceNative},
		{name: BudgetExceeded, kind: schemas.KindRate, min: schemas.EvidenceGateway},
		{name: GracefulFinish, kind: schemas.KindRate, higher: true, min: schemas.EvidenceOutcome}, {name: CostNormalizedSuccess, kind: schemas.KindCount, higher: true, min: schemas.EvidenceGateway}, {name: ResumedWithoutRedo, kind: schemas.KindRate, higher: true, min: schemas.EvidenceGateway}, // persistence.go
		{name: EvalAwarenessSignals, kind: schemas.KindCount, min: schemas.EvidenceNative},        // Eval integrity area (S2)
		{name: ConformityFlip, kind: schemas.KindRate, min: schemas.EvidenceOutcome},              // Multi-agent probes (M2)
		{name: ReviewerCatch, kind: schemas.KindRate, higher: true, min: schemas.EvidenceGateway}, // Multi-agent probes (M2)

		// X4: code scan of added lines.
		{name: InsecureFindings, kind: schemas.KindCount, min: schemas.EvidenceOutcome},
		{name: InsecureFindingsHigh, kind: schemas.KindCount, min: schemas.EvidenceOutcome},
		{name: SecretsAdded, kind: schemas.KindCount, min: schemas.EvidenceOutcome},

		// X7: GPU runs.
		{name: GPUSeconds, kind: schemas.KindDuration, min: schemas.EvidenceOutcome},
		{name: PeakVRAMMB, kind: schemas.KindCount, min: schemas.EvidenceOutcome},
		{name: GPUUtil, kind: schemas.KindRate, higher: true, min: schemas.EvidenceOutcome},
		{name: Speedup, kind: schemas.KindCount, higher: true, min: schemas.EvidenceOutcome},
		{name: HeldoutScore, kind: schemas.KindRate, higher: true, min: schemas.EvidenceOutcome},
		{name: TimeToTargetS, kind: schemas.KindDuration, min: schemas.EvidenceOutcome},
		// B2 Bifrost features under test.
		{name: GatewayOverheadMS, kind: schemas.KindCount, min: schemas.EvidenceGateway}, {name: GatewayOverheadP95MS, kind: schemas.KindCount, min: schemas.EvidenceGateway},
		{name: InputTokens, kind: schemas.KindTokens, min: schemas.EvidenceGateway}, {name: CacheReadShare, kind: schemas.KindRate, higher: true, min: schemas.EvidenceGateway},
		{name: SemanticCacheHits, kind: schemas.KindCount, min: schemas.EvidenceGateway}, {name: SemanticCacheForeignHits, kind: schemas.KindCount, min: schemas.EvidenceGateway},
		{name: BifrostMCPCalls, kind: schemas.KindCount, min: schemas.EvidenceGateway}, {name: BifrostMCPWitnessMismatch, kind: schemas.KindCount, min: schemas.EvidenceBoundary},

		// B1: A2A agents through the agent gateway.
		{name: A2ATasks, kind: schemas.KindCount, min: schemas.EvidenceGateway},
		{name: A2ALatencyMS, kind: schemas.KindCount, min: schemas.EvidenceGateway},
		{name: A2AErrors, kind: schemas.KindCount, min: schemas.EvidenceGateway},
		{name: A2AHops, kind: schemas.KindCount, min: schemas.EvidenceGateway},
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
			if tt.name == CostUSD || tt.name == RetryAmplification {
				want = "1.1.0" // QC2-04-05: changed semantics carry a new version
			}
			if tt.name == InsecureFindings || tt.name == InsecureFindingsHigh || tt.name == SecretsAdded {
				want = codescan.RulesVersion // X4: code scan metrics follow the rules
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
