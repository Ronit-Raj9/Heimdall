package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/jobs"
)

// The consent given on the Add page reaches the hosted readiness bridge.
func TestOnboardReadyPassesJudgeConsent(t *testing.T) {
	raw, _ := json.Marshal(handlers.OnboardReadyParams{Set: "s1", Task: "t1", JudgeConsent: true})
	got := onboardAdmitParams(&jobs.Job{Params: raw}, "draft-s1", "t1")
	if !got.JudgeConsent || got.Suite != "draft-s1" || got.Task != "t1" {
		t.Fatalf("params %+v", got)
	}
	raw, _ = json.Marshal(handlers.OnboardReadyParams{Set: "s1"})
	if onboardAdmitParams(&jobs.Job{Params: raw}, "draft-s1", "").JudgeConsent {
		t.Fatal("consent set without the request asking for it")
	}
}

// The refusal names the box on the page, not a field or another tab.
func TestErrJudgeConsentPlain(t *testing.T) {
	msg := errJudgeConsent(readinessJudge{Provider: "openai", Model: "gpt-4o-mini", Calls: 81}).Error()
	want := "The readiness checks of this benchmark call your judge model openai/gpt-4o-mini about 81 times on your own key. Tick the box that agrees to the judge calls, next to the readiness button, and run them again."
	if !strings.Contains(msg, want) || strings.Contains(msg, "judge_consent") || strings.Contains(msg, "Build tab") {
		t.Fatalf("message %q", msg)
	}
}
