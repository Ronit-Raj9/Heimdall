package agentdiff

import (
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestScriptedLedger(t *testing.T) {
	cases := []struct {
		name, data string
		want       bool
	}{
		{"labelled scripted", `{"upstream":"scripted"}` + "\n", true},
		{"labelled real wins over fingerprint", `{"upstream":"real","kind":"message","action":"pass","status":200,"tokens":{"out":32},"response_id":"msg_abcdefghijklmnopqrstuvwx"}`, false},
		{"fingerprint", `{"kind":"message","action":"pass","status":200,"tokens":{"out":32},"response_id":"msg_abcdefghijklmnopqrstuvwx"}`, true},
		{"real ids", `{"kind":"message","action":"pass","status":200,"tokens":{"out":32},"response_id":"msg_01AbCdEfGhIjKlMnOpQrStUv"}`, false},
		{"empty", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := scriptedLedger([]byte(c.data))
			if err != nil || got != c.want {
				t.Fatalf("got %v %v", got, err)
			}
		})
	}
	if _, err := scriptedLedger([]byte("{not json")); err == nil {
		t.Fatal("want parse error")
	}
}

func TestSyntheticArm(t *testing.T) {
	f := newFake()
	scripted := botArm(hashA, "toy", "m", "p\n")
	scripted.Config.Adapter = adapterScripted
	f.addExp("e1", "s@1", time.Now(), map[string]schemas.ArmManifest{"A": scripted, "B": botArm(hashB, "v", "m", "p\n")})
	id := f.add(runSpec{exp: "e1", arm: "B", task: "t1", outcome: schemas.OutcomePass, sealed: true})
	arms := collectArms(f)
	byArm := map[string]*armRecord{}
	for _, a := range arms {
		byArm[a.ref.Arm] = a
	}
	got, err := syntheticArm(f, byArm["A"], nil)
	if err != nil || !got {
		t.Fatalf("scripted adapter: %v %v", got, err)
	}
	cands := candidates(f, byArm["B"], schemas.ConditionClean)
	if got, err := syntheticArm(f, byArm["B"], cands); err != nil || got {
		t.Fatalf("live arm: %v %v", got, err)
	}
	f.files[id+"/proxy.ledger.jsonl"] = []byte(`{"upstream":"scripted"}`)
	if got, err := syntheticArm(f, byArm["B"], cands); err != nil || !got {
		t.Fatalf("scripted model: %v %v", got, err)
	}
}
