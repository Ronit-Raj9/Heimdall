package issuesync

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// PrivateSourcesFile records, under the org project root, the source
// repository of every benchmark built from a GitHub repository and whether
// it is private. A private entry is never removed (nor turned public): the
// tasks already made stay, and so does their code. Share links read it
// (handlers/shareprivate_w448.go).
const PrivateSourcesFile = "issuesync/private-sources.json"

// PrivateSource is the source of one benchmark built from a repository.
type PrivateSource struct {
	Benchmark string    `json:"benchmark"`
	Repo      string    `json:"repo"`
	At        time.Time `json:"at"`
	// Public is true only when the repository was proven readable without
	// a token when the benchmark got tasks from it; false is private.
	Public bool `json:"public,omitempty"`
}

var privateMu sync.Mutex

// RecordSource is the one writer of PrivateSourcesFile: every path that
// puts tasks from a repository into a benchmark calls it before writing
// them. public must be true only when the repository is known to be public
// (fetched without a token); anything else, a repository reached with the
// organization's GitHub token or a check that failed, records it private.
// Private wins: a public record turns private, a private one never turns
// public. It is idempotent.
func RecordSource(projectRoot, benchmark, repo string, public bool, now time.Time) error {
	if benchmark == "" {
		return errors.New("record the benchmark source: no benchmark")
	}
	path := filepath.Join(projectRoot, filepath.FromSlash(PrivateSourcesFile))
	privateMu.Lock()
	defer privateMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open private sources lock: %w", err)
	}
	release, err := flock(f)
	if err != nil {
		_ = f.Close()
		return err
	}
	defer func() { _ = release() }()
	list, err := readPrivate(path)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(list, func(p PrivateSource) bool { return p.Benchmark == benchmark })
	switch {
	case i >= 0 && (public || !list[i].Public):
		return nil // already recorded, or a public record of a known private one
	case i >= 0:
		list[i].Public, list[i].Repo, list[i].At = false, repo, now.UTC()
	default:
		list = append(list, PrivateSource{Benchmark: benchmark, Repo: repo, At: now.UTC(), Public: public})
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(b)
	if werr == nil {
		werr = tmp.Sync()
	}
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), path)
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
	}
	return werr
}

// MarkPrivateSource records benchmark as built from the private repository
// repo (RecordSource with public false).
func MarkPrivateSource(projectRoot, benchmark, repo string, now time.Time) error {
	return RecordSource(projectRoot, benchmark, repo, false, now)
}

// PrivateSources lists the benchmarks built from private repositories; none
// when the file does not exist, an error when it cannot be read.
func PrivateSources(projectRoot string) ([]PrivateSource, error) {
	list, err := readPrivate(filepath.Join(projectRoot, filepath.FromSlash(PrivateSourcesFile)))
	if err != nil {
		return nil, err
	}
	out := list[:0:0]
	for _, p := range list {
		if !p.Public {
			out = append(out, p)
		}
	}
	return out, nil
}

// KnownPublic says whether repo was recorded public for benchmark (and the
// file reads). Anything else is not known public.
func KnownPublic(projectRoot, benchmark string) bool {
	list, err := readPrivate(filepath.Join(projectRoot, filepath.FromSlash(PrivateSourcesFile)))
	if err != nil {
		return false
	}
	i := slices.IndexFunc(list, func(p PrivateSource) bool { return p.Benchmark == benchmark })
	return i >= 0 && list[i].Public
}

func readPrivate(path string) ([]PrivateSource, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var list []PrivateSource
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("parse private sources: %w", err)
	}
	return list, nil
}
