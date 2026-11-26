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

// PrivateSourcesFile lists the benchmarks built from private repositories,
// under the org project root. An entry is never removed when its weekly sync
// is: the tasks already made stay, and so does their code.
const PrivateSourcesFile = "issuesync/private-sources.json"

// PrivateSource is one benchmark built from a private repository.
type PrivateSource struct {
	Benchmark string    `json:"benchmark"`
	Repo      string    `json:"repo"`
	At        time.Time `json:"at"`
}

var privateMu sync.Mutex

// MarkPrivateSource records that benchmark holds tasks from the private
// repository repo. It is idempotent.
func MarkPrivateSource(projectRoot, benchmark, repo string, now time.Time) error {
	if benchmark == "" {
		return errors.New("mark private source: no benchmark")
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
	if slices.ContainsFunc(list, func(p PrivateSource) bool { return p.Benchmark == benchmark }) {
		return nil
	}
	list = append(list, PrivateSource{Benchmark: benchmark, Repo: repo, At: now.UTC()})
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

// PrivateSources lists the benchmarks built from private repositories; none
// when the file does not exist.
func PrivateSources(projectRoot string) ([]PrivateSource, error) {
	return readPrivate(filepath.Join(projectRoot, filepath.FromSlash(PrivateSourcesFile)))
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
