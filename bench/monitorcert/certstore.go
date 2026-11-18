package monitorcert

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Certificate store layout.
const (
	// RootEnv overrides the certificates root, which defaults to
	// <bench root>/monitor-certs.
	RootEnv = "BENCH_MONITOR_CERTS_ROOT"

	certFile   = "certificate.json"
	reportFile = "certificate.md"
	certPrefix = "mc"
)

var certIDRE = regexp.MustCompile(`^mc_[a-z0-9]{1,64}$`)

// DefaultRoot is $BENCH_MONITOR_CERTS_ROOT, else <bench root>/monitor-certs.
func DefaultRoot(benchRoot string) string {
	if v := strings.TrimSpace(os.Getenv(RootEnv)); v != "" {
		return v
	}
	return filepath.Join(benchRoot, "monitor-certs")
}

// CertStore keeps certificates as child artifacts outside every seal:
// <root>/<cert_id>/certificate.json and certificate.md. A certificate
// points at sealed evidence by experiment manifest hashes and run seal
// hashes; nothing in a run folder is written.
type CertStore struct {
	root string
}

// NewCertStore opens the certificates root. It refuses a root inside the
// runs root, so a certificate can never land in an experiment folder.
func NewCertStore(root, runsRoot string) (*CertStore, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("monitor certificates: empty root")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("monitor certificates: resolve %s: %w", root, err)
	}
	if runsRoot != "" {
		runs, err := filepath.Abs(runsRoot)
		if err != nil {
			return nil, fmt.Errorf("monitor certificates: resolve runs root: %w", err)
		}
		if rel, err := filepath.Rel(runs, abs); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("monitor certificates: root %s is inside the runs root %s", abs, runs)
		}
	}
	return &CertStore{root: abs}, nil
}

// Root returns the absolute certificates root.
func (s *CertStore) Root() string { return s.root }

// Write stores a certificate and its Markdown report in a new folder. A
// certificate id that exists already is schemas.ErrExists.
func (s *CertStore) Write(c *schemas.MonitorCertificate, report []byte) (string, error) {
	if c == nil || !certIDRE.MatchString(c.CertID) {
		return "", errors.New("write monitor certificate: missing or invalid cert_id")
	}
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return "", fmt.Errorf("write monitor certificate: %w", err)
	}
	dir := filepath.Join(s.root, c.CertID)
	if err := os.Mkdir(dir, 0o755); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("write monitor certificate %s: %w", c.CertID, schemas.ErrExists)
		}
		return "", fmt.Errorf("write monitor certificate %s: %w", c.CertID, err)
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode monitor certificate %s: %w", c.CertID, err)
	}
	if err := writeAtomic(filepath.Join(dir, certFile), append(b, '\n')); err != nil {
		return "", fmt.Errorf("write monitor certificate %s: %w", c.CertID, err)
	}
	if err := writeAtomic(filepath.Join(dir, reportFile), report); err != nil {
		return "", fmt.Errorf("write monitor certificate %s report: %w", c.CertID, err)
	}
	return dir, nil
}

// List returns a summary of every certificate, newest first. A missing root
// is an empty list; a folder whose certificate does not parse is skipped.
func (s *CertStore) List(ctx context.Context) (*schemas.MonitorCertList, error) {
	out := &schemas.MonitorCertList{Certifications: []schemas.MonitorCertSummary{}}
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list monitor certificates: %w", err)
	}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !e.IsDir() || !certIDRE.MatchString(e.Name()) {
			continue
		}
		c, err := s.Get(ctx, e.Name())
		if err != nil {
			continue
		}
		out.Certifications = append(out.Certifications, Summary(c))
	}
	sort.SliceStable(out.Certifications, func(i, j int) bool {
		a, b := out.Certifications[i], out.Certifications[j]
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.After(b.CreatedAt)
		}
		return a.CertID > b.CertID
	})
	return out, nil
}

// Get reads one certificate. An unknown or malformed id is
// schemas.ErrNotFound.
func (s *CertStore) Get(ctx context.Context, certID string) (*schemas.MonitorCertificate, error) {
	b, err := s.read(ctx, certID, certFile)
	if err != nil {
		return nil, err
	}
	var c schemas.MonitorCertificate
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("monitor certificate %s: %w", certID, err)
	}
	return &c, nil
}

// Report returns a certificate's Markdown report.
func (s *CertStore) Report(ctx context.Context, certID string) ([]byte, error) {
	return s.read(ctx, certID, reportFile)
}

func (s *CertStore) read(ctx context.Context, certID, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !certIDRE.MatchString(certID) {
		return nil, fmt.Errorf("monitor certificate %q: %w", certID, schemas.ErrNotFound)
	}
	b, err := os.ReadFile(filepath.Join(s.root, certID, name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("monitor certificate %s: %w", certID, schemas.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("monitor certificate %s: %w", certID, err)
	}
	return b, nil
}

// Summary is the list entry of a certificate.
func Summary(c *schemas.MonitorCertificate) schemas.MonitorCertSummary {
	warnings := c.Warnings
	if warnings == nil {
		warnings = []string{}
	}
	exps := c.ExpIDs
	if exps == nil {
		exps = []string{}
	}
	return schemas.MonitorCertSummary{
		CertID: c.CertID, CreatedAt: c.CreatedAt, MonitorName: c.Monitor.Name, MonitorKind: c.Monitor.Kind,
		Mode: c.Monitor.Mode, ExpIDs: exps, Honest: c.Labels.Honest, Attacks: c.Labels.Attack,
		Headline: c.Headline, Warnings: warnings,
	}
}

// writeAtomic writes data to a temp file beside target and renames it.
func writeAtomic(target string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(target), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		return errors.Join(err, f.Close(), os.Remove(tmp))
	}
	if err := f.Sync(); err != nil {
		return errors.Join(err, f.Close(), os.Remove(tmp))
	}
	if err := f.Close(); err != nil {
		return errors.Join(err, os.Remove(tmp))
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		return errors.Join(err, os.Remove(tmp))
	}
	if err := os.Rename(tmp, target); err != nil {
		return errors.Join(err, os.Remove(tmp))
	}
	return nil
}
