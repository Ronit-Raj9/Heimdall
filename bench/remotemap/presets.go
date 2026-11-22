package remotemap

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Platform presets of "Connect your HTTP endpoint": each fills the request
// template, answer path, stream, session steps and suggested auth of one
// agent platform from a few inputs (an assistant id, a region). The form
// shows the result and every field stays editable.

// PresetInput is one value a preset asks for.
type PresetInput struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder,omitempty"`
	Help        string `json:"help,omitempty"`
	Default     string `json:"default,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// Preset is one platform.
type Preset struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Help  string `json:"help"`
	// URL is the agent URL template ({{input.X}} filled from Inputs); empty
	// asks the user for the URL, with URLPlaceholder as the example.
	URL            string                `json:"url,omitempty"`
	URLPlaceholder string                `json:"url_placeholder,omitempty"`
	Inputs         []PresetInput         `json:"inputs,omitempty"`
	Mapping        schemas.RemoteMapping `json:"mapping"`
	// Auth is the suggested auth: its type and fixed fields; the secret
	// names come from the form.
	Auth *schemas.RemoteAuth `json:"auth,omitempty"`
	// AuthHeader is the suggested header of a static token.
	AuthHeader string `json:"auth_header,omitempty"`
	Docs       string `json:"docs,omitempty"`
}

// PresetCustom is the preset id of a hand-written mapping.
const PresetCustom = "custom"

const (
	agentforceAPI = "https://api.salesforce.com/einstein/ai-agent/v1"
	directLine    = "https://directline.botframework.com/v3/directline/conversations"
)

// Presets are the platform presets, in the form's order.
var Presets = []Preset{
	{
		ID: "langgraph", Label: "LangGraph Platform",
		Help:           "A LangGraph Server or LangGraph Platform deployment. Heimdall calls /runs/wait (a stateless run) and reads the last message. Send your LangSmith key as x-api-key.",
		URLPlaceholder: "https://my-deployment.us.langgraph.app/runs/wait",
		Inputs: []PresetInput{{Name: "assistant_id", Label: "Assistant or graph id", Default: "agent", Required: true,
			Help: "The graph id from langgraph.json, or an assistant id."}},
		Mapping: schemas.RemoteMapping{
			Body:       `{"assistant_id": "{{input.assistant_id}}", "input": {"messages": [{"role": "user", "content": "{{instruction}}"}]}, "metadata": "{{metadata}}"}`,
			AnswerPath: "messages[-1].content",
		},
		AuthHeader: "x-api-key",
		Docs:       "https://langchain-ai.github.io/langgraph/cloud/reference/api/api_ref/",
	},
	{
		ID: "adk", Label: "Google ADK",
		Help:           "An Agent Development Kit API server (adk api_server, Cloud Run or GKE). Heimdall creates the session, then calls /run and reads the last event's text. On Cloud Run use a Google ID token.",
		URLPlaceholder: "https://my-agent-abc123-uc.a.run.app/run",
		Inputs: []PresetInput{
			{Name: "app_name", Label: "App name", Placeholder: "support_agent", Required: true, Help: "The agent folder name the server lists at /list-apps."},
			{Name: "user_id", Label: "User id", Default: "heimdall", Required: true},
		},
		Mapping: schemas.RemoteMapping{
			Body:       `{"app_name": "{{input.app_name}}", "user_id": "{{input.user_id}}", "session_id": "{{session_id}}", "new_message": {"role": "user", "parts": [{"text": "{{instruction}}"}]}}`,
			AnswerPath: "[-1].content.parts[*].text",
			Start:      &schemas.RemoteStep{Method: "POST", URL: "/apps/{{input.app_name}}/users/{{input.user_id}}/sessions/{{session_id}}", Body: `{}`},
		},
		Auth: &schemas.RemoteAuth{Type: schemas.RemoteAuthGCPIDToken},
		Docs: "https://google.github.io/adk-docs/runtime/api-server/",
	},
	{
		ID: "mastra", Label: "Mastra",
		Help:           "A Mastra server. Heimdall calls the agent's generate route with the session as the memory thread and reads text.",
		URLPlaceholder: "https://my-mastra.example.com/api/agents/supportAgent/generate",
		Mapping: schemas.RemoteMapping{
			Body:       `{"messages": [{"role": "user", "content": "{{instruction}}"}], "threadId": "{{session_id}}", "resourceId": "heimdall"}`,
			AnswerPath: "text",
		},
		Docs: "https://mastra.ai/reference/client-js/agents",
	},
	{
		ID: "n8n", Label: "n8n webhook",
		Help:           "An n8n workflow that starts with a Webhook node and ends with Respond to Webhook (or an AI Agent node set to respond). Heimdall sends chatInput and sessionId and reads output.",
		URLPlaceholder: "https://my-n8n.example.com/webhook/0b5e1c2a-support",
		Mapping: schemas.RemoteMapping{
			Body:       `{"chatInput": "{{instruction}}", "sessionId": "{{session_id}}", "metadata": "{{metadata}}"}`,
			AnswerPath: "output",
		},
		AuthHeader: "Authorization",
		Docs:       "https://docs.n8n.io/integrations/builtin/core-nodes/n8n-nodes-base.webhook/",
	},
	{
		ID: "openai_responses", Label: "OpenAI Responses API",
		Help:           "An endpoint that speaks the OpenAI Responses API (OpenAI, Azure OpenAI or your own server). Heimdall streams the answer and joins the text deltas.",
		URLPlaceholder: "https://api.openai.com/v1/responses",
		Inputs:         []PresetInput{{Name: "model", Label: "Model or agent name", Placeholder: "gpt-5-mini", Required: true}},
		Mapping: schemas.RemoteMapping{
			Body:       `{"model": "{{input.model}}", "input": "{{instruction}}", "stream": true, "store": false}`,
			Stream:     schemas.RemoteStreamSSE,
			StreamPath: "[?(@.type=='response.output_text.delta')].delta",
		},
		Docs: "https://platform.openai.com/docs/api-reference/responses/create",
	},
	{
		ID: "bedrock_agent", Label: "AWS Bedrock Agents",
		Help: "An Amazon Bedrock agent alias, called with InvokeAgent and signed with SigV4. Heimdall reads the chunk events of the answer stream. The IAM user needs bedrock:InvokeAgent on the alias.",
		URL:  "https://bedrock-agent-runtime.{{input.region}}.amazonaws.com/agents/{{input.agent_id}}/agentAliases/{{input.alias_id}}/sessions/{{session_id}}/text",
		Inputs: []PresetInput{
			{Name: "region", Label: "Region", Placeholder: "us-east-1", Required: true},
			{Name: "agent_id", Label: "Agent id", Placeholder: "ABCDEFGHIJ", Required: true},
			{Name: "alias_id", Label: "Alias id", Placeholder: "TSTALIASID", Required: true},
		},
		Mapping: schemas.RemoteMapping{
			Body:         `{"inputText": "{{instruction}}", "enableTrace": false}`,
			Stream:       schemas.RemoteStreamAWSEventStream,
			StreamPath:   "bytes",
			StreamDecode: "base64",
		},
		Auth: &schemas.RemoteAuth{Type: schemas.RemoteAuthSigV4, Region: "{{input.region}}", Service: "bedrock"},
		Docs: "https://docs.aws.amazon.com/bedrock/latest/APIReference/API_agent-runtime_InvokeAgent.html",
	},
	{
		ID: "agentforce", Label: "Salesforce Agentforce",
		Help: "An Agentforce agent through the Agent API. Heimdall gets a token with your connected app's client credentials, opens a session, sends each task as a message and ends the session.",
		URL:  agentforceAPI + "/sessions/{{start.id}}/messages",
		Inputs: []PresetInput{
			{Name: "my_domain", Label: "My Domain host", Placeholder: "acme.my.salesforce.com", Required: true},
			{Name: "agent_id", Label: "Agent id", Placeholder: "0XxHn000000x9F1KAI", Required: true},
		},
		Mapping: schemas.RemoteMapping{
			Body:       `{"message": {"sequenceId": "{{session_index}}", "type": "Text", "text": "{{instruction}}"}}`,
			AnswerPath: "messages[*].message",
			Start: &schemas.RemoteStep{Method: "POST", URL: agentforceAPI + "/agents/{{input.agent_id}}/sessions", IDPath: "sessionId",
				Body: `{"externalSessionKey": "{{uuid}}", "instanceConfig": {"endpoint": "https://{{input.my_domain}}"}, "streamingCapabilities": {"chunkTypes": ["Text"]}, "bypassUser": true}`},
			End: &schemas.RemoteStep{Method: "DELETE", URL: agentforceAPI + "/sessions/{{start.id}}", Headers: map[string]string{"x-session-end-reason": "UserRequest"}},
		},
		Auth: &schemas.RemoteAuth{Type: schemas.RemoteAuthOAuth2, TokenURL: "https://{{input.my_domain}}/services/oauth2/token"},
		Docs: "https://developer.salesforce.com/docs/einstein/genai/guide/agent-api.html",
	},
	{
		ID: "copilot_studio", Label: "Microsoft Copilot Studio",
		Help: "A Copilot Studio agent through Direct Line. Heimdall opens a conversation, posts each task and polls for the agent's replies to it. Use the Direct Line secret from the agent's web channel security settings as the auth token.",
		URL:  directLine + "/{{start.id}}/activities",
		Mapping: schemas.RemoteMapping{
			Body:  `{"type": "message", "from": {"id": "heimdall-user", "role": "user"}, "text": "{{instruction}}"}`,
			Start: &schemas.RemoteStep{Method: "POST", URL: directLine, IDPath: "conversationId"},
			Poll: &schemas.RemotePoll{URL: directLine + "/{{start.id}}/activities", IntervalS: 1, ReplyIDPath: "id",
				AnswerPath: "activities[?(@.replyToId=='{{reply.id}}')].text"},
		},
		Docs: "https://learn.microsoft.com/en-us/microsoft-copilot-studio/publication-connect-bot-to-custom-application",
	},
	{
		ID: "vercel_ai", Label: "Vercel AI SDK route",
		Help:           "A Next.js or Node route that returns toUIMessageStreamResponse() (AI SDK 5). Heimdall joins the text deltas of the stream.",
		URLPlaceholder: "https://my-app.vercel.app/api/chat",
		Mapping: schemas.RemoteMapping{
			Body:       `{"id": "{{session_id}}", "messages": [{"id": "{{uuid}}", "role": "user", "parts": [{"type": "text", "text": "{{instruction}}"}]}]}`,
			Stream:     schemas.RemoteStreamSSE,
			StreamPath: "[?(@.type=='text-delta')].delta",
		},
		Docs: "https://ai-sdk.dev/docs/ai-sdk-ui/stream-protocol",
	},
	{
		ID: "chat_completions", Label: "Chat completion endpoint",
		Help:           "Any endpoint that takes OpenAI-style chat messages and answers with choices[0].message.content. Use Connect your OpenAI-compatible endpoint instead when Heimdall should run the tool calls.",
		URLPlaceholder: "https://agent.example.com/v1/chat/completions",
		Inputs:         []PresetInput{{Name: "model", Label: "Model or agent name", Placeholder: "my-agent"}},
		Mapping: schemas.RemoteMapping{
			Body:       `{"model": "{{input.model}}", "messages": "{{messages}}"}`,
			AnswerPath: "choices[0].message.content",
		},
	},
}

// FindPreset returns a preset by id.
func FindPreset(id string) (Preset, bool) {
	for _, p := range Presets {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}

var (
	inputRe      = regexp.MustCompile(`\{\{\s*input\.([a-z_]+)\s*\}\}`)
	inputValueRe = regexp.MustCompile(`^[A-Za-z0-9._:@/-]{1,200}$`)
)

// Fill substitutes the preset's inputs and returns the agent URL (url when
// the preset has none), the mapping and the suggested auth type with its
// fixed fields. A missing required input or an input with characters other
// than letters, digits and ._:@/- is refused.
func (p Preset) Fill(rawURL string, inputs map[string]string) (string, *schemas.RemoteMapping, *schemas.RemoteAuth, error) {
	vals := map[string]string{}
	for _, in := range p.Inputs {
		v := strings.TrimSpace(inputs[in.Name])
		if v == "" {
			v = in.Default
		}
		if v == "" && in.Required {
			return "", nil, nil, fmt.Errorf("%s needs %s", p.Label, strings.ToLower(in.Label))
		}
		if v != "" && !inputValueRe.MatchString(v) {
			return "", nil, nil, fmt.Errorf("%s %q may hold only letters, digits and . _ : @ / -", strings.ToLower(in.Label), v)
		}
		vals[in.Name] = v
	}
	var missing error
	sub := func(s string) string {
		return inputRe.ReplaceAllStringFunc(s, func(ref string) string {
			name := inputRe.FindStringSubmatch(ref)[1]
			v, ok := vals[name]
			if !ok && missing == nil {
				missing = fmt.Errorf("preset %s uses unknown input %s", p.ID, name)
			}
			return v
		})
	}
	u := rawURL
	if p.URL != "" {
		u = sub(p.URL)
	}
	m := p.Mapping
	m.Preset = p.ID
	m.Body = sub(m.Body)
	m.AnswerPath = sub(m.AnswerPath)
	subStep := func(s *schemas.RemoteStep) *schemas.RemoteStep {
		if s == nil {
			return nil
		}
		c := *s
		c.URL, c.Body = sub(c.URL), sub(c.Body)
		return &c
	}
	m.Start, m.End = subStep(m.Start), subStep(m.End)
	if m.Poll != nil {
		c := *m.Poll
		c.URL, c.AnswerPath = sub(c.URL), sub(c.AnswerPath)
		m.Poll = &c
	}
	var auth *schemas.RemoteAuth
	if p.Auth != nil {
		a := *p.Auth
		a.TokenURL, a.Region, a.Service = sub(a.TokenURL), sub(a.Region), sub(a.Service)
		auth = &a
	}
	return u, &m, auth, missing
}
