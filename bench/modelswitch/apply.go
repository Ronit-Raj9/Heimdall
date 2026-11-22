package modelswitch

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/maximhq/bifrost/bench/utils"
)

// Apply limits.
const (
	applyTimeout     = 30 * time.Second
	maxGatewayAnswer = 4 << 20
	priorityGap      = 10
)

// ErrGatewayRefused marks a gateway URL or scope the apply path refuses.
var ErrGatewayRefused = errors.New("gateway refused")

// ApplyRequest is POST /evaluations/{id}/model-switch/apply.
type ApplyRequest struct {
	// GatewayURL is the org's own Bifrost (its HTTP API root).
	GatewayURL string `json:"gateway_url"`
	// CredentialSecret names the org secret that holds the gateway's admin
	// credential: "user:password" (sent as Basic) or a session token or
	// API key (sent as Bearer). Empty sends no credential.
	CredentialSecret string `json:"credential_secret,omitempty"`
	// Scope and ScopeID scope every rule; a global scope needs AllowGlobal.
	Scope       string `json:"scope,omitempty"`
	ScopeID     string `json:"scope_id,omitempty"`
	AllowGlobal bool   `json:"allow_global,omitempty"`
	// Enable keeps each rule's measured enabled flag; false (the default)
	// creates every rule disabled, to review in Bifrost and turn on there.
	Enable bool `json:"enable,omitempty"`
	// DryRun lists what would change and writes nothing.
	DryRun bool `json:"dry_run,omitempty"`
}

// ApplyResult is what the apply did (or would do, on a dry run).
type ApplyResult struct {
	Gateway string      `json:"gateway"`
	DryRun  bool        `json:"dry_run"`
	Scope   string      `json:"scope"`
	ScopeID string      `json:"scope_id,omitempty"`
	Steps   []ApplyStep `json:"steps"`
	// Stale are rules with this evaluation's prefix that the new config
	// does not hold; they are left in place, never deleted.
	Stale []string `json:"stale"`
	Notes []string `json:"notes"`
}

// ApplyStep is one rule.
type ApplyStep struct {
	Rule     string `json:"rule"`
	Action   string `json:"action"` // create | update
	RuleID   string `json:"rule_id,omitempty"`
	Priority int    `json:"priority"`
	Enabled  bool   `json:"enabled"`
	Status   string `json:"status"` // planned | done | failed
	Error    string `json:"error,omitempty"`
}

// Gateway is the org's own Bifrost.
type Gateway struct {
	BaseURL    string
	Credential string
	Client     *http.Client
}

// CheckGatewayURL accepts an http(s) URL without credentials; with
// public set (the hosted platform) only a public https host.
func CheckGatewayURL(raw string, public bool) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if raw == "" || len(raw) > 2048 || err != nil || u.Host == "" {
		return nil, fmt.Errorf("%w: give the gateway's http or https URL", ErrGatewayRefused)
	}
	if u.User != nil {
		return nil, fmt.Errorf("%w: put the credential in an org secret, not in the URL", ErrGatewayRefused)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, fmt.Errorf("%w: only http and https gateway URLs are supported", ErrGatewayRefused)
	}
	if !public {
		return u, nil
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("%w: the hosted platform reaches your gateway over https only", ErrGatewayRefused)
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".local") {
		return nil, fmt.Errorf("%w: the hosted platform cannot reach a local host name", ErrGatewayRefused)
	}
	if ip := net.ParseIP(host); ip != nil && blockedIP(ip) {
		return nil, fmt.Errorf("%w: the hosted platform cannot reach a private or local address", ErrGatewayRefused)
	}
	return u, nil
}

func blockedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() ||
		(ip.To4() != nil && ip.To4()[0] == 100 && ip.To4()[1]&0xc0 == 64)
}

// GatewayClient is the HTTP client of the apply path. With public set it
// refuses at dial time any address that resolves to a private or local
// one, so DNS cannot point the hosted server back inside.
func GatewayClient(public bool) *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second}
	if public {
		d.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip == nil || blockedIP(ip) {
				return fmt.Errorf("%w: the host is a private or local address", ErrGatewayRefused)
			}
			return nil
		}
	}
	return &http.Client{
		Timeout: applyTimeout,
		// N104: the dial error names the resolved address; private ones are hidden.
		Transport: utils.HidingAddresses(&http.Transport{DialContext: d.DialContext, Proxy: nil, TLSHandshakeTimeout: 10 * time.Second}),
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse // a redirect could carry the credential elsewhere
		},
	}
}

// storedRule is the part of a Bifrost rule the apply reads.
type storedRule struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Priority int     `json:"priority"`
	Scope    string  `json:"scope"`
	ScopeID  *string `json:"scope_id"`
}

// Apply writes the export's rules to the gateway: every rule is created,
// or updated when a rule of the same name exists in the scope; rules are
// placed after every rule already in the scope (priority above the
// highest), so they never run before the org's own rules; no rule is
// deleted. ex must come from Routing with the same scope.
func (g Gateway) Apply(ctx context.Context, ex Export, req ApplyRequest) (*ApplyResult, error) {
	scope := ex.Options.Scope
	res := &ApplyResult{Gateway: strings.TrimRight(g.BaseURL, "/"), DryRun: req.DryRun, Scope: scope, ScopeID: ex.Options.ScopeID, Steps: []ApplyStep{}, Stale: []string{}, Notes: []string{}}
	if !ex.Ready || len(ex.Config.Governance.RoutingRules) == 0 {
		return nil, fmt.Errorf("%w: the report has no measured rule to apply yet", ErrGatewayRefused)
	}
	if scope == "global" && !req.AllowGlobal {
		return nil, fmt.Errorf("%w: global rules reach every request on the gateway; scope them to a virtual key, team or customer, or confirm allow_global", ErrGatewayRefused)
	}
	q := url.Values{"scope": {scope}}
	if ex.Options.ScopeID != "" {
		q.Set("scope_id", ex.Options.ScopeID)
	}
	var list struct {
		Rules []storedRule `json:"rules"`
	}
	if err := g.call(ctx, http.MethodGet, "/api/routing/rules?"+q.Encode(), nil, &list); err != nil {
		return nil, fmt.Errorf("read the gateway's routing rules: %w", err)
	}
	high, existing := ex.Options.BasePriority-priorityGap, map[string]storedRule{}
	for _, r := range list.Rules {
		high = max(high, r.Priority)
		existing[r.Name] = r
	}
	planned := map[string]bool{}
	prefix := ex.Options.Prefix + "-"
	for i, rule := range ex.Config.Governance.RoutingRules {
		rule.Priority = high + priorityGap + i
		if !req.Enable {
			rule.Enabled = false
		}
		planned[rule.Name] = true
		st := ApplyStep{Rule: rule.Name, Action: "create", Priority: rule.Priority, Enabled: rule.Enabled, Status: "planned"}
		old, update := existing[rule.Name]
		if update {
			st.Action, st.RuleID = "update", old.ID
		}
		if !req.DryRun {
			var err error
			var out struct {
				Rule storedRule `json:"rule"`
			}
			if update {
				err = g.call(ctx, http.MethodPut, "/api/routing/rules/"+url.PathEscape(old.ID), ruleBody(rule), &out)
			} else {
				err = g.call(ctx, http.MethodPost, "/api/routing/rules", ruleBody(rule), &out)
			}
			if err != nil {
				st.Status, st.Error = "failed", err.Error()
			} else {
				st.Status = "done"
				if out.Rule.ID != "" {
					st.RuleID = out.Rule.ID
				}
			}
		}
		res.Steps = append(res.Steps, st)
	}
	for _, r := range list.Rules {
		if strings.HasPrefix(r.Name, prefix) && !planned[r.Name] {
			res.Stale = append(res.Stale, r.Name)
		}
	}
	if len(res.Stale) > 0 {
		res.Notes = append(res.Notes, "Rules from an earlier apply of this report are no longer in it; they were left in place. Delete them in Bifrost if you no longer want them.")
	}
	if !req.Enable {
		res.Notes = append(res.Notes, "Every rule was written disabled. Review them in Bifrost and turn them on there.")
	}
	return res, nil
}

// ruleBody is the create and update body (handlers/routing.go
// CreateRoutingRuleRequest and UpdateRoutingRuleRequest share these
// fields).
func ruleBody(r RoutingRule) map[string]any {
	enabled := r.Enabled
	b := map[string]any{
		"name": r.Name, "description": r.Description, "enabled": &enabled, "cel_expression": r.CelExpression,
		"targets": r.Targets, "scope": r.Scope, "priority": r.Priority,
	}
	if len(r.Fallbacks) > 0 {
		b["fallbacks"] = r.Fallbacks
	}
	if r.ScopeID != "" {
		b["scope_id"] = r.ScopeID
	}
	return b
}

// call sends one request to the gateway and decodes its JSON answer.
func (g Gateway) call(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(g.BaseURL, "/")+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c := strings.TrimSpace(g.Credential); c != "" {
		if strings.Contains(c, ":") {
			req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(c)))
		} else {
			req.Header.Set("Authorization", "Bearer "+c)
		}
	}
	client := g.Client
	if client == nil {
		client = GatewayClient(true)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxGatewayAnswer))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := strings.TrimSpace(string(data))
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
			Message string `json:"message"`
		}
		if json.Unmarshal(data, &e) == nil {
			msg = firstNonEmpty(e.Error.Message, e.Message, msg)
		}
		switch resp.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return fmt.Errorf("the gateway refused the credential (%d): check the admin credential in the org secret", resp.StatusCode)
		}
		return fmt.Errorf("the gateway answered %d: %s", resp.StatusCode, clip(msg, 300))
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("the gateway's answer is not the routing rules JSON: %w", err)
	}
	return nil
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			return x
		}
	}
	return ""
}
