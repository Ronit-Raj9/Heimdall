package bifrost

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
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
	SettingEgressError  = "egress_probe_error" // why the probe did not run; set only then
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
	egressNotRun  = "not_run"
	probeTimeout  = 60 * time.Second
)

// HelperSelfTestTools are the commands the trusted helper image (images.helper)
// must have: every tool of utils.HelperTools (workspace preparation, diff
// export, the hidden_tests grader, the network observer's python3 and
// sleep) plus getent, which the egress probe resolves a public name with.
// It is the one list the egress probe and doctor's helper tools row check,
// so neither can pass on an image the other would refuse.
var HelperSelfTestTools = append(slices.Clone(utils.HelperTools), "getent")

// HelperSelfTestScript is the canonical helper self-test, a POSIX sh script
// that prints "missing <tool>" for every HelperSelfTestTools entry the image
// lacks, and nothing else.
var HelperSelfTestScript = `for t in ` + strings.Join(HelperSelfTestTools, " ") +
	`; do command -v "$t" >/dev/null 2>&1 || echo "missing $t"; done`

// egressScript tries DNS, a public TCP address and the cloud metadata
// address, 3 seconds each, and prints one "<check> open|blocked" line per
// try. It runs inside HelperImage on the evaluation network. It repeats the
// helper self-test first and stops after it when a tool is missing, so a
// missing getent can never read as "dns blocked". getent exits 2 when the
// name does not resolve, which is the blocked answer; any other failure
// prints "dns error <code>", which is no result, so the probe is incomplete.
var egressScript = `missing=$(` + HelperSelfTestScript + `)
if [ -n "$missing" ]; then echo "$missing"; exit 0; fi
getent hosts example.com >/dev/null 2>&1
rc=$?
case "$rc" in 0) echo "dns open" ;; 2) echo "dns blocked" ;; *) echo "dns error $rc" ;; esac
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

// probeEgress runs the helper self-test (HelperSelfTest) and then
// egressScript in a short-lived HelperImage container on the evaluation
// network. The result is "blocked" only when every check failed and "open"
// when one connected. A probe that could not run, or printed too little, is
// "not_run" with a problem and the reason in SettingEgressError, because
// isolation that was not shown is not assumed and a probe that never ran
// proves nothing either way. A helper image that lacks a tool is named with
// the tool (utils.HelperLacks), never reported blocked or open.
func (g *Gateway) probeEgress(ctx context.Context, m *schemas.GatewayManifest, problem func(string, ...any)) {
	m.Settings[SettingEvalNetwork] = g.cfg.EvalNetwork
	m.Settings[SettingEgressProbe] = egressNotRun
	notRun := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		m.Settings[SettingEgressError] = msg
		problem("%s", msg)
	}
	if g.cfg.EvalNetwork == "" || g.cfg.HelperImage == "" {
		notRun("egress probe not run: eval network and helper image are required")
		return
	}
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	missing, err := HelperSelfTest(pctx, g.cfg.Docker, g.cfg.HelperImage)
	if err != nil {
		notRun("egress probe not run: %v", err)
		return
	}
	if len(missing) > 0 {
		notRun("egress probe not run: %s", utils.HelperLacks(g.cfg.HelperImage, missing...))
		return
	}
	args := append([]string{"run", "--rm", "--pull", "never", "--network", g.cfg.EvalNetwork,
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--pids-limit", "64", "--memory", "128m",
		"--entrypoint", "sh"}, utils.LabelArgs("", "")...)
	args = append(args, g.cfg.HelperImage, "-c", egressScript)
	res, err := g.cfg.Docker.Run(pctx, args...)
	if err != nil {
		notRun("egress probe not run: %v", err)
		return
	}
	if missing := missingTools(res); len(missing) > 0 {
		notRun("egress probe not run: %s", utils.HelperLacks(g.cfg.HelperImage, missing...))
		return
	}
	results := parseEgress(string(res.Stdout))
	var open, noResult []string
	for _, c := range egressChecks {
		switch results[c] {
		case egressBlocked:
		case egressOpen:
			open = append(open, c)
		default:
			noResult = append(noResult, c)
		}
	}
	switch {
	case len(open) > 0:
		m.Settings[SettingEgressProbe] = egressOpen
		problem("egress from %s is open: %s", g.cfg.EvalNetwork, strings.Join(open, ", "))
	case len(noResult) > 0:
		notRun("egress probe incomplete (exit %d), no result for %s: %s", res.ExitCode,
			strings.Join(noResult, ", "), snippet(append(append([]byte(nil), res.Stdout...), res.Stderr...)))
	default:
		m.Settings[SettingEgressProbe] = egressBlocked
	}
}

// HelperSelfTest runs HelperSelfTestScript in image with no network and
// returns the tools the image lacks (missingTools), sh included when the
// image cannot even start it. err is non-nil only when the self-test could
// not say: docker did not run, or the script failed without naming a tool.
func HelperSelfTest(ctx context.Context, d utils.Docker, image string) ([]string, error) {
	args := append([]string{"run", "--rm", "--pull", "never", "--network", "none", "--cap-drop", "ALL",
		"--security-opt", "no-new-privileges", "--pids-limit", "64", "--memory", "128m",
		"--entrypoint", "sh"}, utils.LabelArgs("", "")...)
	res, err := d.Run(ctx, append(args, image, "-c", HelperSelfTestScript)...)
	if err != nil {
		return nil, fmt.Errorf("helper self-test in %s: %w", image, err)
	}
	if missing := missingTools(res); len(missing) > 0 {
		return missing, nil
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("helper self-test in %s exited %d: %s", image, res.ExitCode, snippet(res.Stderr))
	}
	return nil, nil
}

// missingTools returns the helper tools a docker run says are missing: the
// "missing <tool>" lines of the self-test, a shell's "<tool>: not found" or
// "<tool>: command not found", and docker's `exec: "<tool>": executable file
// not found` when the entrypoint itself is absent. Only HelperSelfTestTools
// are recognised, in that order. It is pure.
func missingTools(res *utils.DockerResult) []string {
	named := map[string]bool{}
	for _, t := range utils.MissingTools(string(res.Stdout)) {
		named[t] = true
	}
	out := string(res.Stdout) + "\n" + string(res.Stderr)
	var missing []string
	for _, t := range HelperSelfTestTools {
		q := regexp.QuoteMeta(t)
		re := regexp.MustCompile(`(^|[\s/:])` + q + `: (command )?not found|exec: "` + q + `": executable file not found`)
		if named[t] || re.MatchString(out) {
			missing = append(missing, t)
		}
	}
	return missing
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
