package main

// Eval integrity area (S2): monitor certification (docs/monitor-certification.md).

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/monitorcert"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/utils"
)

const monitorUsage = `usage: bifrost-bench monitor certify -monitor <config.yaml> -exp <id> [-exp <id>...] [-fpr 0.01,0.001] [-grade-source original] [-attack-axes safety] [-harm-metrics secret_exposed] [-parallel 4] [-no-injection-check] [-config deploy/bench.yaml]
       bifrost-bench monitor list [-config deploy/bench.yaml]
       bifrost-bench monitor show <cert_id> [-json] [-config deploy/bench.yaml]
The certificates root is $` + monitorcert.RootEnv + `, else <bench root>/monitor-certs.`

func init() {
	register("monitor", "certify a monitor or guardrail on labelled sealed runs: monitor certify|list|show", runMonitor)
}

func runMonitor(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, monitorUsage)
		return exitUsage
	}
	switch args[0] {
	case "certify":
		return runMonitorCertify(args[1:])
	case "list":
		return runMonitorList(args[1:])
	case "show":
		return runMonitorShow(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown monitor subcommand %q\n%s\n", args[0], monitorUsage)
		return exitUsage
	}
}

func runMonitorCertify(args []string) int {
	fs := flag.NewFlagSet("monitor certify", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	monitorPath := fs.String("monitor", "", "monitor config (YAML or JSON)")
	var exps expList
	fs.Var(&exps, "exp", "experiment id; repeat or comma-separate for several")
	fprs := fs.String("fpr", "0.01,0.001", "target false-positive rates, comma-separated; the first is the headline")
	gradeSource := fs.String("grade-source", "original", "original or a regrade id")
	axes := fs.String("attack-axes", strings.Join(monitorcert.DefaultAttackAxes, ","), "probe axes whose vetoed runs are attacks")
	harm := fs.String("harm-metrics", strings.Join(monitorcert.DefaultHarmMetrics, ","), "report-only metrics that exclude an unvetoed run under an attack-axis probe when above 0 or unknown")
	parallel := fs.Int("parallel", 4, "runs scored at once")
	noInjection := fs.Bool("no-injection-check", false, "skip the monitor-injection check")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *monitorPath == "" || len(exps) == 0 {
		fmt.Fprintln(os.Stderr, monitorUsage)
		return exitUsage
	}
	targets, err := parseFPRs(*fprs)
	if err != nil {
		return fail("%v", err)
	}
	mon, err := monitorcert.LoadConfig(*monitorPath)
	if err != nil {
		return fail("%v", err)
	}
	cfg, err := loadConfig(*config)
	if err != nil {
		return fail("%v", err)
	}
	logger := utils.NewStdLogger(os.Stderr, false)
	st, err := store.NewFSStore(cfg.Path(cfg.RunsRoot), logger)
	if err != nil {
		return fail("open run store: %v", err)
	}
	certs, err := monitorcert.NewCertStore(monitorcert.DefaultRoot(cfg.Root), st.Root())
	if err != nil {
		return fail("%v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	deps := monitorcert.Deps{
		Runs: st, Probes: monitorcert.ProbeFiles(cfg.Path(cfg.BenchmarksRoot)),
		Runner: monitorcert.RunnerDeps{Docker: utils.Docker{}, Logger: logger},
		Logger: logger, BenchVersion: benchVersion, Mode: cfg.Server.Mode,
	}
	// One certificate per view the monitor config names (default actions).
	for _, view := range mon.Views() {
		c, err := monitorcert.Certify(ctx, deps, mon, monitorcert.Options{
			ExpIDs: splitList(strings.Join(exps, ",")), TargetFPRs: targets, GradeSource: *gradeSource,
			AttackAxes: splitList(*axes), HarmMetrics: append([]string{}, splitList(*harm)...), Parallel: *parallel, InjectionCheck: !*noInjection,
			View: view,
		})
		if errors.Is(err, monitorcert.ErrRefused) {
			fmt.Fprintf(os.Stderr, "refused (view %s): %v\nno certificate written for this view\n", view, err)
			return exitRefused
		}
		if err != nil {
			return fail("view %s: %v", view, err)
		}
		dir, err := certs.Write(c, monitorcert.RenderMarkdown(c))
		if err != nil {
			return fail("%v", err)
		}
		printCertHeadline(c)
		fmt.Printf("certificate %s (view %s)\n", dir, view)
	}
	return exitOK
}

func runMonitorList(args []string) int {
	fs := flag.NewFlagSet("monitor list", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, monitorUsage)
		return exitUsage
	}
	certs, err := openCertStore(*config)
	if err != nil {
		return fail("%v", err)
	}
	list, err := certs.List(context.Background())
	if err != nil {
		return fail("%v", err)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "CREATED\tCERT\tMONITOR\tKIND\tMODE\tEXPERIMENTS\tHONEST\tATTACKS\tHEADLINE")
	for _, s := range list.Certifications {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%d\t%d\t%s\n", s.CreatedAt.Format(time.RFC3339), s.CertID, s.MonitorName,
			s.MonitorKind, s.Mode, strings.Join(s.ExpIDs, ","), s.Honest, s.Attacks, headlineText(s.Headline))
	}
	if err := tw.Flush(); err != nil {
		return fail("%v", err)
	}
	return exitOK
}

func runMonitorShow(args []string) int {
	fs := flag.NewFlagSet("monitor show", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	asJSON := fs.Bool("json", false, "print the certificate JSON instead of the report")
	var id string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		id, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, monitorUsage)
		return exitUsage
	}
	if id == "" && fs.NArg() == 1 {
		id = fs.Arg(0)
	} else if id == "" || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, monitorUsage)
		return exitUsage
	}
	certs, err := openCertStore(*config)
	if err != nil {
		return fail("%v", err)
	}
	ctx := context.Background()
	if *asJSON {
		c, err := certs.Get(ctx, id)
		if err != nil {
			return fail("%v", err)
		}
		b, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			return fail("encode certificate: %v", err)
		}
		fmt.Println(string(b))
		return exitOK
	}
	b, err := certs.Report(ctx, id)
	if err != nil {
		return fail("%v", err)
	}
	fmt.Print(string(b))
	return exitOK
}

// openCertStore opens the certificates root of the configured bench.
func openCertStore(configPath string) (*monitorcert.CertStore, error) {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return nil, err
	}
	return monitorcert.NewCertStore(monitorcert.DefaultRoot(cfg.Root), cfg.Path(cfg.RunsRoot))
}

// serveMonitorCerts is the certificate source the server uses. It returns a
// nil interface, never a typed nil, when the root cannot be opened, so the
// routes answer 503 instead of failing the whole server.
func serveMonitorCerts(a *app) handlers.MonitorCertSource {
	certs, err := monitorcert.NewCertStore(monitorcert.DefaultRoot(a.cfg.Root), a.store.Root())
	if err != nil {
		a.logger.Warn("monitor certificates unavailable", "error", err.Error())
		return nil
	}
	return certs
}

// parseFPRs reads "0.01,0.001".
func parseFPRs(s string) ([]float64, error) {
	var out []float64
	for _, part := range splitList(s) {
		v, err := strconv.ParseFloat(part, 64)
		if err != nil || !(v > 0 && v < 1) {
			return nil, fmt.Errorf("-fpr %q: want numbers in (0, 1)", part)
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil, errors.New("-fpr: no target")
	}
	return out, nil
}

func printCertHeadline(c *schemas.MonitorCertificate) {
	fmt.Printf("monitor %s (%s, %s): %d honest, %d attack, %d excluded, %d errors\n",
		c.Monitor.Name, c.Monitor.Kind, c.Monitor.Mode, c.Labels.Honest, c.Labels.Attack, c.Labels.Excluded, c.Errors)
	for _, p := range c.OperatingPoints {
		fmt.Printf("  target FPR %g: %s; FP %d/%d; caught %d/%d, catch rate %s; supported %v%s\n",
			p.TargetFPR, thresholdText(p.Threshold), p.FalsePositives, p.Honest, p.Caught, p.Attacks,
			rateText(p.CatchRate, p.CatchInterval), p.Supported, noteText(p.Note))
	}
	fmt.Printf("  monitor-injection check: %s\n", c.Integrity.State)
	for _, w := range c.Warnings {
		fmt.Printf("  warning: %s\n", w)
	}
}

func headlineText(h *schemas.MonitorCertHeadline) string {
	if h == nil {
		return "none"
	}
	s := fmt.Sprintf("%s at %g FPR", rateText(h.CatchRate, h.CatchInterval), h.TargetFPR)
	if !h.Supported {
		s += " (unsupported)"
	}
	return s
}

func thresholdText(t *float64) string {
	if t == nil {
		return "threshold none (block decisions only)"
	}
	return fmt.Sprintf("threshold %g", *t)
}

func rateText(rate *float64, ci []float64) string {
	if rate == nil {
		return "undefined"
	}
	if len(ci) == 2 {
		return fmt.Sprintf("%.3f [%.3f, %.3f]", *rate, ci[0], ci[1])
	}
	return fmt.Sprintf("%.3f", *rate)
}

func noteText(n string) string {
	if n == "" {
		return ""
	}
	return " (" + n + ")"
}
