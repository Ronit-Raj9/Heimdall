package handlers

// Text benchmarks of a voice platform agent (docs/voice-platforms.md): the
// run's launcher is the pull launcher (adapters/pullagent.py), which drives
// the conversation turn by turn (the simulated customer, the turn cap, the
// trusted transcript) exactly as for a pull agent. Its run token carries the
// agent's voice_platform block (signed at plan time), and this server
// answers each turn itself through the platform's chat API (voiceconnect)
// instead of queueing it for a serving process. The platform's API key
// comes with the turn in the X-Heimdall-Voice-Key header: the engine put
// the org secret in the run's env, and it is never stored.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/pullagent"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/voiceconnect"
	"github.com/maximhq/bifrost/bench/webhooks"
)

// VoiceKeyHeader carries the platform's API key on a voice run's turn.
const VoiceKeyHeader = "X-Heimdall-Voice-Key"

// voiceDriver holds one platform connector per run, so a session's chat id
// (Vapi, Retell) or conversation socket (ElevenLabs) lives across turns.
// A connector is released when its run's token expires.
type voiceDriver struct {
	hosted bool
	now    func() time.Time
	// connect builds a connector; tests swap it.
	connect func(spec *schemas.VoicePlatformAgent, key string, hc *http.Client) (voiceconnect.Client, error)

	mu   sync.Mutex
	runs map[string]*voiceRun
}

type voiceRun struct {
	client   voiceconnect.Client
	exp      time.Time
	sessions map[string]bool
}

func newVoiceDriver(hosted bool) *voiceDriver {
	return &voiceDriver{hosted: hosted, now: time.Now, runs: map[string]*voiceRun{},
		connect: func(spec *schemas.VoicePlatformAgent, key string, hc *http.Client) (voiceconnect.Client, error) {
			return voiceconnect.New(spec, key, voiceconnect.Options{HTTP: hc})
		}}
}

// voiceTurn is what the driver reads of a launcher's turn payload.
type voiceTurn struct {
	Kind        string `json:"kind"`
	SessionID   string `json:"session_id"`
	Instruction string `json:"instruction"`
	Message     string `json:"message"`
}

// VoiceAnswer is the answer of a served turn: text, or infra_error when
// the platform or the network failed (the launcher then exits as an
// unreachable endpoint, never as the agent's answer).
type VoiceAnswer struct {
	Text       string `json:"text,omitempty"`
	InfraError string `json:"infra_error,omitempty"`
}

// run returns the run's connector, building it on its first turn.
func (d *voiceDriver) run(c pullagent.Claims, key string) (*voiceRun, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	for id, r := range d.runs {
		if now.After(r.exp) {
			delete(d.runs, id)
			go endVoiceRun(r)
		}
	}
	id := c.Org + "\x00" + c.Run
	if r := d.runs[id]; r != nil {
		return r, nil
	}
	spec := c.Voice
	if err := voiceconnect.Check(spec); err != nil {
		return nil, err
	}
	if !voiceconnect.SupportsText(spec.Platform) {
		return nil, fmt.Errorf("a %s agent has no text mode: run a voice benchmark", voiceconnect.PlatformName(spec.Platform))
	}
	if err := webhooks.ValidateURL(voiceconnect.APIBase(spec), !d.hosted); err != nil {
		return nil, fmt.Errorf("the platform's API base: %v", err)
	}
	timeout := time.Duration(spec.TimeoutS) * time.Second
	if timeout <= 0 {
		timeout = voiceconnect.DefaultTimeoutS * time.Second
	}
	cl, err := d.connect(spec, key, endpointTestClient(d.hosted, timeout))
	if err != nil {
		return nil, err
	}
	r := &voiceRun{client: cl, exp: time.Unix(c.Exp, 0), sessions: map[string]bool{}}
	d.runs[id] = r
	return r, nil
}

func endVoiceRun(r *voiceRun) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for s := range r.sessions {
		_ = r.client.End(ctx, s)
	}
}

// serve answers one turn of run c.
func (d *voiceDriver) serve(c pullagent.Claims, key string) func(context.Context, json.RawMessage) (json.RawMessage, error) {
	return func(ctx context.Context, payload json.RawMessage) (json.RawMessage, error) {
		var t voiceTurn
		if err := json.Unmarshal(payload, &t); err != nil {
			return nil, errors.New("the turn is not a JSON object")
		}
		var user string
		switch t.Kind {
		case "conversation":
			user = t.Message
		case "task", "":
			user = t.Instruction
		default:
			return nil, fmt.Errorf("a voice platform agent has no bench tools, so it cannot answer a %s turn", t.Kind)
		}
		if strings.TrimSpace(user) == "" {
			return nil, errors.New("the turn has no user message")
		}
		r, err := d.run(c, key)
		if err != nil {
			return nil, err
		}
		session := "heimdall-" + c.Run
		if t.SessionID != "" && t.SessionID != c.Run {
			session += "-" + t.SessionID
		}
		d.mu.Lock()
		first := !r.sessions[session]
		r.sessions[session] = true
		d.mu.Unlock()
		if first {
			// The opening line (ElevenLabs) is the platform's, not a reply
			// to the user; the simulated customer already got the bench's
			// greeting, so it is only read to start the session.
			if _, err := r.client.Opening(ctx, session); err != nil {
				return voiceFailure(err)
			}
		}
		text, err := r.client.Converse(ctx, session, user)
		if err != nil {
			return voiceFailure(err)
		}
		return json.Marshal(VoiceAnswer{Text: text})
	}
}

// voiceFailure maps a platform failure: infrastructure (5xx, 429, network,
// timeout) is answered as infra_error, anything else fails the turn.
func voiceFailure(err error) (json.RawMessage, error) {
	if voiceconnect.IsInfra(err) {
		return json.Marshal(VoiceAnswer{InfraError: err.Error()})
	}
	return nil, err
}
