package handlers

// Test my agent for a voice platform agent (docs/voice-platforms.md): one
// text turn through the platform's chat API (voiceconnect), the same call a
// text benchmark makes, from this server. A websocket endpoint has no text
// mode: it is tested by a voice benchmark run.

import (
	"context"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/voiceconnect"
)

func (h *agentTestHandler) runVoicePlatform(ctx context.Context, spec *schemas.VoicePlatformAgent, res *EndpointTestResult, step func(string, string, ...any), fail func(string, ...any) *EndpointTestResult, start time.Time) *EndpointTestResult {
	res.Adapter = "voice_platform"
	if err := voiceconnect.Check(spec); err != nil {
		return fail("%v", err)
	}
	name := voiceconnect.PlatformName(spec.Platform)
	if !voiceconnect.SupportsText(spec.Platform) {
		res.URL = redactEndpoint(strings.Replace(strings.Replace(spec.URL, "wss://", "https://", 1), "ws://", "http://", 1))
		return fail("a voice WebSocket endpoint only takes calls, so this test cannot chat with it: run a voice benchmark to check it")
	}
	base := voiceconnect.APIBase(spec)
	res.URL = redactEndpoint(base)
	if err := h.checkURL(base); err != nil {
		return fail("%v", err)
	}
	key := ""
	if spec.APIKeySecret != "" {
		sec, err := h.secrets(ctx, spec.APIKeySecret)
		if err != nil {
			return fail("%v", err)
		}
		key = sec[spec.APIKeySecret]
	}
	timeout := endpointTimeout(spec.TimeoutS)
	c, err := voiceconnect.New(spec, key, voiceconnect.Options{HTTP: h.client(h.env.Services.Hosted, timeout), GreetingWait: 2 * time.Second})
	if err != nil {
		return fail("%v", err)
	}
	session := "heimdall-test-" + randHex(6)
	defer c.End(context.WithoutCancel(ctx), session)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	step("request", "start a %s conversation with agent %s", name, spec.AgentID)
	if g, err := c.Opening(ctx, session); err != nil {
		return fail("%v", err)
	} else if g != "" {
		step("response", "the agent opened with: %s", etClip(g))
	}
	step("request", "user turn: %s", EndpointTestInstruction)
	answer, err := c.Converse(ctx, session, EndpointTestInstruction)
	if err != nil {
		return fail("%v", err)
	}
	res.LatencyMS = time.Since(start).Milliseconds()
	res.OK, res.Answer = true, answer
	step("answer", "%s", etClip(answer))
	res.Matched = strings.Contains(strings.ToUpper(answer), "READY")
	if !res.Matched {
		res.Notes = append(res.Notes, "The agent answered, but not with READY. That is normal for a support agent that sticks to its script: the connection works.")
	}
	res.Notes = append(res.Notes, "This tested the text mode. A voice benchmark calls the agent over audio, which this test does not do.")
	return res
}

func etClip(s string) string {
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}
