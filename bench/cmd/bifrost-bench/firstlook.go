package main

// `heimdall first-look` (lane Z02, audit/INFRA_I2_AGENT_ONBOARDING.md
// 4.11): a 5 task by 1 trial evaluation on the fastest benchmark the agent
// fits, with the estimate shown first. It uses the server's first_look
// preset (Z01: burst slots, reference row) when the server has it.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/maximhq/bifrost/bench/connectlocal"
)

const firstLookUsage = `usage: bifrost-bench first-look -agent <id> [-model m] [-benchmark id] [-yes] [-no-wait] [-json] [-url server]

Runs a first look: 5 tasks by 1 trial on the fastest benchmark the agent
fits (by the server's estimate), on the organization's own provider keys.
Shows the estimate and asks first; prints the report card link. A first look
is too small for a verdict and is never a release gate. A pull agent must be
served (heimdall serve) while it runs.`

func init() {
	register("first-look", "a 5 task by 1 trial first evaluation of an agent on its fastest benchmark: first-look -agent <id>", runFirstLook)
}

type firstLookOptions struct {
	agent     string
	model     string
	benchmark string
	yes       bool
	asJSON    bool
	wait      bool
}

func runFirstLook(args []string) int {
	var o firstLookOptions
	var server string
	noWait := false
	fs := flag.NewFlagSet("first-look", flag.ContinueOnError)
	fs.StringVar(&o.agent, "agent", "", "agent id (default .heimdall/agent.yaml's)")
	fs.StringVar(&o.model, "model", "", "model (default the agent's own)")
	fs.StringVar(&o.benchmark, "benchmark", "", "benchmark to use instead of the fastest one")
	fs.BoolVar(&o.yes, "yes", false, "start without asking")
	fs.BoolVar(&noWait, "no-wait", false, "start it and print the link; do not wait for the report card")
	fs.BoolVar(&o.asJSON, "json", false, "print the result as JSON")
	fs.StringVar(&server, "url", "", "Heimdall server (env HEIMDALL_URL, else your login)")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, firstLookUsage)
		return exitUsage
	}
	o.wait = !noWait
	if o.agent == "" {
		if pf, _, err := connectlocal.ReadProject("."); err == nil {
			o.agent = pf.Agent
		}
	}
	if o.agent == "" {
		fmt.Fprintln(os.Stderr, firstLookUsage)
		return exitUsage
	}
	srv, tok, _ := connectlocal.Resolve(server, "")
	if srv == "" || tok == "" {
		return fail("first-look: no Heimdall login: run `heimdall login -url https://your-heimdall` (or set HEIMDALL_URL and HEIMDALL_TOKEN)")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runFirstLookFlow(ctx, newOnboardAPI(srv, tok), srv, o, os.Stderr, map[string]any{})
}

// runFirstLookFlow plans, asks, starts and (with wait) waits for a first look.
func runFirstLookFlow(ctx context.Context, api *onboardAPI, srv string, o firstLookOptions, out io.Writer, result map[string]any) int {
	plan, err := api.planFirstLook(ctx, o.agent, o.model, o.benchmark)
	if err != nil {
		return fail("first look: %v", err)
	}
	result["first_look_plan"] = plan
	if !o.asJSON {
		fmt.Fprintf(out, "first look: %d tasks of %s by %d trial (%s)\n", plan.Tasks, plan.Benchmark, plan.Trials, plan.Why)
		fmt.Fprintf(out, "  estimate: %s, %s; spends on your organization's own provider keys\n",
			estimateRangeText(plan.Minutes, " min", ""), estimateRangeText(plan.CostUSD, "", "$"))
		fmt.Fprintf(out, "  %s\n", plan.Note)
	}
	if !o.yes {
		if !interactive(os.Stdin) {
			fmt.Fprintf(out, "not started: pass -yes to run it without a terminal (heimdall first-look -agent %s -yes)\n", o.agent)
			return printJSONIf(o.asJSON, result)
		}
		fmt.Fprint(out, "Run it? [Y/n] ")
		var ans string
		_, _ = fmt.Fscanln(os.Stdin, &ans)
		if a := strings.ToLower(strings.TrimSpace(ans)); a == "n" || a == "no" {
			fmt.Fprintln(out, "not started")
			return printJSONIf(o.asJSON, result)
		}
	}
	ev, err := api.startFirstLook(ctx, plan, true)
	if err != nil {
		return fail("first look: %v", err)
	}
	id, _ := ev["id"].(string)
	if id == "" {
		return fail("first look: Heimdall returned no evaluation id")
	}
	link := evaluationLink(srv, id)
	result["evaluation"] = ev
	result["report_link"] = link
	if !o.asJSON {
		fmt.Fprintf(out, "started evaluation %s: %s\n", id, link)
	}
	if !o.wait {
		return printJSONIf(o.asJSON, result)
	}
	last := ""
	final, err := api.waitEvaluation(ctx, id, 90*time.Minute, func(ev map[string]any) {
		st, _ := ev["status"].(string)
		if st != last && !o.asJSON {
			fmt.Fprintf(out, "  %s\n", st)
			last = st
		}
	})
	if err != nil {
		if ctx.Err() != nil {
			fmt.Fprintf(out, "stopped waiting; the evaluation goes on: %s\n", link)
			return printJSONIf(o.asJSON, result)
		}
		return fail("first look: %v", err)
	}
	result["evaluation"] = final
	if st, _ := final["status"].(string); st == "done" {
		if rep, err := api.report(ctx, id); err == nil {
			result["report"] = rep
			if !o.asJSON {
				printFirstLookCard(out, rep)
			}
		}
	}
	if !o.asJSON {
		fmt.Fprintf(out, "report card: %s\n", link)
	}
	return printJSONIf(o.asJSON, result)
}

// printFirstLookCard prints the headline of a report card: mode and each
// arm's score with its interval, when the report has them.
func printFirstLookCard(w io.Writer, rep map[string]any) {
	mode, _ := rep["mode"].(string)
	arms, _ := rep["arms"].([]any)
	for _, a := range arms {
		m, ok := a.(map[string]any)
		if !ok {
			continue
		}
		label, _ := m["label"].(string)
		if label == "" {
			label, _ = m["agent"].(string)
		}
		score := m["score"]
		if s, ok := score.(map[string]any); ok {
			fmt.Fprintf(w, "  %s: score %v (95%% interval %v to %v)\n", label, s["value"], s["low"], s["high"])
		} else if score != nil {
			fmt.Fprintf(w, "  %s: score %v\n", label, score)
		}
	}
	if mode != "" && mode != "live" {
		fmt.Fprintf(w, "  mode %s: not a live measurement\n", mode)
	}
}
