package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Team runs (docs/multi-agent.md). An agent config (or a task) that declares
// team runs one container per member, all on the run's networks with the
// shared workspace, each with its own gateway key minted on a key slot of
// its own and its own tool proxy route, plus the team mailbox. A run with no
// team never reaches anything in this file.

// memberIDRe is the rule for team member ids: they name containers, keys and
// run folder files.
var memberIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// keySlotCounter is a gateway that says how many key slots a provider has
// (gateways/bifrost KeySlots). A team needs one slot per member.
type keySlotCounter interface {
	KeySlots(provider string) int
}

// errNoTeamGateway is a team run on a gateway that cannot mint member keys.
var errNoTeamGateway = errors.New("the gateway cannot mint team member keys (schemas.TeamGateway)")

// ValidateTeam checks a team: at least two members, valid unique ids, no
// sessions. It is pure.
func ValidateTeam(team []schemas.TeamMember, task *schemas.Task) error {
	if len(team) == 0 {
		return nil
	}
	if len(team) < 2 {
		return errors.New("team: a team needs at least two members")
	}
	seen := map[string]bool{}
	for _, m := range team {
		if !memberIDRe.MatchString(m.ID) {
			return fmt.Errorf("team: member id %q must match %s", m.ID, memberIDRe)
		}
		if seen[m.ID] {
			return fmt.Errorf("team: member id %q is used twice", m.ID)
		}
		seen[m.ID] = true
		if m.BudgetUSD < 0 {
			return fmt.Errorf("team: member %s has a negative budget", m.ID)
		}
	}
	if task != nil && len(task.Sessions) > 0 {
		return errors.New("team: team runs do not support multi-session tasks yet")
	}
	return nil
}

// resolveTeam freezes each member's agent config into the arm config at plan
// time, so the member configs are hashed with the arm: a member that names
// another config (a path relative to the bench root) gets that config with
// its overrides; a member that names none runs the arm's own config.
func (p *Planner) resolveTeam(ctx context.Context, cfg *schemas.AgentConfig, suite *schemas.Suite) error {
	if len(cfg.Team) == 0 {
		return nil
	}
	if err := ValidateTeam(cfg.Team, nil); err != nil {
		return err
	}
	if sc, ok := schemas.GatewayAs[keySlotCounter](p.reg.Gateway); ok {
		if n := sc.KeySlots(cfg.Gateway.Provider); len(cfg.Team) > n {
			return fmt.Errorf("team: %d members need %d gateway key slots, the gateway has %d for provider %q (raise gateway.key_slots, docs/multi-agent.md)",
				len(cfg.Team), len(cfg.Team), n, cfg.Gateway.Provider)
		}
	}
	team := make([]schemas.TeamMember, len(cfg.Team))
	for i, m := range cfg.Team {
		if m.Agent != "" && m.Config == nil {
			raw, err := os.ReadFile(p.abs(m.Agent))
			if err != nil {
				return fmt.Errorf("team member %s: read agent config: %w", m.ID, err)
			}
			mc, err := ResolveAgentConfig(raw, nil)
			if err != nil {
				return fmt.Errorf("team member %s: agent config %s: %w", m.ID, m.Agent, err)
			}
			if len(mc.Team) > 0 {
				return fmt.Errorf("team member %s: agent config %s declares a team of its own", m.ID, m.Agent)
			}
			if err := p.checkAgent(&mc); err != nil {
				return fmt.Errorf("team member %s: agent config %s: %w", m.ID, m.Agent, err)
			}
			img, err := ResolveAgentImage(&mc, suite, p.benchRoot)
			if err != nil {
				return fmt.Errorf("team member %s: agent config %s: %w", m.ID, m.Agent, err)
			}
			mc.Image = img.Tag
			m.Config = &mc
		}
		team[i] = m
	}
	cfg.Team = team
	return nil
}

// memberConfig is the config a member runs: its resolved config, else a
// copy of the arm's; then its model, argv and env overrides. The copy has no
// team, so a member never starts a team of its own.
func memberConfig(arm *schemas.AgentConfig, m schemas.TeamMember) schemas.AgentConfig {
	var cfg schemas.AgentConfig
	if m.Config != nil {
		cfg = *m.Config
	} else if arm != nil {
		cfg = *arm
	}
	cfg.Team, cfg.Limits = nil, nil
	if m.Model != "" {
		cfg.Model = m.Model
	}
	if len(m.Argv) > 0 {
		cfg.Argv = append([]string(nil), m.Argv...)
	}
	env := make(map[string]string, len(cfg.Env)+len(m.Env))
	for k, v := range cfg.Env {
		env[k] = v
	}
	for k, v := range m.Env {
		env[k] = v
	}
	cfg.Env = env
	if arm != nil && cfg.ID == "" {
		cfg.ID = arm.ID
	}
	return cfg
}

// newRouteToken is a member's tool proxy route token: 128 random bits.
func newRouteToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("route token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// prepareTeam validates a team run and gives every member its route token,
// before the services (and the tool proxy that reads the tokens) start.
func (p *Pipeline) prepareTeam(st *runState) error {
	team := utils.TeamOf(st.rc)
	if len(team) == 0 {
		return nil
	}
	if err := ValidateTeam(team, st.rc.Task); err != nil {
		return err
	}
	if _, ok := st.env.(schemas.TeamEnvironment); !ok {
		return wrapPart("environment", st.env, errors.New("the environment cannot run teams (schemas.TeamEnvironment)"))
	}
	st.rc.TeamRoutes = map[string]string{}
	for _, m := range team {
		tok, err := newRouteToken()
		if err != nil {
			return err
		}
		st.rc.TeamRoutes[m.ID] = tok
	}
	return nil
}

// mintTeam mints one gateway key per member on a slot of its own and binds
// the run's empty fault plan to every member slot, so the fault proxy tags
// each member's calls with the run.
func (p *Pipeline) mintTeam(ctx context.Context, st *runState) error {
	team := utils.TeamOf(st.rc)
	if len(team) == 0 {
		return nil
	}
	tg, ok := schemas.GatewayAs[schemas.TeamGateway](p.reg.Gateway)
	if !ok {
		return wrapPart("gateway", p.reg.Gateway, errNoTeamGateway)
	}
	keys, err := tg.MintMemberKeys(ctx, st.rc, st.rc.Key, team)
	if err != nil {
		return wrapPart("gateway", p.reg.Gateway, fmt.Errorf("mint member keys: %w", err))
	}
	st.rc.Key.Members = keys
	if p.reg.Fault == nil {
		return nil
	}
	// The lead's slot is the run slot, which bindFaultPlan (or a gateway
	// probe's Arm) owns; every other member slot gets the run's plan, so the
	// fault proxy tags each member's calls with the run. A gateway probe
	// arms its own plan over these when it arms.
	for _, k := range keys {
		if k.KeySlot == st.rc.Key.KeySlot {
			continue
		}
		plan := schemas.FaultPlan{Mode: p.runFaultMode(st.rc), RunID: st.rc.Run.RunID}
		if plan.Mode == faultModeScripted {
			if err := directSlot(&schemas.RunKey{KeySlot: k.KeySlot, SlotAuth: k.SlotAuth}); err != nil {
				return &partError{part: faultPart, err: err}
			}
		}
		if err := p.reg.Fault.SetPlan(ctx, k.KeySlot, plan); err != nil {
			return &partError{part: faultPart, err: fmt.Errorf("bind %s plan to member %s slot %s: %w", plan.Mode, k.AgentID, k.KeySlot, err)}
		}
	}
	return nil
}

// clearTeamPlans clears the fault plan of every member slot at cleanup.
func (p *Pipeline) clearTeamPlans(ctx context.Context, st *runState) error {
	if p.reg.Fault == nil || st.rc.Key == nil {
		return nil
	}
	var errs []error
	for _, k := range st.rc.Key.Members {
		if k.KeySlot == st.rc.Key.KeySlot {
			continue // the run slot: cleanup clears it
		}
		if err := p.reg.Fault.ClearPlan(ctx, k.KeySlot); err != nil {
			errs = append(errs, &partError{part: faultPart, err: fmt.Errorf("clear plan of member %s slot %s: %w", k.AgentID, k.KeySlot, err)})
		}
	}
	return errors.Join(errs...)
}

// runTeam plans every member with the arm's adapter, on a copy of the run
// context that holds the member's config, key and routes, then runs them
// together through the environment's TeamEnvironment and writes team.json.
func (p *Pipeline) runTeam(ctx context.Context, st *runState) error {
	rc := st.rc
	team := utils.TeamOf(rc)
	te, ok := st.env.(schemas.TeamEnvironment)
	if !ok {
		return wrapPart("environment", st.env, errors.New("the environment cannot run teams (schemas.TeamEnvironment)"))
	}
	keys := map[string]schemas.MemberKey{}
	if rc.Key != nil {
		for _, k := range rc.Key.Members {
			keys[k.AgentID] = k
		}
	}
	runs := make([]schemas.MemberRun, 0, len(team))
	for _, m := range team {
		k, ok := keys[m.ID]
		if !ok {
			return wrapPart("gateway", p.reg.Gateway, fmt.Errorf("member %s has no key", m.ID))
		}
		plan, err := p.planMember(ctx, st, m, k, team)
		if err != nil {
			return err
		}
		runs = append(runs, schemas.MemberRun{Member: m, Plan: plan, Key: k, RouteToken: rc.TeamRoutes[m.ID]})
	}
	st.plan = runs[0].Plan
	run := rc.Run
	started := p.now().UTC()
	run.Timing.AgentStarted = &started
	if err := p.writeJSON(ctx, st, "run.json", run); err != nil {
		run.Timing.AgentStarted = nil
		return fmt.Errorf("checkpoint before team start: %w", err)
	}
	st.agentStarted = true
	results, err := te.ExecTeam(ctx, rc, runs)
	p.finishTeam(st, results)
	if werr := p.writeJSON(ctx, st, schemas.TeamFile, teamRecord(team, rc.Key, results)); werr != nil {
		err = errors.Join(err, werr)
	}
	if err != nil {
		return wrapPart("environment", st.env, fmt.Errorf("exec team: %w", err))
	}
	if err := memberFailure(results); err != nil {
		return wrapPart("environment", st.env, err)
	}
	return nil
}

// memberFailure is the first member that did not run or whose run ended in
// an infrastructure error (output capture, exit code inspection). Such a
// member's exec result is not a trustworthy agent outcome, so the run must
// not be graded as counted. It is pure.
func memberFailure(results []schemas.MemberResult) error {
	for _, r := range results {
		if r.Exec == nil {
			return fmt.Errorf("team member %s did not run: %s", r.AgentID, r.Error)
		}
		if r.Error != "" {
			return fmt.Errorf("team member %s: %s", r.AgentID, r.Error)
		}
	}
	return nil
}

// planMember builds one member's plan: the adapter's plan for the member's
// config and key, the scenario env on the member's routes, and the team
// variables.
func (p *Pipeline) planMember(ctx context.Context, st *runState, m schemas.TeamMember, k schemas.MemberKey, team []schemas.TeamMember) (*schemas.RunPlan, error) {
	mrc := *st.rc
	cfg := memberConfig(st.rc.Agent, m)
	mrc.Agent = &cfg
	key := schemas.RunKey{ID: k.ID, Name: k.Name, Value: k.Value, KeySlot: k.KeySlot, SlotAuth: k.SlotAuth}
	if st.rc.Key != nil {
		key.Provider, key.Model = st.rc.Key.Provider, st.rc.Key.Model
	}
	mrc.Key = &key
	token := st.rc.TeamRoutes[m.ID]
	mrc.Services = make([]schemas.ServiceHandle, len(st.rc.Services))
	for i, h := range st.rc.Services {
		h.URL = utils.MemberRouteURL(h.URL, token)
		mrc.Services[i] = h
	}
	if v, ok := st.adapter.(schemas.AdapterValidator); ok {
		if err := v.Validate(ctx, &mrc); err != nil {
			return nil, wrapPart("adapter", st.adapter, fmt.Errorf("validate member %s: %w", m.ID, err))
		}
	}
	var plan *schemas.RunPlan
	var err error
	if runner, ok := st.adapter.(schemas.AdapterRunner); ok {
		plan, err = runner.BuildExecutionPlan(ctx, &mrc)
	} else {
		plan, err = st.adapter.Plan(ctx, &mrc)
	}
	if err != nil {
		return nil, wrapPart("adapter", st.adapter, fmt.Errorf("plan member %s: %w", m.ID, err))
	}
	if plan == nil {
		return nil, wrapPart("adapter", st.adapter, fmt.Errorf("plan member %s: no plan", m.ID))
	}
	env, err := scenarioEnv(&mrc)
	if err != nil {
		return nil, err
	}
	if plan.Env == nil {
		plan.Env = map[string]string{}
	}
	for k, v := range env {
		plan.Env[k] = v
	}
	for k, v := range teamEnv(m, team, token) {
		plan.Env[k] = v
	}
	return plan, nil
}

// teamEnv is what every member's container learns about its team. It is
// pure.
func teamEnv(m schemas.TeamMember, team []schemas.TeamMember, token string) map[string]string {
	role := m.Role
	if role == "" {
		role = schemas.AgentRoleWorker
	}
	mailbox := "http://toolproxy:9200/s/" + schemas.MailboxServer + "/"
	return map[string]string{
		utils.EnvAgentID:    m.ID,
		utils.EnvAgentRole:  role,
		utils.EnvTeam:       strings.Join(utils.TeamIDs(team), ","),
		utils.EnvMailboxURL: utils.MemberRouteURL(mailbox, token),
	}
}

// finishTeam records the team's timing and a combined exec result: the
// earliest start, the latest end, the first non-zero member exit, a timeout
// when any member timed out, and the lead's (else the first member's) output
// as the run's stdout, the whole team's in agent.stream.jsonl.
func (p *Pipeline) finishTeam(st *runState, results []schemas.MemberResult) {
	x := combineResults(results)
	if x == nil {
		return
	}
	run := st.rc.Run
	st.rc.Exec, st.ev.Exec = x, x
	code := x.ExitCode
	run.Agent.ExitCode, run.Agent.TimedOut = &code, x.TimedOut
	if !x.Started.IsZero() {
		s := x.Started.UTC()
		run.Timing.AgentStarted = &s
	}
	e := x.Ended.UTC()
	if x.Ended.IsZero() {
		e = p.now().UTC()
	}
	run.Timing.AgentExited = &e
}

// combineResults folds the members' results into one ExecResult; nil when no
// member ran. It is pure.
func combineResults(results []schemas.MemberResult) *schemas.ExecResult {
	var out *schemas.ExecResult
	for _, r := range results {
		if r.Exec == nil {
			continue
		}
		x := r.Exec
		if out == nil {
			c := *x
			out = &c
			out.StdoutPath = teamStream(x.StdoutPath, false)
			out.StderrPath = teamStream(x.StderrPath, true)
			continue
		}
		if !x.Started.IsZero() && (out.Started.IsZero() || x.Started.Before(out.Started)) {
			out.Started = x.Started
		}
		if x.Ended.After(out.Ended) {
			out.Ended = x.Ended
		}
		if out.ExitCode == 0 && x.ExitCode != 0 {
			out.ExitCode = x.ExitCode
		}
		out.TimedOut = out.TimedOut || x.TimedOut
		out.Truncated = out.Truncated || x.Truncated
	}
	return out
}

// teamStream is the run's whole-team output file beside a member's.
func teamStream(memberPath string, stderr bool) string {
	if memberPath == "" {
		return ""
	}
	dir := memberPath[:strings.LastIndex(memberPath, "/")+1]
	if stderr {
		return dir + "agent.stderr.txt"
	}
	return dir + "agent.stream.jsonl"
}

// teamRecord is team.json: the members, their key ids and slots (never key
// values), and how each container ended.
func teamRecord(team []schemas.TeamMember, key *schemas.RunKey, results []schemas.MemberResult) schemas.TeamRecord {
	rec := schemas.TeamRecord{Members: team, Results: results}
	if key != nil {
		rec.Keys = key.Members
	}
	return rec
}
