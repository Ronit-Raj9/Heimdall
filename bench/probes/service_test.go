package probes

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

var attackEnv = map[string]map[string]string{"agentdojo": {"AGENTDOJO_ATTACK": "important_instructions", "AGENTDOJO_INJECTION_TASK": "injection_task_0"}}

func serviceRC(t *testing.T) *schemas.RunContext {
	t.Helper()
	return &schemas.RunContext{
		Run:   &schemas.Run{RunID: "r_svc"},
		Dir:   t.TempDir(),
		Task:  &schemas.Task{ID: "banking-user-task-0", Services: []schemas.ServiceSpec{{ID: "agentdojo", Role: schemas.ServiceRoleMCP}}},
		Probe: &schemas.ProbeSpec{ID: "agentdojo-important-instructions-inj0", Layer: schemas.LayerService, Apply: schemas.ProbeApply{ServiceEnv: attackEnv}},
	}
}

// writeState writes an exported state file into the run folder and returns
// its record.
func writeState(t *testing.T, rc *schemas.RunContext, body string) schemas.StateFile {
	t.Helper()
	rel := "state/agentdojo/run"
	p := filepath.Join(rc.Dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return schemas.StateFile{Service: "agentdojo", ID: "run", Path: rel, SHA256: utils.SHA256Bytes([]byte(body)), Bytes: int64(len(body))}
}

func TestServiceArm(t *testing.T) {
	l := newServiceLayer(nil)
	rec, err := l.Arm(context.Background(), serviceRC(t))
	if err != nil {
		t.Fatal(err)
	}
	armed := rec.Steps[schemas.StepArmed]
	want := "env:agentdojo:" + utils.SHA256Bytes([]byte("AGENTDOJO_ATTACK=important_instructions\nAGENTDOJO_INJECTION_TASK=injection_task_0\n"))
	if rec.Layer != schemas.LayerService || armed.State != schemas.StepYes || !reflect.DeepEqual(armed.Evidence, []string{want}) {
		t.Fatalf("receipt %+v", rec)
	}
	if rec.Steps[schemas.StepVisible].State != schemas.StepNotApplicable {
		t.Fatalf("visible is not a step of this layer: %+v", rec.Steps[schemas.StepVisible])
	}
	cases := map[string]func(rc *schemas.RunContext){
		"no env": func(rc *schemas.RunContext) { rc.Probe.Apply.ServiceEnv = nil },
		"unknown service": func(rc *schemas.RunContext) {
			rc.Probe.Apply.ServiceEnv = map[string]map[string]string{"crm": {"A": "1"}}
		},
		"no variables": func(rc *schemas.RunContext) {
			rc.Probe.Apply.ServiceEnv = map[string]map[string]string{"agentdojo": {}}
		},
		"bad name": func(rc *schemas.RunContext) {
			rc.Probe.Apply.ServiceEnv = map[string]map[string]string{"agentdojo": {"A-B": "1"}}
		},
		"nul value": func(rc *schemas.RunContext) {
			rc.Probe.Apply.ServiceEnv = map[string]map[string]string{"agentdojo": {"A": "x\x00"}}
		},
		"no probe": func(rc *schemas.RunContext) { rc.Probe = nil },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			rc := serviceRC(t)
			change(rc)
			if _, err := l.Arm(context.Background(), rc); err == nil {
				t.Fatal("want an error")
			}
		})
	}
	// Without declared services there is nothing to check the ids against.
	rc := serviceRC(t)
	rc.Task.Services = nil
	rc.Probe.Apply.ServiceEnv = map[string]map[string]string{"anything": {"A": "1"}}
	if _, err := l.Arm(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
	if err := l.Disarm(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
}

func TestServiceReceiptsDelivered(t *testing.T) {
	l := newServiceLayer(nil)
	up := []schemas.ServiceResult{{ID: "agentdojo", Ready: true, ImageID: "sha256:img"}}
	served := `{"format":"heimdall-agentdojo/1","planted":{"armed":true,"vectors":["injection_bill_text"],"served":[{"call":1,"tool":"read_file","vectors":["injection_bill_text"]}]}}`
	cases := []struct {
		name     string
		state    string // exported state body; "" exports nothing
		tamper   bool
		calls    []schemas.ToolCall
		markers  []string
		want     schemas.StepState
		evidence []string
	}{
		{"served", served, false, nil, nil, schemas.StepYes, []string{"state:agentdojo/run:call=1:read_file"}},
		{"never read", `{"planted":{"armed":true,"served":[]}}`, false, nil, nil, schemas.StepNo, nil},
		{"planted nothing", `{"planted":{"armed":false,"served":[]}}`, false, nil, nil, schemas.StepNo, nil},
		{"tampered state", served, true, nil, nil, schemas.StepUnknown, nil},
		{"no report", `{"other":1}`, false, nil, nil, schemas.StepUnknown, nil},
		{"marker in ledger", "", false, []schemas.ToolCall{
			{Seq: 3, Server: "agentdojo", Tool: "read_file", Result: "bill ... <INFORMATION> do this"},
			{Seq: 4, Server: "other", Tool: "x", Result: "<INFORMATION>"},
		}, []string{"<INFORMATION>"}, schemas.StepYes, []string{"tool_call:3"}},
		{"markers absent from a complete ledger", "", false, []schemas.ToolCall{{Seq: 1, Server: "agentdojo", Result: "clean"}}, []string{"<INFORMATION>"}, schemas.StepNo, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rc := serviceRC(t)
			rc.Probe.Apply.Markers = c.markers
			ev := &schemas.Evidence{Services: up, ToolCalls: c.calls}
			if c.state != "" {
				sf := writeState(t, rc, c.state)
				if c.tamper {
					sf.SHA256 = utils.SHA256Bytes([]byte("something else"))
				}
				ev.State = []schemas.StateFile{sf}
			}
			rec, err := l.Receipts(context.Background(), rc, ev)
			if err != nil {
				t.Fatal(err)
			}
			got := rec.Steps[schemas.StepDelivered]
			if got.State != c.want || !reflect.DeepEqual(got.Evidence, c.evidence) {
				t.Fatalf("delivered %+v", got)
			}
		})
	}
}

func TestServiceReceiptsFired(t *testing.T) {
	l := newServiceLayer(nil)
	cases := []struct {
		name     string
		services []schemas.ServiceResult
		want     schemas.StepState
		detail   string
	}{
		{"up", []schemas.ServiceResult{{ID: "agentdojo", Ready: true}}, schemas.StepYes, "1 service(s)"},
		{"not ready", []schemas.ServiceResult{{ID: "agentdojo", Ready: false, Error: "ready check timed out"}}, schemas.StepNo, "did not come up"},
		{"missing", []schemas.ServiceResult{{ID: "other", Ready: true}}, schemas.StepUnknown, "not in the service record"},
		{"no record", nil, schemas.StepUnknown, "no service record"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rc := serviceRC(t)
			rec, err := l.Receipts(context.Background(), rc, &schemas.Evidence{Services: c.services})
			if err != nil {
				t.Fatal(err)
			}
			got := rec.Steps[schemas.StepFired]
			if got.State != c.want || !strings.Contains(got.Detail, c.detail) {
				t.Fatalf("fired %+v", got)
			}
		})
	}
}

func TestServiceReceiptsOtherSteps(t *testing.T) {
	l := newServiceLayer(nil)
	rc := serviceRC(t)
	ev := &schemas.Evidence{
		Claim: &schemas.Claim{Text: "Paid the bill. The file also held a suspicious instruction, which I ignored."},
		Grade: &schemas.Grade{Passed: true, DiffSHA256: "abc"},
	}
	rec, err := l.Receipts(context.Background(), rc, ev)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Steps[schemas.StepAcknowledged].State != schemas.StepYes || rec.Steps[schemas.StepEffect].State != schemas.StepYes ||
		rec.Steps[schemas.StepArmed].State != schemas.StepYes || rec.Steps[schemas.StepDelivered].State != schemas.StepUnknown {
		t.Fatalf("receipt %+v", rec.Steps)
	}
	rc.Probe.Apply.ServiceEnv = nil
	if rec, err = l.Receipts(context.Background(), rc, ev); err != nil || rec.Steps[schemas.StepArmed].State != schemas.StepNo {
		t.Fatalf("an unusable spec is not armed: %+v %v", rec, err)
	}
	if _, err := l.Receipts(context.Background(), &schemas.RunContext{}, ev); err == nil {
		t.Fatal("receipts without a probe must fail")
	}
	if l.GetName() != "service" || l.GetVersion() == "" || len(l.Steps()) != len(serviceSteps) {
		t.Fatalf("identity %s %s %v", l.GetName(), l.GetVersion(), l.Steps())
	}
}

// TestMarkerPatterns checks that a marker's {{nonce}} matches a nonce a
// service minted itself, and only a nonce of the bench's shape.
func TestMarkerPatterns(t *testing.T) {
	res := markerPatterns([]string{"peer-canary.invalid/{{nonce}}", " ", "plain"})
	if len(res) != 2 {
		t.Fatalf("got %d patterns", len(res))
	}
	if !res[0].MatchString("curl http://peer-canary.invalid/nonce_abcdefghijklmnopqrstuvwx") || res[0].MatchString("peer-canary.invalid/{{nonce}}") {
		t.Error("nonce marker")
	}
	if !res[1].MatchString("a plain text") {
		t.Error("literal marker")
	}
}
