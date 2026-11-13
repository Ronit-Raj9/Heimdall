package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func init() {
	register("doctor", "check docker, images, the gateway, the fault proxy and the suite lock", runDoctor)
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
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(a.cfg.FaultProxy.ControlURL, "/")+"/health", nil)
	if resp, err := http.DefaultClient.Do(req); err != nil {
		add("fault proxy", schemas.DoctorFail, err.Error())
	} else {
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			add("fault proxy", schemas.DoctorFail, resp.Status)
		} else {
			add("fault proxy", schemas.DoctorPass, a.cfg.FaultProxy.ControlURL)
		}
	}
	if os.Getenv("BENCH_FAULTPROXY_TOKEN") == "" {
		add("fault proxy token", schemas.DoctorWarn, "BENCH_FAULTPROXY_TOKEN is not set; plans cannot be armed")
	}
	return out
}
