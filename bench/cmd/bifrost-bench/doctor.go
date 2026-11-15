package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/faultproxy"
	"github.com/maximhq/bifrost/bench/gateways/bifrost"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
)

func init() {
	register("doctor", "check docker, images, the gateway, the fault proxy and its upstreams, and the suite lock", runDoctor)
}

func runDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	offline := fs.Bool("offline", false, "skip the gateway and fault proxy checks")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	ctx := context.Background()
	a, err := newApp(ctx, *config, appOptions{gateway: !*offline})
	if err != nil {
		return fail("%v", err)
	}
	ok := true
	for _, c := range a.doctor(ctx) {
		fmt.Printf("%-5s %-22s %s\n", strings.ToUpper(string(c.Status)), c.ID, c.Detail)
		if c.Status == schemas.DoctorFail {
			ok = false
		}
	}
	if !ok {
		return 1
	}
	return exitOK
}

// doctor runs the preflight checks shown by `doctor` and GET /doctor.
func (a *app) doctor(ctx context.Context) []schemas.DoctorCheck {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var out []schemas.DoctorCheck
	add := func(id string, st schemas.DoctorStatus, detail string) {
		out = append(out, schemas.DoctorCheck{ID: id, Status: st, Detail: detail})
	}

	if res, err := a.docker.Run(ctx, "version", "--format", "{{.Server.Version}}"); err != nil || res.ExitCode != 0 {
		add("docker", schemas.DoctorFail, "docker is not reachable")
	} else {
		add("docker", schemas.DoctorPass, "server "+strings.TrimSpace(string(res.Stdout)))
	}
	suites, suiteRows := a.doctorSuites(ctx)
	agents, agentRows := loadDoctorAgents(a.cfg.Root)
	needs, needRows := imageNeeds(a.cfg.Root, a.cfg.Images.Helper, suites, agents)
	for _, n := range needs {
		if res, err := a.docker.Run(ctx, "image", "inspect", "--format", "{{.Id}}", n.tag); err != nil || res.ExitCode != 0 {
			add("image "+n.tag, schemas.DoctorWarn, "missing; "+n.hint)
		} else {
			add("image "+n.tag, schemas.DoctorPass, short(strings.TrimSpace(string(res.Stdout))))
			if n.tag == a.cfg.Images.Helper {
				out = append(out, a.helperToolsCheck(ctx))
			}
		}
	}
	out = append(out, needRows...)
	out = append(out, agentRows...)
	if res, err := a.docker.Run(ctx, "network", "inspect", "--format", "{{.Internal}}", a.cfg.Networks.Eval); err != nil || res.ExitCode != 0 {
		add("eval network", schemas.DoctorWarn, a.cfg.Networks.Eval+" does not exist; start the stack")
	} else if strings.TrimSpace(string(res.Stdout)) != "true" {
		add("eval network", schemas.DoctorFail, a.cfg.Networks.Eval+" is not internal")
	} else {
		add("eval network", schemas.DoctorPass, a.cfg.Networks.Eval+" is internal")
	}
	out = append(out, suiteRows...)
	out = append(out, workerModeCheck(a.cfg.ListenIsLoopback())) // Remote area: worker mode row (worker.go)

	if a.reg.Gateway == nil {
		add("gateway", schemas.DoctorSkip, "offline")
		return out
	}
	if gm, err := a.reg.Gateway.Preflight(ctx); err != nil {
		add("gateway", schemas.DoctorFail, err.Error())
	} else if len(gm.Problems) > 0 {
		add("gateway", schemas.DoctorFail, strings.Join(gm.Problems, "; "))
	} else {
		add("gateway", schemas.DoctorPass, fmt.Sprintf("%s %s, egress %s", gm.Name, gm.Version, gm.Settings["egress_probe"]))
	}
	return append(out, faultProxyChecks(ctx, a.cfg.FaultProxy.ControlURL, os.Getenv("BENCH_FAULTPROXY_TOKEN"))...)
}

// doctorSuites loads every suite of the project (benchmarks/*/suite.yaml, by
// folder name) and returns the loaded ones with one row each: how many tasks
// are admitted, or why the suite cannot be used. A project with no suite
// gets one warning row.
func (a *app) doctorSuites(ctx context.Context) ([]*schemas.Suite, []schemas.DoctorCheck) {
	root := a.cfg.Path(a.cfg.BenchmarksRoot)
	files, err := filepath.Glob(filepath.Join(root, "*", "suite.yaml"))
	if err != nil {
		return nil, []schemas.DoctorCheck{{ID: "suites", Status: schemas.DoctorFail, Detail: err.Error()}}
	}
	if len(files) == 0 {
		return nil, []schemas.DoctorCheck{{ID: "suites", Status: schemas.DoctorWarn,
			Detail: "no suite in " + root + "; create one with bifrost-bench suite init <id> -template <name>"}}
	}
	sort.Strings(files)
	var suites []*schemas.Suite
	var rows []schemas.DoctorCheck
	for _, f := range files {
		id := filepath.Base(filepath.Dir(f))
		s, err := loadSuite(ctx, root, id)
		if err != nil {
			rows = append(rows, schemas.DoctorCheck{ID: "suite " + id, Status: schemas.DoctorFail, Detail: err.Error()})
			continue
		}
		suites = append(suites, s)
		rows = append(rows, suiteRow(s))
		if s.Lock != nil { // Lock fingerprints: drift refuses plans (lockfingerprints.go)
			drift, warns, err := suite.CheckFingerprints(s, lockFingerprintOptions(ctx, a.cfg, a.docker, false))
			rows = append(rows, fingerprintRow(s, drift, warns, err))
		}
	}
	return suites, rows
}

// suiteRow says how many of a suite's tasks the lock admits.
func suiteRow(s *schemas.Suite) schemas.DoctorCheck {
	row := schemas.DoctorCheck{ID: "suite " + s.ID}
	if s.Lock == nil {
		row.Status, row.Detail = schemas.DoctorFail, "no suite.lock; run suite admit "+s.ID
		return row
	}
	admitted := 0
	for _, t := range s.Lock.Tasks {
		if t.Status == schemas.TaskAdmitted {
			admitted++
		}
	}
	row.Status = schemas.DoctorPass
	if admitted < len(s.Tasks) {
		row.Status = schemas.DoctorWarn
	}
	row.Detail = fmt.Sprintf("%d of %d tasks admitted", admitted, len(s.Tasks))
	return row
}

// doctorAgent is one agents/*.yaml file, strictly decoded.
type doctorAgent struct {
	file string // path relative to the project root, agents/<id>.yaml
	cfg  *schemas.AgentConfig
}

// loadDoctorAgents reads every agents/*.yaml of the project the way the
// planner does. A file that does not decode is a warning row: doctor still
// checks the others.
func loadDoctorAgents(root string) ([]doctorAgent, []schemas.DoctorCheck) {
	files, err := filepath.Glob(filepath.Join(root, "agents", "*.yaml"))
	if err != nil {
		return nil, []schemas.DoctorCheck{{ID: "agents", Status: schemas.DoctorFail, Detail: err.Error()}}
	}
	sort.Strings(files)
	var agents []doctorAgent
	var rows []schemas.DoctorCheck
	for _, f := range files {
		rel := filepath.ToSlash(filepath.Join("agents", filepath.Base(f)))
		cfg, err := loadAgentConfig(f)
		if err != nil {
			rows = append(rows, schemas.DoctorCheck{ID: "agent " + rel, Status: schemas.DoctorWarn, Detail: err.Error()})
			continue
		}
		agents = append(agents, doctorAgent{file: rel, cfg: cfg})
	}
	return agents, rows
}

// imageNeed is one image the project needs, with the command that makes it.
type imageNeed struct {
	tag  string
	hint string // how to get the image, shown when it is missing
}

// imageNeeds lists, once each and in this order, the images the project
// uses: the helper (images.helper), each suite's base image, and each
// agent's image, resolved per suite with engine.ResolveAgentImage when the
// agent sets build.base_arg. An agent whose per-suite image cannot be
// resolved is a warning row. It is pure.
func imageNeeds(root, helper string, suites []*schemas.Suite, agents []doctorAgent) ([]imageNeed, []schemas.DoctorCheck) {
	var needs []imageNeed
	var rows []schemas.DoctorCheck
	seen := map[string]bool{}
	need := func(tag, hint string) {
		if tag != "" && !seen[tag] {
			seen[tag] = true
			needs = append(needs, imageNeed{tag: tag, hint: hint})
		}
	}
	helperHint := "run make -C bench images"
	if _, err := os.Stat(filepath.Join(root, "deploy", "helper", "Dockerfile")); err == nil {
		helperHint = "docker build -t " + helper + " deploy/helper"
	}
	need(helper, helperHint)
	for _, s := range suites {
		hint := "build or pull it; suite " + s.ID + " names it as base_image.tag"
		if s.BaseImage.Dockerfile != "" {
			hint = "bifrost-bench suite admit " + s.ID + " builds it from its base_image.dockerfile"
		}
		need(s.BaseImage.Tag, hint)
	}
	for _, ag := range agents {
		if ag.cfg.Build == nil || ag.cfg.Build.BaseArg == "" {
			hint := "build or pull it; " + ag.file + " names it"
			if ag.cfg.Build != nil && ag.cfg.Build.Dockerfile != "" {
				hint = "bifrost-bench agent build " + ag.file
			}
			need(ag.cfg.Image, hint)
			continue
		}
		for _, s := range suites {
			img, err := engine.ResolveAgentImage(ag.cfg, s, root)
			if err != nil {
				rows = append(rows, schemas.DoctorCheck{ID: "agent " + ag.file, Status: schemas.DoctorWarn, Detail: err.Error()})
				continue
			}
			need(img.Tag, "bifrost-bench agent build "+ag.file+" -suite "+s.ID)
		}
	}
	return needs, rows
}

// helperToolsCheck runs the canonical helper self-test (bifrost.HelperSelfTest,
// the same one the egress probe runs before it probes) in the helper image,
// with no network, and fails when a tool the bench runs there is missing. A
// toolchain image set as the helper (a node or golang image) lacks python3
// or getent, which otherwise shows up as an egress probe or a network
// observer that "did not run" with no word about the helper.
func (a *app) helperToolsCheck(ctx context.Context) schemas.DoctorCheck {
	img := a.cfg.Images.Helper
	row := schemas.DoctorCheck{ID: "helper tools"}
	missing, err := bifrost.HelperSelfTest(ctx, a.docker, img)
	if err != nil {
		row.Status, row.Detail = schemas.DoctorFail, err.Error()
		return row
	}
	row.Status, row.Detail = helperToolsResult(img, missing)
	return row
}

// helperToolsResult turns the missing tools into a status and detail.
func helperToolsResult(img string, missing []string) (schemas.DoctorStatus, string) {
	if len(missing) > 0 {
		return schemas.DoctorFail, utils.HelperLacks(img, missing...)
	}
	return schemas.DoctorPass, strings.Join(bifrost.HelperSelfTestTools, ", ") + " present"
}

// faultProxyChecks checks the fault proxy's control API and then, with the
// control token, that the proxy can reach each provider it answers from the
// real API: one row per provider. A healthy proxy is not enough. When the
// proxy cannot resolve or connect to the provider, every model call gets a
// 502 while /health still answers, so only the upstream rows show it.
func faultProxyChecks(ctx context.Context, controlURL, token string) []schemas.DoctorCheck {
	var out []schemas.DoctorCheck
	add := func(id string, st schemas.DoctorStatus, detail string) {
		out = append(out, schemas.DoctorCheck{ID: id, Status: st, Detail: detail})
	}
	c := faultproxy.NewClient(controlURL, faultproxy.WithToken(token))
	healthy := true
	if err := c.Health(ctx); err != nil {
		healthy = false
		add("fault proxy", schemas.DoctorFail, err.Error())
	} else {
		add("fault proxy", schemas.DoctorPass, controlURL)
	}
	switch {
	case token == "":
		add("fault proxy token", schemas.DoctorWarn, "BENCH_FAULTPROXY_TOKEN is not set; plans cannot be armed")
		add("upstreams", schemas.DoctorWarn, "BENCH_FAULTPROXY_TOKEN is not set; upstream check skipped")
	case !healthy:
		add("upstreams", schemas.DoctorSkip, "fault proxy is not reachable")
	default:
		out = append(out, upstreamChecks(ctx, c)...)
	}
	return out
}

// upstreamChecks turns the proxy's upstream report into doctor rows. A proxy
// older than the check gives one warning row, not a failure.
func upstreamChecks(ctx context.Context, c *faultproxy.Client) []schemas.DoctorCheck {
	rep, err := c.CheckUpstreams(ctx)
	switch {
	case errors.Is(err, faultproxy.ErrUpstreamCheckUnsupported):
		return []schemas.DoctorCheck{{ID: "upstreams", Status: schemas.DoctorWarn, Detail: "proxy too old for upstream check"}}
	case err != nil:
		return []schemas.DoctorCheck{{ID: "upstreams", Status: schemas.DoctorFail, Detail: err.Error()}}
	case len(rep.Upstreams) == 0:
		return []schemas.DoctorCheck{{ID: "upstreams", Status: schemas.DoctorSkip,
			Detail: "no provider has a real key in the fault proxy; the scripted model answers"}}
	}
	out := make([]schemas.DoctorCheck, 0, len(rep.Upstreams))
	for _, u := range rep.Upstreams {
		row := schemas.DoctorCheck{ID: "upstream " + u.Provider}
		switch {
		case u.Skipped != "":
			row.Status, row.Detail = schemas.DoctorSkip, u.Host+": "+u.Skipped
		case u.OK:
			row.Status, row.Detail = schemas.DoctorPass, fmt.Sprintf("%s, %.0f ms", u.Host, u.LatencyMS)
		default:
			row.Status, row.Detail = schemas.DoctorFail, u.Error
		}
		out = append(out, row)
	}
	return out
}
