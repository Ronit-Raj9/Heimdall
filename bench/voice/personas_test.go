package voice

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestPersonaLibrary(t *testing.T) {
	lib, err := Personas()
	if err != nil {
		t.Fatal(err)
	}
	if len(lib) < 10 {
		t.Fatalf("%d personas", len(lib))
	}
	accents := map[string]bool{}
	for _, p := range lib {
		accents[p.Accent] = true
		if p.Voice != "en_US-ljspeech-medium" && p.Voice != "en_GB-vctk-medium" {
			t.Errorf("persona %s: voice %s is not one of the two openly licensed voices", p.ID, p.Voice)
		}
	}
	for _, a := range []string{"American", "Indian", "Scottish", "Irish", "Australian", "South African"} {
		if !accents[a] {
			t.Errorf("no %s persona", a)
		}
	}
	if p, ok := PersonaByID("interrupter"); !ok || p.InterruptionRate != 1 {
		t.Fatalf("interrupter %+v", p)
	}
}

func TestResolvePersona(t *testing.T) {
	if p, err := ResolvePersona("noisy-street"); err != nil || p.Channel != ChannelPhone {
		t.Fatalf("%+v %v", p, err)
	}
	if p, err := ResolvePersona(`{"id":"my-caller","label":"x","speed":1.3,"interruption_rate":0.2}`); err != nil || p.Speed != 1.3 {
		t.Fatalf("inline %+v %v", p, err)
	}
	for v, want := range map[string]string{
		"pirate":                         "not a persona in the library",
		`{"id":"x","speed":5}`:           "speed",
		`{"id":"x","channel":"radio"}`:   "channel",
		`{"id":"x","accent_strength":1}`: "unknown field",
		`{"id":"x","speaker":"p1"}`:      "multi-speaker voice",
		`{"id":"BAD"}`:                   "persona id",
	} {
		if _, err := ResolvePersona(v); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", v, err, want)
		}
	}
}

func TestCheckConditionPersona(t *testing.T) {
	suite := &schemas.Suite{Tasks: []*schemas.Task{{ID: "t1", Voice: &schemas.VoiceSpec{},
		Services: []schemas.ServiceSpec{{ID: "customer", Role: schemas.ServiceRoleUser, Export: []schemas.StateExport{{ID: TimelineExport}, {ID: TranscriptExport}}}}}}}
	probe := func(v string) *schemas.ProbeSpec {
		p := &schemas.ProbeSpec{ID: "persona"}
		p.Apply.ServiceEnv = map[string]map[string]string{"customer": {EnvPersona: v}}
		return p
	}
	if err := CheckCondition(suite, probe("indian-english"), []string{"t1"}); err != nil {
		t.Fatal(err)
	}
	if err := CheckCondition(suite, probe("klingon"), []string{"t1"}); err == nil || !strings.Contains(err.Error(), "klingon") {
		t.Fatalf("unknown persona: %v", err)
	}
	suite.Tasks[0].Services[0].Env = map[string]string{EnvPersona: "nobody"}
	if err := CheckCondition(suite, nil, []string{"t1"}); err == nil || !strings.Contains(err.Error(), "caller service") {
		t.Fatalf("unknown persona in the service env: %v", err)
	}
}

// TestRTCPython runs rtc_test.py: caller personas (phone line, persona
// interruptions, hesitations), the real-time bridge between the real
// caller and a fake gateway, and the gateway's checks and refusals.
func TestRTCPython(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	cmd := exec.Command(py, "-B", "-m", "unittest", "rtc_test")
	cmd.Dir = "."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rtc_test.py: %v\n%s", err, out)
	}
}
