package graders

import "testing"

func TestBuiltin(t *testing.T) {
	tests := []struct {
		name, version string
	}{
		{HiddenTestsType, hiddenTestsVersion},
		{VerifierCommandType, verifierCommandVersion},
		{AnswerMatchType, answerMatchVersion}, {JudgeType, judgeVersion}, // answer tasks
	}
	gs := Builtin(HiddenTestsConfig{})
	if len(gs) != len(tests) {
		t.Fatalf("got %d graders, want %d", len(gs), len(tests))
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if gs[i].GetName() != tt.name || gs[i].GetVersion() != tt.version {
				t.Fatalf("got %s@%s, want %s@%s", gs[i].GetName(), gs[i].GetVersion(), tt.name, tt.version)
			}
			r, ok := gs[i].(interface{ VerifierNetwork() string })
			if !ok || r.VerifierNetwork() != "none" {
				t.Fatalf("%s does not report verifier network none", tt.name)
			}
		})
	}
}
