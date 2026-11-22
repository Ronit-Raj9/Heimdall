// Package connect is the onboarding brain behind the Connect box (lane Z01,
// audit/INFRA_I2_AGENT_ONBOARDING.md sections 4 and 6): it takes whatever a
// developer has (a URL, a curl command, a repository, an image reference, a
// platform id), works out what it is, probes it with a nonce to infer the
// request field and the answer path, and proposes a normal agent config: an
// agent template id, its form params and the lane Y10 request and answer
// mapping (schemas.RemoteMapping). Verify then proves the config works,
// row by row, and every failure is explained by one entry of the failure
// catalog (failures.go), which real runs share.
//
// Detection is deterministic. No model ever proposes a mapping here: a
// mapping is accepted only when the nonce comes back at the answer path.
package connect

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Class is what an input looks like, decided without any network call.
type Class string

// Input classes (section 4.2).
const (
	ClassURL        Class = "url"
	ClassCurl       Class = "curl"
	ClassRepository Class = "repository"
	ClassImage      Class = "image"
	ClassPlatform   Class = "platform"
	ClassSocket     Class = "socket"
	ClassPhone      Class = "phone"
	ClassPullObject Class = "pull_object"
	ClassTrace      Class = "trace"
	ClassUnknown    Class = "unknown"
)

// Agent template ids a candidate fills (handlers/agentbuilder.go).
const (
	TemplateHTTP   = "http-url"
	TemplateA2A    = "a2a-card"
	TemplateOpenAI = "openai-endpoint"
	TemplateWrap   = "wrap-app"
	TemplateImage  = "docker-image"
)

// Candidate kinds.
const (
	KindHTTP     = "http"
	KindA2A      = "a2a"
	KindOpenAI   = "openai"
	KindMCP      = "mcp"
	KindPull     = "pull"
	KindRepo     = "repository"
	KindImage    = "image"
	KindPlatform = "platform"
)

// Input is what the user pasted, or a platform picked from a list plus the
// agent id on that platform.
type Input struct {
	Raw      string `json:"input"`
	Platform string `json:"platform,omitempty"`
	ID       string `json:"id,omitempty"`
}

// Question is one thing the user still has to answer: a secret value
// (stored on the spot as an org secret, never in the config), an id, or a
// field to pick when the nonce was not found.
type Question struct {
	Kind  string `json:"kind"`  // secret | text | pick_field
	Param string `json:"param"` // the template param the answer fills
	Label string `json:"label"`
	Help  string `json:"help,omitempty"`
	// Secret is the suggested secret name for a secret question.
	Secret string `json:"secret,omitempty"`
	// Header is the header the secret goes in.
	Header string `json:"header,omitempty"`
	// Prefill is a value the user pasted in this same input (a token in a
	// curl command), offered in the inline secret box so it need not be
	// typed again. It is never written to a config.
	Prefill string   `json:"prefill,omitempty"`
	Options []string `json:"options,omitempty"`
}

// Candidate is one way to connect the input, with the evidence for it.
type Candidate struct {
	Kind       string   `json:"kind"`
	Preset     string   `json:"preset,omitempty"` // Y10 preset id (langgraph, adk, mastra, n8n, ...) or custom
	Label      string   `json:"label"`
	Confidence float64  `json:"confidence"` // 0 to 1
	Evidence   []string `json:"evidence"`
	// Supported is false for an input Heimdall recognises but cannot run
	// yet (a phone number, an MCP server as the agent); Message says what
	// to do instead.
	Supported bool   `json:"supported"`
	Message   string `json:"message,omitempty"`
	// Template and Params are the agent template and its form params (the
	// names of handlers/agentconnect.go and Y10's mapping params). Mapping
	// and Auth are the Y10 remote.mapping and remote.auth blocks; nil keeps
	// Heimdall's own shape and the static token.
	Template string                 `json:"template,omitempty"`
	URL      string                 `json:"url,omitempty"`
	Params   map[string]string      `json:"params,omitempty"`
	Mapping  *schemas.RemoteMapping `json:"mapping,omitempty"`
	Auth     *schemas.RemoteAuth    `json:"auth,omitempty"`
	// NonceFound is true when the probe found the check code at the answer
	// path: the mapping is proven for one request.
	NonceFound bool       `json:"nonce_found"`
	Questions  []Question `json:"questions,omitempty"`
	// Capabilities are hints for the benchmark pick (tools, sessions).
	Capabilities []string `json:"capabilities,omitempty"`
	// Command is a command to copy (heimdall serve for a pull object or a
	// private address).
	Command string `json:"command,omitempty"`
}

// Probe is one request the detection made, shown as evidence. Every probe
// is disclosed; its URL never carries user info or a query.
type Probe struct {
	Method string `json:"method"`
	URL    string `json:"url"`
	Status int    `json:"status,omitempty"`
	MS     int64  `json:"ms"`
	Note   string `json:"note,omitempty"`
}

// Proposal is the answer of Detect.
type Proposal struct {
	Class      Class       `json:"class"`
	Candidates []Candidate `json:"candidates"`
	Failures   []Failure   `json:"failures,omitempty"`
	Probes     []Probe     `json:"probes"`
	Nonce      string      `json:"nonce,omitempty"`
}

// Best is the first supported candidate, or nil.
func (p *Proposal) Best() *Candidate {
	for i := range p.Candidates {
		if p.Candidates[i].Supported {
			return &p.Candidates[i]
		}
	}
	return nil
}

var (
	imageRefRe  = regexp.MustCompile(`^([a-z0-9]+([._-][a-z0-9]+)*(:[0-9]+)?/)?[a-z0-9]+([._-][a-z0-9]+)*(/[a-z0-9]+([._-][a-z0-9]+)*)*(:[A-Za-z0-9_][A-Za-z0-9._-]{0,127})?(@sha256:[a-f0-9]{64})?$`)
	pullObjRe   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_./-]*(\.py)?:[A-Za-z_][A-Za-z0-9_]*$`)
	phoneRe     = regexp.MustCompile(`^\+[0-9][0-9 ()-]{5,20}$`)
	arnRe       = regexp.MustCompile(`^arn:aws:bedrock(-agentcore)?:[a-z0-9-]+:[0-9]{12}:(runtime|agent|agent-runtime)/[A-Za-z0-9._/-]+$`)
	scpGitRe    = regexp.MustCompile(`^git@[A-Za-z0-9.-]+:[A-Za-z0-9._/-]+$`)
	repoHostsRe = regexp.MustCompile(`^(https?://)?(www\.)?(github\.com|gitlab\.com|bitbucket\.org)/[^/\s]+/[^/\s]+`)
)

// Classify decides the class of raw without any network call (section 4.2).
func Classify(raw string) Class {
	s := strings.TrimSpace(raw)
	low := strings.ToLower(s)
	switch {
	case s == "":
		return ClassUnknown
	case strings.HasPrefix(low, "curl ") || strings.HasPrefix(low, "curl\t") || strings.HasPrefix(low, "curl.exe "):
		return ClassCurl
	case strings.HasPrefix(low, "{") && (strings.Contains(low, `"resourcespans"`) || strings.Contains(low, `"resource_spans"`)):
		return ClassTrace
	case arnRe.MatchString(s):
		return ClassPlatform
	case strings.HasPrefix(low, "ws://") || strings.HasPrefix(low, "wss://"):
		return ClassSocket
	case strings.HasPrefix(low, "sip:") || strings.HasPrefix(low, "tel:") || phoneRe.MatchString(s):
		return ClassPhone
	case scpGitRe.MatchString(s) || repoHostsRe.MatchString(low) || strings.HasSuffix(low, ".git"):
		return ClassRepository
	case strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://"):
		return ClassURL
	case strings.HasPrefix(s, "/") || strings.HasPrefix(s, "./") || strings.HasPrefix(s, "~/"):
		return ClassRepository // a local directory (CLI and MCP only)
	case pullObjRe.MatchString(s):
		return ClassPullObject
	case strings.Contains(s, ".") && strings.Contains(s, "/") && !strings.Contains(s, " ") && looksLikeHostPath(s):
		return ClassURL // agent.example.com/run without a scheme
	case imageRefRe.MatchString(s) && (strings.Contains(s, "/") || strings.Contains(s, ":") || strings.Contains(s, "@")):
		return ClassImage
	}
	return ClassUnknown
}

// looksLikeHostPath reports whether s starts with a dotted host name that
// is not an image registry path (registry paths have no dots after the
// first segment and usually a tag).
func looksLikeHostPath(s string) bool {
	host, rest, _ := strings.Cut(s, "/")
	if !strings.Contains(host, ".") || strings.Contains(s, "@sha256:") {
		return false
	}
	switch host {
	case "ghcr.io", "docker.io", "quay.io", "gcr.io", "public.ecr.aws", "registry.gitlab.com", "mcr.microsoft.com", "index.docker.io":
		return false
	}
	if strings.HasSuffix(host, ".azurecr.io") || strings.HasSuffix(host, ".pkg.dev") || strings.Contains(host, ".dkr.ecr.") {
		return false
	}
	// an image reference ends in :tag; a URL path rarely does.
	last := rest[strings.LastIndex(rest, "/")+1:]
	return !strings.Contains(last, ":")
}

// redactURL drops user info, the query and the fragment, which may carry
// a key.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(unreadable URL)"
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	return u.String()
}

// RedactURL is redactURL for callers outside the package.
func RedactURL(raw string) string { return redactURL(raw) }

// secretNameFor turns a host and a purpose into a secret name, for example
// bot.acme.com and KEY into BOT_ACME_KEY.
func secretNameFor(host, purpose string) string {
	host = strings.ToLower(host)
	if h, _, ok := strings.Cut(host, ":"); ok {
		host = h
	}
	parts := strings.Split(host, ".")
	var keep []string
	for _, p := range parts {
		switch p {
		case "www", "api", "com", "io", "ai", "net", "org", "dev", "app", "co", "cloud":
			continue
		}
		if p != "" {
			keep = append(keep, p)
		}
		if len(keep) == 2 {
			break
		}
	}
	if len(keep) == 0 {
		keep = []string{"AGENT"}
	}
	name := strings.ToUpper(strings.Join(keep, "_") + "_" + purpose)
	name = regexp.MustCompile(`[^A-Z0-9_]`).ReplaceAllString(name, "_")
	if name[0] >= '0' && name[0] <= '9' {
		name = "AGENT_" + name
	}
	return name
}
