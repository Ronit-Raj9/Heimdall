package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/benchcatalog"
	"github.com/maximhq/bifrost/bench/dataset"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/tenancy"
)

// Lane L12: the catalog.import job kind and the starter catalog seeding of
// new orgs (starter agents and suites from the server's bench folder).
func init() {
	registerJobKind(jobKindSpec{
		Kind: handlers.CatalogImportKind, Local: true, Hosted: true,
		Validate: func(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
			p, _, _, err := catalogImportParams(env, raw)
			if err != nil {
				return nil, "", fmt.Errorf("%w: %v", jobs.ErrInvalidParams, err)
			}
			return p, p.Suite, nil
		},
		Run: func(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
			p, importer, from, err := catalogImportParams(env, j.Params)
			if err != nil {
				return nil, err
			}
			if p.URL != "" {
				if from, err = fetchCatalogDataset(ctx, env, j, p, log); err != nil {
					return nil, err
				}
			}
			log.Printf("importing %s with the %s importer into suite %s (limit %d)", p.Entry, importer, p.Suite, p.Limit)
			benchmarks := ""
			if env.App != nil && env.App.cfg != nil && env.App.cfg.BenchmarksRoot != "" {
				benchmarks = env.App.cfg.Path(env.App.cfg.BenchmarksRoot)
			}
			res, err := benchcatalog.Import(ctx, importer, env.ProjectRoot, benchmarks, from, p, jobSchemaLogger{log})
			if res != nil {
				// Every skip is logged, also when nothing was imported, and
				// the result stays on the failed job so the import dialog
				// can list the skipped tasks (fix wave 3, N263).
				for _, s := range res.Skipped {
					log.Printf("skipped %s: %s", s.ID, s.Reason)
				}
			}
			if err != nil {
				if res == nil {
					return nil, err
				}
				return res, err
			}
			log.Printf("suite %s: %d task(s) written", res.Suite, len(res.Tasks))
			// The Data tab's version log, once per import (fix wave 3, N268).
			source := "Catalog import " + p.Entry
			if importer == "harbor" {
				source = "Harbor import"
			}
			if _, err := dataset.SyncSuite(env.ProjectRoot, res.Suite, res.SuiteDir, jobActor(j), source, time.Now()); err != nil {
				log.Warnf("the imported tasks were not recorded as a dataset version: %v", err)
			}
			for i, n := range res.Notes {
				log.Printf("next %d: %s", i+1, n)
			}
			return res, nil
		},
	})
	orgInitHooks = append(orgInitHooks, seedStarterCatalog)
}

// fetchCatalogDataset downloads p.URL into uploads/datasets/<entry> (SSRF
// guarded, size capped) and returns the resolved data path.
func fetchCatalogDataset(ctx context.Context, env jobEnv, j *jobs.Job, p benchcatalog.ImportParams, log jobs.Logger) (string, error) {
	store := env.Services.Projects
	if store == nil {
		s, err := projectfs.Open(env.ProjectRoot)
		if err != nil {
			return "", fmt.Errorf("open project files: %w", err)
		}
		store = s
	}
	log.Printf("fetching the dataset from %s (at most %d MiB)", redactQuery(p.URL), projectfs.MaxUploadFileBytes>>20)
	rel, err := benchcatalog.FetchDataset(ctx, catalogFetcher, store, p.Entry, p.URL, j.CreatedBy)
	if err != nil {
		return "", err
	}
	log.Printf("dataset saved at %s", rel)
	return benchcatalog.ResolveFrom(env.ProjectRoot, rel, env.Hosted)
}

// catalogFetcher is the dataset fetcher of catalog imports (nil = default).
var catalogFetcher *benchcatalog.Fetcher

func redactQuery(raw string) string {
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		return raw[:i]
	}
	return raw
}

func catalogImportParams(env jobEnv, raw json.RawMessage) (benchcatalog.ImportParams, string, string, error) {
	var p benchcatalog.ImportParams
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return p, "", "", fmt.Errorf("params: %v", err)
	}
	src := benchcatalog.Source{Root: benchcatalog.SourceRoot(), Hosted: env.Hosted}
	importer, from, err := src.Normalize(env.ProjectRoot, &p, env.Hosted)
	return p, importer, from, err
}

// seedStarterCatalog gives a new hosted org the starter agents once (and
// the practice suite only on a development server, benchcatalog.Seed).
func seedStarterCatalog(p tenancy.OrgPaths) error {
	src := benchcatalog.Source{Root: benchcatalog.SourceRoot(), Hosted: true}
	if _, err := src.Seed(p.ProjectRoot); err != nil {
		return fmt.Errorf("seed starter catalog: %w", err)
	}
	return nil
}

// jobSchemaLogger adapts a job log to schemas.Logger for importers.
type jobSchemaLogger struct{ l jobs.Logger }

func (j jobSchemaLogger) Debug(msg string, args ...any) {}
func (j jobSchemaLogger) Info(msg string, args ...any)  { j.l.Printf("%s", kv(msg, args)) }
func (j jobSchemaLogger) Warn(msg string, args ...any)  { j.l.Warnf("%s", kv(msg, args)) }
func (j jobSchemaLogger) Error(msg string, args ...any) { j.l.Errorf("%s", kv(msg, args)) }

func kv(msg string, args []any) string {
	var b strings.Builder
	b.WriteString(msg)
	for i := 0; i+1 < len(args); i += 2 {
		fmt.Fprintf(&b, " %v=%v", args[i], args[i+1])
	}
	return b.String()
}
