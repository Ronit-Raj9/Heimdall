// Package maxim connects an org to Maxim (https://www.getmaxim.ai), the
// hackathon sponsor's observability product: a per-org connection (API key
// in the keystore, workspace and log repository ids in the project), sealed
// run traces pushed to the log repository through the OTLP Maxim preset
// (export/presets.go), the push record behind "Open in Maxim", and a client
// for the repository read the connection test uses.
//
// Maxim's public API as read on 2026-11-22
// (https://www.getmaxim.ai/docs/public-apis/openapi/log-repositories.json):
//
//   - base https://api.getmaxim.ai, header x-maxim-api-key;
//   - GET /v1/log-repositories?workspaceId=&id= lists repositories
//     ({data: [{id, name, workspaceId, ...}]});
//   - OTLP ingest at https://api.getmaxim.ai/v1/otel with x-maxim-api-key
//     and x-maxim-repo-id
//     (https://www.getmaxim.ai/docs/tracing/opentelemetry/ingesting-via-otlp).
//
// The web app's URL for one trace is not documented; the default link
// template is a best guess and every connection may replace it. No real
// Maxim has been called: the tests use local fakes of these endpoints.
package maxim

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Defaults.
const (
	DefaultAPIBase      = "https://api.getmaxim.ai"
	DefaultLinkTemplate = "https://app.getmaxim.ai/workspace/{workspace_id}/log-repository/{repo_id}/logs?traceId={trace_id}"
	// KeyLabel names the connection's API key in the keystore. It is not an
	// environment variable name, so no tool can ask for it through auth_env.
	KeyLabel = "heimdall.maxim.api_key"
	// KeyCreatedBy marks the key as the server's own, so the org secrets page
	// neither lists nor deletes it (handlers/secrets.go systemSecret).
	KeyCreatedBy = "heimdall:maxim"
)

// Push statuses.
const (
	StatusPushed    = "pushed"
	StatusFailed    = "failed"
	StatusNotPushed = "not_pushed"
)

// Files under the project's .heimdall folder, which the project files API
// never serves.
const (
	dirName        = ".heimdall/maxim"
	connectionFile = "connection.json"
	pushesFile     = "pushes.jsonl"
	maxPushLines   = 20000
	maxErrorChars  = 300
)

var idRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

// ErrInvalid marks connection settings that cannot be saved.
var ErrInvalid = errors.New("invalid maxim connection")

// Settings is an org's Maxim connection, without the API key.
type Settings struct {
	WorkspaceID  string    `json:"workspace_id"`
	RepoID       string    `json:"repo_id"`
	AutoPush     bool      `json:"auto_push"`
	LinkTemplate string    `json:"link_template,omitempty"`
	KeyID        string    `json:"key_id"` // the keystore id of the API key
	UpdatedAt    time.Time `json:"updated_at"`
	UpdatedBy    string    `json:"updated_by,omitempty"`
}

// Validate checks the ids and the link template.
func (s Settings) Validate() error {
	if !idRe.MatchString(s.WorkspaceID) {
		return fmt.Errorf("%w: workspace id must be 1 to 128 letters, digits, '_', '.', ':' or '-'", ErrInvalid)
	}
	if !idRe.MatchString(s.RepoID) {
		return fmt.Errorf("%w: log repository id must be 1 to 128 letters, digits, '_', '.', ':' or '-'", ErrInvalid)
	}
	if s.LinkTemplate != "" {
		u, err := url.Parse(strings.NewReplacer("{workspace_id}", "w", "{repo_id}", "r", "{trace_id}", "t").Replace(s.LinkTemplate))
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return fmt.Errorf("%w: link template must be an https URL", ErrInvalid)
		}
		if !strings.Contains(s.LinkTemplate, "{trace_id}") {
			return fmt.Errorf("%w: link template must contain {trace_id}", ErrInvalid)
		}
	}
	return nil
}

// Link renders the trace link of a run's trace id. Every value is
// URL-escaped.
func (s Settings) Link(traceID string) string {
	tmpl := s.LinkTemplate
	if tmpl == "" {
		tmpl = DefaultLinkTemplate
	}
	return strings.NewReplacer("{workspace_id}", url.PathEscape(s.WorkspaceID), "{repo_id}", url.PathEscape(s.RepoID),
		"{trace_id}", url.QueryEscape(traceID)).Replace(tmpl)
}

// Push is one attempt to send a run's trace.
type Push struct {
	RunID   string    `json:"run_id"`
	ExpID   string    `json:"exp_id,omitempty"`
	TraceID string    `json:"trace_id"`
	Status  string    `json:"status"` // pushed | failed
	Error   string    `json:"error,omitempty"`
	At      time.Time `json:"at"`
	Auto    bool      `json:"auto,omitempty"` // sent by the seal hook, not a person
}

// Store keeps one org's connection settings and push record in its project.
type Store struct {
	dir string
	mu  sync.Mutex
}

// Open returns the store of a project root.
func Open(projectRoot string) *Store {
	return &Store{dir: filepath.Join(projectRoot, filepath.FromSlash(dirName))}
}

// Load returns the settings, or nil when the org is not connected.
func (s *Store) Load() (*Settings, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, connectionFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read maxim connection: %w", err)
	}
	var out Settings
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("read maxim connection: %w", err)
	}
	return &out, nil
}

// Save writes the settings atomically.
func (s *Store) Save(st Settings) error {
	if err := st.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return s.writeAtomic(connectionFile, data)
}

// Delete removes the settings; the push record stays for history.
func (s *Store) Delete() error {
	err := os.Remove(filepath.Join(s.dir, connectionFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Record appends a push. Errors are cut to a short line.
func (s *Store) Record(p Push) error {
	p.Error = oneLine(p.Error, maxErrorChars)
	line, err := json.Marshal(p)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("record maxim push: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(s.dir, pushesFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("record maxim push: %w", err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return errors.Join(fmt.Errorf("record maxim push: %w", err), f.Close())
	}
	return f.Close()
}

// Pushes returns the newest push of every run, and how many runs are
// pushed and failed now.
func (s *Store) Pushes() (map[string]Push, int, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]Push{}
	f, err := os.Open(filepath.Join(s.dir, pushesFile))
	if errors.Is(err, fs.ErrNotExist) {
		return out, 0, 0, nil
	}
	if err != nil {
		return nil, 0, 0, fmt.Errorf("read maxim pushes: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	for n := 0; sc.Scan() && n < maxPushLines*4; n++ {
		var p Push
		if json.Unmarshal(sc.Bytes(), &p) == nil && p.RunID != "" {
			out[p.RunID] = p
		}
	}
	if err := sc.Err(); err != nil {
		return nil, 0, 0, fmt.Errorf("read maxim pushes: %w", err)
	}
	pushed, failed := 0, 0
	for _, p := range out {
		if p.Status == StatusPushed {
			pushed++
		} else {
			failed++
		}
	}
	return out, pushed, failed, nil
}

// LastError is the newest failed push's error, or "".
func LastError(pushes map[string]Push) string {
	var last Push
	for _, p := range pushes {
		if p.Status == StatusFailed && p.At.After(last.At) {
			last = p
		}
	}
	return last.Error
}

func (s *Store) writeAtomic(name string, data []byte) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("write maxim %s: %w", name, err)
	}
	tmp, err := os.CreateTemp(s.dir, name+".*.tmp")
	if err != nil {
		return fmt.Errorf("write maxim %s: %w", name, err)
	}
	if _, err := tmp.Write(data); err != nil {
		return errors.Join(fmt.Errorf("write maxim %s: %w", name, err), tmp.Close(), os.Remove(tmp.Name()))
	}
	if err := tmp.Close(); err != nil {
		return errors.Join(fmt.Errorf("write maxim %s: %w", name, err), os.Remove(tmp.Name()))
	}
	if err := os.Rename(tmp.Name(), filepath.Join(s.dir, name)); err != nil {
		return errors.Join(fmt.Errorf("write maxim %s: %w", name, err), os.Remove(tmp.Name()))
	}
	return nil
}

// oneLine flattens and cuts untrusted text for a stored error.
func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "..."
}

// trimBody is the start of an answer body, for errors.
func trimBody(b []byte) string { return oneLine(string(bytes.TrimSpace(b)), 200) }
