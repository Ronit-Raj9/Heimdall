package schemas

import "strings"

// Request and response mapping of a remote_http agent (lane Y10, gap G1
// P0-3): an endpoint that does not speak Heimdall's own JSON shape is
// described here instead of behind a shim the customer writes. The
// launcher (adapters/remotehttp.py) and the "Test my endpoint" check
// (remotemap) read the same block, so a test call and a real run send the
// same request.
//
// Placeholders in Body, URLs, Headers and Query:
//
//	{{instruction}}    the task message (text)
//	{{messages}}       the conversation so far as [{role, content}] (JSON)
//	{{tools}}          the published tools block, {} when none (JSON)
//	{{metadata}}       {session_id, session_index, output, task, run} (JSON)
//	{{gateway}}        {url, key, model} with model_traffic gateway, else null
//	{{session_id}}     the session id (text)
//	{{session_index}}  1-based session number (number)
//	{{output}}         the task's output kind (text)
//	{{model}}          the agent's model, "" when none (text)
//	{{uuid}}           a fresh random id per request (text)
//	{{start.id}}       the id the start step returned (text)
//	{{reply.id}}       the id poll.reply_id_path read from the main answer
//	{{secret.NAME}}    an org secret (Settings > Secrets); headers and query only
//
// In Body a string that is exactly one placeholder takes the placeholder's
// JSON value (so "{{messages}}" becomes an array); a placeholder inside a
// longer string is inserted as text. In a URL a placeholder is path
// escaped.

// Remote stream formats (RemoteMapping.Stream).
const (
	RemoteStreamSSE            = "sse"             // text/event-stream; each data line is JSON (or text)
	RemoteStreamAWSEventStream = "aws_eventstream" // application/vnd.amazon.eventstream (Bedrock)
)

// Remote auth types (RemoteAuth.Type).
const (
	RemoteAuthOAuth2     = "oauth2_client_credentials"
	RemoteAuthEntra      = "azure_entra"
	RemoteAuthSigV4      = "aws_sigv4"
	RemoteAuthGCPIDToken = "gcp_id_token"
)

// RemoteMapping maps a task to the endpoint's own request and reads its
// answer back. Every field is optional; an empty mapping is Heimdall's own
// shape.
type RemoteMapping struct {
	// Preset names the platform the mapping was made for (langgraph, adk,
	// mastra, n8n, openai_responses, bedrock_agent, agentforce,
	// copilot_studio, vercel_ai, chat_completions, custom). Display only:
	// the fields below are what runs.
	Preset string `json:"preset,omitempty" yaml:"preset"`
	// Method of the main request, POST by default.
	Method  string            `json:"method,omitempty" yaml:"method"`
	Headers map[string]string `json:"headers,omitempty" yaml:"headers"`
	Query   map[string]string `json:"query,omitempty" yaml:"query"`
	// Body is the request template: JSON text with placeholders. Empty sends
	// Heimdall's own body.
	Body string `json:"body,omitempty" yaml:"body"`
	// AnswerPath picks the answer out of the JSON response, for example
	// output[?(@.type=='message')].content[*].text. Several matches are
	// joined with a newline. Empty reads "answer" or a text/plain body.
	AnswerPath string `json:"answer_path,omitempty" yaml:"answer_path"`
	// Stream reads a streamed answer: sse or aws_eventstream. StreamPath
	// picks the text of each event (joined without separator); events it
	// does not match are skipped. StreamDecode base64 decodes each piece
	// (Bedrock chunk bytes) before it is read as JSON or text.
	Stream       string `json:"stream,omitempty" yaml:"stream"`
	StreamPath   string `json:"stream_path,omitempty" yaml:"stream_path"`
	StreamDecode string `json:"stream_decode,omitempty" yaml:"stream_decode"` // base64
	// Start opens a session before the main request (Agentforce sessions,
	// Direct Line conversations, ADK sessions); its IDPath value is
	// {{start.id}}. Poll fetches the answer after the main request until
	// its AnswerPath matches (Direct Line activities). End closes the
	// session, best effort.
	Start *RemoteStep `json:"start,omitempty" yaml:"start"`
	Poll  *RemotePoll `json:"poll,omitempty" yaml:"poll"`
	End   *RemoteStep `json:"end,omitempty" yaml:"end"`
}

// RemoteStep is one extra request of a mapping. URL is absolute or a path
// on the agent URL's host; every step stays on that host.
type RemoteStep struct {
	Method  string            `json:"method,omitempty" yaml:"method"`
	URL     string            `json:"url" yaml:"url"`
	Headers map[string]string `json:"headers,omitempty" yaml:"headers"`
	Body    string            `json:"body,omitempty" yaml:"body"`
	IDPath  string            `json:"id_path,omitempty" yaml:"id_path"`
}

// RemotePoll fetches the answer after the main request.
type RemotePoll struct {
	URL        string `json:"url" yaml:"url"`
	AnswerPath string `json:"answer_path" yaml:"answer_path"`
	IntervalS  int    `json:"interval_s,omitempty" yaml:"interval_s"` // 1 by default
	// ReplyIDPath picks an id out of the main request's answer, available
	// to AnswerPath and URL as {{reply.id}} (Direct Line: the id of the
	// posted activity, which the agent's replies name in replyToId).
	ReplyIDPath string `json:"reply_id_path,omitempty" yaml:"reply_id_path"`
}

// RemoteAuth signs or authorizes each request of a remote agent. Every
// credential is the name of an org secret (Settings > Secrets); no value
// is ever part of the config. The client certificate fields add mutual TLS
// to any type (or to none).
type RemoteAuth struct {
	Type string `json:"type,omitempty" yaml:"type"`

	// OAuth 2 client credentials and Microsoft Entra ID. TokenURL is the
	// token endpoint (Entra: derived from TenantID when empty). The token
	// is fetched once per run and fetched again shortly before it expires.
	TokenURL           string `json:"token_url,omitempty" yaml:"token_url"`
	TenantID           string `json:"tenant_id,omitempty" yaml:"tenant_id"`
	ClientIDSecret     string `json:"client_id_secret,omitempty" yaml:"client_id_secret"`
	ClientSecretSecret string `json:"client_secret_secret,omitempty" yaml:"client_secret_secret"`
	Scope              string `json:"scope,omitempty" yaml:"scope"`
	// Audience is the OAuth audience parameter, or the ID token audience
	// (gcp_id_token; the service URL when empty).
	Audience string `json:"audience,omitempty" yaml:"audience"`

	// AWS Signature Version 4.
	AccessKeySecret    string `json:"access_key_secret,omitempty" yaml:"access_key_secret"`
	SecretKeySecret    string `json:"secret_key_secret,omitempty" yaml:"secret_key_secret"`
	SessionTokenSecret string `json:"session_token_secret,omitempty" yaml:"session_token_secret"`
	Region             string `json:"region,omitempty" yaml:"region"`
	Service            string `json:"service,omitempty" yaml:"service"`

	// Google: the service account JSON key, exchanged for an ID token.
	ServiceAccountSecret string `json:"service_account_secret,omitempty" yaml:"service_account_secret"`

	// Mutual TLS: PEM client certificate and key, and an optional CA bundle
	// the server certificate is checked against.
	ClientCertSecret string `json:"client_cert_secret,omitempty" yaml:"client_cert_secret"`
	ClientKeySecret  string `json:"client_key_secret,omitempty" yaml:"client_key_secret"`
	CACertSecret     string `json:"ca_cert_secret,omitempty" yaml:"ca_cert_secret"`
}

// SecretNames returns every org secret a remote agent names: its auth and
// signing secrets, its auth block and every {{secret.NAME}} in the
// mapping's headers and query.
func (r *RemoteAgent) SecretNames() []string {
	if r == nil {
		return nil
	}
	out := []string{r.AuthSecret, r.SignSecret}
	if a := r.Auth; a != nil {
		out = append(out, a.ClientIDSecret, a.ClientSecretSecret, a.AccessKeySecret, a.SecretKeySecret,
			a.SessionTokenSecret, a.ServiceAccountSecret, a.ClientCertSecret, a.ClientKeySecret, a.CACertSecret)
	}
	if m := r.Mapping; m != nil {
		maps := []map[string]string{m.Headers, m.Query}
		for _, s := range []*RemoteStep{m.Start, m.End} {
			if s != nil {
				maps = append(maps, s.Headers)
			}
		}
		for _, h := range maps {
			for _, v := range h {
				out = append(out, secretRefNames(v)...)
			}
		}
	}
	keep := out[:0]
	seen := map[string]bool{}
	for _, n := range out {
		if n != "" && !seen[n] {
			seen[n] = true
			keep = append(keep, n)
		}
	}
	return keep
}

// secretRefNames returns the NAME of every {{secret.NAME}} in v.
func secretRefNames(v string) []string {
	var out []string
	for {
		i := strings.Index(v, "{{")
		if i < 0 {
			return out
		}
		v = v[i+2:]
		j := strings.Index(v, "}}")
		if j < 0 {
			return out
		}
		ref := strings.TrimSpace(v[:j])
		v = v[j+2:]
		if name, ok := strings.CutPrefix(ref, "secret."); ok && name != "" {
			out = append(out, name)
		}
	}
}
