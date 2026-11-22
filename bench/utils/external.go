package utils

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// External APIs (schemas.ExternalAPI, docs/external-apis.md) are real HTTP
// APIs a task's agent may call. The agent reaches each one only through the
// per-run tool proxy at ExternalURL(id); the proxy is the only container that
// may reach the real host, and only in live or record mode. These helpers are
// shared by the planner, the environment kernel, admission and the adapters.
const (
	// EnvExternalAPIs is the agent environment variable holding
	// {"<id>": {"url": "..."}} for every external API of the run.
	EnvExternalAPIs = "BENCH_EXTERNAL_APIS"
	// ExternalPathPrefix is where the tool proxy serves external APIs:
	// /x/<id>/<path>.
	ExternalPathPrefix = "/x/"
	// ExternalFolder is the run folder folder recorded cassettes land in:
	// external/<id>.cassette.jsonl.
	ExternalFolder = "external"
	// ExternalCassetteSuffix names a recorded cassette in the run folder.
	ExternalCassetteSuffix = ".cassette.jsonl"

	DefaultExternalMaxCalls   = 200
	DefaultExternalMaxBytes   = 4 << 20
	DefaultExternalTimeoutS   = 30
	DefaultExternalAuthHeader = "Authorization"

	externalProxyBase = "http://toolproxy:9200"
)

var (
	externalIDRe  = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,40}[a-z0-9])?$`)
	envNameRe     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	headerNameRe  = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")
	httpMethodRe  = regexp.MustCompile(`^[A-Z]+$`)
	reservedHeads = map[string]bool{"host": true, "connection": true, "keep-alive": true, "proxy-authorization": true,
		"proxy-connection": true, "te": true, "trailer": true, "transfer-encoding": true, "upgrade": true, "content-length": true}
)

// ExternalRef is one external API of a run with the folder its cassette is
// relative to.
type ExternalRef struct {
	API  schemas.ExternalAPI
	Base string // the task folder for a task API, the suite root for a suite API
}

// ExternalAPIs lists the run's external APIs: the suite's first, then the
// task's, in file order.
func ExternalAPIs(s *schemas.Suite, t *schemas.Task) []ExternalRef {
	var out []ExternalRef
	if s != nil {
		for _, a := range s.External {
			out = append(out, ExternalRef{API: a, Base: s.Root})
		}
	}
	if t != nil {
		for _, a := range t.External {
			out = append(out, ExternalRef{API: a, Base: t.Dir})
		}
	}
	return out
}

// CheckExternalAPIs validates a run's external APIs: ids host-name safe and
// unique, an http(s) base URL with a host and no user info, query or
// fragment, allowlist entries with a path prefix starting with / and
// upper-case methods, a known mode, valid header and variable names, and no
// negative cap. It is pure.
func CheckExternalAPIs(refs []ExternalRef) error {
	seen := map[string]bool{}
	var errs []error
	for _, r := range refs {
		a := r.API
		if !externalIDRe.MatchString(a.ID) {
			errs = append(errs, fmt.Errorf("external api id %q must be lower-case letters, digits and dashes (host-name safe)", a.ID))
			continue
		}
		if seen[a.ID] {
			errs = append(errs, fmt.Errorf("external api id %q is used twice", a.ID))
		}
		seen[a.ID] = true
		if _, err := ExternalTarget(a.BaseURL); err != nil {
			errs = append(errs, fmt.Errorf("external api %s: %w", a.ID, err))
		}
		for i, al := range a.Allow {
			if !strings.HasPrefix(al.PathPrefix, "/") || strings.Contains(al.PathPrefix, "..") {
				errs = append(errs, fmt.Errorf("external api %s allow %d: path_prefix %q must start with / and hold no ..", a.ID, i+1, al.PathPrefix))
			}
			for _, m := range al.Methods {
				if !httpMethodRe.MatchString(m) {
					errs = append(errs, fmt.Errorf("external api %s allow %d: method %q must be an upper-case HTTP method", a.ID, i+1, m))
				}
			}
		}
		for k := range a.Headers {
			if !headerNameRe.MatchString(k) || reservedHeads[strings.ToLower(k)] {
				errs = append(errs, fmt.Errorf("external api %s: header %q is invalid or reserved", a.ID, k))
			}
		}
		if a.AuthEnv != "" && !envNameRe.MatchString(a.AuthEnv) {
			errs = append(errs, fmt.Errorf("external api %s: auth_env %q is not a variable name", a.ID, a.AuthEnv))
		}
		if a.AuthHeader != "" && (!headerNameRe.MatchString(a.AuthHeader) || reservedHeads[strings.ToLower(a.AuthHeader)]) {
			errs = append(errs, fmt.Errorf("external api %s: auth_header %q is invalid or reserved", a.ID, a.AuthHeader))
		}
		switch a.Mode {
		case "", schemas.ExternalLive, schemas.ExternalRecord, schemas.ExternalReplay:
		default:
			errs = append(errs, fmt.Errorf("external api %s: mode %q is not live, record or replay", a.ID, a.Mode))
		}
		if a.MaxCalls < 0 || a.MaxBytes < 0 || a.TimeoutS < 0 {
			errs = append(errs, fmt.Errorf("external api %s: max_calls, max_bytes and timeout_s must not be negative", a.ID))
		}
	}
	return errors.Join(errs...)
}

// ExternalTarget returns the host:port an external API's base URL connects
// to, and refuses a URL that is not plain http(s)://host[:port][/path].
func ExternalTarget(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("base_url: %w", err)
	}
	if u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", fmt.Errorf("base_url %q must not carry a query or fragment", base)
	}
	return RemoteEgressTarget(base)
}

// ExternalCassette resolves an API's cassette inside its base folder and
// reports whether the file exists. An API without a cassette returns "".
func ExternalCassette(r ExternalRef) (string, bool, error) {
	if r.API.Cassette == "" {
		return "", false, nil
	}
	native := filepath.FromSlash(r.API.Cassette)
	if filepath.IsAbs(native) || !filepath.IsLocal(native) {
		return "", false, fmt.Errorf("external api %s cassette %s: want a path inside the folder", r.API.ID, r.API.Cassette)
	}
	p, err := ResolveInside(r.Base, r.API.Cassette)
	if err != nil {
		if _, serr := os.Lstat(filepath.Join(r.Base, native)); errors.Is(serr, os.ErrNotExist) {
			return filepath.Join(r.Base, native), false, nil
		}
		return "", false, fmt.Errorf("external api %s cassette: %w", r.API.ID, err)
	}
	fi, err := os.Stat(p)
	switch {
	case err != nil:
		return "", false, fmt.Errorf("external api %s cassette: %w", r.API.ID, err)
	case !fi.Mode().IsRegular():
		return "", false, fmt.Errorf("external api %s cassette %s is not a file", r.API.ID, r.API.Cassette)
	}
	return p, true, nil
}

// ExternalMode resolves the mode one external API runs in. The experiment's
// `external:` wins, then the API's own mode, then replay when the cassette
// exists. Replay needs the cassette. Live and record reach the real host and
// make the run unrepeatable, so they also need the experiment to opt in; an
// admission (experiment "") never runs them. It is pure.
func ExternalMode(api schemas.ExternalAPI, experiment string, haveCassette bool) (string, error) {
	mode := experiment
	if mode == "" {
		mode = api.Mode
	}
	switch mode {
	case "":
		if !haveCassette {
			return "", fmt.Errorf("external api %s has no cassette; record one with an experiment that sets external: record, or opt in to live calls with external: live", api.ID)
		}
		return schemas.ExternalReplay, nil
	case schemas.ExternalReplay:
		if !haveCassette {
			return "", fmt.Errorf("external api %s runs in replay mode but its cassette %q is missing", api.ID, api.Cassette)
		}
		return mode, nil
	case schemas.ExternalLive, schemas.ExternalRecord:
		if experiment == "" && !(api.MCP && mode == schemas.ExternalLive) { // an MCP server under test is always called live
			return "", fmt.Errorf("external api %s mode %s calls the real host and is not repeatable; the experiment must opt in with external: %s", api.ID, mode, mode)
		}
		return mode, nil
	}
	return "", fmt.Errorf("external api %s: mode %q is not live, record or replay", api.ID, mode)
}

// RunExternalMode is ExternalMode for one API of a run: the experiment comes
// from the run's manifest (none for an admission).
func RunExternalMode(rc *schemas.RunContext, r ExternalRef) (string, error) {
	_, have, err := ExternalCassette(r)
	if err != nil {
		return "", err
	}
	exp := ""
	if rc != nil && rc.Manifest != nil {
		exp = rc.Manifest.Experiment.External
	}
	return ExternalMode(r.API, exp, have)
}

// ExternalUnrepeatable reports whether an experiment's runs called real
// external hosts (external: live or record): their results cannot be
// repeated, and reports and the leaderboard label or exclude them.
func ExternalUnrepeatable(m *schemas.Manifest) bool {
	return m != nil && (m.Experiment.External == schemas.ExternalLive || m.Experiment.External == schemas.ExternalRecord ||
		m.Experiment.Egress == schemas.EgressLive) // recorded internet: live-allowlisted reaches real hosts too
}

// ExternalURL is where the agent reaches external API id.
func ExternalURL(id string) string {
	return externalProxyBase + ExternalPathPrefix + id
}

// ExternalAllowFor is the allowlist the proxy enforces for a: its own, or
// for an MCP server with none, POST and DELETE on the MCP endpoint (a
// session's messages and its end). GET, the optional server stream, is
// answered 405 by the proxy for MCP servers.
func ExternalAllowFor(a schemas.ExternalAPI) []schemas.ExternalAllow {
	if len(a.Allow) > 0 || !a.MCP {
		return a.Allow
	}
	return []schemas.ExternalAllow{{Methods: []string{"POST", "DELETE"}, PathPrefix: "/"}}
}

// ExternalAuthHeader is the header an API's credential is sent in.
func ExternalAuthHeader(a schemas.ExternalAPI) string {
	if a.AuthHeader != "" {
		return http.CanonicalHeaderKey(a.AuthHeader)
	}
	return DefaultExternalAuthHeader
}

// ExternalServers maps every external API handle of a run to its URL.
func ExternalServers(handles []schemas.ServiceHandle) map[string]string {
	out := map[string]string{}
	for _, h := range handles {
		if h.Spec.Role == schemas.ServiceRoleExternal && h.URL != "" {
			out[h.Spec.ID] = h.URL
		}
	}
	return out
}

// ExternalAPIsJSON is ExternalServers as the JSON object BENCH_EXTERNAL_APIS
// holds: {"<id>": {"url": "..."}}, keys sorted.
func ExternalAPIsJSON(handles []schemas.ServiceHandle) (string, error) {
	type api struct {
		URL string `json:"url"`
	}
	m := map[string]api{}
	for id, u := range ExternalServers(handles) {
		m[id] = api{URL: u}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("encode external apis: %w", err)
	}
	return string(b), nil
}

// AddExternalEnv sets BENCH_EXTERNAL_APIS in env when the run has external
// APIs, and leaves env untouched otherwise.
func AddExternalEnv(env map[string]string, handles []schemas.ServiceHandle) error {
	if len(ExternalServers(handles)) == 0 {
		return nil
	}
	js, err := ExternalAPIsJSON(handles)
	if err != nil {
		return err
	}
	env[EnvExternalAPIs] = js
	return nil
}
