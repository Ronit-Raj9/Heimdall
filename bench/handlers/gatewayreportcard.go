package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/gwfeature"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// The Gateway report card (audit/W4_DESIGN_3_BIFROST.md section 17.3): one
// line per Bifrost feature with whether it is in use and its newest measured
// effect, or an explicit unknown.

// GatewayReportCard is GET /gateway/report-card.
type GatewayReportCard struct {
	Gateway   GatewayInfo       `json:"gateway"`
	Source    GatewaySourceInfo `json:"source"` // the answer of GET /gateway/source
	Lines     []GatewayCardLine `json:"lines"`
	CheckedAt time.Time         `json:"checked_at"`
}

// GatewayInfo is the run gateway as the report card saw it.
type GatewayInfo struct {
	Configured bool     `json:"configured"`
	Host       string   `json:"host,omitempty"`
	Version    string   `json:"version,omitempty"` // GET /api/version
	Reachable  bool     `json:"reachable"`
	Error      string   `json:"error,omitempty"` // plain words, never a URL path or token
	Plugins    []string `json:"plugins"`         // GET /api/plugins/loaded
}

// GatewayCardLine is one feature's line.
type GatewayCardLine struct {
	Feature       string            `json:"feature"`
	Name          string            `json:"name"`
	Enabled       string            `json:"enabled"`        // on | off | unknown
	EnabledSource string            `json:"enabled_source"` // where "enabled" was read, plain words
	State         string            `json:"state"`          // measured | not_enough_data | not_applied | unknown | not_measured
	Result        string            `json:"result,omitempty"`
	Headline      string            `json:"headline"`
	Source        *gwfeature.Source `json:"source,omitempty"`
	ExpID         string            `json:"exp_id,omitempty"`
	MeasuredAt    *time.Time        `json:"measured_at,omitempty"`
	Start         string            `json:"start,omitempty"`   // feature id the "Start a test" button drafts; empty when none
	Blocked       string            `json:"blocked,omitempty"` // plain words: why the test cannot run here and what to do; empty when it can
	Older         *GatewayCardOlder `json:"older,omitempty"`   // an older test's result that still holds, when the newest has none
}

// Report card line values.
const (
	cardOn          = "on"
	cardOff         = "off"
	cardUnknown     = "unknown"
	cardNotMeasured = "not_measured"
	cardNoAnswer    = "the gateway did not answer"
	cardNotMeasHead = "Not tested yet"
)

// Report card ids of the lines that are not feature tests.
const (
	cardNewModel     = "new_model"
	cardAgentGateway = "agent_gateway"
	cardBatch        = "batch"
)

// cardWatchState and cardWatchItem are this file's copy of the new model
// watch state (schedule.WatchState, audit/W4_DESIGN_3_BIFROST.md 6.4).
type cardWatchState struct {
	Schedule  string                   `json:"schedule"`
	Baseline  map[string][]string      `json:"baseline"`
	Seen      map[string]cardWatchItem `json:"seen"`
	CheckedAt time.Time                `json:"checked_at"`
	LastError string                   `json:"last_error,omitempty"`
}

type cardWatchItem struct {
	Provider     string    `json:"provider"`
	Model        string    `json:"model"`
	FirstSeen    time.Time `json:"first_seen"`
	State        string    `json:"state"`
	EvaluationID string    `json:"evaluation_id,omitempty"`
	Reason       string    `json:"reason,omitempty"`
	ClaimedAt    time.Time `json:"claimed_at"`
}

// heimdallRunClient matches the MCP client names the bench creates for a
// run: "<service>_<run id>" with the run id lowered (bifrost.MCPClientName).
var heimdallRunClient = regexp.MustCompile(`_r_[a-z0-9_]+$`)

// gatewayCardNow is the report card clock; tests may replace it.
var gatewayCardNow = time.Now

func (h *gatewayTestsHandler) reportCard(ctx *fasthttp.RequestCtx) {
	work, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, plainServed(buildGatewayReportCard(work, h.env, h.root(), h.source())))
}

// cardRead is one gateway read: ok false when the gateway did not answer.
type cardRead struct {
	ok     bool
	status int // non-2xx answer status, 0 otherwise
}

func readCard(ctx context.Context, get func(ctx context.Context, path string, out any) error, path string, out any) cardRead {
	if get == nil {
		return cardRead{}
	}
	err := get(ctx, path, out)
	if err == nil {
		return cardRead{ok: true}
	}
	var se *gatewayStatusError
	if errors.As(err, &se) {
		return cardRead{status: se.Status}
	}
	return cardRead{}
}

// countListed counts the items of a list answer: a JSON array, or an object
// whose key holds one.
func countListed(raw json.RawMessage, key string) (int, []json.RawMessage) {
	var arr []json.RawMessage
	if json.Unmarshal(raw, &arr) == nil {
		return len(arr), arr
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) == nil {
		if json.Unmarshal(obj[key], &arr) == nil {
			return len(arr), arr
		}
	}
	return 0, nil
}

func buildGatewayReportCard(ctx context.Context, env OrgEnv, root string, lsrc leaderboard.Source) GatewayReportCard {
	card := GatewayReportCard{Gateway: GatewayInfo{Plugins: []string{}}, Source: gatewaySourceInfo(env, root), Lines: []GatewayCardLine{}, CheckedAt: gatewayCardNow().UTC()}
	run := gatewayRunGet(ctx, env)
	if env.Cfg != nil && strings.TrimSpace(env.Cfg.Gateway.URL) != "" {
		card.Gateway.Configured = true
		card.Gateway.Host = urlHost(env.Cfg.Gateway.URL)
	}
	var plugins struct {
		Plugins []string `json:"plugins"`
	}
	if run != nil {
		r := readCard(ctx, run, "/api/plugins/loaded", &plugins)
		switch {
		case r.ok:
			card.Gateway.Reachable = true
			if plugins.Plugins != nil {
				sort.Strings(plugins.Plugins)
				card.Gateway.Plugins = plugins.Plugins
			}
			var version string
			if readCard(ctx, run, "/api/version", &version).ok {
				card.Gateway.Version = version
			}
		case r.status != 0:
			card.Gateway.Reachable = true
			card.Gateway.Error = fmt.Sprintf("the gateway answered %d to the plugin check", r.status)
		default:
			card.Gateway.Error = "the gateway did not answer"
		}
	}
	pluginsKnown := card.Gateway.Reachable && card.Gateway.Error == ""
	// The org's own Bifrost: the saved source, or the run gateway in local
	// mode. Hosted servers without a saved source read nothing of it.
	src := gatewaySourceGet(ctx, env)

	effects, _ := gwfeature.List(root)
	newest := map[string]gwfeature.Effect{}
	for _, e := range effects { // newest first
		if _, ok := newest[e.Feature]; !ok {
			newest[e.Feature] = e
		}
	}
	picks := cardEffects(ctx, env, lsrc, root)
	line := func(id, name string) GatewayCardLine {
		l := GatewayCardLine{Feature: id, Name: name, Enabled: cardUnknown, State: cardNotMeasured, Headline: cardNotMeasHead}
		if _, ok := gwfeature.Lookup(id); ok {
			l.Start = id
		}
		if p, ok := picks[id]; ok {
			e := p.current
			l.State, l.Result, l.Headline, l.ExpID, l.Older = e.State, e.Result, e.Headline, e.ExpID, p.older
			s := e.Source
			l.Source = &s
			at := p.at.UTC()
			l.MeasuredAt = &at
		} else if e, ok := newest[id]; ok && picks == nil {
			l.State, l.Result, l.Headline, l.ExpID = e.State, e.Result, e.Headline, e.ExpID
			s := e.Source
			l.Source = &s
			at := e.ComputedAt
			l.MeasuredAt = &at
		}
		return l
	}
	byPlugin := func(l GatewayCardLine, plugin string) GatewayCardLine {
		switch {
		case run == nil:
			l.Enabled, l.EnabledSource = cardUnknown, "no gateway is configured"
		case !pluginsKnown:
			l.Enabled, l.EnabledSource = cardUnknown, cardNoAnswer
		case hasPlugin(card.Gateway.Plugins, plugin):
			l.Enabled, l.EnabledSource = cardOn, "the gateway's loaded plugins"
		default:
			l.Enabled, l.EnabledSource = cardOff, "the gateway's loaded plugins"
		}
		return l
	}

	// Complexity router.
	l := line(schemas.GatewayFeatureComplexityRouter, "Send easy tasks to a cheaper model")
	var status struct {
		State string `json:"state"`
	}
	switch r := readCard(ctx, run, "/api/routing/complexity-analyzer-status", &status); {
	case run == nil:
		l.EnabledSource = "no gateway is configured"
	case r.status == 404:
		l.Enabled, l.EnabledSource = cardOff, "the gateway's complexity analyzer status"
	case !r.ok:
		l.EnabledSource = cardNoAnswer
	case strings.EqualFold(status.State, "disabled"):
		l.Enabled, l.EnabledSource = cardOff, "the gateway's complexity analyzer status"
	default:
		l.Enabled, l.EnabledSource = cardOn, "the gateway's complexity analyzer status"
	}
	if l.Enabled == cardOff {
		l.Blocked = "The router plugin is not enabled on this gateway. Set up a classifier in Bifrost's routing settings first."
	}
	card.Lines = append(card.Lines, l)

	l = byPlugin(line(schemas.GatewayFeatureSemanticCache, "Semantic cache"), "semantic_cache")
	if l.Enabled == cardOff {
		l.Blocked = "This gateway has no semantic cache plugin."
	}
	card.Lines = append(card.Lines, l)
	card.Lines = append(card.Lines, byPlugin(line(schemas.GatewayFeaturePromptVersion, "Prompt repository"), "prompts"))

	// Skills repository: only through a connected source.
	l = line(schemas.GatewayFeatureSkillVersion, "Skills repository")
	if src == nil {
		l.EnabledSource = "connect your Bifrost to see this"
	} else {
		var raw json.RawMessage
		if r := readCard(ctx, src, "/api/skills", &raw); r.ok {
			n, _ := countListed(raw, "skills")
			l.Enabled, l.EnabledSource = onOff(n > 0), "the skills in your Bifrost"
		} else {
			l.EnabledSource = cardNoAnswer
		}
	}
	card.Lines = append(card.Lines, l)

	// MCP gateway: a client that is not one of Heimdall's run clients.
	l = line(schemas.GatewayFeatureMCPTools, "MCP gateway and tool filter")
	if src == nil {
		l.EnabledSource = "connect your Bifrost to see this"
	} else {
		var page struct {
			Clients []struct {
				Config struct {
					Name string `json:"name"`
				} `json:"config"`
			} `json:"clients"`
		}
		if r := readCard(ctx, src, "/api/mcp/clients?limit=100&offset=0", &page); r.ok {
			own := false
			for _, c := range page.Clients {
				if !heimdallRunClient.MatchString(strings.ToLower(c.Config.Name)) {
					own = true
				}
			}
			l.Enabled, l.EnabledSource = onOff(own), "the MCP clients in your Bifrost"
		} else {
			l.EnabledSource = cardNoAnswer
		}
	}
	card.Lines = append(card.Lines, l)

	l = line(schemas.GatewayFeatureProviders, "Use the same model from several providers")
	l.EnabledSource = "Heimdall cannot see your production routing"
	if n, ok := cardKeyProviders(ctx, env); ok && n < 2 {
		l.Blocked = "Add a key for a second provider in Settings, Model keys."
	}
	card.Lines = append(card.Lines, l)

	l = line(schemas.GatewayFeatureBudgetCurve, "Budgets")
	l.Enabled, l.EnabledSource = cardOn, "governance is always loaded"
	card.Lines = append(card.Lines, l)

	l = line(schemas.GatewayFeatureOverhead, "Time each plugin adds")
	l.Enabled, l.EnabledSource = cardOn, "every gateway call records it"
	card.Lines = append(card.Lines, l)

	card.Lines = append(card.Lines, newModelLine(root))

	// Agent gateway: only through a connected source.
	l = GatewayCardLine{Feature: cardAgentGateway, Name: "Agent gateway", Enabled: cardUnknown, State: cardNotMeasured, Headline: cardNotMeasHead}
	if src == nil {
		l.EnabledSource = "connect your Bifrost to see this"
	} else {
		var raw json.RawMessage
		if r := readCard(ctx, src, "/api/agents", &raw); r.ok {
			n, _ := countListed(raw, "agents")
			l.Enabled, l.EnabledSource = onOff(n > 0), "the agents registered in your Bifrost"
			l.Headline = fmt.Sprintf("%d registered agents", n)
		} else if r.status == 404 {
			l.Enabled, l.EnabledSource = cardOff, "the agents registered in your Bifrost"
			l.Headline = "0 registered agents"
		} else {
			l.EnabledSource = cardNoAnswer
		}
	}
	card.Lines = append(card.Lines, l)

	card.Lines = append(card.Lines, GatewayCardLine{Feature: cardBatch, Name: "Batch API for judge calls", Enabled: cardOff,
		EnabledSource: "Heimdall's own judge calls", State: cardNotMeasured, Headline: "Heimdall does not use Bifrost's batch API yet"})
	return card
}

// cardKeyProviders counts the providers the org has a usable model key for;
// ok is false when the key store cannot be read.
func cardKeyProviders(ctx context.Context, env OrgEnv) (int, bool) {
	if env.Services.Keys == nil {
		return 0, false
	}
	list, err := env.Services.Keys.List(ctx, env.Services.OrgID, keystore.KindProvider)
	if err != nil {
		return 0, false
	}
	seen := map[string]bool{}
	for _, k := range list {
		if (k.Kind == "" || k.Kind == keystore.KindProvider) && k.Provider != "" && usableKey(k) {
			seen[strings.ToLower(k.Provider)] = true
		}
	}
	return len(seen), true
}

func onOff(on bool) string {
	if on {
		return cardOn
	}
	return cardOff
}

// newModelLine reads every new model watch state file of the org.
func newModelLine(root string) GatewayCardLine {
	l := GatewayCardLine{Feature: cardNewModel, Name: "New model watch", Enabled: cardOff, EnabledSource: "your new model watches", State: cardNotMeasured, Headline: cardNotMeasHead}
	if root == "" {
		return l
	}
	files, _ := filepath.Glob(filepath.Join(root, ".heimdall", "gateway", "model-watch", "*.json"))
	if len(files) == 0 {
		return l
	}
	l.Enabled = cardOn
	var newest *cardWatchItem
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var st cardWatchState
		if json.Unmarshal(data, &st) != nil {
			continue
		}
		for _, it := range st.Seen {
			if newest == nil || it.FirstSeen.After(newest.FirstSeen) {
				c := it
				newest = &c
			}
		}
	}
	if newest != nil {
		l.Headline = fmt.Sprintf("%s found on %s: %s", newest.Model, newest.FirstSeen.UTC().Format("2 Jan"), watchStateWords(newest.State))
	} else {
		l.Headline = "No new model found yet"
	}
	return l
}

func watchStateWords(state string) string {
	switch state {
	case "started":
		return "evaluation started"
	case "claimed":
		return "evaluation starting"
	case "failed":
		return "evaluation could not start"
	case "skipped":
		return "skipped"
	}
	return state
}
