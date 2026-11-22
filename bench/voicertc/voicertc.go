// Package voicertc is the Go half of Heimdall's real-time voice harness
// (docs/voice-realtime.md): voice agents reached over WebRTC in a LiveKit
// room (LiveKit Agents, Pipecat with its LiveKit transport, any bot that
// joins rooms) or over a phone line through LiveKit SIP (any agent with a
// number: Vapi, Retell, a contact centre).
//
// The bench never runs the agent. The voice_rtc adapter's run container is
// the bench's bridge (bench/voice/rtcbridge.py): it dials the task's voice
// caller and the operator's real-time voice gateway (bench/voice/
// rtcgateway.py, compose profile voice, next to a LiveKit server and
// LiveKit SIP). The gateway joins a room as the caller, brings the agent in
// and carries audio both ways, so the caller's timeline measures the call
// exactly as it does for an in-container agent.
//
// This package checks a voice_rtc block, builds the bridge's dial message
// (credentials stay {{secret.NAME}} references the engine expands into
// their own env variables), mints the per-run gateway token the gateway
// verifies, and holds the SIP number allowlist rule the gateway enforces.
package voicertc

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Operator environment of the server and workers (.env.example).
const (
	// EnvGatewayURL is the gateway as runs reach it: wss://host[:port]/dial
	// (or ws:// on a private network). Empty: real-time calls are off.
	EnvGatewayURL = "HEIMDALL_VOICE_RTC_URL"
	// EnvGatewaySecret signs per-run gateway tokens; the gateway holds the
	// same value.
	EnvGatewaySecret = "HEIMDALL_VOICE_RTC_SECRET"
	// EnvSIPAllow lists the E.164 prefixes (comma separated) the gateway
	// may dial. Empty: phone calls are off.
	EnvSIPAllow = "HEIMDALL_VOICE_SIP_ALLOW"
)

// Bridge environment (rtcbridge.py).
const (
	EnvBridgeGatewayURL   = "BENCH_RTC_GATEWAY_URL"
	EnvBridgeGatewayToken = "BENCH_RTC_GATEWAY_TOKEN"
	EnvBridgeDial         = "BENCH_RTC_DIAL"
	EnvBridgeLKKey        = "BENCH_RTC_LK_API_KEY"
	EnvBridgeLKSecret     = "BENCH_RTC_LK_API_SECRET"
	EnvBridgeStartToken   = "BENCH_RTC_START_TOKEN"
	EnvBridgeTrunkUser    = "BENCH_RTC_TRUNK_USER"
	EnvBridgeTrunkPass    = "BENCH_RTC_TRUNK_PASSWORD"
)

// Protocol is the bridge-to-gateway protocol.
const Protocol = "heimdall-rtc/1"

// DefaultTimeoutS bounds the call setup when the block sets none.
const DefaultTimeoutS = 30

// tokenPrefix versions the gateway token format.
const tokenPrefix = "hrtc1"

var (
	e164Re       = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)
	secretNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	agentNameRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	roomPrefixRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	trunkIDRe    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	hostRe       = regexp.MustCompile(`^[A-Za-z0-9.-]{1,253}(:[0-9]{1,5})?$`)
)

// lookupEnv reads the operator's environment; tests replace it.
var lookupEnv = os.Getenv

// Check refuses a voice_rtc block that cannot run, with the reason.
func Check(s *schemas.VoiceRTCAgent) error {
	if s == nil {
		return errors.New("a voice_rtc agent needs a voice_rtc block with mode livekit or sip")
	}
	var errs []error
	if s.TimeoutS < 0 || s.TimeoutS > 300 {
		errs = append(errs, fmt.Errorf("voice_rtc.timeout_s %d: want 0 to 300", s.TimeoutS))
	}
	switch s.Mode {
	case schemas.VoiceRTCModeLiveKit:
		if s.SIP != nil {
			errs = append(errs, errors.New("voice_rtc.sip is only for mode sip"))
		}
		if s.LiveKit == nil {
			errs = append(errs, errors.New("mode livekit needs a voice_rtc.livekit block (dispatch and agent_name, or start_url)"))
		} else {
			errs = append(errs, checkLiveKit("voice_rtc.livekit", s.LiveKit, true)...)
		}
	case schemas.VoiceRTCModeSIP:
		if s.LiveKit != nil {
			errs = append(errs, errors.New("mode sip takes its LiveKit server under voice_rtc.sip.livekit, not voice_rtc.livekit"))
		}
		if s.SIP == nil {
			errs = append(errs, errors.New("mode sip needs a voice_rtc.sip block with number"))
		} else {
			errs = append(errs, checkSIP(s.SIP)...)
		}
	default:
		errs = append(errs, fmt.Errorf("voice_rtc.mode %q: want one of %s", s.Mode, strings.Join(schemas.VoiceRTCModes, ", ")))
	}
	return errors.Join(errs...)
}

func checkLiveKit(field string, l *schemas.VoiceRTCLiveKit, needAgent bool) []error {
	var errs []error
	if l.URL != "" {
		u, err := url.Parse(l.URL)
		if err != nil || (u.Scheme != "wss" && u.Scheme != "ws") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" {
			errs = append(errs, fmt.Errorf("%s.url %q: want wss://host[:port] (LiveKit Cloud or your own server) with no user info or query", field, l.URL))
		}
		if l.APIKeySecret == "" || l.APISecretSecret == "" {
			errs = append(errs, fmt.Errorf("%s.url needs api_key_secret and api_secret_secret: the names of the secrets in Settings > Secrets holding that server's API key and secret", field))
		}
	} else if l.APIKeySecret != "" || l.APISecretSecret != "" {
		errs = append(errs, fmt.Errorf("%s: api_key_secret and api_secret_secret are only for your own server (url); Heimdall's server uses its own keys", field))
	}
	for name, v := range map[string]string{"api_key_secret": l.APIKeySecret, "api_secret_secret": l.APISecretSecret, "start_token_secret": l.StartTokenSecret} {
		if v != "" && !secretNameRe.MatchString(v) {
			errs = append(errs, fmt.Errorf("%s.%s %q is not a Settings > Secrets name (letters, digits and _)", field, name, v))
		}
	}
	if l.RoomPrefix != "" && !roomPrefixRe.MatchString(l.RoomPrefix) {
		errs = append(errs, fmt.Errorf("%s.room_prefix %q: want lower case letters, digits and -, at most 32", field, l.RoomPrefix))
	}
	if !needAgent {
		if l.Dispatch != "" || l.AgentName != "" || l.StartURL != "" || l.StartTokenSecret != "" {
			errs = append(errs, fmt.Errorf("%s: dispatch, agent_name and start_url are for mode livekit; a phone call brings the agent in by dialling", field))
		}
		return errs
	}
	switch Dispatch(l) {
	case schemas.VoiceDispatchAgent:
		if !agentNameRe.MatchString(l.AgentName) {
			errs = append(errs, fmt.Errorf("%s.agent_name %q: dispatch agent needs the LiveKit Agents worker's agent_name (WorkerOptions(agent_name=...))", field, l.AgentName))
		}
	case schemas.VoiceDispatchAuto:
		if l.AgentName != "" || l.StartURL != "" {
			errs = append(errs, fmt.Errorf("%s: dispatch auto takes no agent_name or start_url (the worker joins every new room)", field))
		}
	case schemas.VoiceDispatchWebhook:
		u, err := url.Parse(l.StartURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
			errs = append(errs, fmt.Errorf("%s.start_url %q: dispatch webhook needs https://host/path, which receives the room and a join token", field, l.StartURL))
		}
	default:
		errs = append(errs, fmt.Errorf("%s.dispatch %q: want one of %s", field, l.Dispatch, strings.Join(schemas.VoiceDispatches, ", ")))
	}
	if Dispatch(l) != schemas.VoiceDispatchWebhook && (l.StartURL != "" || l.StartTokenSecret != "") {
		errs = append(errs, fmt.Errorf("%s: start_url and start_token_secret are only for dispatch webhook", field))
	}
	return errs
}

// Dispatch is the block's dispatch with its default: agent when an
// agent_name is set, webhook when a start_url is, else agent.
func Dispatch(l *schemas.VoiceRTCLiveKit) string {
	switch {
	case l == nil:
		return ""
	case l.Dispatch != "":
		return l.Dispatch
	case l.StartURL != "":
		return schemas.VoiceDispatchWebhook
	default:
		return schemas.VoiceDispatchAgent
	}
}

func checkSIP(s *schemas.VoiceRTCSIP) []error {
	var errs []error
	if !e164Re.MatchString(s.Number) {
		errs = append(errs, fmt.Errorf("voice_rtc.sip.number %q: want an E.164 number (+14155550100)", s.Number))
	}
	if s.FromNumber != "" && !e164Re.MatchString(s.FromNumber) {
		errs = append(errs, fmt.Errorf("voice_rtc.sip.from_number %q: want an E.164 number", s.FromNumber))
	}
	if s.Transport != "" && !slices.Contains([]string{"udp", "tcp", "tls"}, s.Transport) {
		errs = append(errs, fmt.Errorf("voice_rtc.sip.transport %q: want udp, tcp or tls", s.Transport))
	}
	switch {
	case s.TrunkID != "":
		if !trunkIDRe.MatchString(s.TrunkID) {
			errs = append(errs, fmt.Errorf("voice_rtc.sip.trunk_id %q: want the trunk id LiveKit gave you (ST_...)", s.TrunkID))
		}
		if s.TrunkAddress != "" || s.TrunkUserSecret != "" || s.TrunkPasswordSecret != "" {
			errs = append(errs, errors.New("voice_rtc.sip: trunk_id uses the trunk already on the LiveKit server; leave trunk_address and the trunk secrets empty"))
		}
		if s.LiveKit == nil || s.LiveKit.URL == "" {
			errs = append(errs, errors.New("voice_rtc.sip.trunk_id names a trunk on your own LiveKit server: set sip.livekit.url and its key secrets"))
		}
	default:
		if !hostRe.MatchString(s.TrunkAddress) {
			errs = append(errs, fmt.Errorf("voice_rtc.sip.trunk_address %q: want your SIP provider's host (example.pstn.twilio.com), or set trunk_id", s.TrunkAddress))
		}
		if s.FromNumber == "" {
			errs = append(errs, errors.New("voice_rtc.sip.from_number: a temporary trunk needs the caller id number your SIP provider lets you call from"))
		}
		if (s.TrunkUserSecret == "") != (s.TrunkPasswordSecret == "") {
			errs = append(errs, errors.New("voice_rtc.sip: trunk_user_secret and trunk_password_secret go together"))
		}
	}
	for name, v := range map[string]string{"trunk_user_secret": s.TrunkUserSecret, "trunk_password_secret": s.TrunkPasswordSecret} {
		if v != "" && !secretNameRe.MatchString(v) {
			errs = append(errs, fmt.Errorf("voice_rtc.sip.%s %q is not a Settings > Secrets name", name, v))
		}
	}
	if s.LiveKit != nil {
		errs = append(errs, checkLiveKit("voice_rtc.sip.livekit", s.LiveKit, false)...)
	}
	return errs
}

// SIPAllowed reports whether number may be dialled under the allowlist
// allow (comma separated E.164 prefixes, as in HEIMDALL_VOICE_SIP_ALLOW). An
// empty allowlist allows nothing: phone calls cost money and reach real
// people, so the operator names the numbers the orgs own.
func SIPAllowed(number, allow string) bool {
	if !e164Re.MatchString(number) {
		return false
	}
	for _, p := range strings.Split(allow, ",") {
		p = strings.TrimSpace(p)
		if p == "" || !strings.HasPrefix(p, "+") {
			continue
		}
		if strings.HasPrefix(number, p) {
			return true
		}
	}
	return false
}

// Gateway is the operator's real-time gateway as runs see it.
type Gateway struct {
	URL    string
	Secret string
}

// GatewayFromEnv reads the operator's gateway, or says why real-time calls
// are off.
func GatewayFromEnv() (Gateway, error) {
	g := Gateway{URL: strings.TrimSpace(lookupEnv(EnvGatewayURL)), Secret: lookupEnv(EnvGatewaySecret)}
	if g.URL == "" {
		return g, fmt.Errorf("real-time voice calls are not set up on this Heimdall: the operator starts the voice profile (docker compose --profile voice) and sets %s and %s (docs/voice-realtime.md)", EnvGatewayURL, EnvGatewaySecret)
	}
	u, err := url.Parse(g.URL)
	if err != nil || (u.Scheme != "wss" && u.Scheme != "ws") || u.Hostname() == "" {
		return g, fmt.Errorf("%s %q: want wss://host[:port]/dial", EnvGatewayURL, g.URL)
	}
	if len(g.Secret) < 16 {
		return g, fmt.Errorf("%s is shorter than 16 characters: set a long random value on the server and the gateway", EnvGatewaySecret)
	}
	return g, nil
}

// Target is the gateway's host:port, the run's one egress grant.
func (g Gateway) Target() (string, error) {
	u, err := url.Parse(g.URL)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("%s: no host in %q", EnvGatewayURL, g.URL)
	}
	port := u.Port()
	if port == "" {
		port = "443"
		if u.Scheme == "ws" {
			port = "80"
		}
	}
	return u.Hostname() + ":" + port, nil
}

// RunToken is the gateway token of one run: valid until exp and bound to
// the run id, so a token copied out of a run folder is worth one call of
// that run at most (the gateway also takes each run id once at a time).
func RunToken(secret, runID string, exp time.Time) string {
	body := tokenPrefix + "." + runID + "." + strconv.FormatInt(exp.Unix(), 10)
	return body + "." + sign(secret, body)
}

// VerifyToken checks a run token at time now and returns its run id. The
// gateway (rtcgateway.py) implements the same rule.
func VerifyToken(secret, token string, now time.Time) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 4 || parts[0] != tokenPrefix || parts[1] == "" {
		return "", errors.New("not a run token")
	}
	body := strings.Join(parts[:3], ".")
	if !hmac.Equal([]byte(sign(secret, body)), []byte(parts[3])) {
		return "", errors.New("bad signature")
	}
	exp, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || now.Unix() > exp {
		return "", errors.New("expired")
	}
	return parts[1], nil
}

func sign(secret, body string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(body))
	return hex.EncodeToString(m.Sum(nil))
}

// Dial is the dial message without credentials (rtcbridge.py adds them
// from its env).
type Dial struct {
	Mode     string      `json:"mode"`
	Run      string      `json:"run"`
	Session  string      `json:"session"`
	Task     string      `json:"task,omitempty"`
	SetupS   int         `json:"setup_s"`
	MaxCallS int         `json:"max_call_s"`
	LiveKit  dialLiveKit `json:"livekit"`
	SIP      *dialSIP    `json:"sip,omitempty"`
}

type dialLiveKit struct {
	URL        string `json:"url,omitempty"` // empty: the gateway's own server
	Dispatch   string `json:"dispatch,omitempty"`
	AgentName  string `json:"agent_name,omitempty"`
	StartURL   string `json:"start_url,omitempty"`
	RoomPrefix string `json:"room_prefix"`
}

type dialSIP struct {
	Number       string `json:"number"`
	TrunkID      string `json:"trunk_id,omitempty"`
	TrunkAddress string `json:"trunk_address,omitempty"`
	FromNumber   string `json:"from_number,omitempty"`
	Transport    string `json:"transport,omitempty"`
}

// SetupS is the block's setup timeout with its default.
func SetupS(s *schemas.VoiceRTCAgent) int {
	if s == nil || s.TimeoutS <= 0 {
		return DefaultTimeoutS
	}
	return s.TimeoutS
}

// Server is the LiveKit block that places the call: voice_rtc.livekit, or
// voice_rtc.sip.livekit for a phone call (nil: Heimdall's own server).
func Server(s *schemas.VoiceRTCAgent) *schemas.VoiceRTCLiveKit {
	if s == nil {
		return nil
	}
	if s.Mode == schemas.VoiceRTCModeSIP {
		if s.SIP == nil {
			return nil
		}
		return s.SIP.LiveKit
	}
	return s.LiveKit
}

// BuildDial is the dial message of one run.
func BuildDial(s *schemas.VoiceRTCAgent, runID, session, task string, maxCallS int) Dial {
	d := Dial{Mode: s.Mode, Run: runID, Session: session, Task: task, SetupS: SetupS(s), MaxCallS: maxCallS}
	d.LiveKit.RoomPrefix = "heimdall"
	if l := Server(s); l != nil {
		d.LiveKit.URL = l.URL
		if l.RoomPrefix != "" {
			d.LiveKit.RoomPrefix = l.RoomPrefix
		}
	}
	if s.Mode == schemas.VoiceRTCModeLiveKit && s.LiveKit != nil {
		d.LiveKit.Dispatch = Dispatch(s.LiveKit)
		d.LiveKit.AgentName = s.LiveKit.AgentName
		d.LiveKit.StartURL = s.LiveKit.StartURL
	}
	if s.Mode == schemas.VoiceRTCModeSIP && s.SIP != nil {
		d.SIP = &dialSIP{Number: s.SIP.Number, TrunkID: s.SIP.TrunkID, TrunkAddress: s.SIP.TrunkAddress,
			FromNumber: s.SIP.FromNumber, Transport: s.SIP.Transport}
	}
	return d
}

// JSON is the dial message as the bridge's BENCH_RTC_DIAL.
func (d Dial) JSON() string {
	b, _ := json.Marshal(d)
	return string(b)
}

// SecretEnv maps the bridge's credential variables to {{secret.NAME}}
// references of the block's secrets.
func SecretEnv(s *schemas.VoiceRTCAgent) map[string]string {
	out := map[string]string{}
	ref := func(env, name string) {
		if name != "" {
			out[env] = "{{secret." + name + "}}"
		}
	}
	if l := Server(s); l != nil {
		ref(EnvBridgeLKKey, l.APIKeySecret)
		ref(EnvBridgeLKSecret, l.APISecretSecret)
		ref(EnvBridgeStartToken, l.StartTokenSecret)
	}
	if s.Mode == schemas.VoiceRTCModeSIP && s.SIP != nil {
		ref(EnvBridgeTrunkUser, s.SIP.TrunkUserSecret)
		ref(EnvBridgeTrunkPass, s.SIP.TrunkPasswordSecret)
	}
	return out
}

// SecretNames lists the org secrets a voice_rtc block names, sorted.
func SecretNames(s *schemas.VoiceRTCAgent) []string {
	if s == nil {
		return nil
	}
	var out []string
	for _, v := range SecretEnv(s) {
		out = append(out, strings.TrimSuffix(strings.TrimPrefix(v, "{{secret."), "}}"))
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// CheckRun is the plan-time check of one real-time call against the
// operator's setup: the gateway is configured, and a phone number is on
// the allowlist.
func CheckRun(s *schemas.VoiceRTCAgent) (Gateway, error) {
	g, err := GatewayFromEnv()
	if err != nil {
		return g, err
	}
	if s.Mode == schemas.VoiceRTCModeSIP && s.SIP != nil && !SIPAllowed(s.SIP.Number, lookupEnv(EnvSIPAllow)) {
		return g, fmt.Errorf("phone number %s is not on this Heimdall's allowlist (%s): the operator adds the numbers your org owns before Heimdall calls them", s.SIP.Number, EnvSIPAllow)
	}
	return g, nil
}
