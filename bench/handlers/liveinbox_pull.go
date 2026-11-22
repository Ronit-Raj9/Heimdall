package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/livescore"
	"github.com/maximhq/bifrost/bench/traceinbox"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/maximhq/bifrost/bench/webhooks"
	"github.com/valyala/fasthttp"
)

// Gap lane G03: hosted live and inbox pulls. A pull only makes HTTP calls,
// so the hosted server runs it in process, under these guardrails:
//   - every URL it reads is public https (checkHostedBaseURL), and every
//     connection is checked again at connect time (webhooks.GuardedDialer),
//     with no redirects followed;
//   - every credential comes from an org secret named by its *_env field
//     (OrgSecretGetenv), never from the server environment;
//   - requests, bytes and time of one pull are capped (HostedPullClient,
//     HostedPullTimeout);
//   - the paid live judge runs only on explicit opt-in and only with the
//     org's own provider key (OrgJudgeKey).
// GET /liveinbox/pull tells the Live and Inbox pages what is missing.

// Hosted pull caps.
const (
	HostedPullTimeout     = 5 * time.Minute
	HostedPullMaxRequests = 2000
	HostedPullMaxBytes    = 256 << 20
	hostedPullReqTimeout  = 60 * time.Second
)

// Next-step links the pages render.
const (
	PullLinkSecrets     = "/settings?tab=secrets"
	PullLinkModels      = "/models"
	PullLinkLiveSetup   = "#live-setup"
	PullLinkInboxSource = "#inbox-source-setup"
)

// ErrPullLimit is returned once a hosted pull used up its request or byte cap.
var ErrPullLimit = errors.New("this pull reached the hosted limit of requests or bytes; pull again to continue from the cursor")

// CheckPullURL refuses a URL a hosted pull may not read: not https, with
// credentials, localhost, or a private, loopback or link-local literal.
func CheckPullURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return fmt.Errorf("%q is not a URL; use a public https URL", raw)
	}
	if u.User != nil {
		return errors.New("the URL carries credentials; store them as an org secret instead")
	}
	if err := checkHostedBaseURL(raw); err != nil {
		return fmt.Errorf("%s is not allowed on the hosted server: use a public https URL (%s)", u.Host, strings.TrimPrefix(err.Error(), "base URL "))
	}
	return nil
}

// HostedPullClient is the HTTP client of one hosted pull: guarded dialer, no
// redirects, at most HostedPullMaxRequests requests and HostedPullMaxBytes
// response bytes in total.
func HostedPullClient() *http.Client {
	tr := &http.Transport{DialContext: webhooks.GuardedDialer(10 * time.Second).DialContext, Proxy: nil,
		TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second, MaxIdleConnsPerHost: 4}
	return &http.Client{Timeout: hostedPullReqTimeout,
		Transport:     &capTransport{next: tr, reqs: HostedPullMaxRequests, bytes: HostedPullMaxBytes},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// capTransport counts requests and response bytes against a shared budget.
type capTransport struct {
	next  http.RoundTripper
	mu    sync.Mutex
	reqs  int
	bytes int64
}

func (t *capTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.mu.Lock()
	if t.reqs <= 0 || t.bytes <= 0 {
		t.mu.Unlock()
		if r.Body != nil {
			_ = r.Body.Close()
		}
		return nil, ErrPullLimit
	}
	t.reqs--
	t.mu.Unlock()
	resp, err := t.next.RoundTrip(r)
	if err != nil {
		return nil, utils.HideAddresses(err) // N104: a dial error names the resolved address; pull errors reach the job log
	}
	resp.Body = &capBody{rc: resp.Body, t: t}
	return resp, nil
}

type capBody struct {
	rc io.ReadCloser
	t  *capTransport
}

func (b *capBody) Read(p []byte) (int, error) {
	b.t.mu.Lock()
	left := b.t.bytes
	b.t.mu.Unlock()
	if left <= 0 {
		return 0, ErrPullLimit
	}
	if int64(len(p)) > left {
		p = p[:left]
	}
	n, err := b.rc.Read(p)
	b.t.mu.Lock()
	b.t.bytes -= int64(n)
	b.t.mu.Unlock()
	return n, err
}

func (b *capBody) Close() error { return b.rc.Close() }

// OrgSecretGetenv resolves a variable name to the org secret of that name
// (keystore KindSecret, name in Label). It never reads the environment.
func OrgSecretGetenv(ctx context.Context, s Services) func(string) string {
	return func(name string) string {
		if s.Keys == nil || name == "" || s.OrgID == "" {
			return ""
		}
		keys, err := s.Keys.List(ctx, s.OrgID, keystore.KindSecret)
		if err != nil {
			return ""
		}
		for _, k := range keys {
			if k.Label == name {
				v, _ := s.Keys.Reveal(ctx, s.OrgID, k.ID)
				return v
			}
		}
		return ""
	}
}

// orgSecretNames lists the org's secret names.
func orgSecretNames(ctx context.Context, s Services) map[string]bool {
	out := map[string]bool{}
	if s.Keys == nil || s.OrgID == "" {
		return out
	}
	keys, err := s.Keys.List(ctx, s.OrgID, keystore.KindSecret)
	if err != nil {
		return out
	}
	for _, k := range keys {
		out[k.Label] = true
	}
	return out
}

// OrgJudgeKey finds the org's own provider key for a judge at judgeURL: a
// provider key labelled keyEnv, else one whose base URL (or its provider's
// default) has the judge URL's host. The judge URL must be public https.
func OrgJudgeKey(ctx context.Context, s Services, judgeURL, keyEnv string) (string, error) {
	if err := CheckPullURL(judgeURL); err != nil {
		return "", fmt.Errorf("judge.url: %w", err)
	}
	if s.Keys == nil || s.OrgID == "" {
		return "", errors.New("no key store on this server: add a provider key in Models & keys")
	}
	ju, _ := url.Parse(judgeURL)
	keys, err := s.Keys.List(ctx, s.OrgID, keystore.KindProvider)
	if err != nil {
		return "", err
	}
	defaults := map[string]string{}
	for _, p := range modelProviders() {
		defaults[p.ID] = p.DefaultBaseURL
	}
	pick := func(match func(k keystore.Key) bool) string {
		for _, k := range keys {
			if k.Status == keystore.StatusInvalid || !match(k) {
				continue
			}
			if v, err := s.Keys.Reveal(ctx, s.OrgID, k.ID); err == nil && v != "" {
				return v
			}
		}
		return ""
	}
	if keyEnv != "" {
		if v := pick(func(k keystore.Key) bool { return k.Label == keyEnv }); v != "" {
			return v, nil
		}
	}
	if v := pick(func(k keystore.Key) bool {
		base := k.BaseURL
		if base == "" {
			base = defaults[k.Provider]
		}
		u, err := url.Parse(base)
		return err == nil && u.Host != "" && strings.EqualFold(u.Hostname(), ju.Hostname())
	}); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("no provider key of this org for the judge at %s: add one in Models & keys (label it %s or give it that base URL)", ju.Hostname(), keyEnv)
}

// PullStep is one thing to do before a pull works.
type PullStep struct {
	Problem string `json:"problem"`
	Action  string `json:"action"`
	// Link is where the page sends the user: /settings?tab=secrets,
	// /models, #live-setup or #inbox-source-setup.
	Link   string `json:"link,omitempty"`
	Secret string `json:"secret,omitempty"`
}

// PullSecret is a credential a pull reads, by name, and whether it is set.
type PullSecret struct {
	Name string `json:"name"`
	For  string `json:"for"`
	Set  bool   `json:"set"`
}

// PullCheck says whether one kind of pull can run now.
type PullCheck struct {
	Ready      bool         `json:"ready"`
	Configured bool         `json:"configured"`
	Secrets    []PullSecret `json:"secrets"`
	Steps      []PullStep   `json:"steps"`
	// Judge is set when livescore/config.yaml has a judge section.
	Judge *PullJudge `json:"judge,omitempty"`
}

// PullJudge is the paid live judge's state.
type PullJudge struct {
	KeyReady bool   `json:"key_ready"`
	Next     string `json:"next,omitempty"`
	Link     string `json:"link,omitempty"`
}

// PullReadiness answers GET /api/bench/liveinbox/pull.
type PullReadiness struct {
	Hosted bool      `json:"hosted"`
	Live   PullCheck `json:"live"`
	Inbox  PullCheck `json:"inbox"`
}

type pullChecker struct {
	ctx    context.Context
	s      Services
	names  map[string]bool
	hosted bool
}

func newPullChecker(ctx context.Context, s Services) *pullChecker {
	pc := &pullChecker{ctx: ctx, s: s, hosted: s.Hosted}
	if s.Hosted {
		pc.names = orgSecretNames(ctx, s)
	}
	return pc
}

func (pc *pullChecker) secret(c *PullCheck, name, what string) {
	if name == "" {
		return
	}
	for _, s := range c.Secrets {
		if s.Name == name {
			return
		}
	}
	set := false
	if pc.hosted {
		set = pc.names[name]
	} else {
		set = os.Getenv(name) != ""
	}
	c.Secrets = append(c.Secrets, PullSecret{Name: name, For: what, Set: set})
	if set {
		return
	}
	if pc.hosted {
		c.Steps = append(c.Steps, PullStep{Problem: what + " needs the secret " + name + ", which this org has not stored",
			Action: "Add an org secret named " + name + " in Settings > Secrets", Link: PullLinkSecrets, Secret: name})
	} else {
		c.Steps = append(c.Steps, PullStep{Problem: what + " needs the variable " + name + ", which is not set on this server",
			Action: "Export " + name + " where heimdall serve runs and restart it", Secret: name})
	}
}

func (pc *pullChecker) url(c *PullCheck, raw, what, link string) {
	if raw == "" {
		c.Steps = append(c.Steps, PullStep{Problem: what + " has no URL", Action: "Set the URL in the setup form", Link: link})
		return
	}
	if pc.hosted {
		if err := CheckPullURL(raw); err != nil {
			c.Steps = append(c.Steps, PullStep{Problem: what + ": " + err.Error(), Action: "Use the public https URL of " + what + " in the setup form", Link: link})
		}
	}
}

// LivePullCheck is what livescore.pull needs for the project at root.
func LivePullCheck(ctx context.Context, s Services, cfg *lib.Config) PullCheck {
	pc := newPullChecker(ctx, s)
	c := PullCheck{Secrets: []PullSecret{}, Steps: []PullStep{}}
	if cfg == nil {
		c.Steps = append(c.Steps, PullStep{Problem: "no project config", Action: "Set up live scoring", Link: PullLinkLiveSetup})
		return c
	}
	lc, err := livescore.LoadConfig(filepath.Join(cfg.Path(livescore.Dir), livescore.ConfigFile))
	if err != nil {
		p := "live scoring is not set up"
		if !errors.Is(err, livescore.ErrNotConfigured) {
			p = err.Error()
		}
		c.Steps = append(c.Steps, PullStep{Problem: p, Action: "Fill the Set up live scoring form", Link: PullLinkLiveSetup})
		return c
	}
	c.Configured = true
	pc.url(&c, firstNonEmptyStr(lc.Source.URL, cfg.Gateway.URL), "the gateway", PullLinkLiveSetup)
	pc.secret(&c, firstNonEmptyStr(lc.Source.TokenEnv, cfg.Gateway.AdminTokenEnv), "the gateway read")
	if j := lc.Judge; j != nil {
		pj := &PullJudge{}
		if pc.hosted {
			if _, err := OrgJudgeKey(ctx, s, j.URL, j.KeyEnv); err != nil {
				pj.Next, pj.Link = err.Error(), PullLinkModels
			} else {
				pj.KeyReady = true
			}
		} else if os.Getenv(j.KeyEnv) != "" {
			pj.KeyReady = true
		} else {
			pj.Next = "export " + j.KeyEnv + " where heimdall serve runs to use the judge"
		}
		c.Judge = pj
	}
	c.Ready = len(c.Steps) == 0
	return c
}

// InboxPullCheck is what inbox.pull needs for the project at root.
func InboxPullCheck(ctx context.Context, s Services, cfg *lib.Config) PullCheck {
	pc := newPullChecker(ctx, s)
	c := PullCheck{Secrets: []PullSecret{}, Steps: []PullStep{}}
	if cfg == nil {
		c.Steps = append(c.Steps, PullStep{Problem: "no project config", Action: "Add a pull source", Link: PullLinkInboxSource})
		return c
	}
	ic, err := traceinbox.LoadConfig(filepath.Join(cfg.Path(traceinbox.Dir), traceinbox.SourcesFile))
	if err != nil {
		c.Steps = append(c.Steps, PullStep{Problem: err.Error(), Action: "Fix inbox/sources.yaml or add a source again with the form", Link: PullLinkInboxSource})
		return c
	}
	n := 0
	for _, src := range ic.Sources {
		what := "source " + src.Name
		switch src.Kind {
		case traceinbox.KindBifrost:
			n++
			pc.url(&c, firstNonEmptyStr(src.URL, cfg.Gateway.URL), what, PullLinkInboxSource)
			pc.secret(&c, firstNonEmptyStr(src.TokenEnv, cfg.Gateway.AdminTokenEnv), what)
		case traceinbox.KindLangfuse:
			n++
			pc.url(&c, src.Host, what, PullLinkInboxSource)
			pc.secret(&c, src.PublicKeyEnv, what)
			pc.secret(&c, src.SecretKeyEnv, what)
		case traceinbox.KindLangSmith:
			n++
			pc.url(&c, src.Host, what, PullLinkInboxSource)
			pc.secret(&c, src.APIKeyEnv, what)
		case traceinbox.KindMaxim: // X23
			n++
			pc.url(&c, firstNonEmptyStr(src.Host, traceinbox.DefaultMaximHost), what, PullLinkInboxSource)
			pc.secret(&c, src.APIKeyEnv, what)
		case traceinbox.KindOTLPDir:
			if !pc.hosted {
				n++
			}
		}
	}
	if n == 0 {
		c.Steps = append(c.Steps, PullStep{Problem: "no source to pull in inbox/sources.yaml",
			Action: "Add a Bifrost, Langfuse, LangSmith or Maxim source", Link: PullLinkInboxSource})
		return c
	}
	c.Configured = true
	c.Ready = len(c.Steps) == 0
	return c
}

func firstNonEmptyStr(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func init() {
	RegisterOrgArea("liveinboxpull", func(env OrgEnv, r *router.Router) {
		r.GET(APIPrefix+"/liveinbox/pull", env.R(func(ctx *fasthttp.RequestCtx) {
			lib.SendJSON(ctx, env.Mode, fasthttp.StatusOK, PullReadiness{Hosted: env.Services.Hosted,
				Live: LivePullCheck(ctx, env.Services, env.Cfg), Inbox: InboxPullCheck(ctx, env.Services, env.Cfg)})
		}))
	})
}
