package connect

import (
	"net/url"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Platform mappings Detect proposes. The ids and request shapes match lane
// Y10's presets (remotemap/presets.go) so a proposal opens in the Y10 form
// with the same preset selected; the extra no-code platforms (Flowise,
// Langflow, Dify) are custom mappings the nonce probe must confirm.

const (
	PresetCustom      = "custom"
	PresetLangGraph   = "langgraph"
	PresetADK         = "adk"
	PresetMastra      = "mastra"
	PresetN8N         = "n8n"
	PresetResponses   = "openai_responses"
	PresetBedrock     = "bedrock_agent"
	PresetAgentforce  = "agentforce"
	PresetCopilot     = "copilot_studio"
	PresetVercelAI    = "vercel_ai"
	PresetChat        = "chat_completions"
	PresetAgentCore   = "bedrock_agentcore"
	PresetVertex      = "vertex_agent_engine"
	PresetFlowise     = "flowise"
	PresetLangflow    = "langflow"
	PresetDify        = "dify"
	agentforceBase    = "https://api.salesforce.com/einstein/ai-agent/v1"
	directLineConvURL = "https://directline.botframework.com/v3/directline/conversations"
)

// ownTemplate is Heimdall's own request shape as a template.
const ownTemplate = `{"instruction": "{{instruction}}", "session_id": "{{session_id}}", "session_index": "{{session_index}}", "output": "{{output}}"}`

func langGraphMapping(assistant string) *schemas.RemoteMapping {
	if assistant == "" {
		assistant = "agent"
	}
	return &schemas.RemoteMapping{Preset: PresetLangGraph,
		Body:       `{"assistant_id": "` + jsonEsc(assistant) + `", "input": {"messages": [{"role": "user", "content": "{{instruction}}"}]}, "metadata": "{{metadata}}"}`,
		AnswerPath: "messages[-1].content"}
}

func adkMapping(app, user string) *schemas.RemoteMapping {
	if user == "" {
		user = "heimdall"
	}
	return &schemas.RemoteMapping{Preset: PresetADK,
		Body:       `{"app_name": "` + jsonEsc(app) + `", "user_id": "` + jsonEsc(user) + `", "session_id": "{{session_id}}", "new_message": {"role": "user", "parts": [{"text": "{{instruction}}"}]}}`,
		AnswerPath: "[-1].content.parts[*].text",
		Start:      &schemas.RemoteStep{Method: "POST", URL: "/apps/" + url.PathEscape(app) + "/users/" + url.PathEscape(user) + "/sessions/{{session_id}}", Body: `{}`}}
}

func mastraMapping() *schemas.RemoteMapping {
	return &schemas.RemoteMapping{Preset: PresetMastra,
		Body:       `{"messages": [{"role": "user", "content": "{{instruction}}"}], "threadId": "{{session_id}}", "resourceId": "heimdall"}`,
		AnswerPath: "text"}
}

func n8nMapping() *schemas.RemoteMapping {
	return &schemas.RemoteMapping{Preset: PresetN8N,
		Body:       `{"chatInput": "{{instruction}}", "sessionId": "{{session_id}}", "metadata": "{{metadata}}"}`,
		AnswerPath: "output"}
}

func jsonEsc(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return r.Replace(s)
}

// fingerprint is a host or path match that decides the platform without a
// request (ladder step 1).
type fingerprint struct {
	preset  string
	label   string
	kind    string
	mapping *schemas.RemoteMapping
	auth    *schemas.RemoteAuth
	header  string // static token header
	// final: the platform's API needs credentials before anything can be
	// probed, so detection stops at the questions.
	final     bool
	supported bool
	message   string
	questions []Question
	evidence  string
}

func fingerprintURL(u *url.URL) *fingerprint {
	host := strings.ToLower(u.Hostname())
	p := u.Path
	switch {
	case host == "api.vapi.ai" || host == "api.retellai.com" || host == "api.elevenlabs.io":
		return &fingerprint{preset: strings.Split(host, ".")[1], label: "Voice agent platform", kind: KindPlatform, final: true,
			evidence: host + " is a voice agent platform API",
			message:  "Voice agents connect by platform and agent id: pick the platform in the list, paste the assistant or agent id and one private key."}
	case strings.HasPrefix(host, "bedrock-agentcore.") && strings.HasSuffix(host, ".amazonaws.com"):
		region := strings.TrimSuffix(strings.TrimPrefix(host, "bedrock-agentcore."), ".amazonaws.com")
		return &fingerprint{preset: PresetAgentCore, label: "AWS Bedrock AgentCore runtime", kind: KindHTTP, final: true, supported: true,
			evidence: host + " is the Bedrock AgentCore runtime API (SigV4, region " + region + ")",
			mapping:  &schemas.RemoteMapping{Preset: PresetAgentCore, Body: `{"prompt": "{{instruction}}", "session_id": "{{session_id}}"}`},
			auth:     &schemas.RemoteAuth{Type: schemas.RemoteAuthSigV4, Region: region, Service: "bedrock-agentcore"},
			questions: []Question{
				{Kind: "secret", Param: "access_key_secret", Label: "AWS access key id", Secret: "AWS_ACCESS_KEY_ID", Help: "An access key with bedrock-agentcore:InvokeAgentRuntime."},
				{Kind: "secret", Param: "secret_key_secret", Label: "AWS secret access key", Secret: "AWS_SECRET_ACCESS_KEY"}}}
	case strings.HasPrefix(host, "bedrock-agent-runtime.") && strings.HasSuffix(host, ".amazonaws.com"):
		region := strings.TrimSuffix(strings.TrimPrefix(host, "bedrock-agent-runtime."), ".amazonaws.com")
		return &fingerprint{preset: PresetBedrock, label: "AWS Bedrock Agents", kind: KindHTTP, final: true, supported: true,
			evidence: host + " is the Bedrock Agents runtime API (SigV4)",
			mapping:  &schemas.RemoteMapping{Preset: PresetBedrock, Body: `{"inputText": "{{instruction}}", "enableTrace": false}`, Stream: schemas.RemoteStreamAWSEventStream, StreamPath: "bytes", StreamDecode: "base64"},
			auth:     &schemas.RemoteAuth{Type: schemas.RemoteAuthSigV4, Region: region, Service: "bedrock"},
			questions: []Question{
				{Kind: "secret", Param: "access_key_secret", Label: "AWS access key id", Secret: "AWS_ACCESS_KEY_ID", Help: "An access key with bedrock:InvokeAgent on the alias."},
				{Kind: "secret", Param: "secret_key_secret", Label: "AWS secret access key", Secret: "AWS_SECRET_ACCESS_KEY"}}}
	case strings.HasSuffix(host, "-aiplatform.googleapis.com") && strings.Contains(p, "/reasoningEngines/"):
		return &fingerprint{preset: PresetVertex, label: "Vertex AI Agent Engine", kind: KindHTTP, final: true, supported: true,
			evidence: host + " is a Vertex AI Agent Engine (Google ID token)",
			mapping:  &schemas.RemoteMapping{Preset: PresetVertex, Body: `{"class_method": "query", "input": {"input": "{{instruction}}"}}`, AnswerPath: "output"},
			auth:     &schemas.RemoteAuth{Type: schemas.RemoteAuthGCPIDToken},
			questions: []Question{{Kind: "secret", Param: "service_account_secret", Label: "Service account key (JSON)", Secret: "GCP_SERVICE_ACCOUNT_JSON",
				Help: "A service account allowed to query the engine."}}}
	case host == "api.salesforce.com" && strings.Contains(p, "/einstein/ai-agent/"):
		agentID := ""
		if i := strings.Index(p, "/agents/"); i >= 0 {
			agentID = strings.SplitN(p[i+len("/agents/"):], "/", 2)[0]
		}
		return &fingerprint{preset: PresetAgentforce, label: "Salesforce Agentforce", kind: KindHTTP, final: true, supported: true,
			evidence: "api.salesforce.com/einstein/ai-agent is the Agentforce Agent API (OAuth client credentials)",
			mapping: &schemas.RemoteMapping{Preset: PresetAgentforce,
				Body:       `{"message": {"sequenceId": "{{session_index}}", "type": "Text", "text": "{{instruction}}"}}`,
				AnswerPath: "messages[*].message",
				Start: &schemas.RemoteStep{Method: "POST", URL: agentforceBase + "/agents/" + agentID + "/sessions", IDPath: "sessionId",
					Body: `{"externalSessionKey": "{{uuid}}", "streamingCapabilities": {"chunkTypes": ["Text"]}, "bypassUser": true}`},
				End: &schemas.RemoteStep{Method: "DELETE", URL: agentforceBase + "/sessions/{{start.id}}", Headers: map[string]string{"x-session-end-reason": "UserRequest"}}},
			auth: &schemas.RemoteAuth{Type: schemas.RemoteAuthOAuth2},
			questions: []Question{
				{Kind: "text", Param: "token_url", Label: "Token URL", Help: "https://<your My Domain>/services/oauth2/token"},
				{Kind: "secret", Param: "client_id_secret", Label: "Connected app client id", Secret: "AGENTFORCE_CLIENT_ID"},
				{Kind: "secret", Param: "client_secret_secret", Label: "Connected app client secret", Secret: "AGENTFORCE_CLIENT_SECRET"}}}
	case host == "directline.botframework.com":
		return &fingerprint{preset: PresetCopilot, label: "Microsoft Copilot Studio (Direct Line)", kind: KindHTTP, final: true, supported: true,
			evidence: "directline.botframework.com is the Bot Framework Direct Line API",
			mapping: &schemas.RemoteMapping{Preset: PresetCopilot,
				Body:  `{"type": "message", "from": {"id": "heimdall-user", "role": "user"}, "text": "{{instruction}}"}`,
				Start: &schemas.RemoteStep{Method: "POST", URL: directLineConvURL, IDPath: "conversationId"},
				Poll: &schemas.RemotePoll{URL: directLineConvURL + "/{{start.id}}/activities", IntervalS: 1, ReplyIDPath: "id",
					AnswerPath: "activities[?(@.replyToId=='{{reply.id}}')].text"}},
			header:    "Authorization",
			questions: []Question{{Kind: "secret", Param: "auth_secret", Label: "Direct Line secret", Secret: "DIRECTLINE_SECRET", Header: "Authorization"}}}
	case strings.Contains(p, "/webhook/") || strings.Contains(p, "/webhook-test/"):
		return &fingerprint{preset: PresetN8N, label: "n8n webhook", kind: KindHTTP, supported: true, mapping: n8nMapping(),
			evidence: "the path " + firstSegments(p, 2) + " is an n8n webhook"}
	case strings.Contains(p, "/api/v1/prediction/"):
		return &fingerprint{preset: PresetFlowise, label: "Flowise chatflow", kind: KindHTTP, supported: true,
			evidence: "the path /api/v1/prediction/ is a Flowise prediction route",
			mapping:  &schemas.RemoteMapping{Preset: PresetCustom, Body: `{"question": "{{instruction}}", "overrideConfig": {"sessionId": "{{session_id}}"}}`, AnswerPath: "text"}}
	case strings.Contains(p, "/api/v1/run/"):
		return &fingerprint{preset: PresetLangflow, label: "Langflow flow", kind: KindHTTP, supported: true,
			evidence: "the path /api/v1/run/ is a Langflow run route",
			mapping: &schemas.RemoteMapping{Preset: PresetCustom, Body: `{"input_value": "{{instruction}}", "input_type": "chat", "output_type": "chat", "session_id": "{{session_id}}"}`,
				AnswerPath: "outputs[0].outputs[0].results.message.text"}, header: "x-api-key"}
	case strings.HasSuffix(p, "/v1/chat-messages"):
		return &fingerprint{preset: PresetDify, label: "Dify app", kind: KindHTTP, supported: true,
			evidence:  "the path /v1/chat-messages is a Dify chat app route",
			mapping:   &schemas.RemoteMapping{Preset: PresetCustom, Body: `{"inputs": {}, "query": "{{instruction}}", "response_mode": "blocking", "user": "heimdall-{{session_id}}"}`, AnswerPath: "answer"},
			questions: []Question{{Kind: "secret", Param: "auth_secret", Label: "Dify app API key", Secret: secretNameFor(host, "KEY")}}}
	}
	return nil
}

func firstSegments(p string, n int) string {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) > n {
		parts = parts[:n]
	}
	return "/" + strings.Join(parts, "/") + "/"
}

// Platforms are the platform ids the Connect box offers in its list, with
// the id the user pastes.
type Platform struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	IDLabel string `json:"id_label"`
	Help    string `json:"help"`
}

// PlatformList is the platform picker of the Connect box.
var PlatformList = []Platform{
	{ID: "vapi", Label: "Vapi", IDLabel: "Assistant id", Help: "A Vapi assistant: paste its id and a private key."},
	{ID: "retell", Label: "Retell AI", IDLabel: "Agent id", Help: "A Retell agent: paste its id and an API key."},
	{ID: "elevenlabs", Label: "ElevenLabs Agents", IDLabel: "Agent id", Help: "An ElevenLabs conversational agent: paste its id and an API key."},
	{ID: PresetAgentCore, Label: "AWS Bedrock AgentCore", IDLabel: "Runtime ARN", Help: "arn:aws:bedrock-agentcore:<region>:<account>:runtime/<id>"},
	{ID: PresetBedrock, Label: "AWS Bedrock Agents", IDLabel: "Agent id and alias id (AGENT/ALIAS)", Help: "Plus the region; signed with SigV4."},
	{ID: PresetAgentforce, Label: "Salesforce Agentforce", IDLabel: "Agent id", Help: "The connected app's client id and secret are asked next."},
	{ID: PresetCopilot, Label: "Microsoft Copilot Studio", IDLabel: "Direct Line secret name", Help: "From the agent's web channel security settings."},
	{ID: PresetLangGraph, Label: "LangGraph Platform", IDLabel: "Deployment URL", Help: "Your deployment URL; the graph id is asked next."},
}

// platformCandidate is the candidate for a platform picked from the list.
func platformCandidate(in Input) Candidate {
	id := strings.TrimSpace(in.ID)
	switch in.Platform {
	case "vapi", "retell", "elevenlabs":
		return Candidate{Kind: KindPlatform, Preset: in.Platform, Label: platformLabel(in.Platform), Confidence: 1,
			Evidence: []string{"picked " + platformLabel(in.Platform) + " from the list"}, Supported: false,
			Message: "Fill in the " + platformLabel(in.Platform) + " fields under Or pick a platform, or use Import from a platform on the New agent page."}
	case PresetAgentCore:
		if c, ok := arnCandidate(id); ok {
			return c
		}
	case PresetAgentforce:
		u, _ := url.Parse(agentforceBase + "/agents/" + url.PathEscape(id) + "/sessions")
		return fromFingerprint(fingerprintURL(u), agentforceBase+"/sessions/{{start.id}}/messages")
	case PresetCopilot:
		u, _ := url.Parse(directLineConvURL)
		c := fromFingerprint(fingerprintURL(u), directLineConvURL+"/{{start.id}}/activities")
		return c
	case PresetLangGraph:
		if u, err := url.Parse(id); err == nil && u.Host != "" {
			c := Candidate{Kind: KindHTTP, Preset: PresetLangGraph, Label: "LangGraph Platform", Confidence: 0.9, Supported: true,
				Template: TemplateHTTP, URL: strings.TrimRight(id, "/") + "/runs/wait", Mapping: langGraphMapping("agent"),
				Evidence: []string{"picked LangGraph Platform from the list"},
				Questions: []Question{{Kind: "secret", Param: "auth_secret", Label: "LangSmith API key", Secret: "LANGSMITH_API_KEY", Header: "x-api-key"},
					{Kind: "text", Param: "preset_assistant_id", Label: "Assistant or graph id", Help: "The graph id from langgraph.json."}}}
			c.Params = map[string]string{"url": c.URL, "auth_header": "x-api-key", "auth_secret": "LANGSMITH_API_KEY"}
			return c
		}
	}
	return Candidate{Kind: KindPlatform, Preset: in.Platform, Label: platformLabel(in.Platform), Supported: false,
		Evidence: []string{"picked " + platformLabel(in.Platform)}, Message: "Paste the " + platformIDLabel(in.Platform) + " to continue."}
}

func platformLabel(id string) string {
	for _, p := range PlatformList {
		if p.ID == id {
			return p.Label
		}
	}
	return id
}

func platformIDLabel(id string) string {
	for _, p := range PlatformList {
		if p.ID == id {
			return strings.ToLower(p.IDLabel)
		}
	}
	return "agent id"
}

// arnCandidate turns an AgentCore runtime ARN into its candidate.
func arnCandidate(arn string) (Candidate, bool) {
	m := arnRe.FindStringSubmatch(strings.TrimSpace(arn))
	if m == nil {
		return Candidate{}, false
	}
	parts := strings.Split(arn, ":")
	region := parts[3]
	if strings.HasPrefix(arn, "arn:aws:bedrock:") {
		return Candidate{Kind: KindHTTP, Preset: PresetBedrock, Label: "AWS Bedrock Agents", Confidence: 0.9, Supported: true, Template: TemplateHTTP,
			Evidence: []string{"a Bedrock agent ARN in " + region},
			Message:  "Bedrock Agents are called by agent id and alias id: give the alias id.",
			Mapping:  &schemas.RemoteMapping{Preset: PresetBedrock, Body: `{"inputText": "{{instruction}}", "enableTrace": false}`, Stream: schemas.RemoteStreamAWSEventStream, StreamPath: "bytes", StreamDecode: "base64"},
			Auth:     &schemas.RemoteAuth{Type: schemas.RemoteAuthSigV4, Region: region, Service: "bedrock"},
			Questions: []Question{{Kind: "text", Param: "preset_alias_id", Label: "Alias id"},
				{Kind: "secret", Param: "access_key_secret", Label: "AWS access key id", Secret: "AWS_ACCESS_KEY_ID"},
				{Kind: "secret", Param: "secret_key_secret", Label: "AWS secret access key", Secret: "AWS_SECRET_ACCESS_KEY"}}}, true
	}
	endpoint := "https://bedrock-agentcore." + region + ".amazonaws.com/runtimes/" + url.PathEscape(arn) + "/invocations?qualifier=DEFAULT"
	u, _ := url.Parse(endpoint)
	c := fromFingerprint(fingerprintURL(u), endpoint)
	c.Evidence = append([]string{"an AgentCore runtime ARN in " + region}, c.Evidence...)
	c.Confidence = 0.95
	return c, true
}

// fromFingerprint makes a candidate of a fingerprint.
func fromFingerprint(fp *fingerprint, rawURL string) Candidate {
	c := Candidate{Kind: fp.kind, Preset: fp.preset, Label: fp.label, Confidence: 0.9, Supported: fp.supported, Message: fp.message,
		Evidence: []string{fp.evidence}, Mapping: fp.mapping, Auth: fp.auth, Questions: fp.questions, URL: rawURL}
	if fp.kind == KindHTTP {
		c.Template = TemplateHTTP
		c.Params = map[string]string{"url": rawURL}
		if fp.header != "" && !strings.EqualFold(fp.header, "Authorization") {
			c.Params["auth_header"] = fp.header
		}
		for _, q := range fp.questions {
			if q.Kind == "secret" && q.Param == "auth_secret" {
				c.Params["auth_secret"] = q.Secret
			}
		}
		if fp.auth != nil {
			a := *fp.auth
			for _, q := range fp.questions {
				if q.Kind != "secret" {
					continue
				}
				switch q.Param {
				case "access_key_secret":
					a.AccessKeySecret = q.Secret
				case "secret_key_secret":
					a.SecretKeySecret = q.Secret
				case "client_id_secret":
					a.ClientIDSecret = q.Secret
				case "client_secret_secret":
					a.ClientSecretSecret = q.Secret
				case "service_account_secret":
					a.ServiceAccountSecret = q.Secret
				}
			}
			c.Auth = &a
		}
	}
	return c
}
