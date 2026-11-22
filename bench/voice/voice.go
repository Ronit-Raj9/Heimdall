// Package voice is Heimdall's voice agent track (docs/voice-agents.md,
// docs/voice-protocol.md): a task is a support call spoken over WebSocket.
//
// The Python half runs in containers: voicecaller.py is the voice caller
// service (a role user service the bench starts beside the agent: it speaks
// the simulated customer's lines with local text to speech, plays the agent's
// audio on a real-time clock, transcribes it with local speech to text and
// writes the call timeline), tonecode.py the deterministic test codec and
// audio helpers, wsproto.py a standard-library WebSocket, voiceclient.py the
// agent-side helpers, fakeopenai.py the scripted audio and chat endpoints for
// $0 runs, and cascadedagent.py and realtimebridge.py the reference agents.
//
// The Go half pins and checks voice tasks at plan time and reads the
// caller's timeline into the voice metrics (Analyze, metrics/voice.go). The
// timeline is written by a bench-owned service, so it is the bench's witness
// of the call, like the tool proxy's ledger: the agent only ever sends audio
// and control messages, and what the agent reports about itself is kept
// apart and labelled.
package voice

import (
	"embed"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Protocol is the wire protocol the caller speaks (docs/voice-protocol.md).
const Protocol = "heimdall-voice/1"

// Defaults of a VoiceSpec.
const (
	DefaultCaller         = "customer"
	DefaultBargeInBoundMS = 1000
	DefaultDeadAirMS      = 2000
)

// Exports of the voice caller (state/<caller>/<id>).
const (
	TimelineExport   = "timeline.jsonl"
	TranscriptExport = "transcript.json"
	CallerAudio      = "caller.wav"
	AgentAudio       = "agent.wav"
)

// Voice probes: values of BENCH_VOICE_PROBE, set on the caller service by a
// service-layer probe (apply.service_env.<caller>.BENCH_VOICE_PROBE).
const (
	ProbeNoise     = "noise"     // white noise on the caller's audio (BENCH_VOICE_NOISE_SNR_DB)
	ProbeSpeed     = "speed"     // the caller's audio played faster, pitch up (BENCH_VOICE_SPEED)
	ProbeInterrupt = "interrupt" // the caller talks over the agent (barge-in)
	ProbeSilent    = "silent"    // the caller goes quiet before one reply (BENCH_VOICE_SILENT_MS)
	EnvProbe       = "BENCH_VOICE_PROBE"
)

// Probes are the caller-side voice probes, in documentation order. The
// speech-to-text outage is a gateway probe on the transcription route, not
// a caller probe (docs/voice-agents.md).
var Probes = []string{ProbeNoise, ProbeSpeed, ProbeInterrupt, ProbeSilent}

//go:embed voicecaller.py tonecode.py wsproto.py voiceclient.py fakeopenai.py cascadedagent.py realtimebridge.py personas.json rtcbridge.py
var assets embed.FS

// CallerFiles are the files a voice suite's service image carries next to
// its Dockerfile (simuser.py comes from bench/simuser).
var CallerFiles = []string{"voicecaller.py", "tonecode.py", "wsproto.py", "voiceclient.py"}

// AgentFiles are the reference agents' files (an agent image carries them
// with the client helpers).
var AgentFiles = []string{"cascadedagent.py", "realtimebridge.py", "voiceclient.py", "tonecode.py", "wsproto.py"}

// File returns one embedded Python file.
func File(name string) []byte {
	b, err := assets.ReadFile(name)
	if err != nil {
		panic("voice: embedded " + name + " missing: " + err.Error())
	}
	return b
}

// Files returns the named embedded files by name.
func Files(names []string) map[string][]byte {
	out := make(map[string][]byte, len(names))
	for _, n := range names {
		out[n] = File(n)
	}
	return out
}

// Effective is spec with its defaults filled; nil stays nil.
func Effective(spec *schemas.VoiceSpec) *schemas.VoiceSpec {
	if spec == nil {
		return nil
	}
	out := *spec
	if out.Caller == "" {
		out.Caller = DefaultCaller
	}
	if out.Transport == "" {
		out.Transport = schemas.VoiceTransportWebSocket
	}
	if out.BargeInBoundMS == 0 {
		out.BargeInBoundMS = DefaultBargeInBoundMS
	}
	if out.DeadAirMS == 0 {
		out.DeadAirMS = DefaultDeadAirMS
	}
	return &out
}

// IsVoiceTask reports whether a task is a voice call.
func IsVoiceTask(t *schemas.Task) bool { return t != nil && t.Voice != nil }

// CallerService returns the task's voice caller service, from the task's
// services and then the suite's.
func CallerService(suite *schemas.Suite, t *schemas.Task) (schemas.ServiceSpec, bool) {
	spec := Effective(t.Voice)
	if spec == nil {
		return schemas.ServiceSpec{}, false
	}
	for _, s := range t.Services {
		if s.ID == spec.Caller {
			return s, true
		}
	}
	if suite != nil {
		for _, s := range suite.Services {
			if s.ID == spec.Caller {
				return s, true
			}
		}
	}
	return schemas.ServiceSpec{}, false
}

// CheckTask refuses a voice task that cannot run: an unknown transport, a
// caller that is not a role user service, a caller that is proxied or hidden
// (the agent dials it directly; the tool proxy does not carry WebSocket
// audio), a caller without the timeline and transcript exports the metrics
// and the verifier read, or more than one session (one call per run).
func CheckTask(suite *schemas.Suite, t *schemas.Task) error {
	spec := Effective(t.Voice)
	if spec == nil {
		return nil
	}
	var errs []error
	if spec.Transport != schemas.VoiceTransportWebSocket {
		errs = append(errs, fmt.Errorf("voice transport %q: only %s (heimdall-voice/1) is supported; telephony (SIP, PSTN) is not", spec.Transport, schemas.VoiceTransportWebSocket))
	}
	for _, p := range spec.Probes {
		if !knownProbe(p) {
			errs = append(errs, fmt.Errorf("voice probe %q: want one of %s", p, strings.Join(Probes, ", ")))
		}
	}
	if spec.BargeInBoundMS < 100 || spec.BargeInBoundMS > 10000 {
		errs = append(errs, fmt.Errorf("voice barge_in_bound_ms %d: want 100 to 10000", spec.BargeInBoundMS))
	}
	if spec.DeadAirMS < 500 || spec.DeadAirMS > 60000 {
		errs = append(errs, fmt.Errorf("voice dead_air_ms %d: want 500 to 60000", spec.DeadAirMS))
	}
	if len(t.Sessions) > 1 {
		// The caller service starts once per run and takes one call
		// (heimdall-voice/1); a second session's call would be refused.
		errs = append(errs, fmt.Errorf("voice task has %d sessions: the caller takes one call per run, so a voice task has at most one session", len(t.Sessions)))
	}
	s, ok := CallerService(suite, t)
	switch {
	case !ok:
		errs = append(errs, fmt.Errorf("voice caller service %q is not declared in the task or suite services", spec.Caller))
	default:
		if s.Role != schemas.ServiceRoleUser {
			errs = append(errs, fmt.Errorf("voice caller service %q has role %q: want role user", s.ID, s.Role))
		}
		if s.Hidden {
			errs = append(errs, fmt.Errorf("voice caller service %q is hidden: the agent must be able to dial it", s.ID))
		}
		if s.Proxy != nil && *s.Proxy {
			errs = append(errs, fmt.Errorf("voice caller service %q is proxied: the tool proxy does not carry WebSocket audio; leave proxy unset", s.ID))
		}
		have := map[string]bool{}
		for _, e := range s.Export {
			have[e.ID] = true
		}
		for _, want := range []string{TimelineExport, TranscriptExport} {
			if !have[want] {
				errs = append(errs, fmt.Errorf("voice caller service %q does not export %s", s.ID, want))
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("task %s: %w", t.ID, err)
	}
	return nil
}

func knownProbe(p string) bool {
	for _, k := range Probes {
		if p == k {
			return true
		}
	}
	return false
}

// ProbeOf returns the voice probe a probe spec sets on a task's caller, or
// "" when it sets none.
func ProbeOf(probe *schemas.ProbeSpec, caller string) string {
	if probe == nil || probe.Apply.ServiceEnv == nil {
		return ""
	}
	return probe.Apply.ServiceEnv[caller][EnvProbe]
}

// CheckCondition is the plan-time check of one condition: every selected
// voice task must pass CheckTask, and a condition whose probe sets a voice
// probe is refused for a task that does not allow it, for a task that is not
// a voice call, and for an unknown voice probe. Refusals carry the reason;
// nothing runs half way.
func CheckCondition(suite *schemas.Suite, probe *schemas.ProbeSpec, taskIDs []string) error {
	ids := append([]string(nil), taskIDs...)
	sort.Strings(ids)
	var errs []error
	for _, id := range ids {
		t := suite.TaskByID(id)
		if t == nil {
			continue
		}
		if !IsVoiceTask(t) {
			if probe != nil && probeSetsVoice(probe) {
				errs = append(errs, fmt.Errorf("probe %s sets a voice probe, but task %s is not a voice call", probe.ID, id))
			}
			continue
		}
		if err := CheckTask(suite, t); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := checkPersonaEnvs(suite, probe, t); err != nil { // caller personas (personas.go)
			errs = append(errs, err)
		}
		spec := Effective(t.Voice)
		p := ProbeOf(probe, spec.Caller)
		if p == "" {
			if probe != nil && probeSetsVoice(probe) {
				errs = append(errs, fmt.Errorf("probe %s sets %s on a service other than task %s's caller %q", probe.ID, EnvProbe, id, spec.Caller))
			}
			continue
		}
		if !knownProbe(p) {
			errs = append(errs, fmt.Errorf("probe %s: voice probe %q is not one of %s", probe.ID, p, strings.Join(Probes, ", ")))
			continue
		}
		if len(spec.Probes) > 0 && !contains(spec.Probes, p) {
			errs = append(errs, fmt.Errorf("probe %s: task %s allows voice probes %s, not %s", probe.ID, id, strings.Join(spec.Probes, ", "), p))
		}
	}
	return errors.Join(errs...)
}

func probeSetsVoice(probe *schemas.ProbeSpec) bool {
	for _, env := range probe.Apply.ServiceEnv {
		if _, ok := env[EnvProbe]; ok {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// CheckGatewayModels refuses an agent config's gateway.models the run's key
// cannot serve: an empty or repeated entry, the arm's own model, a model
// prefixed with another provider (a run key serves one provider), or the
// field combined with a routing policy (W9), which builds its own key body.
func CheckGatewayModels(cfg schemas.AgentConfig) error {
	if len(cfg.Gateway.Models) == 0 {
		return nil
	}
	var errs []error
	if cfg.Gateway.Routing != nil {
		errs = append(errs, errors.New("gateway.models with gateway.routing is not supported: the routing policy names every model its key serves"))
	}
	seen := map[string]bool{}
	for _, m := range cfg.Gateway.Models {
		name := m
		if p, rest, ok := strings.Cut(m, "/"); ok {
			if p != cfg.Gateway.Provider {
				errs = append(errs, fmt.Errorf("gateway.models entry %q is for provider %s, but the run's key serves provider %s only", m, p, cfg.Gateway.Provider))
				continue
			}
			name = rest
		}
		switch {
		case strings.TrimSpace(name) == "":
			errs = append(errs, errors.New("gateway.models has an empty entry"))
		case seen[name]:
			errs = append(errs, fmt.Errorf("gateway.models lists %q twice", m))
		case name == cfg.Model:
			errs = append(errs, fmt.Errorf("gateway.models lists the arm's own model %q", m))
		}
		seen[name] = true
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("agent %s: %w", cfg.ID, err)
	}
	return nil
}
