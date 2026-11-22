package main

// Public site (lane Y08, audit G4-03, G4-09, G4-19): the platform area that
// serves the landing page's data, the public benchmark catalog, the user
// guides, pricing, the downloads the server image built and security.txt,
// without sign-in (handlers/publicsite.go). The library is read through the
// same suite loader and summary cache the org Benchmarks pages use, so the
// counts are the server's real ones.

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/benchcatalog"
	"github.com/maximhq/bifrost/bench/ci"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/packs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/server"
	"github.com/maximhq/bifrost/bench/suite"
)

// DownloadsDirEnv names the folder of built downloads (manifest.json,
// SHA256SUMS and the files). The server image sets it to
// /srv/heimdall/downloads (deploy/hosted/Dockerfile.server).
const DownloadsDirEnv = "HEIMDALL_DOWNLOADS_DIR"

// SecurityContactEnv overrides the security.txt Contact (mailto: or https:).
const SecurityContactEnv = "HEIMDALL_SECURITY_CONTACT"

// publicPricing is the plan source of the pricing page; nil serves the beta
// page. A plans lane sets it from an init() in its own file.
var publicPricing func() handlers.PublicPricing

func init() {
	hostedAreas = append(hostedAreas, func(env *hostedEnv) (server.RouteRegistrar, error) {
		a := env.base
		lib := &publicLibrary{a: a, library: a.library}
		return handlers.NewPublicSiteHandler(handlers.PublicSiteConfig{
			Mode:            a.cfg.Server.Mode,
			Library:         lib.list,
			Pricing:         sitePricing(env),
			DownloadsDir:    strings.TrimSpace(os.Getenv(DownloadsDirEnv)),
			GitLabTemplate:  ci.GitLabTemplate(),
			PublicURL:       a.cfg.Auth.PublicURL,
			Version:         benchVersion,
			SecurityContact: strings.TrimSpace(os.Getenv(SecurityContactEnv)),
			MCP:             true, // mcphttp_hosted.go
		}), nil
	})
}

// publicLibrary lists the built-in library for the public catalog. The
// summaries come from the shared suite summary cache; the list is kept for
// a minute so anonymous traffic never reloads packs.
type publicLibrary struct {
	a       *app
	library string // library root (suite.LibraryDir); "" for none

	once  sync.Once
	suite *handlers.SuitesHandler

	mu   sync.Mutex
	last handlers.PublicLibrary
	at   time.Time
}

const publicLibraryTTL = time.Minute

func (p *publicLibrary) list(ctx context.Context) (handlers.PublicLibrary, error) {
	empty := handlers.PublicLibrary{Benchmarks: []handlers.PublicBenchmark{}, Packs: []handlers.PublicPack{}, UpdatedAt: time.Now().UTC()}
	if p.library == "" {
		return empty, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.at.IsZero() && time.Since(p.at) < publicLibraryTTL {
		return p.last, nil
	}
	p.once.Do(func() {
		// No org folder: only the library layer is listed. The root is a
		// path that never exists, so no org pack can shadow a library one.
		src := serveSuites{root: filepath.Join(p.library, ".no-org"), cfg: p.a.cfg, reg: p.a.reg, library: suite.LibraryBenchmarks(p.library)}
		p.suite = handlers.NewSuitesHandler(p.a.cfg.Server.Mode, src).WarmCache()
	})
	sums, err := p.suite.ListSummaries(ctx)
	if err != nil {
		return empty, err
	}
	out := buildPublicLibrary(sums, catalogMeta())
	p.last, p.at = out, time.Now()
	return out, nil
}

// catalogInfo is what the benchmark catalog knows about a suite id.
type catalogInfo struct {
	title, summary, license, source, track, grading string
}

// catalogMeta maps suite ids to their catalog entry (title, upstream
// licence and source), from the hosted catalog.
func catalogMeta() map[string]catalogInfo {
	out := map[string]catalogInfo{}
	src := benchcatalog.Source{Root: benchcatalog.SourceRoot(), Hosted: true}
	for _, e := range src.List("") {
		info := catalogInfo{title: e.Title, summary: e.Summary, license: e.License, source: e.Source, track: e.Track, grading: e.Grading}
		for _, id := range append([]string{e.SuiteID}, e.Suites...) {
			if id == "" {
				continue
			}
			if _, seen := out[id]; !seen {
				out[id] = info
			}
		}
	}
	return out
}

// buildPublicLibrary turns suite summaries into the public catalog: counts,
// licences and packs only. Held-out and quarantined tasks count as tasks
// but not as ready.
func buildPublicLibrary(sums []schemas.SuiteSummary, meta map[string]catalogInfo) handlers.PublicLibrary {
	out := handlers.PublicLibrary{Available: true, Benchmarks: []handlers.PublicBenchmark{}, Packs: []handlers.PublicPack{}, UpdatedAt: time.Now().UTC()}
	ids := make([]string, 0, len(sums))
	for _, s := range sums {
		if s.Builtin && s.Error == "" {
			ids = append(ids, s.ID)
		}
	}
	sort.Strings(ids)
	packOf := map[string][]string{}
	for _, pk := range packs.All() {
		if pk.ID == packs.AllCompatible {
			continue
		}
		members := packs.Resolve(pk.ID, ids)
		if len(members) == 0 {
			continue
		}
		out.Packs = append(out.Packs, handlers.PublicPack{ID: pk.ID, Name: pk.Name, Description: pk.Description, Benchmarks: members})
		for _, id := range members {
			packOf[id] = append(packOf[id], pk.ID)
		}
	}
	for _, s := range sums {
		if !s.Builtin {
			continue
		}
		m := meta[s.ID]
		b := handlers.PublicBenchmark{
			ID: s.ID, Title: s.ID, Description: strings.TrimSpace(s.Description),
			Track: s.Track, Grading: s.Grading, Source: m.source,
			TaskCount: s.TaskCount, ReadyCount: s.AdmittedCount, HeldOut: s.HeldOutCount,
			Packs: append([]string{}, packOf[s.ID]...), Licenses: taskLicenses(s, m.license),
		}
		if m.title != "" {
			b.Title = m.title
		}
		if b.Description == "" {
			b.Description = m.summary
		}
		if b.Track == "" {
			b.Track = m.track
		}
		if b.Grading == "" {
			b.Grading = m.grading
		}
		if s.Error != "" {
			b.Error = "this benchmark could not be read on the server"
			b.TaskCount, b.ReadyCount, b.HeldOut = 0, 0, 0
		}
		out.Benchmarks = append(out.Benchmarks, b)
		out.Totals.Benchmarks++
		out.Totals.Tasks += b.TaskCount
		out.Totals.Ready += b.ReadyCount
	}
	sort.Slice(out.Benchmarks, func(i, j int) bool { return out.Benchmarks[i].ID < out.Benchmarks[j].ID })
	out.Totals.Packs = len(out.Packs)
	return out
}

// taskLicenses lists the distinct licences the suite's tasks record, or the
// upstream licence from the catalog when they record none.
func taskLicenses(s schemas.SuiteSummary, upstream string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range s.Tasks {
		if t.Provenance == nil {
			continue
		}
		if l := strings.TrimSpace(t.Provenance.License); l != "" && !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	if len(out) == 0 && strings.TrimSpace(upstream) != "" {
		out = append(out, strings.TrimSpace(upstream))
	}
	sort.Strings(out)
	if out == nil {
		out = []string{}
	}
	return out
}
