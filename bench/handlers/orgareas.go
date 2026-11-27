package handlers

import (
	"context"
	"sort"
	"sync"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/quota"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/webhooks"
	"github.com/valyala/fasthttp"
)

// Services are the per-org services the web-parity areas use. Any field may
// be nil; a handler that needs a missing one answers 503.
type Services struct {
	OrgID  string
	Hosted bool

	Projects projectfs.Store
	Keys     keystore.Store
	Quota    quota.Store

	Jobs     JobsAPI
	Launcher Launcher
	Index    ReadIndex
	Suites   SuiteSource
	Agents   AgentSource
	Catalog  CatalogSource

	// Releases is the org's release log and Analyzer writes an experiment's
	// analysis; releases from an evaluation use both. nil: not available.
	Releases ReleaseLog
	Analyzer Analyzer

	// Holds reads and releases the breakpoints of a run (X20b, rundebug.go);
	// nil when this server cannot reach the fault proxy its runs use.
	Holds schemas.HoldController

	// Reindex brings the org's read indexes (the in-memory experiment index
	// and, hosted, the database results index) up to date after runs were
	// added outside a job, such as a sealed-run upload. nil: nothing to do.
	Reindex func(ctx context.Context) error

	// Data erases stored data on request (lane X16, dataops.go). nil: the
	// delete routes answer 503.
	Data DataEraser
	// WebhookCipher seals webhook signing secrets at rest (the keystore's
	// master key); nil stores them in plaintext (0600).
	WebhookCipher webhooks.Cipher
	// WorkerSlots is how many runs the org's worker for target runs at once
	// (N90): target "" or "shared" is the shared worker, "worker:<pool>" an
	// own worker's pool, a platform target 0 (not bounded). nil in local
	// mode; the estimate is then not bounded by it.
	WorkerSlots func(ctx context.Context, target string) (int, error)
	// WorkerStatus says whether the worker for target is online and when
	// it was last seen (fix wave M3-21), from the same runner rows as
	// WorkerSlots. nil in local mode.
	WorkerStatus func(ctx context.Context, target string) (WorkerStatus, error)
}

// OrgEnv is what an org area gets when it registers its routes.
type OrgEnv struct {
	Cfg      *lib.Config // Cfg.Root = org project root
	Mode     schemas.Mode
	Services Services
	Write    lib.Middleware   // nil while listing routes (OrgRoutes)
	Common   []lib.Middleware // reader + 1 MiB body limit
	Upload   []lib.Middleware // reader + lib.MaxUploadBytes body limit
}

// R wraps a read handler with the common middlewares.
func (e OrgEnv) R(h fasthttp.RequestHandler) fasthttp.RequestHandler {
	return lib.ChainMiddlewares(h, e.Common...)
}

// W wraps a write handler with the write middleware (when set) and the common ones.
func (e OrgEnv) W(h fasthttp.RequestHandler) fasthttp.RequestHandler {
	if e.Write != nil {
		h = e.Write(h)
	}
	return lib.ChainMiddlewares(h, e.Common...)
}

// U wraps an upload handler with the write middleware (when set) and the upload ones.
func (e OrgEnv) U(h fasthttp.RequestHandler) fasthttp.RequestHandler {
	if e.Write != nil {
		h = e.Write(h)
	}
	return lib.ChainMiddlewares(h, e.Upload...)
}

// OrgArea registers one area's org-scoped routes.
type OrgArea func(env OrgEnv, r *router.Router)

var (
	orgAreasMu sync.Mutex
	orgAreas   = map[string]OrgArea{}
)

// RegisterOrgArea adds an area; lanes call it from init(). A duplicate name panics.
func RegisterOrgArea(name string, f OrgArea) {
	orgAreasMu.Lock()
	defer orgAreasMu.Unlock()
	if _, dup := orgAreas[name]; dup {
		panic("handlers: org area registered twice: " + name)
	}
	orgAreas[name] = f
}

// OrgAreas returns the registered areas sorted by name.
func OrgAreas() []OrgArea {
	orgAreasMu.Lock()
	defer orgAreasMu.Unlock()
	names := make([]string, 0, len(orgAreas))
	for n := range orgAreas {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]OrgArea, 0, len(names))
	for _, n := range names {
		out = append(out, orgAreas[n])
	}
	return out
}
