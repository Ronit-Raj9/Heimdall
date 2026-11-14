package leaderboard

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Ledger lines as the fault proxy writes them: the scripted Anthropic and
// OpenAI models, and real OpenAI and Anthropic upstreams.
const (
	scriptedAnthropicLine = `{"slot":"fp-slot-2","run_id":"r_0ppcga450x","call":1,"kind":"message","path":"/v1/messages","stream":false,"action":"pass","status":200,"response_id":"msg_dvimkra0resgf3rxw26q65hu","tokens":{"fresh":619,"cache_read":0,"cache_write":0,"out":32},"upstream_ms":0.034}`
	scriptedOpenAILine    = `{"slot":"oa-slot-1","run_id":"r_x","call":1,"kind":"message","path":"/v1/responses","stream":true,"action":"pass","status":200,"response_id":"resp_0a1b2c3d4e5f6g7h8i9j0k1l","tokens":{"fresh":100,"cache_read":100,"cache_write":0,"out":32},"upstream_ms":0.1}`
	realOpenAILine        = `{"slot":"oa-slot-1","run_id":"r_h7jrktwqib","call":1,"kind":"message","path":"/v1/responses","stream":true,"action":"pass","status":200,"response_id":"resp_0d642ac4396093ad016af7a70fb52c87d2b66e110dfb071774","tokens":{"fresh":8288,"cache_read":0,"cache_write":0,"out":100},"upstream_ms":1554.319}`
	realAnthropic32Line   = `{"slot":"fp-slot-1","run_id":"r_y","call":1,"kind":"message","path":"/v1/messages","action":"pass","status":200,"response_id":"msg_01XFDUDYJgAACzvnptvVoYEL","tokens":{"fresh":10,"out":32}}`
	injectedLine          = `{"slot":"fp-slot-2","run_id":"r_0ppcga450x","call":3,"kind":"message","path":"/v1/messages","action":"inject","status":503,"nonce":"nonce_d3youkc9og8izfcx3hg3bfi6"}`
)

// Ledger lines as a proxy that labels its upstream writes them. The labelled
// real line deliberately carries the scripted fingerprint and the labelled
// scripted line deliberately does not, so only the label can decide.
const (
	labelledRealFingerprintLine = `{"slot":"fp-slot-1","run_id":"r_z","call":1,"kind":"message","path":"/v1/messages","action":"pass","upstream":"real","status":200,"response_id":"msg_dvimkra0resgf3rxw26q65hu","tokens":{"fresh":10,"out":32}}`
	labelledScriptedLine        = `{"slot":"fp-slot-1","run_id":"r_z","call":1,"kind":"message","path":"/v1/messages","action":"pass","upstream":"scripted","status":200,"response_id":"msg_01XFDUDYJgAACzvnptvVoYEL","tokens":{"fresh":10,"out":7}}`
	labelledScriptedInjectLine  = `{"slot":"fp-slot-1","run_id":"r_z","call":1,"kind":"message","path":"/v1/messages","action":"inject","upstream":"scripted","status":503}`
	labelledRealInjectLine      = `{"slot":"fp-slot-1","run_id":"r_z","call":2,"kind":"message","path":"/v1/messages","action":"inject","upstream":"real","status":503}`
	unlabelledUnconfiguredLine  = `{"slot":"groq-slot-1","run_id":"r_z","call":1,"kind":"message","path":"/v1/chat/completions","action":"pass","status":503}`
)

func TestScriptedLedger(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		want    bool
		wantErr bool
	}{
		{name: "scripted anthropic", data: injectedLine + "\n" + scriptedAnthropicLine + "\n", want: true},
		{name: "scripted openai responses", data: scriptedOpenAILine, want: true},
		{name: "real openai", data: realOpenAILine + "\n" + realOpenAILine, want: false},
		{name: "real anthropic id with 32 output tokens", data: realAnthropic32Line, want: false},
		{name: "injected fault only", data: injectedLine, want: false},
		{name: "empty", data: "", want: false},
		{name: "blank lines", data: "\n\n" + scriptedAnthropicLine + "\n\n", want: true},
		{name: "corrupt line", data: "{oops", wantErr: true},
		{name: "label real beats fingerprint", data: labelledRealFingerprintLine, want: false},
		{name: "label scripted without fingerprint", data: labelledScriptedLine, want: true},
		{name: "label scripted on injected line only", data: labelledScriptedInjectLine, want: true},
		{name: "label real on injected line only", data: labelledRealInjectLine, want: false},
		{name: "labelled ledger ignores fingerprint of unlabelled lines", data: labelledRealInjectLine + "\n" + scriptedAnthropicLine, want: false},
		{name: "unlabelled unconfigured line beside a real label", data: unlabelledUnconfiguredLine + "\n" + labelledRealFingerprintLine, want: false},
		{name: "unlabelled unconfigured line beside a scripted label", data: unlabelledUnconfiguredLine + "\n" + labelledScriptedLine, want: true},
		{name: "corrupt line after a label", data: labelledScriptedLine + "\n{oops", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := scriptedLedger([]byte(tt.data))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, want error %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("scripted = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClassify(t *testing.T) {
	f := newFake()
	f.exp("e_live", "s@1", 0, map[string]schemas.ArmManifest{"A": arm("a", "h_live", "command")})
	f.exp("e_dry", "s@1", 1, map[string]schemas.ArmManifest{"A": arm("a", "h_live", "command")})
	f.exp("e_toy", "s@1", 2, map[string]schemas.ArmManifest{"A": arm("toy", "h_toy", "scripted")})
	f.files[f.pass("e_live", "t1", "A", "clean", 1, 0.1)][fileLedger] = []byte(realOpenAILine)
	f.files[f.pass("e_dry", "t1", "A", "clean", 1, 0.1)][fileLedger] = []byte(scriptedOpenAILine)
	// A run that made no model call follows the other runs of its pair.
	f.fail("e_dry", "t2", "A", "clean", 1, 0)
	f.pass("e_toy", "t1", "A", "clean", 1, 0.1)
	recs, err := collect(context.Background(), f)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	got, err := classify(context.Background(), f, recs)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	want := map[string]bool{"e_live\x00h_live": false, "e_dry\x00h_live": true, "e_toy\x00h_toy": true}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("synthetic[%q] = %v, want %v", k, got[k], w)
		}
	}
	lb, err := Build(context.Background(), f, Query{Now: t0})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(lb.Entries) != 1 || lb.Entries[0].Runs != 1 || lb.Entries[0].Experiments[0] != "e_live" {
		t.Errorf("live board = %+v, want h_live over e_live only", lb.Entries)
	}
}

// TestClassifyLabelled checks classify on ledgers whose lines carry the
// upstream label: the label decides even against the fingerprint.
func TestClassifyLabelled(t *testing.T) {
	f := newFake()
	f.exp("e_real", "s@1", 0, map[string]schemas.ArmManifest{"A": arm("a", "h_a", "command")})
	f.exp("e_scripted", "s@1", 1, map[string]schemas.ArmManifest{"A": arm("a", "h_a", "command")})
	f.files[f.pass("e_real", "t1", "A", "clean", 1, 0.1)][fileLedger] = []byte(labelledRealFingerprintLine)
	f.files[f.pass("e_scripted", "t1", "A", "clean", 1, 0.1)][fileLedger] = []byte(labelledScriptedInjectLine + "\n" + labelledScriptedLine)
	recs, err := collect(context.Background(), f)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	got, err := classify(context.Background(), f, recs)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	want := map[string]bool{"e_real\x00h_a": false, "e_scripted\x00h_a": true}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("synthetic[%q] = %v, want %v", k, got[k], w)
		}
	}
}

func TestScriptedAgent(t *testing.T) {
	tests := []struct {
		name string
		rec  record
		want bool
	}{
		{name: "command", rec: record{arm: &schemas.ArmManifest{Config: schemas.AgentConfig{Adapter: "command"}}}, want: false},
		{name: "scripted adapter", rec: record{arm: &schemas.ArmManifest{Config: schemas.AgentConfig{Adapter: "scripted"}}}, want: true},
		{name: "scripted kind", rec: record{arm: &schemas.ArmManifest{Config: schemas.AgentConfig{Scripted: &schemas.ScriptedSpec{Kind: "oracle"}}}}, want: true},
		{name: "no arm, run says scripted", rec: record{run: schemas.Run{Agent: schemas.RunAgentInfo{Adapter: "scripted"}}}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := scriptedAgent(&tt.rec); got != tt.want {
				t.Errorf("scriptedAgent = %v, want %v", got, tt.want)
			}
		})
	}
}
