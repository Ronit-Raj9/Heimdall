package benchcatalog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/schemas"
)

// ImportParams are the params of the catalog.import job.
type ImportParams struct {
	Entry string            `json:"entry"`           // catalog entry id
	From  string            `json:"from"`            // path of the local upstream data
	URL   string            `json:"url,omitempty"`   // public https URL the job fetches into uploads/datasets/<entry> (instead of From)
	Suite string            `json:"suite,omitempty"` // suite id; default the entry's
	Limit int               `json:"limit,omitempty"` // at most this many tasks (default 5, 0 kept as 5)
	IDs   []string          `json:"ids,omitempty"`
	Set   map[string]string `json:"set,omitempty"` // importer settings
	Force bool              `json:"force,omitempty"`
}

// DefaultImportLimit is the task count an import from the web takes by default.
const DefaultImportLimit = 5

var suiteIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// DatasetsRoot is HEIMDALL_DATASETS_DIR: a server folder of upstream data
// that hosted imports may read besides the org's own project folder.
func DatasetsRoot() string { return strings.TrimSpace(os.Getenv("HEIMDALL_DATASETS_DIR")) }

// ResolveFrom confines the data path. A relative path resolves against the
// project. Hosted mode accepts only paths under the project or under
// HEIMDALL_DATASETS_DIR; local mode accepts any existing path.
func ResolveFrom(project, from string, hosted bool) (string, error) {
	from = strings.TrimSpace(from)
	if from == "" {
		return "", errors.New("from is required: the path of a local copy of the upstream data")
	}
	p := from
	if !filepath.IsAbs(p) {
		p = filepath.Join(project, p)
	}
	p = filepath.Clean(p)
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", fmt.Errorf("data path %q not found on the server; use Upload dataset or Fetch from URL on the catalog import form", from)
	}
	if hosted {
		ok := within(real, project)
		if d := DatasetsRoot(); d != "" && within(real, d) {
			ok = true
		}
		if !ok {
			return "", fmt.Errorf("data path %q is outside your project; upload the data (Upload page) or ask the server owner to place it under HEIMDALL_DATASETS_DIR", from)
		}
	}
	return real, nil
}

func within(p, dir string) bool {
	d, err := filepath.EvalSymlinks(dir)
	if err != nil {
		d = filepath.Clean(dir)
	}
	rel, err := filepath.Rel(d, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// Normalize checks params against the catalog and fills defaults. It returns
// the importer name and resolved data path.
func (s Source) Normalize(project string, p *ImportParams, hosted bool) (string, string, error) {
	e, ok := s.Find(project, p.Entry)
	if !ok {
		return "", "", fmt.Errorf("unknown catalog entry %q", p.Entry)
	}
	if e.Kind != KindImporter || e.Importer == "" {
		return "", "", fmt.Errorf("entry %q is added by a copy, not an import", p.Entry)
	}
	if _, ok := importers.Get(e.Importer); !ok {
		return "", "", fmt.Errorf("importer %q is not in this server build", e.Importer)
	}
	if p.Suite == "" {
		p.Suite = e.SuiteID
	}
	if !suiteIDRe.MatchString(p.Suite) {
		return "", "", fmt.Errorf("suite %q: use lower-case letters, digits, '.', '-' or '_'", p.Suite)
	}
	if p.Limit <= 0 {
		p.Limit = DefaultImportLimit
	}
	if p.Limit > 500 {
		return "", "", errors.New("limit is at most 500 tasks per import")
	}
	set := map[string]string{}
	for k, v := range e.Extra {
		set[k] = v
	}
	for k, v := range p.Set {
		set[k] = v
	}
	p.Set = set
	if strings.TrimSpace(p.URL) != "" {
		if strings.TrimSpace(p.From) != "" {
			return "", "", errors.New("give either from (a path on the server) or url (a public URL to fetch), not both")
		}
		if !DataFor(e.ID, e.Importer).Fetch {
			return "", "", fmt.Errorf("entry %q cannot be fetched from a URL: upload %s instead", p.Entry, DataFor(e.ID, e.Importer).Upload)
		}
		if _, err := NewFetcher().CheckURL(p.URL); err != nil {
			return "", "", err
		}
		return e.Importer, "", nil // the job fetches, then resolves the path
	}
	from, err := ResolveFrom(project, p.From, hosted)
	if err != nil {
		return "", "", err
	}
	return e.Importer, from, nil
}

// Import runs the importer in process.
func Import(ctx context.Context, importer, project, benchmarksDir, from string, p ImportParams, logger schemas.Logger) (*importers.Result, error) {
	im, ok := importers.Get(importer)
	if !ok {
		return nil, fmt.Errorf("importer %q is not in this server build", importer)
	}
	return im.Import(ctx, importers.Options{BenchRoot: project, BenchmarksDir: benchmarksDir, From: from,
		SuiteID: p.Suite, IDs: p.IDs, Limit: p.Limit, Force: p.Force, Extra: p.Set, Logger: logger})
}
