package main

// X1 vision grading (docs/visual-grading.md): `visual render`, `visual
// capture` and `visual compare`, the author's tools for making a reference
// image with the exact capture path a grade uses and for trying a task's
// checks on an image. None of them calls a model.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/maximhq/bifrost/bench/calibration"
	"github.com/maximhq/bifrost/bench/graders"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/maximhq/bifrost/bench/visual"
)

func init() {
	register("visual", "vision grading: visual render|capture|compare (reference images and checks of output.visual, $0)", runVisual)
}

const visualUsage = `usage: bifrost-bench visual <render|capture|compare> ...
  visual render <file.svg|file.html> -out <png> [-width 1280 -height 800 -scale 1 -full-page -wait-ms 0 -image <renderer>]
      render a file in the clean-room renderer a grade uses (no network, pinned Playwright image)
  visual capture -desktop <container> [-port 8000 -path /mcp] -out <png>
  visual capture -browser <container> -url <http url> [-width -height -scale -full-page -wait-ms] -out <png>
      capture a running service the way a grade does, to make a reference
  visual compare <suite>/<task> -image <png> [-json] [-config deploy/bench.yaml]
      run the task's output.visual checks on an image against its reference
  visual label <suite> -task <id> -image <png> -label correct|incorrect -labeller <name> [-note <text>] [-ref <run id>]
      add a labelled image to the suite's calibration set (calibration/images/<sha256>.png)
  visual calibrate <suite> [-yes] [-policy <file>]
      run the pinned vision judge on every labelled image and write a calibration record (asks before spending)`

// withGrader puts g in place of every registered grader of the same name,
// or first when there is none, so a lookup that takes the first match and
// one that takes the last both find it.
func withGrader(list []schemas.Grader, g schemas.Grader) []schemas.Grader {
	out := []schemas.Grader{g}
	for _, x := range list {
		if x.GetName() != g.GetName() {
			out = append(out, x)
		}
	}
	return out
}

func runVisual(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, visualUsage)
		return exitUsage
	}
	switch args[0] {
	case "render":
		return runVisualRender(args[1:])
	case "capture":
		return runVisualCapture(args[1:])
	case "compare":
		return runVisualCompare(args[1:])
	case "label":
		return runVisualLabel(args[1:])
	case "calibrate":
		return runVisualCalibrate(args[1:])
	}
	fmt.Fprintln(os.Stderr, visualUsage)
	return exitUsage
}

// captureFlags are the settings render and capture share.
func captureFlags(fs *flag.FlagSet) *schemas.VisualCapture {
	c := &schemas.VisualCapture{}
	fs.IntVar(&c.Width, "width", visual.DefaultWidth, "viewport width")
	fs.IntVar(&c.Height, "height", visual.DefaultHeight, "viewport height")
	fs.IntVar(&c.Scale, "scale", visual.DefaultScale, "device scale factor, 1 to 3")
	fs.BoolVar(&c.FullPage, "full-page", false, "the whole page, not only the viewport (html, browser)")
	fs.IntVar(&c.WaitMS, "wait-ms", 0, "settle time after load")
	fs.StringVar(&c.Image, "image", "", "renderer image (default the pinned Playwright image)")
	return c
}

func runVisualRender(args []string) int {
	fs := flag.NewFlagSet("visual render", flag.ContinueOnError)
	out := fs.String("out", "", "png file to write")
	set := captureFlags(fs)
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 1 || *out == "" {
		fmt.Fprintln(os.Stderr, visualUsage)
		return exitUsage
	}
	abs, err := filepath.Abs(pos[0])
	if err != nil {
		return fail("visual render: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	shot, err := visual.Capturer{}.RenderArtifact(ctx, *set, filepath.Dir(abs), filepath.Base(abs), utils.LabelArgs("", ""))
	if err != nil {
		return fail("visual render: %v", err)
	}
	return writeShot(*out, shot)
}

func runVisualCapture(args []string) int {
	fs := flag.NewFlagSet("visual capture", flag.ContinueOnError)
	out := fs.String("out", "", "png file to write")
	desktop := fs.String("desktop", "", "desktop service container")
	port := fs.Int("port", 8000, "desktop MCP port inside the container")
	mcpPath := fs.String("path", "/mcp", "desktop MCP path")
	browser := fs.String("browser", "", "browser service container (the Playwright MCP image)")
	set := captureFlags(fs)
	fs.StringVar(&set.URL, "url", "", "page the browser opens")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 0 || *out == "" || (*desktop == "") == (*browser == "") {
		fmt.Fprintln(os.Stderr, visualUsage)
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var shot *visual.Shot
	if *desktop != "" {
		shot, err = visual.Capturer{}.CaptureDesktop(ctx, *desktop, *port, *mcpPath)
	} else {
		shot, err = visual.Capturer{}.CaptureBrowser(ctx, *set, *browser)
	}
	if err != nil {
		return fail("visual capture: %v", err)
	}
	return writeShot(*out, shot)
}

// writeShot writes the capture encoded the way a grade keeps it (from its
// decoded pixels), so the file's sha256 is the one a grade of the same
// state records as visual/capture.png.
func writeShot(out string, shot *visual.Shot) int {
	img, err := visual.Decode(shot.PNG)
	if err != nil {
		return fail("visual: the capture does not decode: %v", err)
	}
	data, err := img.EncodePNG()
	if err != nil {
		return fail("visual: %v", err)
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return fail("visual: %v", err)
	}
	fmt.Printf("wrote %s: %dx%d png, %d bytes, sha256 %s", out, img.W, img.H, len(data), utils.SHA256Bytes(data))
	if shot.Fonts != "" {
		fmt.Printf(", fonts %s", shot.Fonts[:12])
	}
	fmt.Println()
	return exitOK
}

func runVisualCompare(args []string) int {
	fs := flag.NewFlagSet("visual compare", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	image := fs.String("image", "", "png or jpg to check")
	asJSON := fs.Bool("json", false, "print the results as JSON")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 1 || *image == "" || !strings.Contains(pos[0], "/") {
		fmt.Fprintln(os.Stderr, visualUsage)
		return exitUsage
	}
	ref, taskID, _ := strings.Cut(pos[0], "/")
	cfg, err := loadConfig(*config)
	if err != nil {
		return fail("visual compare: %v", err)
	}
	s, err := loadSuite(context.Background(), cfg.Path(cfg.BenchmarksRoot), ref)
	if err != nil {
		return fail("visual compare: %v", err)
	}
	t := s.TaskByID(taskID)
	if t == nil {
		return fail("visual compare: suite %s has no task %s", s.ID, taskID)
	}
	spec := graders.VisualSpec(s, t)
	if spec == nil {
		return fail("visual compare: task %s has no output.visual", taskID)
	}
	data, err := os.ReadFile(*image)
	if err != nil {
		return fail("visual compare: %v", err)
	}
	img, err := visual.Decode(data)
	if err != nil {
		return fail("visual compare: %v", err)
	}
	in := visual.Inputs{Capture: img, TaskDir: t.Dir}
	if spec.Reference != "" {
		p, err := visual.TaskPath(t.Dir, spec.Reference)
		if err != nil {
			return fail("visual compare: %v", err)
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return fail("visual compare: reference: %v", err)
		}
		if in.Reference, err = visual.Decode(raw); err != nil {
			return fail("visual compare: reference: %v", err)
		}
	}
	res := visual.Evaluate(spec, in)
	passed, score := visual.Score(res)
	if *asJSON {
		b, _ := json.MarshalIndent(map[string]any{"task": taskID, "passed": passed, "checks": res, "score": score}, "", "  ")
		fmt.Println(string(b))
	} else {
		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "CHECK\tMETHOD\tVALUE\tTHRESHOLD\tPASS\tDETAIL")
		for _, r := range res {
			fmt.Fprintf(tw, "%s\t%s\t%g\t%g\t%v\t%s\n", r.ID, r.Method, r.Value, r.Threshold, r.Passed, r.Detail)
		}
		_ = tw.Flush()
		fmt.Printf("%d of %d checks passed\n", passed, len(res))
	}
	if passed != len(res) {
		return 1
	}
	return exitOK
}

func runVisualLabel(args []string) int {
	fs := flag.NewFlagSet("visual label", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	task := fs.String("task", "", "task the image is an answer to")
	image := fs.String("image", "", "png or jpg the label is about (a run's visual/capture.png, say)")
	label := fs.String("label", "", "correct or incorrect")
	labeller := fs.String("labeller", "", "who labelled it (self-declared)")
	note := fs.String("note", "", "why")
	ref := fs.String("ref", "", "run id or file the image came from")
	id, rest := splitRunArg(args)
	if err := fs.Parse(rest); err != nil || id == "" || fs.NArg() != 0 || *task == "" || *image == "" {
		fmt.Fprintln(os.Stderr, visualUsage)
		return exitUsage
	}
	cfg, err := loadConfig(*config)
	if err != nil {
		return fail("visual label: %v", err)
	}
	s, err := loadSuite(context.Background(), cfg.Path(cfg.BenchmarksRoot), id)
	if err != nil {
		return fail("visual label: %v", err)
	}
	if t := s.TaskByID(*task); t == nil || graders.VisualSpec(s, t) == nil {
		return fail("visual label: suite %s has no task %s with output.visual", s.ID, *task)
	} else if !graders.VisualJudged(s, t) {
		// Only a task with a rubric and a pinned vision judge is calibrated;
		// a label on a deterministic-only task would never be used (7-C-03).
		return fail("visual label: task %s of suite %s has no output.visual rubric and vision judge, so visual calibrate would never use this label", *task, s.ID)
	}
	data, err := os.ReadFile(*image)
	if err != nil {
		return fail("visual label: %v", err)
	}
	img, err := visual.Decode(data)
	if err != nil {
		return fail("visual label: %v", err)
	}
	// The labelled image is kept as the bench encodes it, like a capture.
	if data, err = img.EncodePNG(); err != nil {
		return fail("visual label: %v", err)
	}
	sum, err := calibration.AddImage(s.Root, data)
	if err != nil {
		return fail("visual label: %v", err)
	}
	it := schemas.CalibrationItem{Task: *task, Image: sum, Label: strings.ToLower(strings.TrimSpace(*label)), Labeller: strings.TrimSpace(*labeller),
		Note: strings.TrimSpace(*note), Source: schemas.CalibrationSourceLabel, Ref: strings.TrimSpace(*ref), At: time.Now().UTC().Truncate(time.Second)}
	it.ID = calibration.ItemIDOf(it)
	if err := calibration.ValidateItem(it); err != nil {
		return fail("visual label: %v", err)
	}
	n, err := calibration.Append(s.Root, []schemas.CalibrationItem{it})
	if err != nil {
		return fail("visual label: %v", err)
	}
	fmt.Printf("item %s: image %s labelled %s by %s (%d new row)\n", it.ID, sum[:12], it.Label, it.Labeller, n)
	return exitOK
}

func runVisualCalibrate(args []string) int {
	fs := flag.NewFlagSet("visual calibrate", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	yes := fs.Bool("yes", false, "run without asking, even though the judge calls cost money")
	policyFile := fs.String("policy", "", "release policy file whose judge_calibration thresholds apply")
	id, rest := splitRunArg(args)
	if err := fs.Parse(rest); err != nil || id == "" || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, visualUsage)
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := newApp(ctx, *config, appOptions{})
	if err != nil {
		return fail("%v", err)
	}
	s, err := loadSuite(ctx, a.cfg.Path(a.cfg.BenchmarksRoot), id)
	if err != nil {
		return fail("visual calibrate: %v", err)
	}
	th, err := thresholdsFrom(*policyFile)
	if err != nil {
		return fail("visual calibrate: %v", err)
	}
	set, err := calibration.LoadSet(s.Root)
	if err != nil {
		return fail("visual calibrate: %v", err)
	}
	human, seed, disputed := calibration.Resolve(set.Items)
	plan, err := graders.PlanVisualCalibration(s, append(append([]calibration.Labelled{}, human...), seed...))
	if err != nil {
		return fail("visual calibrate: %v", err)
	}
	fmt.Printf("vision judge %s/%s, prompt %s (%s), %d vote(s) per call\n", plan.Spec.Provider, plan.Spec.Model, plan.Spec.PromptVersion, plan.PromptSHA[:12], plan.Spec.Votes)
	fmt.Printf("items: %d labelled images, %d disputed (left out), %d skipped\n", len(plan.Items), disputed, len(plan.Skipped))
	for _, sk := range plan.Skipped {
		fmt.Printf("  skipped: %s\n", sk)
	}
	if len(plan.Items) < th.MinLabels {
		fmt.Printf("warning: %d labelled images, fewer than %d: the intervals will be wide\n", len(plan.Items), th.MinLabels)
	}
	fmt.Printf("forecast: %d vision judge calls (first pass and repeat pass), unpriced until they run\n", plan.Calls)
	if len(plan.Items) == 0 {
		return fail("visual calibrate: no labelled image can be judged; add some with visual label")
	}
	if !*yes {
		fmt.Println("not run: this spends real model money; rerun with -yes")
		return exitOK
	}
	live, err := newApp(ctx, *config, appOptions{gateway: true})
	if err != nil {
		return fail("%v", err)
	}
	cfg := graders.JudgeConfig{Gateway: live.reg.Gateway, URL: live.cfg.Gateway.URL, Price: graders.PricesFrom(live.prices.Lookup), PriceRef: live.prices.Ref(), Logger: live.logger}
	runs, spend, err := graders.RunVisualCalibration(ctx, cfg, plan, func(done, total int) {
		if done == total || done%10 == 0 {
			fmt.Printf("  judged %d of %d images\n", done, total)
		}
	})
	if err != nil {
		return fail("visual calibrate: %v (no record written)", err)
	}
	if _, err := calibration.Snapshot(set); err != nil {
		return fail("visual calibrate: %v", err)
	}
	rec := graders.VisualCalibrationRecord(s, plan, set, runs, spend, disputed, cfg.PriceRef)
	if err := calibration.WriteRecord(s.Root, rec); err != nil {
		return fail("visual calibrate: write calibration record: %v", err)
	}
	fmt.Printf("record %s written (set %s)\n", rec.RecordID, rec.SetSHA256[:12])
	writeRecordSummary(os.Stdout, rec, th)
	return exitOK
}
