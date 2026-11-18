package environments

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Realism (Eval integrity area, S2; docs/eval-awareness.md). A standard run
// shows the agent the bench's own names: BENCH_* variables, the tool proxy at
// toolproxy, the aux volume at /opt/bench, files under /tmp/bench, the
// wrapper's bench-wrap name and the suite's canary line. A neutral run
// (schemas.EffectiveRealism) shows neutral names instead, at the boundary
// only: the plan the bench records (plan.json), the service handles and
// every bench-side reader keep the standard names, and the agent container
// gets a rewritten copy of the plan. Standard runs go through none of this
// code, so they are byte-identical to runs recorded before it.
const (
	// NeutralToolProxyHost is the tool proxy's host name in a neutral run.
	// The proxy also keeps its toolproxy alias, which the bench itself and
	// team runs use; the agent is only ever told this one.
	NeutralToolProxyHost = "tools.internal"
	// RealismFile records, in the run folder of a neutral run only, the
	// realism and the names the agent saw instead of the bench's.
	RealismFile = utils.RealismFile

	neutralAuxMount   = "/opt/tools"
	neutralLinkMount  = "/mnt/data"
	neutralTmpDir     = "/tmp/task"
	neutralWrapPrefix = "_ENTRY_"
	neutralWrapName   = "sh"
	neutralOTELAttr   = "service.instance.id="
	standardOTELAttr  = "heimdall.run_id="
	neutralEnvPrefix  = "AGENT_"
	standardEnvPrefix = "BENCH_"
)

// neutralEnvNames are the renames that do not follow the BENCH_ to AGENT_
// rule, because the rest of the name is itself a cue.
var neutralEnvNames = map[string]string{
	"BENCH_FAULT_PROTOCOL": "AGENT_HOOK_PROTOCOL",
	"BENCH_FAULT_URL":      "AGENT_HOOK_URL",
	"BENCH_FAULT_TOKEN":    "AGENT_HOOK_TOKEN",
	"BENCH_FAULT_RUN_ID":   "AGENT_HOOK_RUN_ID",
	"BENCH_FAULT_SESSION":  "AGENT_HOOK_SESSION",
	"BENCH_FAULT_HOOKS":    "AGENT_HOOK_HOOKS",
}

var (
	benchEnvRefRe = regexp.MustCompile(`\bBENCH_[A-Z0-9_]*[A-Z0-9]\b`)
	benchTmpRe    = regexp.MustCompile(`/tmp/bench\b`)
	benchAuxRe    = regexp.MustCompile(`/opt/bench/(shim|events|log)\b`)
	proxyHostPort = ToolProxyHost + ":" + strconv.Itoa(ToolProxyPort)
)

// agentView is the set of names one run's agent container is shown.
type agentView struct {
	realism    string
	auxMount   string // where the aux volume is mounted
	linkMount  string // where volumes reached through a link are mounted
	wrapPrefix string // prefix of the wrapper's own variables
	wrapName   string // $0 of the wrapper's sh -c
	otelAttr   string // OTEL_RESOURCE_ATTRIBUTES key of the run id
}

// standardView is what every run used before realism existed.
var standardView = agentView{
	realism: schemas.RealismStandard, auxMount: auxMount, linkMount: benchMount,
	wrapPrefix: wrapPrefix, wrapName: wrapName, otelAttr: standardOTELAttr,
}

// neutralView hides the bench's names.
var neutralView = agentView{
	realism: schemas.RealismNeutral, auxMount: neutralAuxMount, linkMount: neutralLinkMount,
	wrapPrefix: neutralWrapPrefix, wrapName: neutralWrapName, otelAttr: neutralOTELAttr,
}

// viewFor returns the run's view: neutral only when the experiment, or else
// the suite, says neutral. Any other value (the planner refuses unknown
// ones) keeps the standard view.
func viewFor(rc *schemas.RunContext) agentView {
	if rc != nil && schemas.EffectiveRealism(rc.Experiment, rc.Suite) == schemas.RealismNeutral {
		return neutralView
	}
	return standardView
}

func (v agentView) neutral() bool { return v.realism == schemas.RealismNeutral }

// shimDir is the PATH entry of the tool shims.
func (v agentView) shimDir() string { return v.auxMount + "/shim" }

// eventsFile is where the agent's events file is.
func (v agentView) eventsFile() string { return v.auxMount + "/events/events.jsonl" }

// envName is the name the agent sees for a variable the bench sets.
func (v agentView) envName(name string) string {
	if !v.neutral() {
		return name
	}
	return NeutralEnvName(name)
}

// reserved are the container paths a neutral plan may not use, besides the
// standard ones checkAgentPath refuses.
func (v agentView) reserved() []string {
	if !v.neutral() {
		return nil
	}
	return []string{v.auxMount, v.linkMount}
}

// NeutralEnvName is the name a neutral run gives the agent for a variable:
// the table in neutralEnvNames, else BENCH_ replaced by AGENT_, else the name
// itself. Helpers inside agent images read the neutral name first and the
// BENCH_ one second (docs/eval-awareness.md). It is pure.
func NeutralEnvName(name string) string {
	if n, ok := neutralEnvNames[name]; ok {
		return n
	}
	if rest, ok := strings.CutPrefix(name, standardEnvPrefix); ok && rest != "" {
		return neutralEnvPrefix + rest
	}
	return name
}

// NeutralText rewrites one string the agent will see in a neutral run: BENCH_
// variable names, the tool proxy's host name, /tmp/bench and the aux volume
// paths. It is pure.
func NeutralText(s string) string {
	if !strings.Contains(s, "BENCH_") && !strings.Contains(s, ToolProxyHost) && !strings.Contains(s, "/bench") {
		return s
	}
	s = benchEnvRefRe.ReplaceAllStringFunc(s, NeutralEnvName)
	s = strings.ReplaceAll(s, proxyHostPort, NeutralToolProxyHost+":"+strconv.Itoa(ToolProxyPort))
	s = benchTmpRe.ReplaceAllString(s, neutralTmpDir)
	return benchAuxRe.ReplaceAllString(s, neutralAuxMount+"/$1")
}

// StripCanaryLines removes every contamination canary line (utils.CanaryLine
// in any comment form) from text, and the blank line stamping put before it.
// Text without one is returned unchanged. It is pure.
func StripCanaryLines(text string) string {
	if !strings.Contains(text, utils.CanaryMarker) {
		return text
	}
	var b strings.Builder
	removed := false
	for _, line := range strings.SplitAfter(text, "\n") {
		if _, ok := utils.CanaryLineGUID(line); ok {
			removed = true
			continue
		}
		b.WriteString(line)
	}
	out := b.String()
	if !removed {
		return text
	}
	nl := "\n"
	if strings.Contains(text, "\r\n") {
		nl = "\r\n"
	}
	if trimmed := strings.TrimRight(out, "\r\n"); trimmed != "" && strings.HasSuffix(text, "\n") {
		return trimmed + nl
	} else if trimmed != "" {
		return trimmed
	}
	return ""
}

// agentPlan returns the plan as the agent container receives it. The
// standard view returns plan itself. The neutral view returns a copy with
// variables renamed (NeutralEnvName), every argument, value, file path and
// file content passed through NeutralText, canary lines removed from
// arguments and files, and folder paths moved off /tmp/bench. plan is never
// changed: plan.json keeps what the bench planned.
func (v agentView) agentPlan(plan *schemas.RunPlan) (*schemas.RunPlan, error) {
	if !v.neutral() || plan == nil {
		return plan, nil
	}
	out := *plan
	out.Argv = make([]string, len(plan.Argv))
	for i, a := range plan.Argv {
		out.Argv[i] = NeutralText(StripCanaryLines(a))
	}
	if plan.Env != nil {
		out.Env = make(map[string]string, len(plan.Env))
		from := map[string]string{}
		for _, k := range sortedKeys(plan.Env) {
			n := NeutralEnvName(k)
			if prev, taken := from[n]; taken {
				return nil, fmt.Errorf("neutral realism: variables %s and %s both become %s", prev, k, n)
			}
			from[n] = k
			out.Env[n] = NeutralText(plan.Env[k])
		}
	}
	if plan.Files != nil {
		out.Files = make(map[string]string, len(plan.Files))
		for _, p := range sortedKeys(plan.Files) {
			np := NeutralText(p)
			if err := v.checkPath(np); err != nil {
				return nil, err
			}
			out.Files[np] = NeutralText(StripCanaryLines(plan.Files[p]))
		}
	}
	if plan.Dirs != nil {
		out.Dirs = make(map[string]string, len(plan.Dirs))
		for _, p := range sortedKeys(plan.Dirs) {
			np := NeutralText(p)
			if err := v.checkPath(np); err != nil {
				return nil, err
			}
			out.Dirs[np] = plan.Dirs[p]
		}
	}
	out.Workdir = NeutralText(plan.Workdir)
	return &out, nil
}

// checkPath refuses a plan path inside one of the view's own mounts.
func (v agentView) checkPath(p string) error {
	for _, r := range v.reserved() {
		if p == r || strings.HasPrefix(p, r+"/") {
			return fmt.Errorf("neutral realism: plan path %q is inside %s, which the bench reserves", p, r)
		}
	}
	return nil
}

// writeRealism records a neutral run's view in the run folder, so reports can
// label the run and say what the agent saw. Standard runs write nothing. A
// failure is returned: an unlabelled neutral run would read as standard.
func (v agentView) writeRealism(dir string, plan *schemas.RunPlan, env []string) error {
	if !v.neutral() || dir == "" {
		return nil
	}
	rec := utils.RealismRecord{Realism: v.realism, ToolHost: NeutralToolProxyHost, AuxMount: v.auxMount, LinkMount: v.linkMount, CanaryHidden: true,
		Note: "the agent saw these neutral names; plan.json keeps the bench's own names"}
	names := map[string]string{}
	if plan != nil {
		for k := range plan.Env {
			if n := NeutralEnvName(k); n != k {
				names[k] = n
			}
		}
	}
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		for _, std := range []string{"BENCH_EVENTS_FILE", "BENCH_EVENTS_URL"} {
			if k == NeutralEnvName(std) {
				names[std] = k
			}
		}
	}
	if len(names) > 0 {
		rec.EnvNames = names
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", RealismFile, err)
	}
	p := filepath.Join(dir, RealismFile)
	if err := os.WriteFile(p, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", p, err)
	}
	return nil
}

// linkTarget is where the view mounts a linked volume: kind is "memory" or
// "dirs".
func (v agentView) linkTarget(kind string, i int) string {
	return path.Join(v.linkMount, kind, strconv.Itoa(i))
}

// proxyAliasArgs are the docker create arguments that name the tool proxy
// (or the collector that stands in for it) on the service network: alias and
// host name toolproxy in a standard run; in a neutral run the host name
// tools.internal and both aliases, because the bench's own containers and
// team runs still reach it as toolproxy.
func proxyAliasArgs(rc *schemas.RunContext) []string {
	if !viewFor(rc).neutral() {
		return []string{"--network-alias", ToolProxyHost, "--hostname", ToolProxyHost}
	}
	return []string{"--network-alias", ToolProxyHost, "--network-alias", NeutralToolProxyHost, "--hostname", NeutralToolProxyHost}
}

// procWatchCommand is the end of the process observer's docker create: the
// entrypoint, image and arguments. The observer shares the agent's PID
// namespace, so its command line is what the agent sees in ps. Its script
// always arrives on stdin, so no file name shows; a standard run passes the
// poll interval as an argument ("python3 -u - 20"), a neutral run passes it
// in the environment, which the agent user cannot read, and shows only
// "python3 -u -".
func procWatchCommand(v agentView, image string, intervalMS int) []string {
	if !v.neutral() {
		return []string{"--entrypoint", "python3", image, "-u", "-", strconv.Itoa(intervalMS)}
	}
	return []string{"-e", procWatchIntervalEnv + "=" + strconv.Itoa(intervalMS), "--entrypoint", "python3", image, "-u", "-"}
}

// procWatchIntervalEnv carries the poll interval of a neutral run's process
// observer (procwatch.py reads it when argv has none).
const procWatchIntervalEnv = "POLL_MS"

// neutralVolumePrefix starts a neutral run's volume names. Volume names show
// in the agent's /proc/self/mountinfo (/var/lib/docker/volumes/<name>/_data),
// so a neutral run's volumes are vol-ws-<run>, vol-aux-<run>, ... instead of
// bench-ws-<run>, bench-aux-<run>, ... Both carry the run's labels, which is
// what the janitor and Destroy find them by.
const neutralVolumePrefix = "vol-"

// workspaceVolume is the run's workspace volume name in this view.
func (v agentView) workspaceVolume(runID string) string {
	if !v.neutral() {
		return workspaceVolume(runID)
	}
	return neutralVolumePrefix + "ws-" + runID
}

// auxVolume is the run's aux volume name in this view.
func (v agentView) auxVolume(runID string) string {
	if !v.neutral() {
		return auxVolume(runID)
	}
	return neutralVolumePrefix + "aux-" + runID
}

// dirVolume is the volume of plan folder i in this view.
func (v agentView) dirVolume(runID string, i int) string {
	if !v.neutral() {
		return dirVolume(runID, i)
	}
	return neutralVolumePrefix + "dir-" + runID + "-" + strconv.Itoa(i)
}

// memoryVolumes are the run's memory volumes in this view: /memory first,
// then one per memory path.
func (v agentView) memoryVolumes(runID string, paths int) []string {
	out := memoryVolumes(runID, paths)
	if !v.neutral() {
		return out
	}
	for i, name := range out {
		out[i] = neutralVolumePrefix + strings.TrimPrefix(name, "bench-")
	}
	return out
}

// Docker names of a neutral run that the agent can read: reverse DNS of the
// agent's own address answers <container>.<network>, so the agent container
// and the service network get neutral names. The tool proxy's container keeps
// bench-tp-<run>, because the tool fault probe layer finds it by that name.
const (
	neutralAgentPrefix   = "ws-"
	neutralNetworkPrefix = "net-"
)

// neutralRuns marks the runs of this process whose realism is neutral, by
// run id. The name helpers that take only a run id (agentContainer,
// serviceNetwork) read it, so every caller, the team and mailbox code
// included, agrees on a run's names. runIDs marks a run on every entry point
// that has its run context; Destroy forgets it.
var neutralRuns sync.Map

// markRealism records a neutral run in neutralRuns.
func markRealism(rc *schemas.RunContext, runID string) {
	if viewFor(rc).neutral() {
		neutralRuns.Store(runID, true)
	}
}

// neutralRun reports whether runID is a neutral run of this process.
func neutralRun(runID string) bool {
	_, ok := neutralRuns.Load(runID)
	return ok
}
