package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/faultproxy"
	"github.com/maximhq/bifrost/bench/schemas"
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
	for _, img := range []string{a.cfg.Images.Helper, "bench/agent-claude-code:1"} {
		if res, err := a.docker.Run(ctx, "image", "inspect", "--format", "{{.Id}}", img); err != nil || res.ExitCode != 0 {
			add("image "+img, schemas.DoctorWarn, "missing; run make -C bench images")
		} else {
			add("image "+img, schemas.DoctorPass, short(strings.TrimSpace(string(res.Stdout))))
		}
	}
	if res, err := a.docker.Run(ctx, "network", "inspect", "--format", "{{.Internal}}", a.cfg.Networks.Eval); err != nil || res.ExitCode != 0 {
		add("eval network", schemas.DoctorWarn, a.cfg.Networks.Eval+" does not exist; start the stack")
	} else if strings.TrimSpace(string(res.Stdout)) != "true" {
		add("eval network", schemas.DoctorFail, a.cfg.Networks.Eval+" is not internal")
	} else {
		add("eval network", schemas.DoctorPass, a.cfg.Networks.Eval+" is internal")
	}

	s, err := loadSuite(ctx, a.cfg.Path(a.cfg.BenchmarksRoot), "coding-core")
	switch {
	case err != nil:
		add("suite coding-core", schemas.DoctorFail, err.Error())
	case s.Lock == nil:
		add("suite coding-core", schemas.DoctorFail, "no suite.lock; run suite admit")
	default:
		admitted := 0
		for _, t := range s.Lock.Tasks {
			if t.Status == schemas.TaskAdmitted {
				admitted++
			}
		}
		st := schemas.DoctorPass
		if admitted < len(s.Tasks) {
			st = schemas.DoctorWarn
		}
		add("suite coding-core", st, fmt.Sprintf("%d of %d tasks admitted", admitted, len(s.Tasks)))
	}

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
