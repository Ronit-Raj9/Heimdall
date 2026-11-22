// Package voiceconnect talks to voice agents hosted on a voice platform
// (Vapi, Retell, ElevenLabs Agents) in text mode, one user turn at a time,
// so a bench-driven conversation (a tau-style or support task's simulated
// customer) can run against the same agent a team ships on the phone
// (docs/voice-platforms.md). The audio side is bench/voice/platformbridge.py.
//
// The driver holds a Conversation and calls Converse once per user turn with
// the run's session id; the connector keeps the platform's own conversation
// state (a Vapi chat chain, a Retell chat, an ElevenLabs conversation
// socket) per session id and returns the agent's reply. End releases the
// session. Errors say whether the platform failed (Error.Infra: a 5xx, a
// 429, a broken connection, so the run is infrastructure, never the agent's
// failure) or refused the request (a 4xx: wrong key, unknown agent).
package voiceconnect

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Default API bases.
const (
	VapiBase       = "https://api.vapi.ai"
	RetellBase     = "https://api.retellai.com"
	ElevenLabsBase = "https://api.elevenlabs.io"
)

// DefaultTimeoutS bounds one turn when the spec sets no timeout_s.
const DefaultTimeoutS = 60

var (
	agentIDRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
	secretNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// Check refuses a voice_platform block that cannot run, with the reason.
func Check(s *schemas.VoicePlatformAgent) error {
	if s == nil {
		return fmt.Errorf("a voice_platform agent needs a voice_platform block with platform")
	}
	if !slices.Contains(schemas.VoicePlatforms, s.Platform) {
		return fmt.Errorf("voice_platform.platform %q: want one of %s", s.Platform, strings.Join(schemas.VoicePlatforms, ", "))
	}
	if s.APIKeySecret != "" && !secretNameRe.MatchString(s.APIKeySecret) {
		return fmt.Errorf("voice_platform.api_key_secret %q is not a Settings > Secrets name (letters, digits and _)", s.APIKeySecret)
	}
	if s.TimeoutS < 0 || s.TimeoutS > 3600 {
		return fmt.Errorf("voice_platform.timeout_s %d: want 0 to 3600", s.TimeoutS)
	}
	switch s.Platform {
	case schemas.VoicePlatformWebSocket:
		if s.AgentID != "" {
			return fmt.Errorf("voice_platform.agent_id is not used by a websocket endpoint; leave it empty")
		}
		if s.URL == "" {
			return fmt.Errorf("a websocket voice endpoint needs voice_platform.url (wss://...)")
		}
		if _, err := wsURL(s.URL); err != nil {
			return err
		}
		if s.Framing != "" && !slices.Contains(schemas.VoiceFramings, s.Framing) {
			return fmt.Errorf("voice_platform.framing %q: want one of %s", s.Framing, strings.Join(schemas.VoiceFramings, ", "))
		}
		return nil
	}
	if s.Framing != "" {
		return fmt.Errorf("voice_platform.framing is only for a websocket endpoint; %s uses its own", s.Platform)
	}
	if !agentIDRe.MatchString(s.AgentID) {
		return fmt.Errorf("voice_platform.agent_id %q: want the platform's agent id (letters, digits, _ . : -)", s.AgentID)
	}
	if s.URL != "" {
		if _, err := httpBase(s.URL); err != nil {
			return err
		}
	}
	if s.Platform != schemas.VoicePlatformElevenLabs && s.APIKeySecret == "" {
		return fmt.Errorf("%s needs voice_platform.api_key_secret: the name of the secret in Settings > Secrets holding your API key", PlatformName(s.Platform))
	}
	return nil
}

// PlatformName is the platform's display name.
func PlatformName(p string) string {
	switch p {
	case schemas.VoicePlatformVapi:
		return "Vapi"
	case schemas.VoicePlatformRetell:
		return "Retell"
	case schemas.VoicePlatformElevenLabs:
		return "ElevenLabs"
	case schemas.VoicePlatformWebSocket:
		return "Voice WebSocket"
	}
	return p
}

// SupportsText reports whether the platform has a text mode (every one
// except a bare websocket endpoint).
func SupportsText(p string) bool { return p != schemas.VoicePlatformWebSocket && p != "" }

// SupportsAudio reports whether the platform has an audio mode the bench
// can dial. Retell's calls go over its own WebRTC and telephony stacks, not
// a WebSocket the bench can open, so Retell is text only.
func SupportsAudio(p string) bool { return p != schemas.VoicePlatformRetell && p != "" }

// APIBase is the http(s) API base of the spec, without a trailing slash.
func APIBase(s *schemas.VoicePlatformAgent) string {
	if s.URL != "" && s.Platform != schemas.VoicePlatformWebSocket {
		b, _ := httpBase(s.URL)
		return b
	}
	switch s.Platform {
	case schemas.VoicePlatformVapi:
		return VapiBase
	case schemas.VoicePlatformRetell:
		return RetellBase
	case schemas.VoicePlatformElevenLabs:
		return ElevenLabsBase
	}
	return ""
}

// AudioTarget is the one host:port an audio call reaches (the run's egress
// grant): the websocket endpoint's, or the platform API's, whose host also
// serves its call WebSocket (wss://api.vapi.ai/<call>/transport,
// wss://api.elevenlabs.io/v1/convai/conversation).
func AudioTarget(s *schemas.VoicePlatformAgent) (string, error) {
	raw := APIBase(s)
	if s.Platform == schemas.VoicePlatformWebSocket {
		u, err := wsURL(s.URL)
		if err != nil {
			return "", err
		}
		raw = u
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("voice_platform: no host in %q", raw)
	}
	port := u.Port()
	if port == "" {
		port = "443"
		if u.Scheme == "http" || u.Scheme == "ws" {
			port = "80"
		}
	}
	return u.Hostname() + ":" + port, nil
}

func httpBase(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" {
		return "", fmt.Errorf("voice_platform.url %q: want https://host[:port][/path] with no user info or query", raw)
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func wsURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "wss" && u.Scheme != "ws") || u.Hostname() == "" || u.User != nil {
		return "", fmt.Errorf("voice_platform.url %q: want wss://host[:port]/path with no user info", raw)
	}
	return u.String(), nil
}
