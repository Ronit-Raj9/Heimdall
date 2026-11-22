package agentplatform

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// API bases of the voice platforms (lane Y12 uses the same hosts).
const (
	VapiAPI       = "https://api.vapi.ai"
	RetellAPI     = "https://api.retellai.com"
	ElevenLabsAPI = "https://api.elevenlabs.io"
)

// voiceIDRe is the agent id form lane Y12's voice_platform block accepts.
var voiceIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

// maxToolReads caps the extra tool lookups of one import.
const maxToolReads = 20

func voiceBlock(j *job, platform string) map[string]any {
	j.res.Adapter = "voice_platform"
	j.hint("voice")
	j.hint("sessions")
	return map[string]any{"platform": platform, "agent_id": j.id, "api_key_secret": j.secretName(SecretAPIKey)}
}

func checkVoiceID(j *job, label string) error {
	if !voiceIDRe.MatchString(j.id) {
		return errorf("%s id %q: want letters, digits and _ . : - (at most 128)", label, j.id)
	}
	return nil
}

// importVapi reads GET /assistant/{id} and the assistant's saved tools.
func importVapi(ctx context.Context, c *Client, j *job) error {
	if err := checkVoiceID(j, "Vapi assistant"); err != nil {
		return err
	}
	bearer := http.Header{"Authorization": {"Bearer " + j.secret(SecretAPIKey)}}
	src := VapiAPI + "/assistant/" + url.PathEscape(j.id)
	doc, err := c.getJSON(ctx, call{method: http.MethodGet, url: src, header: bearer, what: "the Vapi assistant", secrets: j.secretValues()})
	if err != nil {
		return err
	}
	d := &j.res.Definition
	d.Source = src
	d.Name = str(doc, "name")
	d.FirstMessage = str(doc, "firstMessage")
	d.Model = joinModel(str(doc, "model.provider"), str(doc, "model.model"))
	for _, m := range list(doc, "model.messages") {
		if str(m, "role") == "system" {
			d.Prompt = str(m, "content")
			break
		}
	}
	d.Language = str(doc, "transcriber.language")
	if v := obj(doc, "voice"); v != nil {
		d.Voice = &Voice{Provider: str(v, "provider"), VoiceID: str(v, "voiceId"), Model: str(v, "model"), Settings: pick(v, "stability", "similarityBoost", "speed", "style", "language")}
	}
	for _, t := range list(doc, "model.tools") {
		d.Tools = append(d.Tools, Tool{Name: firstOf(str(t, "function.name"), str(t, "name"), str(t, "type")), Type: str(t, "type"), Description: str(t, "function.description")})
	}
	for i, id := range list(doc, "model.toolIds") {
		sid, _ := id.(string)
		if sid == "" {
			continue
		}
		if i >= maxToolReads {
			j.warn("Only the first %d saved tools were read.", maxToolReads)
			break
		}
		t, err := c.getJSON(ctx, call{method: http.MethodGet, url: VapiAPI + "/tool/" + url.PathEscape(sid), header: bearer, what: "the Vapi tool " + sid, secrets: j.secretValues()})
		if err != nil {
			j.warn("Tool %s could not be read: %v", sid, err)
			d.Tools = append(d.Tools, Tool{Name: sid})
			continue
		}
		d.Tools = append(d.Tools, Tool{Name: firstOf(str(t, "function.name"), str(t, "name"), sid), Type: str(t, "type"), Description: str(t, "function.description")})
	}
	if d.Name == "" && str(doc, "id") == "" {
		return errorf("the Vapi answer for %s has no assistant in it", j.id)
	}
	j.res.Block = voiceBlock(j, Vapi)
	return nil
}

// importRetell reads the chat agent (or the voice agent) and its Retell
// LLM.
func importRetell(ctx context.Context, c *Client, j *job) error {
	if err := checkVoiceID(j, "Retell agent"); err != nil {
		return err
	}
	bearer := http.Header{"Authorization": {"Bearer " + j.secret(SecretAPIKey)}}
	src := RetellAPI + "/get-chat-agent/" + url.PathEscape(j.id)
	doc, err := c.getJSON(ctx, call{method: http.MethodGet, url: src, header: bearer, what: "the Retell chat agent", secrets: j.secretValues()})
	voiceAgent := false
	if isStatus(err, http.StatusNotFound, http.StatusBadRequest, http.StatusUnprocessableEntity) {
		src = RetellAPI + "/get-agent/" + url.PathEscape(j.id)
		doc, err = c.getJSON(ctx, call{method: http.MethodGet, url: src, header: bearer, what: "the Retell agent", secrets: j.secretValues()})
		voiceAgent = true
	}
	if err != nil {
		return err
	}
	d := &j.res.Definition
	d.Source = src
	d.Name = str(doc, "agent_name")
	d.Language = str(doc, "language")
	if vid := str(doc, "voice_id"); vid != "" {
		d.Voice = &Voice{Provider: "retell", VoiceID: vid, Model: str(doc, "voice_model"), Settings: pick(doc, "voice_temperature", "voice_speed", "volume", "responsiveness", "interruption_sensitivity")}
	}
	engine := str(doc, "response_engine.type")
	d.Fields = map[string]string{"response_engine": engine}
	switch engine {
	case "retell-llm":
		llmID := str(doc, "response_engine.llm_id")
		if llmID == "" {
			break
		}
		llm, err := c.getJSON(ctx, call{method: http.MethodGet, url: RetellAPI + "/get-retell-llm/" + url.PathEscape(llmID), header: bearer, what: "the Retell LLM " + llmID, secrets: j.secretValues()})
		if err != nil {
			j.warn("The agent's Retell LLM %s could not be read: %v", llmID, err)
			break
		}
		d.Prompt = str(llm, "general_prompt")
		d.FirstMessage = str(llm, "begin_message")
		d.Model = str(llm, "model")
		for _, t := range list(llm, "general_tools") {
			d.Tools = append(d.Tools, Tool{Name: firstOf(str(t, "name"), str(t, "type")), Type: str(t, "type"), Description: str(t, "description")})
		}
		d.Fields["llm_id"] = llmID
	case "conversation-flow":
		d.Fields["conversation_flow_id"] = str(doc, "response_engine.conversation_flow_id")
		j.warn("This agent runs a conversation flow; its nodes are not imported, only the agent settings.")
	case "custom-llm":
		j.warn("This agent answers through your own LLM websocket; its prompt lives there and is not imported.")
	}
	if voiceAgent {
		j.warn("This is a Retell voice agent. Heimdall's text conversations use Retell's chat API, which needs a chat agent: create a chat agent on the same Retell LLM and import that id for text runs. Audio runs need Retell's web call support.")
	}
	if d.Name == "" && str(doc, "agent_id") == "" {
		return errorf("the Retell answer for %s has no agent in it", j.id)
	}
	j.res.Block = voiceBlock(j, Retell)
	return nil
}

// importElevenLabs reads GET /v1/convai/agents/{id} and its saved tools.
func importElevenLabs(ctx context.Context, c *Client, j *job) error {
	if err := checkVoiceID(j, "ElevenLabs agent"); err != nil {
		return err
	}
	key := http.Header{"xi-api-key": {j.secret(SecretAPIKey)}}
	src := ElevenLabsAPI + "/v1/convai/agents/" + url.PathEscape(j.id)
	doc, err := c.getJSON(ctx, call{method: http.MethodGet, url: src, header: key, what: "the ElevenLabs agent", secrets: j.secretValues()})
	if err != nil {
		return err
	}
	d := &j.res.Definition
	d.Source = src
	d.Name = str(doc, "name")
	a := obj(doc, "conversation_config.agent")
	d.FirstMessage = str(a, "first_message")
	d.Language = str(a, "language")
	d.Prompt = str(a, "prompt.prompt")
	d.Model = str(a, "prompt.llm")
	for _, t := range list(a, "prompt.tools") {
		d.Tools = append(d.Tools, Tool{Name: firstOf(str(t, "name"), str(t, "type")), Type: str(t, "type"), Description: str(t, "description")})
	}
	if bt := obj(a, "prompt.built_in_tools"); bt != nil {
		names := make([]string, 0, len(bt))
		for name, v := range bt {
			if v != nil {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		for _, name := range names {
			d.Tools = append(d.Tools, Tool{Name: name, Type: "system", Description: str(bt[name], "description")})
		}
	}
	for i, id := range list(a, "prompt.tool_ids") {
		sid, _ := id.(string)
		if sid == "" {
			continue
		}
		if i >= maxToolReads {
			j.warn("Only the first %d saved tools were read.", maxToolReads)
			break
		}
		t, err := c.getJSON(ctx, call{method: http.MethodGet, url: ElevenLabsAPI + "/v1/convai/tools/" + url.PathEscape(sid), header: key, what: "the ElevenLabs tool " + sid, secrets: j.secretValues()})
		if err != nil {
			j.warn("Tool %s could not be read: %v", sid, err)
			d.Tools = append(d.Tools, Tool{Name: sid})
			continue
		}
		d.Tools = append(d.Tools, Tool{Name: firstOf(str(t, "tool_config.name"), sid), Type: str(t, "tool_config.type"), Description: str(t, "tool_config.description")})
	}
	if tts := obj(doc, "conversation_config.tts"); tts != nil {
		d.Voice = &Voice{Provider: "elevenlabs", VoiceID: str(tts, "voice_id"), Model: str(tts, "model_id"),
			Settings: pick(tts, "stability", "similarity_boost", "speed", "optimize_streaming_latency", "agent_output_audio_format")}
	}
	if d.Name == "" && str(doc, "agent_id") == "" {
		return errorf("the ElevenLabs answer for %s has no agent in it", j.id)
	}
	j.res.Block = voiceBlock(j, ElevenLabs)
	return nil
}

// pick copies the keys of m that are set.
func pick(m map[string]any, keys ...string) map[string]any {
	out := map[string]any{}
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func firstOf(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func joinModel(provider, model string) string {
	if provider == "" || model == "" || strings.Contains(model, "/") {
		return model
	}
	return provider + "/" + model
}
