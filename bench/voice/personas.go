package voice

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Caller personas (docs/voice-realtime.md). A persona is how the simulated
// caller sounds and behaves: its voice and accent (a Piper voice and, for a
// multi-speaker voice, a speaker id), speaking rate, reaction time, how
// often it talks over a long answer, background noise, a narrowband phone
// line and how many times it asks "are you there?" before hanging up. The
// caller reads BENCH_VOICE_PERSONA (a library id, or an inline JSON object)
// from its service env, so a persona is set per task in the caller
// service's env or per condition with apply.service_env.<caller>, exactly
// like a voice probe, and the report card's per-condition rows show the
// agent under each persona.

// EnvPersona is the caller's persona variable.
const EnvPersona = "BENCH_VOICE_PERSONA"

// Persona channels.
const (
	ChannelWideband = "wideband" // 16 kHz as synthesized (default)
	ChannelPhone    = "phone"    // 300 to 3400 Hz, 8 kHz G.711 mu-law round trip
)

// PersonaFile is the persona library the caller image carries next to
// voicecaller.py.
const PersonaFile = "personas.json"

// Persona is one caller persona.
type Persona struct {
	ID               string  `json:"id"`
	Label            string  `json:"label"`
	Accent           string  `json:"accent,omitempty"`
	Voice            string  `json:"voice,omitempty"`   // Piper voice file name without .onnx; empty: the image's default voice
	Speaker          string  `json:"speaker,omitempty"` // speaker id of a multi-speaker voice (speaker_id_map)
	Speed            float64 `json:"speed,omitempty"`   // speaking rate, 1 = as synthesized
	ReplyDelayMS     int     `json:"reply_delay_ms,omitempty"`
	InterruptionRate float64 `json:"interruption_rate,omitempty"`  // chance of talking over each long agent answer
	InterruptAfterMS int     `json:"interrupt_after_ms,omitempty"` // how long the agent talks before the caller cuts in
	NoiseSNRDB       float64 `json:"noise_snr_db,omitempty"`       // 0: no added noise
	Channel          string  `json:"channel,omitempty"`
	Patience         int     `json:"patience,omitempty"`    // reprompts before hanging up on silence
	FillerRate       float64 `json:"filler_rate,omitempty"` // chance a line starts with "um,"
}

var (
	personaOnce sync.Once
	personaLib  []Persona
	personaErr  error
	personaIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)
	voiceNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,80}$`)
)

// Personas returns the persona library, in file order.
func Personas() ([]Persona, error) {
	personaOnce.Do(func() {
		var f struct {
			Personas []Persona `json:"personas"`
		}
		if err := json.Unmarshal(File(PersonaFile), &f); err != nil {
			personaErr = fmt.Errorf("%s: %w", PersonaFile, err)
			return
		}
		seen := map[string]bool{}
		for _, p := range f.Personas {
			if err := CheckPersona(p); err != nil {
				personaErr = fmt.Errorf("%s: %w", PersonaFile, err)
				return
			}
			if seen[p.ID] {
				personaErr = fmt.Errorf("%s: persona %s twice", PersonaFile, p.ID)
				return
			}
			seen[p.ID] = true
		}
		personaLib = f.Personas
	})
	return append([]Persona(nil), personaLib...), personaErr
}

// PersonaByID returns the library persona with that id.
func PersonaByID(id string) (Persona, bool) {
	lib, err := Personas()
	if err != nil {
		return Persona{}, false
	}
	for _, p := range lib {
		if p.ID == id {
			return p, true
		}
	}
	return Persona{}, false
}

// CheckPersona refuses a persona the caller could not play, with the reason.
func CheckPersona(p Persona) error {
	var errs []error
	if !personaIDRe.MatchString(p.ID) {
		errs = append(errs, fmt.Errorf("persona id %q: want lower case letters, digits and -", p.ID))
	}
	if p.Voice != "" && !voiceNameRe.MatchString(p.Voice) {
		errs = append(errs, fmt.Errorf("persona %s: voice %q is not a Piper voice name", p.ID, p.Voice))
	}
	if p.Speaker != "" && (p.Voice == "" || !voiceNameRe.MatchString(p.Speaker)) {
		errs = append(errs, fmt.Errorf("persona %s: speaker %q needs a multi-speaker voice", p.ID, p.Speaker))
	}
	if p.Speed != 0 && (p.Speed < 0.5 || p.Speed > 2) {
		errs = append(errs, fmt.Errorf("persona %s: speed %g: want 0.5 to 2", p.ID, p.Speed))
	}
	if p.ReplyDelayMS < 0 || p.ReplyDelayMS > 10000 {
		errs = append(errs, fmt.Errorf("persona %s: reply_delay_ms %d: want 0 to 10000", p.ID, p.ReplyDelayMS))
	}
	if p.InterruptionRate < 0 || p.InterruptionRate > 1 {
		errs = append(errs, fmt.Errorf("persona %s: interruption_rate %g: want 0 to 1", p.ID, p.InterruptionRate))
	}
	if p.InterruptAfterMS < 0 || p.InterruptAfterMS > 30000 {
		errs = append(errs, fmt.Errorf("persona %s: interrupt_after_ms %d: want 0 to 30000", p.ID, p.InterruptAfterMS))
	}
	if p.NoiseSNRDB < 0 || p.NoiseSNRDB > 60 {
		errs = append(errs, fmt.Errorf("persona %s: noise_snr_db %g: want 0 (none) to 60", p.ID, p.NoiseSNRDB))
	}
	if p.Channel != "" && p.Channel != ChannelWideband && p.Channel != ChannelPhone {
		errs = append(errs, fmt.Errorf("persona %s: channel %q: want %s or %s", p.ID, p.Channel, ChannelWideband, ChannelPhone))
	}
	if p.Patience < 0 || p.Patience > 10 {
		errs = append(errs, fmt.Errorf("persona %s: patience %d: want 0 to 10", p.ID, p.Patience))
	}
	if p.FillerRate < 0 || p.FillerRate > 1 {
		errs = append(errs, fmt.Errorf("persona %s: filler_rate %g: want 0 to 1", p.ID, p.FillerRate))
	}
	return errors.Join(errs...)
}

// ResolvePersona reads a BENCH_VOICE_PERSONA value: a library id, or an
// inline JSON persona object.
func ResolvePersona(v string) (Persona, error) {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "{") {
		var p Persona
		dec := json.NewDecoder(strings.NewReader(v))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&p); err != nil {
			return Persona{}, fmt.Errorf("%s: inline persona: %w", EnvPersona, err)
		}
		if err := CheckPersona(p); err != nil {
			return Persona{}, fmt.Errorf("%s: %w", EnvPersona, err)
		}
		return p, nil
	}
	if p, ok := PersonaByID(v); ok {
		return p, nil
	}
	return Persona{}, fmt.Errorf("%s %q: not a persona in the library (%s) and not an inline JSON persona", EnvPersona, v, strings.Join(PersonaIDs(), ", "))
}

// PersonaIDs lists the library's ids, sorted.
func PersonaIDs() []string {
	lib, _ := Personas()
	out := make([]string, 0, len(lib))
	for _, p := range lib {
		out = append(out, p.ID)
	}
	sort.Strings(out)
	return out
}

// checkPersonaEnvs refuses an unknown persona set on the caller, by the
// caller service's own env or by a condition's service env.
func checkPersonaEnvs(suite *schemas.Suite, probe *schemas.ProbeSpec, t *schemas.Task) error {
	spec := Effective(t.Voice)
	var errs []error
	if s, ok := CallerService(suite, t); ok {
		if v := s.Env[EnvPersona]; v != "" {
			if _, err := ResolvePersona(v); err != nil {
				errs = append(errs, fmt.Errorf("task %s caller service: %w", t.ID, err))
			}
		}
	}
	if probe != nil && probe.Apply.ServiceEnv != nil {
		if v := probe.Apply.ServiceEnv[spec.Caller][EnvPersona]; v != "" {
			if _, err := ResolvePersona(v); err != nil {
				errs = append(errs, fmt.Errorf("probe %s: %w", probe.ID, err))
			}
		}
	}
	return errors.Join(errs...)
}
