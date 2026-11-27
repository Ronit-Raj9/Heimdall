// Package sandbox runs evaluation runs in an organization's own cloud
// sandbox account (bring your own worker, docs/runners.md "Cloud sandbox
// platforms"): Modal, Daytona and E2B today, each behind the Platform
// interface so more platforms plug in without touching the driver.
//
// A Platform is the narrow set of calls the driver needs from any sandbox
// service: create a sandbox from an OCI image with egress rules and a time
// limit, run a short command in it as root, move files in and out, and
// delete it. Everything run specific (seeding the workspace, the agent's
// launch, the wall budget, the diff export, the run record) lives once, in
// Driver, so every platform seeds, runs, exports and cleans up a run the
// same way.
//
// Platform tokens belong to the organization. They are stored in the
// keystore as an org secret (Connections), revealed only to build a client,
// and never logged: every error a client returns has the token removed.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Platform names.
const (
	PlatformModal   = "modal"
	PlatformDaytona = "daytona"
	PlatformE2B     = "e2b"
)

// Spec is one sandbox to create.
type Spec struct {
	// Name is a stable, human readable name (heimdall-<run id>).
	Name string
	// Image is the OCI image the sandbox runs, pullable by the platform.
	Image string
	// Env is set for every process of the sandbox. The driver keeps secrets
	// out of it (they go to each command instead).
	Env map[string]string
	// Labels are attached where the platform supports them (run, exp,
	// install), so a leftover sandbox can be traced to its run.
	Labels map[string]string
	CPUs   float64
	MemGB  float64
	DiskGB int
	// Lifetime is how long the platform keeps the sandbox at most, whatever
	// happens to this process: the run's wall budget plus setup and export
	// time. Destroy removes it earlier.
	Lifetime time.Duration
	Egress   Egress
	// GPU is the platform's GPU request ("L4", "H100:2"); "" for none.
	// Only GPUCapable platforms get one (gpu.go).
	GPU string
}

// Egress is a sandbox's outbound network rule. With BlockAll the sandbox
// reaches nothing; otherwise it reaches only AllowHosts (DNS names) and
// AllowCIDRs. A Spec always carries an explicit rule: the driver never
// creates a sandbox with open internet.
type Egress struct {
	BlockAll   bool
	AllowHosts []string
	AllowCIDRs []string
}

// Box is a created sandbox.
type Box struct {
	ID       string
	Platform string
	// Data holds what later calls need (an access token, a toolbox URL).
	// It may hold secrets and is never written to disk or logs.
	Data map[string]string
}

// Command is one short command run in a sandbox as root.
type Command struct {
	Argv    []string
	Env     map[string]string
	Cwd     string
	Timeout time.Duration
}

// Result is how a command ended. Output is its combined stdout and stderr,
// capped by the platform client.
type Result struct {
	ExitCode int
	Output   []byte
}

// Platform is one cloud sandbox service, used with one organization's token.
type Platform interface {
	// Name is the platform name (PlatformModal, ...).
	Name() string
	// Check proves the token works: a cheap authenticated read. It returns
	// ErrUnauthorized for a token the platform refuses.
	Check(ctx context.Context) error
	// Create starts a sandbox from s and waits until commands can run in it.
	Create(ctx context.Context, s Spec) (*Box, error)
	// Exec runs a command and waits for it under c.Timeout. A non-zero exit
	// is a Result, not an error.
	Exec(ctx context.Context, b *Box, c Command) (*Result, error)
	// Upload writes data to an absolute path, creating parent folders.
	Upload(ctx context.Context, b *Box, path string, data []byte) error
	// Download reads at most max bytes of an absolute path; ErrTooLarge when
	// the file is bigger.
	Download(ctx context.Context, b *Box, path string, max int64) ([]byte, error)
	// Destroy deletes the sandbox. A sandbox that is already gone is not an
	// error, so it is safe to call more than once.
	Destroy(ctx context.Context, b *Box) error
}

// Credentials are what a platform client is built from.
type Credentials struct {
	// Token is the org's API token (Modal: a proxy auth token id and
	// secret, "wk-...:ws-..."). Never logged.
	Token string
	// Endpoint overrides the platform's API URL (Daytona self-hosted, E2B
	// domain) and is required for Modal: the URL of the org's deployed
	// bridge app.
	Endpoint string
}

// Factory builds a platform client.
type Factory func(Credentials) (Platform, error)

// Info describes a platform for the settings screen.
type Info struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	// TokenHint says where the token comes from.
	TokenHint string `json:"token_hint"`
	// EndpointLabel names the endpoint field; EndpointRequired when the
	// platform cannot work without one.
	EndpointLabel string `json:"endpoint_label,omitempty"`
	// EndpointPlaceholder is an example value for the endpoint field (fix
	// wave X-60); empty when the platform has no endpoint field.
	EndpointPlaceholder string `json:"endpoint_placeholder,omitempty"`
	EndpointRequired    bool   `json:"endpoint_required,omitempty"`
	DocsURL             string `json:"docs_url,omitempty"`
}

// Errors.
var (
	ErrUnauthorized    = errors.New("the platform refused the token")
	ErrUnknownPlatform = errors.New("unknown sandbox platform")
	ErrTooLarge        = errors.New("file is larger than allowed")
	ErrNotFound        = errors.New("sandbox not found")
)

type registered struct {
	info    Info
	factory Factory
}

var (
	regMu    sync.RWMutex
	registry = map[string]registered{}
)

// Register adds a platform. Each platform file registers itself in init;
// another package may register more (X19b). A second registration of the
// same name panics: it is a programming error.
func Register(info Info, f Factory) {
	regMu.Lock()
	defer regMu.Unlock()
	if info.Name == "" || f == nil {
		panic("sandbox: Register needs a name and a factory")
	}
	if _, dup := registry[info.Name]; dup {
		panic("sandbox: platform " + info.Name + " registered twice")
	}
	registry[info.Name] = registered{info: info, factory: f}
}

// New builds a client for the named platform.
func New(name string, c Credentials) (Platform, error) {
	regMu.RLock()
	r, ok := registry[name]
	regMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w %q", ErrUnknownPlatform, name)
	}
	if strings.TrimSpace(c.Token) == "" {
		return nil, fmt.Errorf("%s: no API token", r.info.Title)
	}
	if r.info.EndpointRequired && strings.TrimSpace(c.Endpoint) == "" {
		return nil, fmt.Errorf("%s: %s is required", r.info.Title, strings.ToLower(r.info.EndpointLabel))
	}
	if err := checkEndpoint(r.info.Title, c.Endpoint); err != nil { // G3 B6
		return nil, err
	}
	return r.factory(c)
}

// Platforms lists the registered platforms by name.
func Platforms() []Info {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]Info, 0, len(registry))
	for _, r := range registry {
		out = append(out, r.info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Lookup returns a platform's description.
func Lookup(name string) (Info, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	r, ok := registry[name]
	return r.info, ok
}

// IsPlatform reports whether name is a registered platform.
func IsPlatform(name string) bool {
	_, ok := Lookup(name)
	return ok
}

// shellQuote quotes one argument for a POSIX shell.
func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./=:@,+", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellJoin quotes argv into one shell command line.
func shellJoin(argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		q[i] = shellQuote(a)
	}
	return strings.Join(q, " ")
}
