package voice

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func voiceTask() (*schemas.Suite, *schemas.Task) {
	t := &schemas.Task{ID: "t1", Voice: &schemas.VoiceSpec{}, Services: []schemas.ServiceSpec{{
		ID: "customer", Role: schemas.ServiceRoleUser,
		Export: []schemas.StateExport{{ID: TimelineExport, Path: "/state/timeline.jsonl"}, {ID: TranscriptExport, Path: "/state/transcript.json"}},
	}}}
	return &schemas.Suite{ID: "s", Tasks: []*schemas.Task{t}}, t
}

func TestEffective(t *testing.T) {
	if Effective(nil) != nil {
		t.Fatal("nil spec")
	}
	e := Effective(&schemas.VoiceSpec{})
	if e.Caller != DefaultCaller || e.Transport != schemas.VoiceTransportWebSocket || e.BargeInBoundMS != 1000 || e.DeadAirMS != 2000 {
		t.Fatalf("defaults %+v", e)
	}
}

func TestCheckTask(t *testing.T) {
	s, task := voiceTask()
	if err := CheckTask(s, task); err != nil {
		t.Fatal(err)
	}
	yes := true
	for name, mut := range map[string]func(*schemas.Task){
		"transport":   func(t *schemas.Task) { t.Voice.Transport = "sip" },
		"probe":       func(t *schemas.Task) { t.Voice.Probes = []string{"loud"} },
		"bound":       func(t *schemas.Task) { t.Voice.BargeInBoundMS = 20 },
		"dead air":    func(t *schemas.Task) { t.Voice.DeadAirMS = 100 },
		"no caller":   func(t *schemas.Task) { t.Voice.Caller = "phone" },
		"role":        func(t *schemas.Task) { t.Services[0].Role = schemas.ServiceRoleMCP },
		"hidden":      func(t *schemas.Task) { t.Services[0].Hidden = true },
		"proxied":     func(t *schemas.Task) { t.Services[0].Proxy = &yes },
		"no timeline": func(t *schemas.Task) { t.Services[0].Export = t.Services[0].Export[1:] },
		"sessions":    func(t *schemas.Task) { t.Sessions = []schemas.SessionSpec{{ID: "a"}, {ID: "b"}} }, // QC2-24-03
	} {
		s, task := voiceTask()
		mut(task)
		if err := CheckTask(s, task); err == nil {
			t.Errorf("%s: want a refusal", name)
		}
	}
	if err := CheckTask(&schemas.Suite{}, &schemas.Task{ID: "plain"}); err != nil {
		t.Fatalf("a task without voice: %v", err)
	}
}

func TestCheckCondition(t *testing.T) {
	s, task := voiceTask()
	probe := func(id, v string) *schemas.ProbeSpec {
		return &schemas.ProbeSpec{ID: id, Apply: schemas.ProbeApply{ServiceEnv: map[string]map[string]string{"customer": {EnvProbe: v}}}}
	}
	if err := CheckCondition(s, nil, []string{"t1"}); err != nil {
		t.Fatal(err)
	}
	if err := CheckCondition(s, probe("caller-noise", ProbeNoise), []string{"t1"}); err != nil {
		t.Fatal(err)
	}
	if err := CheckCondition(s, probe("x", "loud"), []string{"t1"}); err == nil {
		t.Fatal("unknown voice probe accepted")
	}
	task.Voice.Probes = []string{ProbeNoise}
	if err := CheckCondition(s, probe("caller-silent", ProbeSilent), []string{"t1"}); err == nil || !strings.Contains(err.Error(), "allows voice probes noise") {
		t.Fatalf("disallowed probe: %v", err)
	}
	plain := &schemas.Task{ID: "t2"}
	s.Tasks = append(s.Tasks, plain)
	if err := CheckCondition(s, probe("caller-noise", ProbeNoise), []string{"t2"}); err == nil {
		t.Fatal("voice probe on a task without a call accepted")
	}
	other := &schemas.ProbeSpec{ID: "p", Apply: schemas.ProbeApply{ServiceEnv: map[string]map[string]string{"helpdesk": {EnvProbe: ProbeNoise}}}}
	if err := CheckCondition(s, other, []string{"t1"}); err == nil {
		t.Fatal("voice probe on another service accepted")
	}
}

func TestCheckGatewayModels(t *testing.T) {
	ok := schemas.AgentConfig{ID: "a", Model: "gpt-4o-mini", Gateway: schemas.GatewayPolicy{Provider: "openai", Models: []string{"whisper-1", "openai/gpt-4o-mini-tts"}}}
	if err := CheckGatewayModels(ok); err != nil {
		t.Fatal(err)
	}
	for name, models := range map[string][]string{
		"empty":    {""},
		"twice":    {"whisper-1", "openai/whisper-1"},
		"own":      {"gpt-4o-mini"},
		"provider": {"anthropic/claude-x"},
	} {
		c := ok
		c.Gateway.Models = models
		if err := CheckGatewayModels(c); err == nil {
			t.Errorf("%s: want a refusal", name)
		}
	}
	c := ok
	c.Gateway.Routing = &schemas.RoutingPolicy{Strategy: schemas.RoutingFixed}
	if err := CheckGatewayModels(c); err == nil {
		t.Fatal("models with routing accepted")
	}
}

// TestCopiesInStep keeps the example's copies of the voice files equal to
// bench/voice (the images are built from the copies).
func TestCopiesInStep(t *testing.T) {
	ex := filepath.Join("..", "examples", "acme-voice-desk")
	for dir, names := range map[string][]string{
		filepath.Join(ex, "benchmarks", "acme-voice-desk", "services", "helpdesk"): CallerFiles,
		filepath.Join(ex, "agents", "images", "voice-agent"):                       AgentFiles,
	} {
		for _, n := range names {
			got, err := os.ReadFile(filepath.Join(dir, n))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, File(n)) {
				t.Errorf("%s/%s differs from bench/voice/%s: copy it again", dir, n, n)
			}
		}
	}
}

// TestVoicePython runs voice_test.py: protocol framing, the caller service
// with every probe, barge-in, the no-call exports and the fakes.
func TestVoicePython(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	cmd := exec.Command(py, "-B", "-m", "unittest", "voice_test")
	cmd.Dir = "."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("voice_test.py: %v\n%s", err, out)
	}
}
