package main

// Reference board (gap G4-03): `bifrost-bench reference import|publish|
// unpublish|list` and the hosted platform route that serves the board.
//
// The operator runs known agents on the built-in library benchmarks in an
// operator-owned organization (reference.OrgSlug), either there directly
// (an evaluation launched in the app) or elsewhere (bifrost-bench run on a
// fleet machine, then `reference import <runs dir>`). Publishing one
// finished evaluation verifies every sealed run of its experiments and each
// seal chain, builds its report card from those runs, makes a share link to
// that report card that never expires, and writes one board entry per
// benchmark and agent. Nothing is published without that verification and
// no number is ever typed in by hand.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/evalstore"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/reference"
	"github.com/maximhq/bifrost/bench/reportcard"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/server"
	"github.com/maximhq/bifrost/bench/sharelinks"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/maximhq/bifrost/bench/utils"
)

const referenceUsage = `usage: bifrost-bench reference <import|publish|unpublish|list> ...

  reference import <runs dir> [-owner email] [-evaluations dir] [-json]
      copy sealed experiments made elsewhere (bifrost-bench run) into the
      Heimdall reference organization, creating it on first use. Every sealed
      run and each seal chain is verified on the copy first; an experiment
      that does not verify is refused. Each imported experiment becomes an
      evaluation (ev_ref_<experiment>) unless one of -evaluations (a folder
      of evaluation files, e.g. a project's .heimdall/evaluations) covers it.
  reference publish <evaluation> [-by name] [-json]
      verify the evaluation's experiments again, build its report card and
      put one entry per benchmark and agent on the public reference board,
      with a read-only report card link. Only finished evaluations on
      built-in library benchmarks are published.
  reference unpublish <evaluation>
      take the evaluation off the board and turn its report card link off.
  reference list [-json]
      the published entries, then the reference evaluations not published.

every subcommand takes -config deploy/bench.yaml (a hosted config: the
server database and storage root). Run it where the server runs, with
` + suite.LibraryEnv + ` set (inside the server container).`

func init() {
	register("reference", "public reference board of known agents on the library benchmarks: reference import|publish|unpublish|list", runReference)
	hostedAreas = append(hostedAreas, func(env *hostedEnv) (server.RouteRegistrar, error) {
		return handlers.NewReferenceHandler(env.base.cfg.Server.Mode, reference.Open(env.layout.StorageRoot)), nil
	})
}

func runReference(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprintln(os.Stderr, referenceUsage)
		if len(args) == 0 {
			return exitUsage
		}
		return exitOK
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("reference "+sub, flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config (hosted)")
	asJSON := fs.Bool("json", false, "print JSON")
	owner := fs.String("owner", "", "import: email of an existing user who owns the reference organization when it is created")
	evals := fs.String("evaluations", "", "import: folder of evaluation files covering the imported experiments")
	by := fs.String("by", "operator", "publish: who published, shown on the board")
	// The positional argument may come before or after the flags.
	var pos []string
	for len(rest) > 0 {
		if err := fs.Parse(rest); err != nil {
			fmt.Fprintln(os.Stderr, referenceUsage)
			return exitUsage
		}
		rest = fs.Args()
		if len(rest) > 0 {
			pos, rest = append(pos, rest[0]), rest[1:]
		}
	}
	need := map[string]int{"import": 1, "publish": 1, "unpublish": 1, "list": 0}
	n, ok := need[sub]
	if !ok || len(pos) != n {
		fmt.Fprintln(os.Stderr, referenceUsage)
		return exitUsage
	}
	cfg, d, err := openOrgDB(ctx, *config)
	if err != nil {
		return fail("reference %s: %v", sub, err)
	}
	defer d.Close()
	logger := utils.NewStdLogger(os.Stderr, false)
	w := io.Writer(os.Stdout)
	switch sub {
	case "import":
		return refImportCmd(ctx, cfg, d, pos[0], *owner, *evals, *asJSON, logger, w)
	case "publish":
		return refPublishCmd(ctx, cfg, d, pos[0], *by, *asJSON, logger, w)
	case "unpublish":
		return refUnpublishCmd(ctx, cfg, d, pos[0], logger, w)
	default:
		return refListCmd(ctx, cfg, d, *asJSON, logger, w)
	}
}

// refTarget is the reference organization on disk.
type refTarget struct {
	orgID       string
	paths       tenancy.OrgPaths
	storageRoot string // the board lives under it
	logger      schemas.Logger
	// library reports a built-in library benchmark; nil when the server has
	// no library (publishing then refuses).
	library func(benchmark string) bool
	now     func() time.Time
}

// referenceOrg finds the reference organization, creating it when create
// is set (with owner, an email, as its owner when given).
func referenceOrg(ctx context.Context, cfg *lib.Config, d *db.DB, create bool, owner string) (tenancy.Org, error) {
	org, err := tenancy.FindOrg(ctx, tenancy.DBDirectory{DB: d}, reference.OrgSlug)
	if err == nil || !errors.Is(err, tenancy.ErrOrgNotFound) {
		return org, err
	}
	if !create {
		return org, fmt.Errorf("the %s organization does not exist yet: import runs with `reference import` or create it with `org create -slug %s`", reference.OrgSlug, reference.OrgSlug)
	}
	ownerID := ""
	if owner != "" {
		u, err := d.UserByEmail(ctx, owner)
		if err != nil {
			return org, fmt.Errorf("owner %s: %w", owner, err)
		}
		ownerID = u.ID
	}
	o, err := createOrg(ctx, d, hostedLayout(cfg), cfg, reference.OrgSlug, reference.OrgName, ownerID, false)
	if err != nil {
		return org, err
	}
	_, _ = d.AppendAudit(ctx, db.AuditEntry{OrgID: o.ID, ActorType: db.ActorSystem, ActorID: "cli", Action: "org.create",
		TargetType: "org", TargetID: o.ID, Details: db.AuditDetails(map[string]any{"slug": o.Slug, "reference": true, "owner": owner})})
	return tenancy.FindOrg(ctx, tenancy.DBDirectory{DB: d}, o.ID)
}

// newRefTarget is the target of the reference organization org.
func newRefTarget(cfg *lib.Config, org tenancy.Org, logger schemas.Logger) (refTarget, error) {
	layout := hostedLayout(cfg)
	p, err := layout.Paths(org.ID)
	if err != nil {
		return refTarget{}, err
	}
	t := refTarget{orgID: org.ID, paths: p, storageRoot: layout.StorageRoot, logger: logger, now: time.Now}
	if lib := suite.LibraryDir(); lib != "" {
		bench := p.BenchmarksRoot()
		libBench := suite.LibraryBenchmarks(lib)
		t.library = func(id string) bool {
			_, fromLib := suite.PickSuiteDir(bench, libBench, reference.BenchmarkKey(id))
			return fromLib
		}
	}
	return t, nil
}

// refWrapped is one evaluation import made or kept for imported runs.
type refWrapped struct {
	Evaluation string   `json:"evaluation"`
	Benchmark  []string `json:"benchmarks"`
	Status     string   `json:"status"` // created | kept (already there) | copied (from -evaluations) | refused
	Reason     string   `json:"reason,omitempty"`
}

// wrapImported gives every experiment of expIDs an evaluation in the
// reference organization: an evaluation file of evalsDir whose children
// are all in the organization's store is copied (its org set to the
// reference org); an experiment no evaluation covers gets reference.Wrap.
func (t refTarget) wrapImported(ctx context.Context, expIDs []string, evalsDir string) ([]refWrapped, error) {
	st, err := store.NewFSStore(t.paths.StoreRoot, t.logger)
	if err != nil {
		return nil, err
	}
	present := map[string]bool{}
	if ids, err := st.ListExperiments(ctx); err == nil {
		for _, id := range ids {
			present[id] = true
		}
	}
	evs := evalstore.Open(t.paths.ProjectRoot)
	existing, err := evs.List()
	if err != nil {
		return nil, err
	}
	covered := map[string]bool{}
	for _, ev := range existing {
		for _, c := range ev.Children {
			covered[c.ExperimentID] = true
		}
	}
	var out []refWrapped
	if evalsDir != "" {
		src, err := readEvaluationFiles(evalsDir)
		if err != nil {
			return nil, err
		}
		for _, ev := range src {
			res := refWrapped{Evaluation: ev.ID, Benchmark: ev.Spec.Benchmarks}
			var missing []string
			uses := false
			for _, c := range ev.Children {
				if c.ExperimentID == "" {
					continue
				}
				if !present[c.ExperimentID] {
					missing = append(missing, c.ExperimentID)
				}
				if slicesContains(expIDs, c.ExperimentID) {
					uses = true
				}
			}
			if !uses {
				continue // it covers nothing this import brought in
			}
			switch {
			case len(missing) > 0:
				res.Status, res.Reason = "refused", "its experiments "+strings.Join(missing, ", ")+" are not in the reference organization"
			default:
				if _, err := evs.Load(ev.ID); err == nil {
					res.Status, res.Reason = "kept", "already in the reference organization"
				} else {
					cp := *ev
					cp.Org = t.orgID
					cp.Schedule, cp.GateResult = "", nil
					if err := evs.Save(&cp); err != nil {
						return out, err
					}
					res.Status = "copied"
				}
				for _, c := range ev.Children {
					covered[c.ExperimentID] = true
				}
			}
			out = append(out, res)
		}
	}
	for _, id := range expIDs {
		if covered[id] {
			continue
		}
		m, err := st.LoadManifest(ctx, id)
		if err != nil {
			out = append(out, refWrapped{Evaluation: reference.WrapID(id), Status: "refused", Reason: "manifest: " + err.Error()})
			continue
		}
		ev, err := reference.Wrap(m, t.orgID, t.now())
		if err != nil {
			out = append(out, refWrapped{Evaluation: reference.WrapID(id), Status: "refused", Reason: err.Error()})
			continue
		}
		res := refWrapped{Evaluation: ev.ID, Benchmark: ev.Spec.Benchmarks, Status: "created"}
		if _, err := evs.Load(ev.ID); err == nil {
			res.Status, res.Reason = "kept", "already in the reference organization"
		} else if err := evs.Save(ev); err != nil {
			return out, err
		}
		covered[id] = true
		out = append(out, res)
	}
	return out, nil
}

// readEvaluationFiles reads every <id>.json evaluation file of dir (a
// project's .heimdall/evaluations folder), sorted by id.
func readEvaluationFiles(dir string) ([]*schemas.Evaluation, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	var out []*schemas.Evaluation
	for _, e := range ents {
		id := strings.TrimSuffix(e.Name(), ".json")
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || !evalstore.ValidID(id) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var ev schemas.Evaluation
		if err := json.Unmarshal(b, &ev); err != nil {
			return nil, fmt.Errorf("evaluation file %s: %w", e.Name(), err)
		}
		if ev.ID != id {
			return nil, fmt.Errorf("evaluation file %s names evaluation %q", e.Name(), ev.ID)
		}
		out = append(out, &ev)
	}
	return out, nil
}

func slicesContains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// refPublished is what publish did.
type refPublished struct {
	Evaluation string            `json:"evaluation"`
	Entries    []reference.Entry `json:"entries"`
	Skipped    []string          `json:"skipped,omitempty"`
	ReportPath string            `json:"report_path"`
	Board      string            `json:"board"`
}

// verifyChildren verifies every sealed run and the seal chain of each
// child experiment of ev in the reference store, and reads its chain head
// and newest sealed time.
func (t refTarget) verifyChildren(ctx context.Context, ev *schemas.Evaluation, ix *store.Index) (map[string]reference.Seal, map[string]*schemas.Manifest, error) {
	seals := map[string]reference.Seal{}
	manifests := map[string]*schemas.Manifest{}
	for _, c := range ev.Children {
		if c.Skipped != "" || c.ExperimentID == "" {
			continue
		}
		if _, done := seals[c.ExperimentID]; done {
			continue
		}
		n, err := tenancy.VerifyExperiment(ctx, t.paths.StoreRoot, c.ExperimentID, t.logger)
		if err != nil {
			return nil, nil, fmt.Errorf("%s (%s) does not verify: %w", c.ExperimentID, c.Benchmark, err)
		}
		head, logged, err := reference.ChainHead(t.paths.StoreRoot, c.ExperimentID)
		if err != nil {
			return nil, nil, err
		}
		if logged != n {
			return nil, nil, fmt.Errorf("%s: %d runs verified but %d in its seal chain", c.ExperimentID, n, logged)
		}
		s := reference.Seal{Head: head, Runs: n}
		runs, _ := ix.Runs(c.ExperimentID)
		for _, r := range runs {
			if r.Sealed && r.Run.Timing.Sealed != nil && r.Run.Timing.Sealed.After(s.RanAt) {
				s.RanAt = r.Run.Timing.Sealed.UTC()
			}
		}
		seals[c.ExperimentID] = s
		if m, ok := ix.Manifest(c.ExperimentID); ok {
			manifests[c.ExperimentID] = m
		}
	}
	return seals, manifests, nil
}

// publish puts one evaluation of the reference organization on the board.
func (t refTarget) publish(ctx context.Context, evalID, by string) (*refPublished, error) {
	if !evalstore.ValidID(evalID) {
		return nil, fmt.Errorf("%q is not an evaluation id (ev_...)", evalID)
	}
	if t.library == nil {
		return nil, fmt.Errorf("the built-in benchmark library is not set (%s): run this inside the server container", suite.LibraryEnv)
	}
	ev, err := evalstore.Open(t.paths.ProjectRoot).Load(evalID)
	if err != nil {
		return nil, fmt.Errorf("evaluation %s is not in the reference organization: %w", evalID, err)
	}
	if err := reference.Check(ev); err != nil {
		return nil, err
	}
	st, err := store.NewFSStore(t.paths.StoreRoot, t.logger)
	if err != nil {
		return nil, err
	}
	ix := store.NewIndex(st)
	if err := ix.Rebuild(ctx); err != nil {
		return nil, fmt.Errorf("index the reference runs: %w", err)
	}
	seals, manifests, err := t.verifyChildren(ctx, ev, ix)
	if err != nil {
		return nil, err
	}
	rep, err := reportcard.Build(ctx, handlers.LeaderboardSource(indexAdapter{ix: ix, root: st.Root()}), ev, reportcard.Options{})
	if err != nil {
		return nil, fmt.Errorf("report card: %w", err)
	}
	now := t.now()
	entries, skipped, err := reference.Entries(reference.Input{
		Evaluation: ev, Report: rep, Seals: seals, Manifests: manifests,
		Library: t.library, PublishedBy: by, Now: now,
	})
	if err != nil {
		return nil, err
	}
	board := reference.Open(t.storageRoot)
	current, err := board.Load()
	if err != nil {
		return nil, err
	}
	shareID, path, err := t.reportLink(ev, current.Shares[ev.ID], by)
	if err != nil {
		return nil, fmt.Errorf("report card link: %w", err)
	}
	for i := range entries {
		entries[i].ReportPath = path
	}
	prev, err := board.Publish(ev.ID, entries, shareID, now)
	if err != nil {
		return nil, err
	}
	if prev != "" && prev != shareID {
		_, _ = sharelinks.Open(t.paths.ProjectRoot).Revoke(prev, by)
	}
	return &refPublished{Evaluation: ev.ID, Entries: entries, Skipped: skipped, ReportPath: path, Board: board.Path()}, nil
}

// reportLink returns the share link of ev's report card: prev when it is
// still active, else a new one that never expires.
func (t refTarget) reportLink(ev *schemas.Evaluation, prev, by string) (id, path string, _ error) {
	links := sharelinks.Open(t.paths.ProjectRoot)
	if prev != "" {
		if sh, err := links.Get(prev); err == nil && sh.State(t.now().UTC()) == sharelinks.StateActive {
			tok, err := links.Token(t.orgID, sh.ID)
			if err != nil {
				return "", "", err
			}
			return sh.ID, "/s/" + tok, nil
		}
	}
	never := 0
	label := ev.Spec.Name
	if label == "" {
		label = ev.ID
	}
	sh, err := links.Create(sharelinks.NewShare{
		Kind: sharelinks.KindEvaluation, Target: ev.ID, Label: clipLabel(reference.OrgName+": "+label, sharelinks.MaxLabel),
		ExpiresInDays: &never,
	}, "reference publish ("+by+")")
	if err != nil {
		return "", "", err
	}
	tok, err := links.Token(t.orgID, sh.ID)
	if err != nil {
		return "", "", err
	}
	return sh.ID, "/s/" + tok, nil
}

// clipLabel shortens s to at most n bytes on a rune boundary.
func clipLabel(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return strings.TrimSpace(s[:n])
}

// unpublish takes one evaluation off the board and revokes its link.
func (t refTarget) unpublish(evalID, by string) ([]reference.Entry, error) {
	removed, shareID, err := reference.Open(t.storageRoot).Unpublish(evalID, t.now())
	if err != nil {
		return nil, err
	}
	if shareID != "" {
		if _, err := sharelinks.Open(t.paths.ProjectRoot).Revoke(shareID, by); err != nil && !errors.Is(err, sharelinks.ErrNotFound) && !errors.Is(err, sharelinks.ErrRevoked) {
			return removed, fmt.Errorf("taken off the board, but its report card link was not turned off: %w", err)
		}
	}
	return removed, nil
}

func refImportCmd(ctx context.Context, cfg *lib.Config, d *db.DB, from, owner, evalsDir string, asJSON bool, logger schemas.Logger, w io.Writer) int {
	org, err := referenceOrg(ctx, cfg, d, true, owner)
	if err != nil {
		return fail("reference import: %v", err)
	}
	rep, _, err := importIntoOrg(ctx, cfg, d, org.ID, from, logger)
	if err != nil {
		return fail("reference import: %v", err)
	}
	t, err := newRefTarget(cfg, org, logger)
	if err != nil {
		return fail("reference import: %v", err)
	}
	var ids []string
	for _, e := range rep.Experiments {
		if e.Status == "imported" || e.Status == "skipped" {
			ids = append(ids, e.ExpID)
		}
	}
	wrapped, err := t.wrapImported(ctx, ids, evalsDir)
	if err != nil {
		return fail("reference import: %v", err)
	}
	if asJSON {
		code := printJSON(map[string]any{"org": org.Slug, "import": rep, "evaluations": wrapped}, "reference import")
		if code == exitOK && rep.Refused > 0 {
			return 1
		}
		return code
	}
	printImport(w, org, rep)
	for _, r := range wrapped {
		line := fmt.Sprintf("  %-8s %s", r.Status, r.Evaluation)
		if len(r.Benchmark) > 0 {
			line += " (" + strings.Join(r.Benchmark, ", ") + ")"
		}
		if r.Reason != "" {
			line += ": " + r.Reason
		}
		fmt.Fprintln(w, line)
	}
	fmt.Fprintln(w, "next: check each report card in the app, then `bifrost-bench reference publish <evaluation>`")
	if rep.Refused > 0 {
		return 1
	}
	return exitOK
}

func refPublishCmd(ctx context.Context, cfg *lib.Config, d *db.DB, evalID, by string, asJSON bool, logger schemas.Logger, w io.Writer) int {
	org, err := referenceOrg(ctx, cfg, d, false, "")
	if err != nil {
		return fail("reference publish: %v", err)
	}
	t, err := newRefTarget(cfg, org, logger)
	if err != nil {
		return fail("reference publish: %v", err)
	}
	res, err := t.publish(ctx, evalID, by)
	if err != nil {
		return fail("reference publish: %v", err)
	}
	_, _ = d.AppendAudit(ctx, db.AuditEntry{OrgID: org.ID, ActorType: db.ActorSystem, ActorID: "cli", Action: "reference.publish",
		TargetType: "evaluation", TargetID: evalID, Details: db.AuditDetails(map[string]any{"entries": len(res.Entries), "by": by})})
	if asJSON {
		return printJSON(res, "reference publish")
	}
	fmt.Fprintf(w, "published %s: %d entries on the reference board (%s)\n", res.Evaluation, len(res.Entries), res.Board)
	printReferenceEntries(w, res.Entries)
	for _, s := range res.Skipped {
		fmt.Fprintf(w, "  left out: %s\n", s)
	}
	fmt.Fprintf(w, "report card: %s%s\n", strings.TrimRight(cfg.Auth.PublicURL, "/"), res.ReportPath)
	return exitOK
}

func refUnpublishCmd(ctx context.Context, cfg *lib.Config, d *db.DB, evalID string, logger schemas.Logger, w io.Writer) int {
	org, err := referenceOrg(ctx, cfg, d, false, "")
	if err != nil {
		return fail("reference unpublish: %v", err)
	}
	t, err := newRefTarget(cfg, org, logger)
	if err != nil {
		return fail("reference unpublish: %v", err)
	}
	removed, err := t.unpublish(evalID, "operator")
	if err != nil {
		return fail("reference unpublish: %v", err)
	}
	if len(removed) == 0 {
		fmt.Fprintf(w, "%s is not on the reference board\n", evalID)
		return exitOK
	}
	_, _ = d.AppendAudit(ctx, db.AuditEntry{OrgID: org.ID, ActorType: db.ActorSystem, ActorID: "cli", Action: "reference.unpublish",
		TargetType: "evaluation", TargetID: evalID, Details: db.AuditDetails(map[string]any{"entries": len(removed)})})
	fmt.Fprintf(w, "unpublished %s: %d entries removed, report card link turned off\n", evalID, len(removed))
	return exitOK
}

func refListCmd(ctx context.Context, cfg *lib.Config, d *db.DB, asJSON bool, logger schemas.Logger, w io.Writer) int {
	b, err := reference.Open(hostedLayout(cfg).StorageRoot).Load()
	if err != nil {
		return fail("reference list: %v", err)
	}
	var unpublished []string
	if org, err := referenceOrg(ctx, cfg, d, false, ""); err == nil {
		if t, err := newRefTarget(cfg, org, logger); err == nil {
			published := map[string]bool{}
			for _, id := range b.Evaluations() {
				published[id] = true
			}
			evs, _ := evalstore.Open(t.paths.ProjectRoot).List()
			for _, ev := range evs {
				if !published[ev.ID] && reference.Check(ev) == nil {
					unpublished = append(unpublished, ev.ID)
				}
			}
			sort.Strings(unpublished)
		}
	}
	if asJSON {
		return printJSON(map[string]any{"entries": b.Entries, "updated_at": b.UpdatedAt, "unpublished": unpublished}, "reference list")
	}
	if len(b.Entries) == 0 {
		fmt.Fprintln(w, "the reference board is empty: nothing is published")
	} else {
		printReferenceEntries(w, b.Entries)
	}
	if len(unpublished) > 0 {
		fmt.Fprintf(w, "finished reference evaluations not published: %s\n", strings.Join(unpublished, ", "))
	}
	return exitOK
}

func printReferenceEntries(w io.Writer, es []reference.Entry) {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "BENCHMARK\tAGENT\tMODEL\tSCORE\t95% RANGE\tCOST/SOLVED\tRUNS\tRAN\tSEAL\tEVALUATION")
	for _, e := range es {
		agent := e.Agent
		if e.Version > 0 {
			agent += fmt.Sprintf(" v%d", e.Version)
		}
		if e.AgentBuild != "" {
			agent += " (" + e.AgentBuild + ")"
		}
		cost := "unknown"
		if e.CostPerSolvedUSD != nil {
			cost = fmt.Sprintf("$%.4f", *e.CostPerSolvedUSD)
		} else if e.CostUSD != nil {
			cost = "none solved"
		}
		ran := ""
		if !e.RanAt.IsZero() {
			ran = e.RanAt.Format("2006-01-02")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%.0f%%\t%.0f-%.0f%%\t%s\t%d\t%s\t%s\t%s\n", e.Benchmark, agent, e.Model,
			e.Score*100, e.CILow*100, e.CIHigh*100, cost, e.Runs, ran, short(e.SealHash), e.EvaluationID)
	}
	_ = tw.Flush()
}
