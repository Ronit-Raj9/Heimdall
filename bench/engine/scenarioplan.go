package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// adapterRemoteHTTP is the adapter of agents that are deployed services. It
// mirrors adapters/remotehttp.go, which the engine does not import.
const adapterRemoteHTTP = "remote_http"

// TaskImageEnsurer is implemented by an environment that can make a task's
// own image (Task.Environment) present: it builds the Dockerfile or pulls
// the tag when missing and returns the tag and the image id. A task without
// its own image returns empty strings. The planner records the id in the
// manifest (Manifest.TaskImages).
type TaskImageEnsurer interface {
	EnsureTaskImage(ctx context.Context, s *schemas.Suite, t *schemas.Task) (string, string, error)
}

// checkScenario refuses a plan whose tasks need more than the suite's
// environment can do (services, sessions, a snapshot, an answer export),
// a judged suite without its judge, and a remote agent on a task whose
// output it cannot produce: a remote agent never sits beside the workspace,
// so it runs answer tasks, state tasks only with remote.tools expose (and a
// proxied service or external API to change), artifact tasks only with
// remote.artifacts inline, and never diff tasks (utils.RemoteOutputAllowed).
//
// The main arms are checked only against the main conditions' tasks and the
// compatibility agent only against its own conditions' tasks: those are the
// only combinations the scheduler builds (audit 8-B-10).
func (p *Planner) checkScenario(suite *schemas.Suite, conds, compatConds []condPlan, arms map[string]schemas.ArmManifest, compat *schemas.ArmManifest) error {
	var errs []error
	if utils.Grading(suite) == schemas.GradingJudged && suite.Judge == nil && !utils.VisualJudged(suite) { // X1 vision grading: a rubric's pinned vision judge stands in
		errs = append(errs, fmt.Errorf("suite %s is judged but names no judge", suite.ID))
	}
	env, _ := findPart(p.reg.Environments, suite.Environment)
	var remote []string
	remoteCfg := map[string]*schemas.RemoteAgent{}
	for _, id := range sortedKeys(arms) {
		am := arms[id]
		if isRemote(&am.Config) {
			remote = append(remote, id)
			remoteCfg[id] = am.Config.Remote
		}
	}
	var compatRemote []string
	if compat != nil && isRemote(&compat.Config) {
		compatRemote = []string{CompatArm}
		remoteCfg[CompatArm] = compat.Config.Remote
	}
	for _, id := range usedTasks(append(append([]condPlan(nil), conds...), compatConds...)) {
		t := suite.TaskByID(id)
		if t == nil || env == nil {
			continue
		}
		if missing := envMissing(env, suite, t); len(missing) > 0 {
			errs = append(errs, fmt.Errorf("task %s needs %s, which environment %s cannot provide", id, strings.Join(missing, ", "), env.GetName()))
		}
	}
	for _, id := range usedTasks(conds) {
		if t := suite.TaskByID(id); t != nil {
			errs = append(errs, remoteTaskErrs(suite, t, remote, remoteCfg)...) // Remote coverage (scenarioremote.go)
			errs = append(errs, a2aTaskErrs(suite, t, arms, nil)...)            // B1: a2a arms (a2a.go)
		}
	}
	if compat != nil {
		for _, id := range usedTasks(compatConds) {
			if t := suite.TaskByID(id); t != nil {
				errs = append(errs, remoteTaskErrs(suite, t, compatRemote, remoteCfg)...)
				errs = append(errs, a2aTaskErrs(suite, t, nil, compat)...)
			}
		}
	}
	return errors.Join(errs...)
}

// envMissing lists what task t needs that env does not implement.
func envMissing(env schemas.Environment, suite *schemas.Suite, t *schemas.Task) []string {
	var out []string
	if len(utils.TaskServices(suite, t)) > 0 || len(utils.ExternalAPIs(suite, t)) > 0 { // External APIs area: external APIs need the tool proxy
		if _, ok := env.(schemas.ServiceEnvironment); !ok {
			out = append(out, "services")
		}
	}
	if len(t.Sessions) > 0 {
		if _, ok := env.(schemas.SessionEnvironment); !ok {
			out = append(out, "sessions")
		}
	}
	if utils.VerifierMode(suite) == schemas.VerifierSnapshot {
		if _, ok := env.(schemas.SnapshotEnvironment); !ok {
			out = append(out, "snapshot grading")
		}
	}
	if utils.OutputKind(suite, t) == schemas.OutputAnswer {
		if _, ok := env.(schemas.AnswerEnvironment); !ok {
			out = append(out, "an answer export")
		}
	}
	if utils.OutputKind(suite, t) == schemas.OutputArtifact {
		if _, ok := env.(schemas.ArtifactEnvironment); !ok {
			out = append(out, "an artifact export")
		}
	}
	return out
}

func isRemote(cfg *schemas.AgentConfig) bool {
	return cfg.Adapter == adapterRemoteHTTP || cfg.Remote != nil
}

// taskImages makes every used task's own image present through the suite's
// environment and returns task id -> image id. A task image that cannot be
// built refuses the plan: none of its runs could start. An environment that
// cannot build task images records none and warns; its Create then fails a
// run whose image is missing. The result is nil when no task has its own
// image, so a plan without them hashes as before.
func (p *Planner) taskImages(ctx context.Context, suite *schemas.Suite, conds []condPlan) (map[string]string, error) {
	var need []*schemas.Task
	for _, id := range usedTasks(conds) {
		if t := suite.TaskByID(id); t != nil && t.Environment != nil {
			need = append(need, t)
		}
	}
	if len(need) == 0 || p.deferGateway {
		// A hosted plan (gateway deferred to runners) runs on a coordinator
		// without Docker: the tags are deterministic (utils.TaskImage) and
		// each runner builds or pulls them and records the ids it ran
		// (remoteimages.go), so nothing is inspected here (audit 8-B-06).
		return nil, nil
	}
	env, _ := findPart(p.reg.Environments, suite.Environment)
	ens, ok := any(env).(TaskImageEnsurer)
	if !ok {
		p.logger.Warn("environment cannot build task images; the manifest records none", "suite", suite.ID, "tasks", len(need))
		return nil, nil
	}
	out := map[string]string{}
	var errs []error
	for _, t := range need {
		tag, id, err := ens.EnsureTaskImage(ctx, suite, t)
		if err != nil {
			errs = append(errs, fmt.Errorf("task %s image: %w", t.ID, err))
			continue
		}
		switch {
		case id != "":
			out[t.ID] = id
		case tag != "":
			out[t.ID] = tag
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// scenarioHashes hashes the suite-level scenario inputs a run depends on but
// the suite lock does not pin: services, judge, output, grading and verifier
// mode. Each key is present only when its input is set, and the map is nil
// when none is, so a plan without scenario inputs hashes as before. Skill
// content is hashed with the arm's files (layFiles).
func (p *Planner) scenarioHashes(suite *schemas.Suite) (map[string]string, error) {
	out := map[string]string{}
	var errs []error
	put := func(key string, v any) {
		sum, err := utils.HashJSON(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("hash %s: %w", key, err))
			return
		}
		out[key] = sum
	}
	if len(suite.Services) > 0 {
		put("suite.services", suite.Services)
	}
	if suite.Judge != nil {
		put("suite.judge", suite.Judge)
	}
	if suite.Output != nil {
		put("suite.output", suite.Output)
	}
	if suite.Grading != "" {
		put("suite.grading", suite.Grading)
	}
	if suite.Verifier.Mode != "" {
		put("suite.verifier.mode", suite.Verifier.Mode)
	}
	if len(suite.External) > 0 {
		put("suite.external", suite.External) // External APIs area: suite-level external APIs
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}
