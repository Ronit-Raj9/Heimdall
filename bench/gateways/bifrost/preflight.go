package bifrost

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Manifest setting keys. The egress gate reads egress_probe and eval_network.
const (
	SettingEgressProbe  = "egress_probe"
	SettingEvalNetwork  = "eval_network"
	SettingMaxRetries   = "max_retries"
	SettingStoreRaw     = "store_raw_request_response"
	SettingBaseURL      = "base_url"
	SettingAutoToolOff  = "mcp_disable_auto_tool_inject"
	SettingAuthRequired = "enforce_auth_on_inference"
	SettingLogging      = "enable_logging"
	SettingContentLogs  = "content_logging"
	SettingDirectKeys   = "allow_direct_keys"
	SettingKeySlots     = "key_slots"
	SettingMode         = "mode" // proxied | direct, per provider

	egressBlocked = "blocked"
	egressOpen    = "open"
	probeTimeout  = 60 * time.Second
)

// egressScript tries DNS, a public TCP address and the cloud metadata
// address, 3 seconds each, and prints one "<check> open|blocked" line per
// try. It runs inside HelperImage on the evaluation network.
const egressScript = `if getent hosts example.com >/dev/null 2>&1; then echo "dns open"; else echo "dns blocked"; fi
python3 -c '
import socket
for host, port in (("1.1.1.1", 443), ("169.254.169.254", 80)):
    s = socket.socket()
    s.settimeout(3)
    try:
        s.connect((host, port))
        print("tcp:%s:%d open" % (host, port))
    except Exception:
        print("tcp:%s:%d blocked" % (host, port))
    finally:
        s.close()
'`

// egressChecks are the lines egressScript must print for a complete probe.
var egressChecks = []string{"dns", "tcp:1.1.1.1:443", "tcp:169.254.169.254:80"}

// Preflight implements schemas.Gateway. It records the version, the sha256 of
// the canonical /api/config, the loaded plugins and the settings the bench
// relies on, the hygiene record (checkHygiene) and the global drop counter as
// the experiment's baseline, and probes egress from the evaluation network.
// Anything wrong is a Problem in the manifest; a hygiene violation is also
// one, and the planner refuses it. Only a cancelled context is an error.
func (g *Gateway) Preflight(ctx context.Context) (*schemas.GatewayManifest, error) {
	m := &schemas.GatewayManifest{Name: Name, Settings: map[string]string{}, CheckedAt: time.Now().UTC(),
		Hygiene: &schemas.GatewayHygiene{}}
	problem := func(format string, args ...any) { m.Problems = append(m.Problems, fmt.Sprintf(format, args...)) }
	hygiene := &hygieneFindings{h: m.Hygiene}
	var health struct {
		Status string `json:"status"`
	}
	if err := g.call(ctx, http.MethodGet, "/health", nil, &health); err != nil {
		problem("health: %v", err)
	} else if health.Status != "ok" {
		problem("health status is %q", health.Status)
	}
	if err := g.call(ctx, http.MethodGet, "/api/version", nil, &m.Version); err != nil {
		problem("version: %v", err)
	}
	g.checkConfig(ctx, m, problem)
	var plugins struct {
		Plugins []string `json:"plugins"`
	}
	pluginsErr := g.call(ctx, http.MethodGet, pathPluginsLoaded, nil, &plugins)
	if pluginsErr == nil {
		m.Plugins = append([]string{}, plugins.Plugins...)
		sort.Strings(m.Plugins)
	}
	g.checkProviders(ctx, m, hygiene, problem)
	g.checkHygiene(ctx, m, hygiene, m.Plugins, pluginsErr)
	for _, v := range m.Hygiene.Violations {
		problem("hygiene: %s", v)
	}
	if n, err := g.DroppedLogs(ctx); err != nil {
		problem("dropped log counter: %v", err)
	} else {
		m.DroppedLogs = &n
	}
	g.probeEgress(ctx, m, problem)
	if ctx.Err() != nil {
		return m, fmt.Errorf("gateway preflight: %w", ctx.Err())
	}
	return m, nil
}

// checkConfig hashes /api/config and checks the client settings of DESIGN
// 13.5: auto tool injection off, auth on inference, logging with content on,
// no direct provider keys from clients.
func (g *Gateway) checkConfig(ctx context.Context, m *schemas.GatewayManifest, problem func(string, ...any)) {
	raw, err := g.callRaw(ctx, http.MethodGet, "/api/config", nil)
	if err != nil {
		problem("config: %v", err)
		return
	}
	var cfg struct {
		Client map[string]any `json:"client_config"`
	}
	var whole any
	if err := json.Unmarshal(raw, &whole); err != nil {
		problem("config: decode: %v", err)
		return
	}
	if sum, err := utils.HashJSON(whole); err == nil {
		m.ConfigSHA256 = sum
	} else {
		problem("config: hash: %v", err)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil || cfg.Client == nil {
		problem("config: no client_config")
		return
	}
	checks := []struct {
		setting, field string
		want, invert   bool
	}{
		{SettingAutoToolOff, "mcp_disable_auto_tool_inject", true, false},
		{SettingAuthRequired, "enforce_auth_on_inference", true, false},
		{SettingLogging, "enable_logging", true, false},
		{SettingContentLogs, "disable_content_logging", true, true},
		{SettingDirectKeys, "allow_direct_keys", false, false},
	}
	for _, c := range checks {
		v, ok := cfg.Client[c.field].(bool)
		if !ok {
			v = false // absent booleans are false in Bifrost's client config
		}
		if c.invert {
			v = !v
		}
		m.Settings[c.setting] = strconv.FormatBool(v)
		if v != c.want {
			problem("%s is %t, want %t", c.setting, v, c.want)
		}
	}
}

// checkProviders checks every provider. The first provider's settings are
// also recorded under the unprefixed keys, which older manifests used.
func (g *Gateway) checkProviders(ctx context.Context, m *schemas.GatewayManifest, hygiene *hygieneFindings, problem func(string, ...any)) {
	for i, p := range g.providers {
		set := func(key, value string) {
			m.Settings[p.Name+"."+key] = value
			if i == 0 {
				m.Settings[key] = value
			}
		}
		g.checkProvider(ctx, p, set, hygiene, problem)
	}
}

// checkProvider checks one provider's base URL, retries and raw storage, and
// that every key slot exists, and adds its injected tools and key aliases to
// the hygiene record.
func (g *Gateway) checkProvider(ctx context.Context, p ProviderSetup, set func(string, string), hygiene *hygieneFindings, problem func(string, ...any)) {
	set(SettingMode, p.Mode)
	var cur providerConfig
	if err := g.call(ctx, http.MethodGet, providerPath(p.Name), nil, &cur); err != nil {
		problem("provider %s: %v (run Setup)", p.Name, err)
		hygiene.violate("provider %s could not be read: %v", p.Name, err)
		return
	}
	n := cur.NetworkConfig
	set(SettingBaseURL, n.BaseURL)
	set(SettingMaxRetries, strconv.Itoa(n.MaxRetries))
	set(SettingStoreRaw, strconv.FormatBool(cur.StoreRaw))
	if want := g.baseURL(p); n.BaseURL != want {
		problem("provider base_url is %q, want %q (%s, %s)", n.BaseURL, want, p.Name, p.Mode)
	}
	if n.MaxRetries != g.cfg.MaxRetries {
		problem("provider max_retries is %d, want %d (%s)", n.MaxRetries, g.cfg.MaxRetries, p.Name)
	}
	if !cur.StoreRaw {
		problem("provider %s store_raw_request_response is off; rows cannot be joined to the ledger", p.Name)
	}
	var keys struct {
		Keys []providerKey `json:"keys"`
	}
	if err := g.call(ctx, http.MethodGet, providerPath(p.Name)+"/keys", nil, &keys); err != nil {
		problem("provider %s keys: %v", p.Name, err)
		hygiene.violate("provider %s keys could not be read: %v", p.Name, err)
		return
	}
	checkProviderHygiene(hygiene, p.Name, cur, p.Slots, keys.Keys)
	have := map[string]bool{}
	for _, k := range keys.Keys {
		have[k.Name] = k.Enabled == nil || *k.Enabled
	}
	var missing []string
	for _, s := range p.Slots {
		if !have[s.Name] {
			missing = append(missing, s.Name)
		}
	}
	set(SettingKeySlots, strconv.Itoa(len(p.Slots)-len(missing)))
	if len(missing) > 0 {
		problem("provider %s key slots missing or disabled: %s", p.Name, strings.Join(missing, ", "))
	}
}

// probeEgress runs egressScript in a short-lived HelperImage container on
// the evaluation network. The result is "blocked" only when every check
// failed; a probe that could not run, or printed too little, is "open" with
// a problem, because isolation that was not shown is not assumed.
func (g *Gateway) probeEgress(ctx context.Context, m *schemas.GatewayManifest, problem func(string, ...any)) {
	m.Settings[SettingEvalNetwork] = g.cfg.EvalNetwork
	m.Settings[SettingEgressProbe] = egressOpen
	if g.cfg.EvalNetwork == "" || g.cfg.HelperImage == "" {
		problem("egress probe not run: eval network and helper image are required")
		return
	}
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	args := append([]string{"run", "--rm", "--pull", "never", "--network", g.cfg.EvalNetwork,
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--pids-limit", "64", "--memory", "128m",
		"--entrypoint", "sh"}, utils.LabelArgs("", "")...)
	args = append(args, g.cfg.HelperImage, "-c", egressScript)
	res, err := g.cfg.Docker.Run(pctx, args...)
	if err != nil {
		problem("egress probe: %v", err)
		return
	}
	results := parseEgress(string(res.Stdout))
	var open, missing []string
	for _, c := range egressChecks {
		switch results[c] {
		case egressBlocked:
		case egressOpen:
			open = append(open, c)
		default:
			missing = append(missing, c)
		}
	}
	switch {
	case len(missing) > 0:
		problem("egress probe incomplete (exit %d), no result for %s: %s", res.ExitCode,
			strings.Join(missing, ", "), snippet(res.Stderr))
	case len(open) > 0:
		problem("egress from %s is open: %s", g.cfg.EvalNetwork, strings.Join(open, ", "))
	default:
		m.Settings[SettingEgressProbe] = egressBlocked
	}
}

// parseEgress reads "<check> open|blocked" lines.
func parseEgress(out string) map[string]string {
	res := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && (f[1] == egressOpen || f[1] == egressBlocked) {
			res[f[0]] = f[1]
		}
	}
	return res
}
