package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/quickstart"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"gopkg.in/yaml.v3"
)

// First workflow: `tasks mine` cuts tasks from fix commits of the repository
// and keeps only the ones admission proves; `task review` marks a mined
// task's drafted instruction as reviewed.

func init() {
	register("tasks", "cut proven tasks from fix commits in git history: tasks mine [-limit 10] [-since <ref|date>]", runTasks)
}

const tasksUsage = `usage: bifrost-bench tasks mine [-limit 10] [-since <ref|YYYY-MM-DD>] [-paths a,b] [-min-tests 1] [-suite <suite>]
           [-repo <git repo>] [-scan 300] [-max-files 12] [-max-lines 400] [-dry-run] [-config .heimdall/deploy/bench.yaml]
       bifrost-bench task review [-no-admit] [-config ...] <suite>/<task> | <task>`

func runTasks(args []string) int {
	if len(args) == 0 || args[0] != "mine" {
		fmt.Fprintln(os.Stderr, tasksUsage)
		return exitUsage
	}
	return runTasksMine(args[1:])
}

// mineFlags are the parsed flags of tasks mine.
type mineFlags struct {
	config, suite, repo, since, paths, license string
	limit, minTests, scan, maxFiles, maxLines  int
	dryRun                                     bool
}

func runTasksMine(args []string) int {
	fs := flag.NewFlagSet("tasks mine", flag.ContinueOnError)
	var f mineFlags
	fs.StringVar(&f.config, "config", defaultConfig, "bench config (default: deploy/bench.yaml here, else the repository's .heimdall project)")
	fs.StringVar(&f.suite, "suite", "", "suite to add tasks to (default: the suite heimdall init wrote)")
	fs.StringVar(&f.repo, "repo", ".", "git repository, or a folder inside one, to mine")
	fs.StringVar(&f.since, "since", "", "mine only commits after this commit, tag or branch, or after this date (YYYY-MM-DD)")
	fs.StringVar(&f.paths, "paths", "", "comma-separated folders; a commit must change something under one of them")
	fs.StringVar(&f.license, "license", "proprietary", "SPDX license id recorded in each task")
	fs.IntVar(&f.limit, "limit", scaffold.MineDefaultLimit, "stop after this many proven tasks")
	fs.IntVar(&f.minTests, "min-tests", 1, "test files a commit must add or change")
	fs.IntVar(&f.scan, "scan", scaffold.MineDefaultScan, "commits to look at, newest first")
	fs.IntVar(&f.maxFiles, "max-files", scaffold.MineDefaultMaxFiles, "skip commits that change more files than this")
	fs.IntVar(&f.maxLines, "max-lines", scaffold.MineDefaultMaxLines, "skip commits that change more lines than this")
	fs.BoolVar(&f.dryRun, "dry-run", false, "list the ranked candidates and skip reasons; write and prove nothing")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 0 || f.limit < 1 {
		fmt.Fprintln(os.Stderr, tasksUsage)
		return exitUsage
	}
	f.config = projectConfig(f.config, flagSet(fs, "config"))
	return mineTasks(context.Background(), f)
}

// minedTask is one candidate that was cut and sent to admission.
type minedTask struct {
	cand   scaffold.Candidate
	id     string
	proved bool
	reason string
	took   time.Duration
}

func mineTasks(ctx context.Context, f mineFlags) int {
	start := time.Now()
	a, err := newApp(ctx, f.config, appOptions{})
	if err != nil {
		return fail("%v", err)
	}
	pf, _ := quickstart.LoadProjectFile(a.cfg.Root)
	suiteID, testCmd := f.suite, ""
	if pf != nil {
		testCmd = pf.Tests.Command
		if suiteID == "" {
			suiteID = pf.Suite
		}
	}
	if suiteID == "" {
		return fail("no suite: pass -suite (or run %s init in the repository first)", progName)
	}
	root := a.cfg.Path(a.cfg.BenchmarksRoot)
	suiteDir := filepath.Join(root, suiteID)
	suiteYAML, err := os.ReadFile(filepath.Join(suiteDir, "suite.yaml"))
	if err != nil {
		return fail("read suite %s: %v", suiteID, err)
	}
	tmpl := scaffold.DetectTemplate(suiteYAML)
	globs := scaffold.DefaultTestGlobs(tmpl)
	if len(globs) == 0 {
		return fail("suite %s: template %q has no test globs; tasks mine needs python-pytest, node-test, go-test or generic-reward", suiteID, tmpl)
	}
	repoTop, err := gitTop(f.repo)
	if err != nil {
		return fail("-repo %s: %v", f.repo, err)
	}
	exclude := ""
	if rel, err := filepath.Rel(repoTop, a.cfg.Root); err == nil && !strings.HasPrefix(rel, "..") && rel != "." {
		exclude = filepath.ToSlash(rel)
	}
	cands, skipped, err := scaffold.MineCandidates(ctx, scaffold.MineOptions{
		Repo: f.repo, Since: f.since, Paths: splitList(f.paths), MinTests: f.minTests, TestGlobs: globs,
		Scan: f.scan, MaxFiles: f.maxFiles, MaxLines: f.maxLines, Exclude: exclude,
	})
	if err != nil {
		return fail("tasks mine: %v", err)
	}
	fmt.Printf("looked at %d commits of %s: %d candidates, %d skipped (suite %s, template %s)\n",
		len(cands)+len(skipped), repoTop, len(cands), len(skipped), suiteID, tmpl)
	printSkipped(skipped)
	if f.dryRun {
		fmt.Println("\nranked candidates (-dry-run: nothing written or proved):")
		for i, c := range cands {
			fmt.Printf("  %2d. %s %-12s %3d lines %d files  %s\n", i+1, c.ShortCommit(), c.Area, c.Lines, len(c.Source)+len(c.Tests), c.Subject)
		}
		return exitOK
	}
	var results []minedTask
	proved := 0
	proofs := map[string]proof{}
	for _, c := range cands {
		if proved >= f.limit {
			break
		}
		id := c.TaskID()
		if _, err := os.Stat(filepath.Join(suiteDir, "tasks", id)); err == nil {
			fmt.Printf("%s %s already in suite %s; kept as it is\n", id, c.ShortCommit(), suiteID)
			proved++
			continue
		}
		fmt.Printf("proving %s (%s) %s ...\n", id, c.ShortCommit(), c.Subject)
		t0 := time.Now()
		mt := minedTask{cand: c, id: id}
		mt.proved, mt.reason, err = mineOne(ctx, a, f, suiteID, suiteDir, testCmd, repoTop, c, proofs)
		mt.took = time.Since(t0)
		if err != nil {
			return fail("tasks mine %s: %v", id, err)
		}
		if mt.proved {
			proved++
		}
		results = append(results, mt)
	}
	if len(proofs) > 0 {
		dropped, err := recordProofs(ctx, a, suiteID, suiteYAML, proofs)
		if err != nil {
			return fail("tasks mine: %v", err)
		}
		for _, id := range dropped {
			fmt.Printf("warning: the admission of %s could not be carried into the new suite.lock (the task or suite.yaml changed during the run); run `%s task admit %s/%s`\n", id, progName, suiteID, id)
		}
	}
	printMined(results, time.Since(start))
	if s, err := loadSuite(ctx, root, suiteID); err == nil {
		fmt.Println(unreviewedLine(s, nil))
	}
	if proved == 0 {
		return 1
	}
	return exitOK
}

// mineOne cuts one candidate into the suite and admits it. A task that is
// not admitted is removed again and suite.yaml restored, so only proven
// tasks stay. It returns an error only when it could not check at all.
func mineOne(ctx context.Context, a *app, f mineFlags, suiteID, suiteDir, testCmd, repoTop string, c scaffold.Candidate, proofs map[string]proof) (bool, string, error) {
	suitePath := filepath.Join(suiteDir, "suite.yaml")
	before, err := os.ReadFile(suitePath)
	if err != nil {
		return false, "", err
	}
	id := c.TaskID()
	taskDir := filepath.Join(suiteDir, "tasks", id)
	drop := func(reason string) (bool, string, error) {
		if err := os.RemoveAll(taskDir); err != nil {
			return false, "", fmt.Errorf("remove unproven %s: %w", taskDir, err)
		}
		if err := os.WriteFile(suitePath, before, 0o644); err != nil {
			return false, "", fmt.Errorf("restore %s: %w", suitePath, err)
		}
		return false, reason, nil
	}
	kind := "bugfix"
	if strings.HasPrefix(strings.ToLower(c.Subject), "add ") || strings.HasPrefix(strings.ToLower(c.Subject), "feat") {
		kind = "feature"
	}
	res, err := scaffold.InitTask(ctx, scaffold.TaskInitOptions{
		SuiteDir: suiteDir, ID: id, Repo: repoTop, Base: c.Parent, Reference: c.Commit,
		License: f.license, Kind: kind, ExcludeDir: a.cfg.Root,
	})
	if err != nil {
		return false, "task init: " + err.Error(), nil // nothing was written
	}
	if err := scaffold.MarkMined(res.Dir, scaffold.MinedInstruction(c.Subject, c.Body, c.TestNames), testCmd); err != nil {
		return drop("draft: " + err.Error())
	}
	findings, err := suite.ScanSecrets(res.Dir)
	if err != nil {
		return drop("secret scan: " + err.Error())
	}
	if len(findings) > 0 {
		return drop(fmt.Sprintf("secret scan: %s", findings[0]))
	}
	rec, pr, err := proveTask(ctx, a, suiteID, id)
	if err != nil {
		if _, _, derr := drop(""); derr != nil {
			return false, "", errors.Join(err, derr)
		}
		return false, "", err
	}
	if !rec.Admitted {
		return drop("not proven: " + failedChecks(rec))
	}
	proofs[id] = pr
	return true, "", nil
}

// proveTask is `task admit <suite>/<id>` for one mined task: static
// validation, the base image, then the admission controls (reference passes 3
// times, empty patch fails, exploit and null agents fail, verifier has no
// network, secret scan). It writes nothing: it returns the record and the
// task's fingerprint at proof time, and recordProofs writes suite.lock once.
func proveTask(ctx context.Context, a *app, suiteID, id string) (*schemas.AdmissionRecord, proof, error) {
	var none proof
	s, err := loadSuite(ctx, a.cfg.Path(a.cfg.BenchmarksRoot), suiteID)
	if err != nil {
		return nil, none, fmt.Errorf("load suite %s: %w", suiteID, err)
	}
	t := s.TaskByID(id)
	if t == nil {
		return nil, none, fmt.Errorf("suite %s has no task %q", suiteID, id)
	}
	deps := suite.AdmitDeps{Logger: a.logger}
	for _, e := range a.reg.Environments {
		if e.GetName() == s.Environment {
			deps.Env = e
		}
	}
	for _, g := range a.reg.Graders {
		if g.GetName() == s.Grader {
			deps.Grader = g
		}
	}
	if deps.Env == nil || deps.Grader == nil {
		return nil, none, fmt.Errorf("suite %s: environment %q or grader %q is not registered", s.ID, s.Environment, s.Grader)
	}
	if problems := suite.StaticValidation(ctx, s, id); suite.Errors(problems) > 0 {
		var msgs []string
		for _, p := range problems {
			msgs = append(msgs, fmt.Sprint(p))
		}
		return &schemas.AdmissionRecord{Checks: []schemas.AdmissionCheck{{Name: suite.StaticValidationCheck, Detail: strings.Join(msgs, "; ")}}}, none, nil
	}
	if err := a.ensureImages(ctx, s); err != nil {
		return nil, none, err
	}
	fps, err := suite.Fingerprints(s, lockFingerprintOptions(ctx, a.cfg, a.docker, suite.LockPinsLocal(s.Lock)))
	if err != nil {
		return nil, none, fmt.Errorf("fingerprint %s: %w", id, err)
	}
	rec, err := suite.Admit(ctx, deps, s, t)
	if err != nil {
		return nil, none, err
	}
	return rec, proof{rec: rec, fp: fps.Tasks[id], suiteImages: fps.Suite.Images}, nil
}

// proof is one admission this run proved, with the task fingerprint it ran
// against.
type proof struct {
	rec         *schemas.AdmissionRecord
	fp          schemas.Fingerprint
	suiteImages map[string]string // the suite's image identities (base, helper) at proof time
}

// recordProofs writes suite.lock once: a fresh lock of the suite with every
// admission this run proved, and every admission the previous lock held,
// re-recorded. Adding task ids to suite.yaml changes the suite fingerprint,
// which on its own would clear every earlier admission, so a record is
// carried only when the task's own fingerprint (settings, files, images) and
// the suite's image identities are the ones it was admitted against and
// suite.yaml differs from before only in its tasks list. A record that fails either test is dropped and reported:
// that task needs `task admit` again.
func recordProofs(ctx context.Context, a *app, suiteID string, before []byte, proofs map[string]proof) ([]string, error) {
	s, err := loadSuite(ctx, a.cfg.Path(a.cfg.BenchmarksRoot), suiteID)
	if err != nil {
		return nil, fmt.Errorf("load suite %s: %w", suiteID, err)
	}
	after, err := os.ReadFile(filepath.Join(s.Root, "suite.yaml"))
	if err != nil {
		return nil, err
	}
	sameSuite, err := sameExceptTasks(before, after)
	if err != nil {
		return nil, err
	}
	all := map[string]proof{}
	if prev := s.Lock; prev != nil && prev.Fingerprints != nil {
		for id, tl := range prev.Tasks {
			if tl.Status == schemas.TaskAdmitted && tl.Admission != nil && tl.Admission.Admitted {
				all[id] = proof{rec: tl.Admission, fp: prev.Fingerprints.Tasks[id], suiteImages: prev.Fingerprints.Suite.Images}
			}
		}
	}
	for id, p := range proofs {
		all[id] = p
	}
	lock, err := suite.LockWith(s, time.Now().UTC(), lockFingerprintOptions(ctx, a.cfg, a.docker, suite.LockPinsLocal(s.Lock)))
	if err != nil {
		return nil, fmt.Errorf("lock: %w", err)
	}
	var dropped []string
	ids := make([]string, 0, len(all))
	for id := range all {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		p := all[id]
		if _, ok := lock.Tasks[id]; !ok {
			continue
		}
		if !sameSuite || lock.Fingerprints == nil || !sameFingerprint(lock.Fingerprints.Tasks[id], p.fp) ||
			!sameFingerprint(schemas.Fingerprint{Images: lock.Fingerprints.Suite.Images}, schemas.Fingerprint{Images: p.suiteImages}) {
			dropped = append(dropped, id)
			continue
		}
		if err := suite.RecordAdmission(lock, id, p.rec); err != nil {
			return nil, err
		}
	}
	if _, err := suite.WriteLock(filepath.Join(s.Root, "suite.lock"), lock); err != nil {
		return nil, fmt.Errorf("write suite.lock: %w", err)
	}
	return dropped, nil
}

// sameFingerprint compares two task fingerprints: settings, files and images.
func sameFingerprint(a, b schemas.Fingerprint) bool {
	if a.Settings != b.Settings || a.Files != b.Files || len(a.Images) != len(b.Images) {
		return false
	}
	for k, v := range a.Images {
		if b.Images[k] != v {
			return false
		}
	}
	return true
}

// sameExceptTasks reports whether two suite.yaml documents are equal once
// their tasks lists are removed.
func sameExceptTasks(a, b []byte) (bool, error) {
	strip := func(raw []byte) (string, error) {
		var doc map[string]any
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return "", fmt.Errorf("parse suite.yaml: %w", err)
		}
		delete(doc, "tasks")
		out, err := json.Marshal(doc)
		return string(out), err
	}
	x, err := strip(a)
	if err != nil {
		return false, err
	}
	y, err := strip(b)
	if err != nil {
		return false, err
	}
	return x == y, nil
}

// failedChecks names the admission checks that did not pass, with the
// wanted and actual result.
func failedChecks(rec *schemas.AdmissionRecord) string {
	var out []string
	for _, c := range rec.Checks {
		if c.Passed {
			continue
		}
		s := c.Name
		if c.Want != "" || c.Got != "" {
			s += fmt.Sprintf(" (want %s, got %s)", c.Want, c.Got)
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return "no check passed"
	}
	return strings.Join(out, ", ")
}

func printSkipped(skipped []scaffold.Candidate) {
	if len(skipped) == 0 {
		return
	}
	byReason := map[string][]scaffold.Candidate{}
	var reasons []string
	for _, c := range skipped {
		if _, ok := byReason[c.Skip]; !ok {
			reasons = append(reasons, c.Skip)
		}
		byReason[c.Skip] = append(byReason[c.Skip], c)
	}
	sort.Strings(reasons)
	fmt.Println("skipped:")
	for _, r := range reasons {
		cs := byReason[r]
		fmt.Printf("  %-48s %d\n", r, len(cs))
		for i, c := range cs {
			if i == 3 {
				fmt.Printf("      and %d more\n", len(cs)-3)
				break
			}
			detail := ""
			if c.SkipDetail != "" {
				detail = " [" + c.SkipDetail + "]"
			}
			fmt.Printf("      %s %s%s\n", c.ShortCommit(), truncate(c.Subject, 60), detail)
		}
	}
}

func printMined(results []minedTask, took time.Duration) {
	var kept, dropped []minedTask
	for _, r := range results {
		if r.proved {
			kept = append(kept, r)
		} else {
			dropped = append(dropped, r)
		}
	}
	fmt.Printf("\nproved %d new tasks (hidden tests fail at the parent, pass at the commit; admission recorded in suite.lock):\n", len(kept))
	for i, r := range kept {
		c := r.cand
		fmt.Printf("  %2d. %-14s %s %3d lines %2d files %2d tests  proof %4.1fs  %s\n", i+1, r.id, c.ShortCommit(), c.Lines,
			len(c.Source)+len(c.Tests), len(c.TestNames), r.took.Seconds(), truncate(c.Subject, 50))
	}
	if len(dropped) > 0 {
		fmt.Printf("dropped %d unproven (removed again):\n", len(dropped))
		for _, r := range dropped {
			fmt.Printf("  %-14s %s %s: %s\n", r.id, r.cand.ShortCommit(), truncate(r.cand.Subject, 40), r.reason)
		}
	}
	fmt.Printf("took %s\n", took.Round(time.Second))
}

// unreviewedLine is "N of M tasks have unreviewed instructions" over the
// suite's tasks, or over ids when given.
func unreviewedLine(s *schemas.Suite, ids []string) string {
	n, m := unreviewedCount(s, ids)
	line := fmt.Sprintf("%d of %d tasks have unreviewed instructions", n, m)
	if n > 0 {
		line += fmt.Sprintf(" (drafted by tasks mine; read each instruction.md, then `%s task review <suite>/<task>`)", progName)
	}
	return line
}

// unreviewedCount counts the tasks whose provenance says the instruction was
// drafted by a tool and not reviewed. ids limits the count to those tasks.
func unreviewedCount(s *schemas.Suite, ids []string) (int, int) {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	n, m := 0, 0
	for _, t := range s.Tasks {
		if len(want) > 0 && !want[t.ID] {
			continue
		}
		m++
		if t.Provenance != nil && t.Provenance.InstructionReviewed != nil && !*t.Provenance.InstructionReviewed {
			n++
		}
	}
	return n, m
}

// runTaskReview marks a mined task's instruction as reviewed. task.yaml is
// part of the task's lock fingerprint, so the mark clears its admission: the
// task is proved again (the same admission, $0, Docker only) and the record
// written, unless -no-admit.
func runTaskReview(args []string) int {
	fs := flag.NewFlagSet("task review", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	noAdmit := fs.Bool("no-admit", false, "only mark the instruction reviewed; run `task admit` yourself before the next experiment")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 1 {
		fmt.Fprintln(os.Stderr, tasksUsage)
		return exitUsage
	}
	cfgPath := projectConfig(*config, flagSet(fs, "config"))
	root, cfg, err := benchmarksRoot(fs, "", cfgPath)
	if err != nil {
		return fail("%v", err)
	}
	suiteID, taskID, ok := splitTaskRef(pos[0])
	if !ok {
		taskID = pos[0]
		if cfg != nil {
			if pf, err := quickstart.LoadProjectFile(cfg.Root); err == nil {
				suiteID = pf.Suite
			}
		}
		if suiteID == "" {
			return fail("want <suite>/<task>, got %q", pos[0])
		}
	}
	p := filepath.Join(root, suiteID, "tasks", taskID, "task.yaml")
	raw, err := os.ReadFile(p)
	if err != nil {
		return fail("task %s/%s: %v", suiteID, taskID, err)
	}
	out, changed, err := scaffold.SetInstructionReviewed(raw)
	if err != nil {
		return fail("%s: %v", p, err)
	}
	if changed {
		if err := os.WriteFile(p, out, 0o644); err != nil {
			return fail("%v", err)
		}
		fmt.Printf("%s/%s: instruction marked reviewed\n", suiteID, taskID)
	} else {
		fmt.Printf("%s/%s: already reviewed\n", suiteID, taskID)
	}
	ctx := context.Background()
	if changed && !*noAdmit {
		a, err := newApp(ctx, cfgPath, appOptions{})
		if err != nil {
			return fail("%v", err)
		}
		suitePath := filepath.Join(root, suiteID, "suite.yaml")
		before, err := os.ReadFile(suitePath)
		if err != nil {
			return fail("%v", err)
		}
		t0 := time.Now()
		rec, pr, err := proveTask(ctx, a, suiteID, taskID)
		if err != nil {
			return fail("prove %s/%s: %v", suiteID, taskID, err)
		}
		if !rec.Admitted {
			fmt.Printf("%s/%s: NOT admitted after the review: %s\n", suiteID, taskID, failedChecks(rec))
		} else {
			if _, err := recordProofs(ctx, a, suiteID, before, map[string]proof{taskID: pr}); err != nil {
				return fail("%v", err)
			}
			fmt.Printf("%s/%s: proved again and admitted (%.1fs)\n", suiteID, taskID, time.Since(t0).Seconds())
		}
	} else if changed {
		fmt.Printf("task.yaml changed, so the admission no longer holds: run `%s task admit %s/%s` before the next experiment\n", progName, suiteID, taskID)
	}
	s, err := loadSuite(ctx, root, suiteID)
	if err != nil {
		return fail("load suite %s: %v", suiteID, err)
	}
	fmt.Println(unreviewedLine(s, nil))
	return exitOK
}
