package handlers

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/maximhq/bifrost/bench/traceinbox"
	"github.com/valyala/fasthttp"
)

// Web parity lane L18: GET /inbox/webhook tells the Inbox page how a tool
// pushes failures in: the ingest URL, the token variable the server reads,
// whether it is set and a ready curl line. It never returns the token.

// InboxWebhookInfo answers GET /api/bench/inbox/webhook.
type InboxWebhookInfo struct {
	// URL is the ingest endpoint as this request reached the server.
	URL string `json:"url"`
	// Enabled is true when a push would be accepted now.
	Enabled bool `json:"enabled"`
	// Configured is true when inbox/sources.yaml has a webhook source.
	Configured bool   `json:"configured"`
	Source     string `json:"source,omitempty"`
	TokenEnv   string `json:"token_env,omitempty"`
	// TokenSet is true when the token variable is set on the server.
	TokenSet      bool  `json:"token_set"`
	RatePerMinute int   `json:"rate_per_minute,omitempty"`
	MaxBytes      int64 `json:"max_bytes,omitempty"`
	Hosted        bool  `json:"hosted"`
	// IngestTokens is true when this server keeps per-org ingest tokens
	// (hosted): the page lists, creates and revokes them at /inbox/tokens.
	IngestTokens bool   `json:"ingest_tokens,omitempty"`
	Problem      string `json:"problem,omitempty"`
	// Next says what to do to turn the webhook on, empty when it is on.
	Next string `json:"next,omitempty"`
	// Example is a curl line with a placeholder token.
	Example string `json:"example"`
	// SourcesSnippet is the YAML to add to inbox/sources.yaml when no
	// webhook source exists.
	SourcesSnippet string `json:"sources_snippet,omitempty"`
}

// DefaultInboxTokenEnv is the token variable the setup snippet suggests.
const DefaultInboxTokenEnv = "HEIMDALL_INBOX_TOKEN"

func init() {
	RegisterOrgArea("liveinbox", func(env OrgEnv, r *router.Router) {
		h := inboxWebhookHandler{env: env, getenv: os.Getenv}
		r.GET(APIPrefix+"/inbox/webhook", env.R(h.get))
	})
}

type inboxWebhookHandler struct {
	env    OrgEnv
	getenv func(string) string
}

func (h inboxWebhookHandler) get(ctx *fasthttp.RequestCtx) {
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, h.info(ctx))
}

func (h inboxWebhookHandler) info(ctx *fasthttp.RequestCtx) InboxWebhookInfo {
	out := InboxWebhookInfo{URL: ingestURL(ctx), Hosted: h.env.Services.Hosted}
	out.Example = "curl -X POST " + out.URL + ` -H "Authorization: Bearer $` + DefaultInboxTokenEnv + `" -H "Content-Type: application/json" --data @session.json`
	if out.Hosted {
		// Gap G02: the hosted server takes pushes per org, on a URL with the
		// org in its path, behind the org's own ingest tokens
		// (handlers/inboxingest.go). Whether a token exists is the token
		// list's business; the page reads it from GET /inbox/tokens.
		orgID := h.env.Services.OrgID
		if o, ok := tenancy.OrgFrom(ctx); ok {
			orgID = o.ID
		}
		if orgID == "" {
			out.Problem = "this request is not resolved to an organization"
			out.Next = "sign in and pick an organization"
			return out
		}
		out.URL = orgIngestURL(ctx, orgID)
		out.Example = ingestExample(out.URL)
		out.Configured, out.Enabled, out.IngestTokens = true, true, true
		out.TokenEnv = DefaultInboxTokenEnv
		out.RatePerMinute, out.MaxBytes = traceinbox.DefaultRate, traceinbox.DefaultMaxBody
		out.Next = "generate an inbox token below (org admins), export it as " + DefaultInboxTokenEnv + " where your tool runs and push with the curl line"
		return out
	}
	if h.env.Mode != schemas.ModeLive {
		out.Problem = "the server runs in mode " + string(h.env.Mode) + "; the webhook needs a live server"
		out.Next = "start the server without -demo"
		return out
	}
	root := ""
	if h.env.Cfg != nil {
		root = h.env.Cfg.Root
	}
	cfg, err := traceinbox.LoadConfig(filepath.Join(root, traceinbox.Dir, traceinbox.SourcesFile))
	if err != nil {
		out.Problem = err.Error()
		out.Next = "fix inbox/sources.yaml (Files page), then restart the server"
		return out
	}
	wh, ok := cfg.Webhook()
	if !ok {
		out.TokenEnv = DefaultInboxTokenEnv
		out.Next = "add the webhook source below to inbox/sources.yaml, set " + DefaultInboxTokenEnv + " on the server and restart it"
		out.SourcesSnippet = "sources:\n  - name: webhook\n    kind: webhook\n    token_env: " + DefaultInboxTokenEnv + "\n"
		return out
	}
	out.Configured, out.Source, out.TokenEnv = true, wh.Name, wh.TokenEnv
	out.RatePerMinute, out.MaxBytes = wh.RatePerMinute, wh.MaxBytes
	out.TokenSet = h.getenv(wh.TokenEnv) != ""
	out.Example = strings.Replace(out.Example, "$"+DefaultInboxTokenEnv, "$"+wh.TokenEnv, 1)
	if !out.TokenSet {
		out.Next = "set " + wh.TokenEnv + " on the server (use Generate token below) and restart it"
		return out
	}
	out.Enabled = true
	return out
}

// ingestURL is the ingest endpoint as the caller reached this server,
// honouring a proxy's X-Forwarded-Proto and X-Forwarded-Host.
func ingestURL(ctx *fasthttp.RequestCtx) string {
	scheme := "http"
	if ctx.IsTLS() {
		scheme = "https"
	}
	if p := strings.ToLower(strings.TrimSpace(string(ctx.Request.Header.Peek("X-Forwarded-Proto")))); p == "http" || p == "https" {
		scheme = p
	}
	host := string(ctx.Host())
	if fh := strings.TrimSpace(string(ctx.Request.Header.Peek("X-Forwarded-Host"))); fh != "" && validHost(fh) {
		host = fh
	}
	if host == "" || !validHost(host) {
		host = "localhost"
	}
	return scheme + "://" + host + APIPrefix + "/inbox/ingest"
}

// validHost admits host[:port] characters only, so a crafted header cannot
// put markup or a path into the URL the page shows.
func validHost(h string) bool {
	if len(h) > 255 {
		return false
	}
	for _, c := range h {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '-', c == ':', c == '[', c == ']':
		default:
			return false
		}
	}
	return true
}
