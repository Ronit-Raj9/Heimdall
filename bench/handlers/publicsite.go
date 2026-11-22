package handlers

// Public site (lane Y08, audit G4-03, G4-09, G4-19): the few routes a
// visitor reaches without signing in. They hold no organization data:
//
//	GET /api/bench/public/overview          what the server offers: library totals, version, commit
//	GET /api/bench/public/benchmarks        the built-in library, read-only (no task content)
//	GET /api/bench/public/docs              the user guides (internal notes left out)
//	GET /api/bench/public/docs/{name}       one guide
//	GET /api/bench/public/pricing           plans (free during beta unless a plan source is set)
//	GET /api/bench/public/downloads         the files the server image built at its commit
//	GET /downloads/{file}                   one of those files, the GitLab template, SHA256SUMS
//	GET /.well-known/security.txt           RFC 9116 contact for security reports
//
// Every other route keeps answering 401 to anonymous callers (tenancy policy).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fasthttp/router"
	benchdocs "github.com/maximhq/bifrost/bench/docs"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// PublicPrefix is the API prefix of the public site's read routes.
const PublicPrefix = APIPrefix + "/public"

// DownloadsPath is where the server serves its built files.
const DownloadsPath = "/downloads"

// SecurityTxtPath is the RFC 9116 location of the security contact.
const SecurityTxtPath = "/.well-known/security.txt"

// DownloadsManifestFile is the manifest the image build writes beside the
// files (cmd/heimdall-dist).
const DownloadsManifestFile = "manifest.json"

// DownloadsChecksumsFile lists "sha256  name" for every built file, the
// format `sha256sum -c` reads.
const DownloadsChecksumsFile = "SHA256SUMS"

// MCPPath is the remote MCP endpoint (Streamable HTTP, bearer API token).
const MCPPath = "/mcp"

// GitLabTemplateFile is the GitLab CI template served from the server, with
// this server's address filled in as the HEIMDALL_URL default.
const GitLabTemplateFile = "heimdall.gitlab-ci.yml"

// PublicBenchmark is one benchmark of the built-in library as the public
// catalog lists it: counts and terms, never task content.
type PublicBenchmark struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Track       string   `json:"track,omitempty"`   // coding, terminal, support ...
	Grading     string   `json:"grading,omitempty"` // verified | judged
	Licenses    []string `json:"licenses"`          // SPDX ids of the tasks (and of the upstream data when the tasks record none)
	Source      string   `json:"source,omitempty"`  // where the upstream data comes from
	TaskCount   int      `json:"task_count"`
	ReadyCount  int      `json:"ready_count"`              // tasks that passed the readiness checks and still match the lock
	HeldOut     int      `json:"held_out_count,omitempty"` // tasks kept out of sight to catch overfitting
	Packs       []string `json:"packs"`                    // evaluation packs that include it
	Error       string   `json:"error,omitempty"`          // why it could not be read; counts are then zero
}

// PublicPack is one evaluation pack with the library benchmarks it holds.
type PublicPack struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Benchmarks  []string `json:"benchmarks"`
}

// PublicLibraryTotals are the library's headline counts.
type PublicLibraryTotals struct {
	Benchmarks int `json:"benchmarks"`
	Tasks      int `json:"tasks"`
	Ready      int `json:"ready"`
	Packs      int `json:"packs"`
}

// PublicLibrary is GET /public/benchmarks.
type PublicLibrary struct {
	Available  bool                `json:"available"` // false: this server has no built-in library
	Totals     PublicLibraryTotals `json:"totals"`
	Benchmarks []PublicBenchmark   `json:"benchmarks"`
	Packs      []PublicPack        `json:"packs"`
	UpdatedAt  time.Time           `json:"updated_at"`
}

// PublicOverview is GET /public/overview: what the landing page shows.
type PublicOverview struct {
	Product   string              `json:"product"`
	Version   string              `json:"version"`
	Commit    string              `json:"commit,omitempty"` // the commit the server image was built at
	Library   PublicLibraryTotals `json:"library"`
	Downloads bool                `json:"downloads"` // the server has built files to download
	MCP       bool                `json:"mcp"`       // the remote MCP endpoint is on
	MCPPath   string              `json:"mcp_path,omitempty"`
}

// PublicLimit is one stated limit of a plan.
type PublicLimit struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// PublicPlan is one plan on the pricing page.
type PublicPlan struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Price    string        `json:"price"`  // display text, e.g. "Free" or "$49"
	Period   string        `json:"period"` // e.g. "during beta", "per seat per month"
	Summary  string        `json:"summary"`
	Features []string      `json:"features"`
	Limits   []PublicLimit `json:"limits"`
	CTA      string        `json:"cta"` // signup | contact
}

// PublicPricing is GET /public/pricing.
type PublicPricing struct {
	Beta     bool         `json:"beta"`
	Headline string       `json:"headline"`
	Note     string       `json:"note"`
	Plans    []PublicPlan `json:"plans"`
	FAQ      []PublicFAQ  `json:"faq"`
}

// PublicFAQ is one question on the pricing page.
type PublicFAQ struct {
	Q string `json:"q"`
	A string `json:"a"`
}

// DownloadFile is one built file.
type DownloadFile struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"` // cli | python-wheel | python-sdist | typescript | source
	OS     string `json:"os,omitempty"`
	Arch   string `json:"arch,omitempty"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// DownloadKinds of DownloadFile.Kind.
const (
	DownloadCLI          = "cli"
	DownloadPythonWheel  = "python-wheel"
	DownloadPythonSdist  = "python-sdist"
	DownloadTypeScript   = "typescript"
	DownloadSource       = "source"
	DownloadCITemplate   = "ci-template"
	DownloadChecksumList = "checksums"
)

// DownloadsManifest is manifest.json and GET /public/downloads.
type DownloadsManifest struct {
	Available bool           `json:"available"`
	Commit    string         `json:"commit"`
	Version   string         `json:"version"`
	BuiltAt   *time.Time     `json:"built_at,omitempty"`
	Files     []DownloadFile `json:"files"`
	// Filled by the server, not the manifest:
	BaseURL   string `json:"base_url,omitempty"`   // this server's public address
	MCPURL    string `json:"mcp_url,omitempty"`    // the remote MCP endpoint
	GitLabURL string `json:"gitlab_url,omitempty"` // the GitLab CI template
}

// PublicSiteConfig is what the public site is built from.
type PublicSiteConfig struct {
	Mode schemas.Mode
	// Library lists the built-in library; nil means none.
	Library func(ctx context.Context) (PublicLibrary, error)
	// Pricing returns the plans; nil serves the beta page.
	Pricing func() PublicPricing
	// DownloadsDir holds manifest.json, SHA256SUMS and the files; "" or a
	// folder without a manifest means nothing to download.
	DownloadsDir string
	// GitLabTemplate is the GitLab CI template; nil serves none.
	GitLabTemplate []byte
	PublicURL      string
	Version        string
	// SecurityContact is the security.txt Contact (mailto: or https:); ""
	// uses <public url>/security.
	SecurityContact string
	// MCP reports that the remote MCP endpoint is registered.
	MCP bool
	Now func() time.Time
}

// PublicSiteHandler serves the public site routes.
type PublicSiteHandler struct {
	c PublicSiteConfig

	mu       sync.Mutex
	manifest *DownloadsManifest
	stamp    time.Time
}

// NewPublicSiteHandler builds the public site handler.
func NewPublicSiteHandler(c PublicSiteConfig) *PublicSiteHandler {
	if c.Now == nil {
		c.Now = time.Now
	}
	c.PublicURL = strings.TrimRight(strings.TrimSpace(c.PublicURL), "/")
	return &PublicSiteHandler{c: c}
}

// RegisterRoutes registers the public routes.
func (h *PublicSiteHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET(PublicPrefix+"/overview", lib.ChainMiddlewares(h.overview, middlewares...))
	r.GET(PublicPrefix+"/benchmarks", lib.ChainMiddlewares(h.benchmarks, middlewares...))
	r.GET(PublicPrefix+"/docs", lib.ChainMiddlewares(h.docs, middlewares...))
	r.GET(PublicPrefix+"/docs/{name}", lib.ChainMiddlewares(h.doc, middlewares...))
	r.GET(PublicPrefix+"/pricing", lib.ChainMiddlewares(h.pricing, middlewares...))
	r.GET(PublicPrefix+"/downloads", lib.ChainMiddlewares(h.downloads, middlewares...))
	r.GET(DownloadsPath+"/{file}", lib.ChainMiddlewares(h.download, middlewares...))
	r.GET(SecurityTxtPath, lib.ChainMiddlewares(h.securityTxt, middlewares...))
}

func (h *PublicSiteHandler) library(ctx *fasthttp.RequestCtx) (PublicLibrary, error) {
	if h.c.Library == nil {
		return PublicLibrary{Benchmarks: []PublicBenchmark{}, Packs: []PublicPack{}, UpdatedAt: h.c.Now().UTC()}, nil
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	return h.c.Library(work)
}

func (h *PublicSiteHandler) overview(ctx *fasthttp.RequestCtx) {
	l, err := h.library(ctx)
	if err != nil {
		sendDepError(ctx, h.c.Mode, "read the library", err)
		return
	}
	m := h.readManifest()
	out := PublicOverview{Product: "Heimdall", Version: h.c.Version, Commit: m.Commit, Library: l.Totals, Downloads: m.Available, MCP: h.c.MCP}
	if h.c.MCP {
		out.MCPPath = MCPPath
	}
	publicCache(ctx, 60)
	lib.SendJSON(ctx, h.c.Mode, fasthttp.StatusOK, out)
}

func (h *PublicSiteHandler) benchmarks(ctx *fasthttp.RequestCtx) {
	l, err := h.library(ctx)
	if err != nil {
		sendDepError(ctx, h.c.Mode, "read the library", err)
		return
	}
	publicCache(ctx, 60)
	lib.SendJSON(ctx, h.c.Mode, fasthttp.StatusOK, l)
}

func (h *PublicSiteHandler) docs(ctx *fasthttp.RequestCtx) {
	names := benchdocs.PublicNames()
	out := DocList{Docs: make([]DocSummary, 0, len(names))}
	for _, n := range names {
		if data, ok := benchdocs.ReadPublic(n); ok {
			out.Docs = append(out.Docs, DocSummary{Name: n, Title: docTitle(n, data)})
		}
	}
	publicCache(ctx, 300)
	lib.SendJSON(ctx, h.c.Mode, fasthttp.StatusOK, out)
}

func (h *PublicSiteHandler) doc(ctx *fasthttp.RequestCtx) {
	name, _ := ctx.UserValue("name").(string)
	name = strings.TrimSuffix(name, ".md")
	if !validDocName(name) {
		lib.SendError(ctx, h.c.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest,
			"doc name must be letters, digits, '-', '_' or '.', for example quickstart")
		return
	}
	data, ok := readGuide(name, true)
	if !ok {
		lib.SendError(ctx, h.c.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound,
			"no guide named "+name+": GET "+PublicPrefix+"/docs lists them")
		return
	}
	publicCache(ctx, 300)
	lib.SendJSON(ctx, h.c.Mode, fasthttp.StatusOK, DocBody{Name: name, Title: docTitle(name, data), Markdown: string(data)})
}

func (h *PublicSiteHandler) pricing(ctx *fasthttp.RequestCtx) {
	p := BetaPricing()
	if h.c.Pricing != nil {
		p = h.c.Pricing()
	}
	if p.Plans == nil {
		p.Plans = []PublicPlan{}
	}
	if p.FAQ == nil {
		p.FAQ = []PublicFAQ{}
	}
	publicCache(ctx, 300)
	lib.SendJSON(ctx, h.c.Mode, fasthttp.StatusOK, p)
}

// BetaPricing is the pricing page while Heimdall is in beta: no charge, the
// customer's own model key pays the provider directly.
func BetaPricing() PublicPricing {
	return PublicPricing{
		Beta:     true,
		Headline: "Free during beta. Bring your own model key.",
		Note:     "Heimdall charges nothing while it is in beta. Model calls your agent makes go to your provider on your own key and are billed by your provider; Heimdall adds no markup. Paid plans will be announced here at least 30 days before any charge, and nothing is billed without your consent.",
		Plans: []PublicPlan{{
			ID: "beta", Name: "Beta", Price: "Free", Period: "during beta",
			Summary: "Everything Heimdall does, for every team that signs up during the beta.",
			Features: []string{
				"Evaluate your agent on the built-in benchmark library or your own tasks",
				"Report cards with intervals, faults and cost per task",
				"Pull request gate for GitHub Actions and GitLab CI",
				"Shared worker, or connect your own worker at no cost",
				"Command line tool, Python and TypeScript SDKs, remote MCP endpoint",
				"Read-only share links for report cards",
			},
			Limits: []PublicLimit{
				{Name: "Model spend", Value: "Paid by you to your provider, on your own key"},
				{Name: "Shared worker", Value: "Available to every organization; any limit is shown in the app before you launch"},
				{Name: "Your own worker", Value: "Unlimited"},
				{Name: "Evidence retention", Value: "Kept until you delete it or the organization"},
			},
			CTA: "signup",
		}},
		FAQ: []PublicFAQ{
			{Q: "Why do I need my own model key?", A: "Evaluations call the model your agent uses. Running them on your own key keeps the spend visible on your provider's bill and keeps your data under your provider agreement."},
			{Q: "Is any limit applied without telling me?", A: "No. Every limit that applies to your organization is shown in the app and returned by the API before an evaluation starts."},
			{Q: "What happens when the beta ends?", A: "Paid plans will be published on this page at least 30 days before any charge. Organizations that do not choose a plan keep read access to their evidence and can export or delete it."},
		},
	}
}

func (h *PublicSiteHandler) downloads(ctx *fasthttp.RequestCtx) {
	m := h.readManifest()
	m.BaseURL = h.c.PublicURL
	if h.c.MCP && h.c.PublicURL != "" {
		m.MCPURL = h.c.PublicURL + MCPPath
	}
	if h.c.GitLabTemplate != nil && h.c.PublicURL != "" {
		m.GitLabURL = h.c.PublicURL + DownloadsPath + "/" + GitLabTemplateFile
	}
	ctx.Response.Header.Set("Cache-Control", "no-cache")
	lib.SendJSON(ctx, h.c.Mode, fasthttp.StatusOK, m)
}

var downloadNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$`)

func (h *PublicSiteHandler) download(ctx *fasthttp.RequestCtx) {
	name, _ := ctx.UserValue("file").(string)
	if !downloadNameRe.MatchString(name) || strings.Contains(name, "..") {
		lib.SendError(ctx, h.c.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "no download named "+name)
		return
	}
	if name == GitLabTemplateFile && h.c.GitLabTemplate != nil {
		ctx.Response.Header.Set("Cache-Control", "no-cache")
		ctx.SetContentType("text/yaml; charset=utf-8")
		ctx.SetBody(GitLabTemplateFor(h.c.GitLabTemplate, h.c.PublicURL))
		return
	}
	m := h.readManifest()
	// <file>.sha256: the listed file's digest and name, one line, which the
	// worker install script (deploy/runner/install.sh) checks every
	// download against.
	if base, ok := strings.CutSuffix(name, ".sha256"); ok && m.Available {
		for _, f := range m.Files {
			if f.Name == base && f.SHA256 != "" {
				ctx.Response.Header.Set("Cache-Control", "no-cache")
				ctx.SetContentType("text/plain; charset=utf-8")
				ctx.SetBodyString(f.SHA256 + "  " + f.Name + "\n")
				return
			}
		}
	}
	listed := name == DownloadsChecksumsFile || name == DownloadsManifestFile
	var file DownloadFile
	for _, f := range m.Files {
		if f.Name == name {
			listed, file = true, f
			break
		}
	}
	if !m.Available || !listed {
		lib.SendError(ctx, h.c.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound,
			"no download named "+name+": GET "+PublicPrefix+"/downloads lists them")
		return
	}
	path := filepath.Join(h.c.DownloadsDir, name)
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() {
		lib.SendError(ctx, h.c.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "download "+name+" is listed but missing on this server")
		return
	}
	ctx.Response.Header.Set("Content-Type", downloadContentType(name))
	if name != DownloadsChecksumsFile && name != DownloadsManifestFile {
		ctx.Response.Header.Set("Content-Disposition", `attachment; filename="`+name+`"`)
	}
	if file.SHA256 != "" {
		ctx.Response.Header.Set("ETag", `"`+file.SHA256+`"`)
		ctx.Response.Header.Set("X-Checksum-Sha256", file.SHA256)
	}
	ctx.Response.Header.Set("Cache-Control", "no-cache")
	if file.SHA256 != "" && string(ctx.Request.Header.Peek("If-None-Match")) == `"`+file.SHA256+`"` {
		ctx.SetStatusCode(fasthttp.StatusNotModified)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		lib.SendError(ctx, h.c.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "open "+name)
		return
	}
	ctx.SetBodyStream(f, int(fi.Size())) // fasthttp closes f once sent
}

func downloadContentType(name string) string {
	switch {
	case strings.HasSuffix(name, ".whl"):
		return "application/zip"
	case strings.HasSuffix(name, ".tar.gz"), strings.HasSuffix(name, ".tgz"):
		return "application/gzip"
	case name == DownloadsChecksumsFile:
		return "text/plain; charset=utf-8"
	case strings.HasSuffix(name, ".json"):
		return "application/json"
	}
	return "application/octet-stream"
}

// readManifest reads manifest.json, again only when it changed on disk.
func (h *PublicSiteHandler) readManifest() DownloadsManifest {
	empty := DownloadsManifest{Files: []DownloadFile{}}
	if h.c.DownloadsDir == "" {
		return empty
	}
	path := filepath.Join(h.c.DownloadsDir, DownloadsManifestFile)
	fi, err := os.Stat(path)
	if err != nil {
		return empty
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.manifest != nil && fi.ModTime().Equal(h.stamp) {
		return copyManifest(*h.manifest)
	}
	m, err := ReadDownloadsManifest(path)
	if err != nil {
		return empty
	}
	h.manifest, h.stamp = &m, fi.ModTime()
	return copyManifest(m)
}

func copyManifest(m DownloadsManifest) DownloadsManifest {
	m.Files = append([]DownloadFile{}, m.Files...)
	return m
}

// ReadDownloadsManifest reads and checks a manifest.json.
func ReadDownloadsManifest(path string) (DownloadsManifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return DownloadsManifest{}, err
	}
	var m DownloadsManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return DownloadsManifest{}, fmt.Errorf("manifest %s: %w", path, err)
	}
	kept := m.Files[:0]
	for _, f := range m.Files {
		if downloadNameRe.MatchString(f.Name) && !strings.Contains(f.Name, "..") {
			kept = append(kept, f)
		}
	}
	m.Files = kept
	if m.Files == nil {
		m.Files = []DownloadFile{}
	}
	sort.SliceStable(m.Files, func(i, j int) bool { return m.Files[i].Name < m.Files[j].Name })
	m.Available = len(m.Files) > 0
	return m, nil
}

// GitLabTemplateFor fills the template's HEIMDALL_URL default with
// publicURL. Project CI/CD variables still win over it in GitLab.
func GitLabTemplateFor(tmpl []byte, publicURL string) []byte {
	if publicURL == "" || !installServerRe.MatchString(publicURL) {
		return tmpl
	}
	return []byte(strings.Replace(string(tmpl), `HEIMDALL_URL: ""`, `HEIMDALL_URL: "`+publicURL+`"`, 1))
}

func (h *PublicSiteHandler) securityTxt(ctx *fasthttp.RequestCtx) {
	contact := strings.TrimSpace(h.c.SecurityContact)
	if contact == "" && h.c.PublicURL != "" {
		contact = h.c.PublicURL + "/security"
	}
	if contact == "" {
		lib.SendError(ctx, h.c.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "no security contact configured")
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Contact: %s\n", contact)
	fmt.Fprintf(&b, "Expires: %s\n", h.c.Now().UTC().AddDate(0, 6, 0).Truncate(24*time.Hour).Format(time.RFC3339))
	b.WriteString("Preferred-Languages: en\n")
	if h.c.PublicURL != "" {
		fmt.Fprintf(&b, "Policy: %s/security\n", h.c.PublicURL)
		fmt.Fprintf(&b, "Canonical: %s%s\n", h.c.PublicURL, SecurityTxtPath)
	}
	ctx.Response.Header.Set("Cache-Control", "public, max-age=86400")
	ctx.SetContentType("text/plain; charset=utf-8")
	ctx.SetBodyString(b.String())
}

// publicCache lets browsers and the edge keep a public answer briefly.
func publicCache(ctx *fasthttp.RequestCtx, seconds int) {
	ctx.Response.Header.Set("Cache-Control", fmt.Sprintf("public, max-age=%d", seconds))
}
