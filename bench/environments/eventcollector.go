package environments

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/bench/agentevents"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Agent events area (docs/agent-events.md). The event collector is part of
// the tool proxy: a run with proxied services reaches it on its tool proxy;
// a run without them gets the same container (bench-tp-<run id>, host name
// toolproxy, port 9200) running the collector alone. The agent also gets an
// events file in the aux volume and may print prefixed stdout lines. None of
// this can fail a run: a collector that does not start leaves the agent's
// reports to the file and stdout channels and is recorded as missing.
const (
	// EventsDir is where the agent's events file lives (aux volume). The
	// helper that seeds the workspace makes it (populateScript, taskimage.go).
	EventsDir = auxMount + "/events"
	// EventsFilePath is BENCH_EVENTS_FILE.
	EventsFilePath = EventsDir + "/events.jsonl"
	// CollectorLedgerFile and CollectorCountsFile are the collector's ledger
	// and counters, copied into the run folder by ExportState.
	CollectorLedgerFile = "agent_events.collector.jsonl"
	CollectorCountsFile = "agent_events.counts.json"
	// CollectorStateFile records whether the collector ran ("on", "missing"
	// with a reason); absent when the collector was switched off.
	CollectorStateFile = "agent_events.collector.json"
	collectorLedger    = "/ledger/" + agentevents.LedgerName
	collectorCounts    = "/ledger/" + agentevents.CounterName
)

// collectorRuns marks the runs whose collector is serving, by run id;
// collectorTried the runs where starting it was attempted.
var collectorRuns, collectorTried sync.Map

func collectorRunning(runID string) bool {
	_, ok := collectorRuns.Load(runID)
	return ok
}

// forgetCollector drops the run's collector bookkeeping once the run is
// destroyed, so a long-lived process does not keep an entry per run.
func forgetCollector(runID string) {
	collectorRuns.Delete(runID)
	collectorTried.Delete(runID)
}

// markCollector records that the run's tool proxy (and so its collector)
// is serving.
func (e *dockerWorkspace) markCollector(runID string) {
	if e.cfg.EventsCollector {
		collectorRuns.Store(runID, true)
	}
}

// startEventCollector starts the collector alone for a run whose tool proxy
// does not run. The service network and probe already exist when the run
// has unproxied services (net true); otherwise they are created here. A
// failure is logged and recorded, never returned.
func (e *dockerWorkspace) startEventCollector(ctx context.Context, rc *schemas.RunContext, expID, runID string, probe string) {
	if !e.cfg.EventsCollector {
		return
	}
	collectorTried.Store(runID, true)
	reason := e.tryStartCollector(ctx, rc, expID, runID, probe)
	if reason != "" {
		e.cfg.Logger.Warn("agent event collector not started; file and stdout channels only", "run", runID, "reason", reason)
		writeCollectorState(rc, "missing", reason)
		return
	}
	collectorRuns.Store(runID, true)
}

func (e *dockerWorkspace) tryStartCollector(ctx context.Context, rc *schemas.RunContext, expID, runID, probe string) string {
	if probe == "" {
		if err := e.createNetworks(ctx, expID, runID, false); err != nil {
			return "service network: " + err.Error()
		}
		p, err := e.startProbe(ctx, rc, expID, runID, false)
		if err != nil {
			return "probe: " + err.Error()
		}
		defer e.removeContainers(ctx, p)
		probe = p
	}
	image := e.toolProxyImage()
	info, err := InspectImage(ctx, e.cfg.Docker, image)
	if err != nil {
		return "tool proxy image (images.toolproxy): " + err.Error()
	}
	name := toolProxyContainer(runID)
	e.removeContainers(ctx, name)
	ledger := toolProxyLedgerVolume(runID)
	if err := e.freshVolume(ctx, ledger, expID, runID); err != nil {
		return err.Error()
	}
	if err := e.ownVolumes(ctx, rc, expID, runID, info.User, ledger); err != nil {
		return err.Error()
	}
	args := append([]string{"create", "--name", name}, utils.LabelArgs(expID, runID)...)
	args = append(args, "--network", serviceNetwork(runID))
	args = append(args, proxyAliasArgs(rc)...) // Eval integrity area (S2): toolproxy, plus tools.internal in a neutral run
	args = append(args,
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--read-only", "--tmpfs", "/tmp:size=16m",
		"--pids-limit", "64", "--memory", "128m", "--log-driver", "json-file", "--log-opt", "max-size=1m",
		"-v", ledger+":/ledger", "--entrypoint", "bifrost-bench", image,
		"eventcollector", "-listen", ":"+strconv.Itoa(ToolProxyPort), "-ledger", collectorLedger)
	if _, err := e.cfg.Docker.MustRun(ctx, args...); err != nil {
		return "create collector: " + err.Error()
	}
	if _, err := e.cfg.Docker.MustRun(ctx, "start", name); err != nil {
		return "start collector: " + err.Error()
	}
	health := schemas.ServiceSpec{ID: ToolProxyHost, Port: ToolProxyPort, Ready: &schemas.ReadyCheck{HTTP: "/healthz", TimeoutS: toolProxyReadyS}}
	if err := e.waitReady(ctx, name, health, probe); err != nil {
		return "collector not ready: " + err.Error()
	}
	return ""
}

// ensureEventCollector starts the collector alone before an agent session
// when nothing started it yet: the run kernel starts services (and so the
// tool proxy) only for a task that has some, so a plain repository task gets
// its collector here, before its first session. A remote agent acts outside
// the bench and reports through its own channel, so it gets none.
func (e *dockerWorkspace) ensureEventCollector(ctx context.Context, rc *schemas.RunContext, plan *schemas.RunPlan, expID, runID string) {
	if !e.cfg.EventsCollector || len(plan.Egress) > 0 || collectorRunning(runID) {
		return
	}
	if _, tried := collectorTried.Load(runID); tried {
		return
	}
	e.startEventCollector(ctx, rc, expID, runID, "")
}

// eventEnv adds the agent event variables to the agent's env lines, each
// only when the agent config did not set it: the events file always, the
// collector URL and the standard OpenTelemetry exporter variables when the
// collector serves.
func (e *dockerWorkspace) eventEnv(rc *schemas.RunContext, runID string, env []string) []string {
	return e.eventEnvView(rc, runID, env, standardView)
}

// eventEnvView is eventEnv with the names, host and events file path of the
// run's view (realism.go): a neutral run's agent gets AGENT_EVENTS_FILE and
// AGENT_EVENTS_URL on the neutral tool host, and no heimdall attribute. The
// standard view gives exactly eventEnv.
func (e *dockerWorkspace) eventEnvView(rc *schemas.RunContext, runID string, env []string, v agentView) []string { // Eval integrity area (S2)
	if !e.cfg.EventsCollector {
		return env
	}
	set := map[string]bool{}
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		set[k] = true
	}
	add := func(k, v string) {
		if !set[k] {
			env = append(env, k+"="+v)
			set[k] = true
		}
	}
	eventsFile, host := EventsFilePath, ToolProxyHost // Eval integrity area (S2): the view's path and host
	if v.neutral() {
		eventsFile, host = v.eventsFile(), NeutralToolProxyHost
	}
	add(v.envName("BENCH_EVENTS_FILE"), eventsFile)
	if !collectorRunning(runID) {
		return env
	}
	base := "http://" + host + ":" + strconv.Itoa(ToolProxyPort)
	add(v.envName("BENCH_EVENTS_URL"), base+agentevents.PathEvents)
	add("OTEL_EXPORTER_OTLP_ENDPOINT", base+"/otlp")
	add("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", base+agentevents.PathOTLP)
	add("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	add("OTEL_TRACES_EXPORTER", "otlp")
	// The collector takes traces only; metrics and logs exporters left on
	// would retry against it for nothing.
	add("OTEL_METRICS_EXPORTER", "none")
	add("OTEL_LOGS_EXPORTER", "none")
	service := "agent"
	if rc != nil && rc.Agent != nil && rc.Agent.ID != "" {
		service = rc.Agent.ID
	}
	add("OTEL_SERVICE_NAME", service)
	add("OTEL_RESOURCE_ATTRIBUTES", v.otelAttr+runID) // Eval integrity area (S2): heimdall.run_id= in a standard run
	return env
}

// copyAgentEvents copies the collector's ledger and counters into the run
// folder, after every agent session (the ledger grows across sessions, so
// the last copy holds them all) and again when a run's state is exported. A
// missing ledger means the agent posted nothing.
func (e *dockerWorkspace) copyAgentEvents(ctx context.Context, runID, dir string) {
	if !collectorRunning(runID) {
		return
	}
	writeCollectorState(&schemas.RunContext{Dir: dir}, "on", "")
	cctx, cancel := context.WithTimeout(ctx, exportTimeout)
	defer cancel()
	for src, dst := range map[string]string{collectorLedger: CollectorLedgerFile, collectorCounts: CollectorCountsFile} {
		if _, problem, err := e.exportPath(cctx, toolProxyContainer(runID), src, filepath.Join(dir, dst), maxLedgerBytes); err != nil || problem != "" {
			e.cfg.Logger.Debug("agent event file not copied", "run", runID, "file", dst, "problem", problem)
		}
	}
}

// writeCollectorState writes agent_events.collector.json into the run folder.
func writeCollectorState(rc *schemas.RunContext, state, reason string) {
	if rc == nil || rc.Dir == "" {
		return
	}
	b := []byte(`{"collector":` + strconv.Quote(state))
	if reason != "" {
		b = append(b, []byte(`,"reason":`+strconv.Quote(reason))...)
	}
	b = append(b, '}', '\n')
	_ = os.WriteFile(filepath.Join(rc.Dir, CollectorStateFile), b, 0o644)
}
