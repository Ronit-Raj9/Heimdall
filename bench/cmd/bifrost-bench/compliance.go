package main

// Eval integrity area (S2): the compliance evidence pack (docs/compliance.md).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/compliance"
	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/schemas"
)

const complianceUsage = `usage: bifrost-bench compliance report -exp <id> [-exp <id>...] [-analysis <id>] [-o dir] [-format md|json|both] [-config deploy/bench.yaml]
Writes compliance-<exp>.md and/or .json: each executed test mapped to OWASP LLM 2025, OWASP Agentic 2026 and NIST AI RMF ids, the
record-keeping the bench does (EU AI Act Art. 12 and 26), links to the sealed runs, and what the pack does not certify.
The same pack is written by: bifrost-bench release export <release_id> -compliance`

func init() {
	register("compliance", "compliance evidence pack: compliance report -exp <id> (OWASP, NIST AI RMF, EU AI Act record-keeping, sealed evidence links)", runCompliance)
}

// packExps is a repeatable -exp flag that also splits commas.
type packExps []string

func (l *packExps) String() string { return strings.Join(*l, ",") }

func (l *packExps) Set(v string) error {
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			*l = append(*l, s)
		}
	}
	return nil
}

func runCompliance(args []string) int {
	if len(args) < 1 || args[0] != "report" {
		fmt.Fprintln(os.Stderr, complianceUsage)
		return exitUsage
	}
	fs := flag.NewFlagSet("compliance report", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	var exps packExps
	fs.Var(&exps, "exp", "experiment id (repeat or comma-separate for several)")
	analysis := fs.String("analysis", "", "analysis id of the single experiment (default: the newest)")
	out := fs.String("o", ".", "output directory")
	format := fs.String("format", "both", "md, json or both")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 || len(exps) == 0 || !validPackFormat(*format) || (*analysis != "" && len(exps) != 1) {
		fmt.Fprintln(os.Stderr, complianceUsage)
		return exitUsage
	}
	ctx := context.Background()
	a, err := newApp(ctx, *config, appOptions{})
	if err != nil {
		return fail("%v", err)
	}
	p, err := a.compliancePack(ctx, exps, *analysis, nil)
	if err != nil {
		return fail("%v", err)
	}
	paths, err := writeCompliancePack(p, *out, "compliance-"+strings.Join(exps, "+"), *format)
	if err != nil {
		return fail("%v", err)
	}
	printPackSummary(p, paths)
	return exitOK
}

// compliancePack loads every experiment and builds the pack. rel, when set,
// is the release the pack is exported with, and its analysis is the one
// reported.
func (a *app) compliancePack(ctx context.Context, exps []string, analysisID string, rel *schemas.ReleaseRecord) (*schemas.CompliancePack, error) {
	in := compliance.Input{Release: rel, RunsRoot: a.store.Root(), BenchVersion: benchVersion, Now: time.Now()}
	for _, id := range exps {
		ex, err := a.complianceInput(ctx, id, analysisID)
		if err != nil {
			return nil, err
		}
		in.Experiments = append(in.Experiments, ex)
	}
	return compliance.Build(in)
}

// complianceInput loads one experiment's evidence: its manifest, the
// analysis (the named one, else the newest on disk, else a new one written
// as the verdict route would), its sealed runs under that analysis's grade
// source, the seals.log chain and the suite's probes.
func (a *app) complianceInput(ctx context.Context, expID, analysisID string) (compliance.ExperimentInput, error) {
	var ex compliance.ExperimentInput
	m, err := a.store.LoadManifest(ctx, expID)
	if err != nil {
		return ex, fmt.Errorf("compliance %s: %w", expID, err)
	}
	ex.Manifest = m
	if ex.Analysis, err = a.packAnalysis(ctx, expID, analysisID); err != nil {
		return ex, fmt.Errorf("compliance %s: %w", expID, err)
	}
	gradeSource := schemas.GradeSourceOriginal
	if ex.Analysis != nil && ex.Analysis.GradeSource != "" {
		gradeSource = ex.Analysis.GradeSource
	}
	if ex.Runs, err = a.store.SealedRuns(ctx, expID, gradeSource); err != nil {
		return ex, fmt.Errorf("compliance %s: sealed runs: %w", expID, err)
	}
	ex.ChainChecked = true
	if err := a.store.VerifyChain(expID); err != nil {
		if !errors.Is(err, schemas.ErrSealMismatch) {
			return ex, fmt.Errorf("compliance %s: verify seal chain: %w", expID, err)
		}
		ex.ChainErr = err
	}
	ex.SealsTail = sealsTail(filepath.Join(a.store.Root(), expID, "seals.log"))
	if suite, err := engine.LoadSuite(ctx, a.reg, m.Experiment.Suite); err == nil {
		ex.Probes = map[string]*schemas.ProbeSpec{}
		for _, p := range suite.Probes {
			ex.Probes[p.ID] = p
		}
	} else {
		a.logger.Warn("compliance: suite not loaded; probes are mapped by id only", "suite", m.Experiment.Suite, "error", err.Error())
	}
	return ex, nil
}

// packAnalysis returns the named analysis, else the newest on disk, else a
// new analysis (written, as the verdict route writes one).
func (a *app) packAnalysis(ctx context.Context, expID, analysisID string) (*schemas.Analysis, error) {
	if analysisID == "" {
		if id, ok := (indexAdapter{root: a.store.Root()}).LatestAnalysis(expID); ok {
			analysisID = id
		}
	}
	if analysisID == "" {
		an, _, err := a.analyze(ctx, expID, schemas.GradeSourceOriginal)
		return an, err
	}
	b, err := os.ReadFile(filepath.Join(a.store.Root(), expID, "analysis", filepath.Base(analysisID), "verdict.json"))
	if err != nil {
		return nil, fmt.Errorf("analysis %s: %w", analysisID, err)
	}
	var an schemas.Analysis
	if err := json.Unmarshal(b, &an); err != nil {
		return nil, fmt.Errorf("analysis %s: %w", analysisID, err)
	}
	if an.AnalysisID == "" {
		an.AnalysisID = analysisID
	}
	return &an, nil
}

// sealsTail returns the last non-empty line of a seals.log, or "".
func sealsTail(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := bytes.Split(bytes.TrimSpace(b), []byte("\n"))
	return strings.TrimSpace(string(lines[len(lines)-1]))
}

// writeCompliancePack writes the pack as <base>.md and/or <base>.json in dir
// and returns the paths written.
func writeCompliancePack(p *schemas.CompliancePack, dir, base, format string) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}
	var paths []string
	if format == "md" || format == "both" {
		path := filepath.Join(dir, base+".md")
		if err := os.WriteFile(path, compliance.Markdown(p), 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", path, err)
		}
		paths = append(paths, path)
	}
	if format == "json" || format == "both" {
		b, err := json.MarshalIndent(p, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("encode compliance pack: %w", err)
		}
		path := filepath.Join(dir, base+".json")
		if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", path, err)
		}
		paths = append(paths, path)
	}
	return paths, nil
}

func validPackFormat(f string) bool { return f == "md" || f == "json" || f == "both" }

// printPackSummary prints the headline counts and the files written.
func printPackSummary(p *schemas.CompliancePack, paths []string) {
	conds, gates, fired := 0, 0, 0
	for _, t := range p.Tests {
		if t.Kind == schemas.ComplianceTestGate {
			gates++
			fired += t.Fired
		} else {
			conds++
		}
	}
	for _, e := range p.Experiments {
		fmt.Printf("%s: %d counted runs, %d seals verified, chain %s, verdict %s\n", e.ExpID, e.CountedRuns, e.RunsVerified, e.ChainStatus, orNone(string(e.Decision)))
	}
	fmt.Printf("%d conditions, %d gates (%d gate firings), %d unmapped tests, %d warnings\n", conds, gates, fired, len(p.Unmapped), len(p.Warnings))
	for _, path := range paths {
		fmt.Printf("wrote %s\n", path)
	}
}

// serveCompliance builds packs for GET /experiments/{id}/compliance
// (handlers/compliance.go).
type serveCompliance struct {
	a *app
}

// CompliancePack implements handlers.ComplianceSource.
func (s serveCompliance) CompliancePack(ctx context.Context, expID string) (*schemas.CompliancePack, []byte, error) {
	p, err := s.a.compliancePack(ctx, []string{expID}, "", nil)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, fmt.Errorf("%w: %w", schemas.ErrNotFound, err)
		}
		return nil, nil, err
	}
	return p, compliance.Markdown(p), nil
}

// releaseCompliance writes the compliance pack of a release beside its
// export: in the directory of -o, else the current directory.
func releaseCompliance(ctx context.Context, configPath string, rec *schemas.ReleaseRecord, out string) ([]string, error) {
	a, err := newApp(ctx, configPath, appOptions{})
	if err != nil {
		return nil, err
	}
	p, err := a.compliancePack(ctx, []string{rec.ExpID}, rec.AnalysisID, rec)
	if err != nil {
		return nil, err
	}
	dir := "."
	if out != "" {
		dir = filepath.Dir(out)
	}
	return writeCompliancePack(p, dir, "compliance-"+rec.ReleaseID, "both")
}
