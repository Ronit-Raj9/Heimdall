package environments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/simuser"
	"github.com/maximhq/bifrost/bench/utils"
)

// W3 customer support: service gateway access (docs/customer-support.md).
//
// A service whose spec sets gateway (or a role user service under an llm
// user simulator) gets its own virtual key, minted when the service is
// created and never the agent's run key. The service, and only the service,
// receives the gateway's in-network URL, the key and the model in its
// environment, through an env file (never on a docker command line); it
// joins the evaluation network so it can reach the gateway. After the agent
// exits, ExportState reads the key's gateway rows, prices them from the
// pinned snapshot, writes them to state/<service>/_gateway.json with a
// summary in that StateFile's Gateway, and revokes the key; Destroy revokes
// any key still live. The spend is the service's own evidence and is never
// added to the agent's cost.
//
// The agent cannot read the key: it is not in the agent's environment, the
// agent's container has no docker access, and simuser.py never echoes its
// environment. The service shares the evaluation network with agents, so an
// agent could reach the service directly as well as through the tool proxy;
// that was already true on the service network.

// Environment variables a service with gateway access receives.
const (
	EnvServiceGatewayURL = "BENCH_SVC_GATEWAY_URL"
	EnvServiceGatewayKey = "BENCH_SVC_GATEWAY_KEY"
	EnvServiceModel      = "BENCH_SVC_MODEL"
	EnvServiceBudgetUSD  = "BENCH_SVC_BUDGET_USD"
	// ServiceGatewayExport is the export id of a service's gateway rows.
	ServiceGatewayExport = "_gateway.json"
)

const (
	serviceKeySuffixLen = 6
	serviceFetchTimeout = 45 * time.Second
	serviceRevokeWait   = 30 * time.Second
	faultModeNone       = "none"
	faultModeScripted   = "scripted"
)

// ServiceGatewayConfig wires services' own gateway access. Gateway mints and
// revokes the keys and reads their rows; AgentURL is the gateway as a
// container on the evaluation network reaches it (gateway.agent_url); Fault
// binds a plan carrying the key name to the key's slot so the fault proxy
// tags its ledger lines (scripted in a smoke experiment, so a smoke run is
// answered by the scripted model and costs nothing); Price prices the rows
// from the snapshot PriceRef names. Without Gateway or AgentURL a service
// that needs a key is refused, so it never runs without its model.
type ServiceGatewayConfig struct {
	Gateway  schemas.Gateway
	AgentURL string
	Fault    schemas.FaultController
	Price    func(model string, tok schemas.Tokens) (float64, bool)
	PriceRef string
}

// serviceKey is one minted service key of a run.
type serviceKey struct {
	service   string
	spec      schemas.ServiceGateway
	key       *schemas.RunKey
	faultMode string
	done      bool
}

// serviceKeySet is a run's service keys.
type serviceKeySet struct {
	mu   sync.Mutex
	keys []*serviceKey
}

// serviceKeys maps a run id to its *serviceKeySet.
var serviceKeys sync.Map

func runServiceKeys(runID string) *serviceKeySet {
	v, _ := serviceKeys.LoadOrStore(runID, &serviceKeySet{})
	return v.(*serviceKeySet)
}

// serviceGateway is the gateway access spec gets in rc, nil for none.
func serviceGateway(rc *schemas.RunContext, spec schemas.ServiceSpec) *schemas.ServiceGateway {
	return simuser.GatewayFor(spec, simuser.Effective(rc.Suite, rc.Experiment))
}

// smokeRun reports whether rc is a run whose model calls must never reach a
// paid provider: a smoke or scripted_upstream experiment, or a run the engine
// marked (agent check's scripted mode).
func smokeRun(rc *schemas.RunContext) bool {
	return rc.NoPaidTraffic || rc.Experiment.NoPaidTraffic() || (rc.Manifest != nil && rc.Manifest.Experiment.NoPaidTraffic())
}

// mintServiceKey mints the service's own key, refuses the run's key, binds
// the key's fault plan and records the key for export and cleanup.
func (e *dockerWorkspace) mintServiceKey(ctx context.Context, rc *schemas.RunContext, runID string, spec schemas.ServiceSpec, g *schemas.ServiceGateway) (*schemas.RunKey, error) {
	cfg := e.cfg.ServiceGateway
	switch {
	case cfg.Gateway == nil || cfg.AgentURL == "":
		return nil, errors.New("needs gateway access, but the environment is not wired to the gateway (gateway.agent_url)")
	case g.Provider == "" || g.Model == "":
		return nil, errors.New("gateway access needs a provider and a model")
	case g.Provider == simuser.Placeholder || g.Model == simuser.Placeholder:
		return nil, fmt.Errorf("gateway provider and model are still %s: pin them", simuser.Placeholder)
	case g.BudgetUSD < 0:
		return nil, fmt.Errorf("gateway budget_usd %v is negative", g.BudgetUSD)
	case e.cfg.EvalNetwork == "":
		return nil, errors.New("needs gateway access, but there is no evaluation network to reach it on")
	}
	mode := faultModeNone
	if smokeRun(rc) {
		mode = faultModeScripted
		if cfg.Fault == nil {
			return nil, errors.New("a smoke experiment answers service model calls from the scripted model, which needs the fault proxy; refusing to call a real provider")
		}
	}
	name := runID + "s" + strings.TrimPrefix(utils.NewID("s", serviceKeySuffixLen), "s_")
	// The keeper volume carries the key's name as its run label before the
	// key exists, so any bench process's janitor sweeping the shared gateway
	// sees a young "run" of that name and keeps the key (a binary that does
	// not know derived key names would otherwise revoke it mid-run).
	keeper := keeperVolume(name)
	if _, err := e.cfg.Docker.MustRun(ctx, append(append([]string{"volume", "create"}, utils.LabelArgs(rc.Run.ExpID, name)...), keeper)...); err != nil {
		return nil, fmt.Errorf("create the service key's keeper volume: %w", err)
	}
	keyRC := &schemas.RunContext{
		Run:   &schemas.Run{RunID: name, ExpID: rc.Run.ExpID},
		Agent: &schemas.AgentConfig{ID: "service-" + spec.ID, Model: g.Model, Gateway: schemas.GatewayPolicy{Provider: g.Provider}},
	}
	key, err := cfg.Gateway.MintKey(ctx, keyRC)
	if err == nil && key == nil {
		err = errors.New("gateway returned no key")
	}
	if err != nil {
		e.removeKeeper(ctx, keeper)
		return nil, fmt.Errorf("mint the service's key: %w", err)
	}
	if rc.Key != nil && (key.Value == rc.Key.Value || key.ID == rc.Key.ID) {
		return nil, errors.New("gateway returned the run's own key for a service")
	}
	sk := &serviceKey{service: spec.ID, spec: *g, key: key, faultMode: mode}
	set := runServiceKeys(runID)
	set.mu.Lock()
	set.keys = append(set.keys, sk)
	set.mu.Unlock()
	if mode == faultModeScripted && key.SlotAuth == "" {
		// A direct slot holds a real provider key in the gateway and never
		// passes the fault proxy, so no plan could keep it off the provider.
		return nil, fmt.Errorf("key slot %s of provider %s is direct, not proxied: a smoke experiment cannot answer it from the scripted model", key.KeySlot, key.Provider)
	}
	if cfg.Fault != nil {
		if err := cfg.Fault.SetPlan(ctx, key.KeySlot, schemas.FaultPlan{Mode: mode, RunID: name}); err != nil {
			return nil, fmt.Errorf("bind %s plan to the service's key slot %s: %w", mode, key.KeySlot, err)
		}
	}
	e.cfg.Logger.Info("service virtual key minted", "run", runID, "service", spec.ID, "virtual_key_id", key.ID,
		"key_slot", key.KeySlot, "model", g.Model, "fault_mode", mode)
	return key, nil
}

// serviceGatewayEnvFile writes the service's gateway variables to an env
// file and returns its path; the caller removes it.
func serviceGatewayEnvFile(agentURL string, g *schemas.ServiceGateway, key *schemas.RunKey) (string, error) {
	lines := []string{
		EnvServiceGatewayURL + "=" + strings.TrimRight(agentURL, "/"),
		EnvServiceGatewayKey + "=" + key.Value,
		EnvServiceModel + "=" + g.Provider + "/" + g.Model,
	}
	if g.BudgetUSD > 0 {
		lines = append(lines, fmt.Sprintf("%s=%g", EnvServiceBudgetUSD, g.BudgetUSD))
	}
	return writeEnvFile(lines)
}

// prepareServiceGateway mints the key of a service that needs one and
// returns the docker create arguments that hand it over (an env file the
// caller removes after create), or nothing for a service without access.
func (e *dockerWorkspace) prepareServiceGateway(ctx context.Context, rc *schemas.RunContext, runID string, spec schemas.ServiceSpec) ([]string, func(), error) {
	g := serviceGateway(rc, spec)
	if g == nil {
		return nil, func() {}, nil
	}
	key, err := e.mintServiceKey(ctx, rc, runID, spec, g)
	if err != nil {
		return nil, func() {}, err
	}
	f, err := serviceGatewayEnvFile(e.cfg.ServiceGateway.AgentURL, g, key)
	if err != nil {
		return nil, func() {}, err
	}
	cleanup := func() {
		if rerr := os.Remove(f); rerr != nil && !os.IsNotExist(rerr) {
			e.cfg.Logger.Warn("remove service env file", "path", f, "error", rerr)
		}
	}
	return []string{"--env-file", f}, cleanup, nil
}

// connectGatewayNetwork puts a service with gateway access on the
// evaluation network, where the gateway is.
func (e *dockerWorkspace) connectGatewayNetwork(ctx context.Context, rc *schemas.RunContext, spec schemas.ServiceSpec, container string) error {
	if serviceGateway(rc, spec) == nil {
		return nil
	}
	if _, err := e.cfg.Docker.MustRun(ctx, "network", "connect", e.cfg.EvalNetwork, container); err != nil {
		return fmt.Errorf("connect evaluation network for gateway access: %w", err)
	}
	return nil
}

// finishServiceKeys reads each live service key's rows into
// state/<service>/_gateway.json, revokes the key and returns one StateFile
// per key. A key whose rows cannot be read is still revoked; its usage says
// why the spend is unknown.
func (e *dockerWorkspace) finishServiceKeys(ctx context.Context, rc *schemas.RunContext, runID string) ([]schemas.StateFile, error) {
	v, ok := serviceKeys.Load(runID)
	if !ok {
		return nil, nil
	}
	set := v.(*serviceKeySet)
	set.mu.Lock()
	defer set.mu.Unlock()
	var files []schemas.StateFile
	for _, sk := range set.keys {
		if sk.done {
			continue
		}
		usage, rows := e.serviceUsage(ctx, sk)
		e.revokeServiceKey(ctx, sk)
		f, err := writeServiceGateway(rc.Dir, sk.service, usage, rows)
		if err != nil {
			return files, err
		}
		files = append(files, f)
	}
	return files, nil
}

// serviceGatewayDoc is the file written at state/<service>/_gateway.json.
type serviceGatewayDoc struct {
	Usage    schemas.ServiceGatewayUsage `json:"usage"`
	Requests []schemas.GatewayRequest    `json:"requests"`
}

// writeServiceGateway writes the usage and rows and returns their StateFile.
func writeServiceGateway(dir, service string, usage schemas.ServiceGatewayUsage, rows []schemas.GatewayRequest) (schemas.StateFile, error) {
	rel := path.Join(StateFolder, service, ServiceGatewayExport)
	f := schemas.StateFile{Service: service, ID: ServiceGatewayExport, Path: rel, Gateway: &usage}
	out := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return f, fmt.Errorf("create %s: %w", filepath.Dir(out), err)
	}
	for i := range rows {
		rows[i].Raw = nil // request bodies stay in the gateway; the summary and rows are enough
	}
	raw, err := json.MarshalIndent(serviceGatewayDoc{Usage: usage, Requests: rows}, "", " ")
	if err != nil {
		return f, err
	}
	if err := os.WriteFile(out, append(raw, '\n'), 0o644); err != nil {
		return f, fmt.Errorf("write %s: %w", out, err)
	}
	f.SHA256, f.Bytes = utils.SHA256Bytes(append(raw, '\n')), int64(len(raw)+1)
	return f, nil
}

// serviceUsage fetches and prices one key's rows.
func (e *dockerWorkspace) serviceUsage(ctx context.Context, sk *serviceKey) (schemas.ServiceGatewayUsage, []schemas.GatewayRequest) {
	cfg := e.cfg.ServiceGateway
	u := schemas.ServiceGatewayUsage{Provider: sk.spec.Provider, Model: sk.spec.Model, KeyID: sk.key.ID, BudgetUSD: sk.spec.BudgetUSD, PriceRef: cfg.PriceRef}
	if sk.faultMode == faultModeScripted {
		u.FaultMode = faultModeScripted
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), serviceFetchTimeout+5*time.Second)
	defer cancel()
	var ledger []schemas.LedgerEntry
	if cfg.Fault != nil {
		l, err := cfg.Fault.Ledger(cctx, sk.key.Name)
		if err != nil {
			e.cfg.Logger.Warn("service key ledger not read", "service", sk.service, "error", err.Error())
		}
		ledger = l
	}
	log, err := cfg.Gateway.FetchRequests(cctx, sk.key, schemas.FetchExpectation{LedgerEntries: ledger, Timeout: serviceFetchTimeout})
	if err != nil || log == nil {
		reason := "gateway rows not read"
		if err != nil {
			reason += ": " + err.Error()
		}
		u.Reason = reason
		u.USD = schemas.Unknown(reason, schemas.EvidenceGateway)
		return u, nil
	}
	u.Complete, u.Reason = log.Complete, log.Reason
	return priceServiceRows(u, log.Requests, cfg.Price), log.Requests
}

// priceServiceRows counts, sums and prices the message rows of one key. It
// is pure. Spend is unknown when the rows are incomplete, a row has no
// usage, no price snapshot is wired, or the model is not in it.
func priceServiceRows(u schemas.ServiceGatewayUsage, rows []schemas.GatewayRequest, price func(string, schemas.Tokens) (float64, bool)) schemas.ServiceGatewayUsage {
	usd, unknown := 0.0, ""
	if !u.Complete {
		unknown = "gateway rows incomplete"
		if u.Reason != "" {
			unknown += ": " + u.Reason
		}
	}
	for _, r := range rows {
		if r.Kind != "" && r.Kind != "message" {
			continue
		}
		u.Requests++
		if r.Status == "error" {
			u.Errors++
		}
		if r.Tokens == nil {
			if r.Status != "error" && unknown == "" {
				unknown = "a service gateway row carried no usage"
			}
			continue
		}
		u.Tokens.Fresh += r.Tokens.Fresh
		u.Tokens.CacheRead += r.Tokens.CacheRead
		u.Tokens.CacheWrite += r.Tokens.CacheWrite
		u.Tokens.Out += r.Tokens.Out
		switch {
		case price == nil:
			if unknown == "" {
				unknown = "no price snapshot wired to the service gateway"
			}
		default:
			v, ok := price(u.Model, *r.Tokens)
			if !ok && unknown == "" {
				unknown = fmt.Sprintf("service model %s is not in the price snapshot", u.Model)
			}
			usd += v
		}
	}
	if u.FaultMode == faultModeScripted {
		// Answered by the fault proxy's scripted model: nothing was spent,
		// whatever the snapshot says the tokens would cost.
		usd = 0
	}
	if unknown != "" {
		u.USD = schemas.Unknown(unknown, schemas.EvidenceGateway)
		return u
	}
	u.USD = schemas.Observed(usd, schemas.EvidenceGateway)
	u.OverBudget = u.BudgetUSD > 0 && usd > u.BudgetUSD
	return u
}

// revokeServiceKey revokes one key and clears its slot's plan, on a context
// that outlives a cancelled run, and marks it done.
func (e *dockerWorkspace) revokeServiceKey(ctx context.Context, sk *serviceKey) {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), serviceRevokeWait)
	defer cancel()
	cfg := e.cfg.ServiceGateway
	if cfg.Fault != nil {
		if err := cfg.Fault.ClearPlan(rctx, sk.key.KeySlot); err != nil {
			e.cfg.Logger.Warn("service key slot plan not cleared", "service", sk.service, "key_slot", sk.key.KeySlot, "error", err.Error())
		}
	}
	if err := cfg.Gateway.RevokeKey(rctx, sk.key); err != nil {
		e.cfg.Logger.Warn("service key not revoked; the janitor removes it", "service", sk.service, "virtual_key_id", sk.key.ID, "error", err.Error())
	}
	e.removeKeeper(rctx, keeperVolume(sk.key.Name))
	sk.done = true
}

// keeperVolume names the volume that marks a service key as live.
func keeperVolume(keyName string) string { return "bench-svckey-" + keyName }

// removeKeeper removes a keeper volume; a failure is logged, and the
// janitor removes the volume once it is older than its grace period.
func (e *dockerWorkspace) removeKeeper(ctx context.Context, name string) {
	if _, err := e.cfg.Docker.MustRun(context.WithoutCancel(ctx), "volume", "rm", "-f", name); err != nil {
		e.cfg.Logger.Warn("service key keeper volume not removed", "volume", name, "error", err.Error())
	}
}

// revokeServiceKeys revokes every live key of the run and forgets the run.
func (e *dockerWorkspace) revokeServiceKeys(ctx context.Context, runID string) {
	v, ok := serviceKeys.LoadAndDelete(runID)
	if !ok {
		return
	}
	set := v.(*serviceKeySet)
	set.mu.Lock()
	defer set.mu.Unlock()
	for _, sk := range set.keys {
		if !sk.done {
			e.revokeServiceKey(ctx, sk)
		}
	}
}

// liveServiceKeys lists the services of a run whose key is not revoked, for
// tests and diagnostics.
func liveServiceKeys(runID string) []string {
	v, ok := serviceKeys.Load(runID)
	if !ok {
		return nil
	}
	set := v.(*serviceKeySet)
	set.mu.Lock()
	defer set.mu.Unlock()
	var out []string
	for _, sk := range set.keys {
		if !sk.done {
			out = append(out, sk.service)
		}
	}
	sort.Strings(out)
	return out
}
