package onboard

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/schemas"
)

// importerSource wraps the registered benchmark importers: a folder (an
// uploaded zip) or a data file in a format one of them reads becomes a
// draft suite written by that importer.
type importerSource struct{}

func init() { Register(importerSource{}) }

func (importerSource) Name() string { return "benchmark" }
func (importerSource) Kind() Kind   { return KindImporter }

// DefaultImportLimit is the tasks a benchmark-format draft takes by default.
const DefaultImportLimit = 25

// guessImporter names the importer a peek most likely is, or "".
func guessImporter(p Peek) (string, string, int) {
	if p.Dir != "" {
		if p.HasFile("task.toml") {
			return "harbor", "Harbor tasks (task.toml)", 100
		}
		for _, f := range p.Files {
			if strings.HasSuffix(f, ".jsonl") || strings.HasSuffix(f, ".json") {
				if b, err := readHead(filepath.Join(p.Dir, filepath.FromSlash(f)), 64<<10); err == nil && bytes.Contains(b, []byte("FAIL_TO_PASS")) {
					return "swebench", "SWE-bench style instances", 95
				}
			}
		}
		return "", "Benchmark folder", 30
	}
	if bytes.Contains(head(p.Data, 64<<10), []byte("FAIL_TO_PASS")) {
		return "swebench", "SWE-bench style instances", 95
	}
	if p.Ext == ".zip" && len(p.Data) > 0 {
		return "", "Zip of a benchmark", 30
	}
	return "", "", 0
}

func head(b []byte, n int) []byte {
	if len(b) > n {
		return b[:n]
	}
	return b
}

func readHead(p string, n int64) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, n))
}

func (importerSource) Detect(p Peek) (Match, bool) {
	name, label, score := guessImporter(p)
	if score == 0 {
		return Match{}, false
	}
	kind := KindImporter
	if name == "harbor" {
		kind = KindHarbor
	}
	if name != "" {
		if _, ok := importers.Get(name); !ok {
			return Match{Kind: kind, Label: label, Score: score, Note: "the " + name + " importer is not in this server build"}, true
		}
	}
	m := Match{Kind: kind, Label: label, Score: score, CanDraft: true}
	if name == "" {
		m.Note = "pick the benchmark format on the plan"
	}
	return m, true
}

// materialize is the local path the importer reads: the uploaded folder or
// file, or a fetched file (a zip is extracted) under dir.
func materialize(ctx context.Context, env *Env, in Input, dir string) (string, Peek, error) {
	p, err := env.Peek(ctx, in)
	if err != nil {
		return "", p, err
	}
	if p.FetchErr != "" {
		return "", p, errors.New(p.FetchErr)
	}
	if p.Dir != "" {
		return p.Dir, p, nil
	}
	if in.Upload != "" {
		abs, err := env.UploadPath(in.Upload)
		return abs, p, err
	}
	if len(p.Data) == 0 {
		return "", p, errors.New("nothing to read: the data is empty or too large")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", p, err
	}
	if p.Ext == ".zip" || bytes.HasPrefix(p.Data, []byte("PK\x03\x04")) {
		if err := unzipSafe(p.Data, dir); err != nil {
			return "", p, err
		}
		p.Dir, p.Files = dir, listFiles(dir, maxPeekFiles)
		return dir, p, nil
	}
	name := p.Name
	if name == "" || strings.ContainsAny(name, `/\`) {
		name = "data"
	}
	f := filepath.Join(dir, name)
	return f, p, os.WriteFile(f, p.Data, 0o644)
}

// Zip limits.
const (
	maxZipFiles = 20000
	maxZipBytes = 512 << 20
)

// unzipSafe extracts a zip into dir: regular files only, no path escapes,
// size and count caps.
func unzipSafe(data []byte, dir string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("not a zip file: %w", err)
	}
	if len(zr.File) > maxZipFiles {
		return fmt.Errorf("the zip holds %d files; at most %d", len(zr.File), maxZipFiles)
	}
	var total int64
	for _, f := range zr.File {
		name := path.Clean(strings.ReplaceAll(f.Name, `\`, "/"))
		if strings.HasPrefix(name, "/") || name == ".." || strings.HasPrefix(name, "../") || strings.HasPrefix(name, "__MACOSX") {
			continue
		}
		if f.FileInfo().IsDir() {
			continue
		}
		if !f.Mode().IsRegular() {
			continue // symlinks and devices are dropped
		}
		total += int64(f.UncompressedSize64)
		if total > maxZipBytes {
			return fmt.Errorf("the zip unpacks to more than %d MiB", maxZipBytes>>20)
		}
		dst := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if f.Mode()&0o111 != 0 {
			mode = 0o755
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, io.LimitReader(rc, maxZipBytes))
		rc.Close()
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// harborTasks lists the task folders (holding task.toml) of a Harbor folder.
func harborTasks(p Peek) []string {
	var out []string
	for _, f := range p.Files {
		if path.Base(f) == "task.toml" {
			out = append(out, path.Dir(f))
		}
	}
	return out
}

func (s importerSource) Inspect(ctx context.Context, env *Env, in Input) (*Inventory, error) {
	tmp, err := os.MkdirTemp("", "onboard-inspect-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	_, p, err := materialize(ctx, env, in, tmp)
	if err != nil {
		return nil, err
	}
	return inspectImporter(p, false)
}

// inspectImporter describes a benchmark-format input.
func inspectImporter(p Peek, public bool) (*Inventory, error) {
	name, label, _ := guessImporter(p)
	inv := &Inventory{Kind: KindImporter, Title: strings.TrimSuffix(p.Name, filepath.Ext(p.Name)), Unit: "file", Units: len(p.Files), CanDraft: true}
	if inv.Title == "" {
		inv.Title = "benchmark"
	}
	if name == "harbor" {
		inv.Kind = KindHarbor
		dirs := harborTasks(p)
		inv.Unit, inv.Units = "task", len(dirs)
		for i, d := range dirs {
			if i >= 5 {
				break
			}
			ins := ""
			for _, cand := range []string{"instruction.md", "task.md"} {
				if b, err := readHead(filepath.Join(p.Dir, filepath.FromSlash(d), cand), 4<<10); err == nil {
					ins = string(b)
					break
				}
			}
			inv.Samples = append(inv.Samples, Sample{Ref: "task " + path.Base(d), Fields: map[string]string{"instruction": ins}})
		}
	}
	for _, info := range importers.All() {
		if info.Name == name {
			inv.Importers = append([]string{info.Name}, inv.Importers...)
			inv.License = info.License
			inv.Needs = append(inv.Needs, info.Needs...)
		} else {
			inv.Importers = append(inv.Importers, info.Name)
		}
	}
	if name == "" {
		inv.Warnings = append(inv.Warnings, "the format was not recognised from the files: pick the benchmark format on the plan ("+label+")")
		inv.License = "unknown: check the upstream licence before you publish"
	}
	limit := DefaultImportLimit
	tasks := inv.Units
	if inv.Unit != "task" || tasks > limit {
		tasks = limit
	}
	inv.Suggest = Plan{Suite: slug(inv.Title), Importer: name, Limit: limit}
	if public || name == "harbor" || name == "swebench" {
		inv.Warnings = append(inv.Warnings, "a public benchmark: models may have seen it in training; nothing is held out by default")
	} else {
		inv.Suggest.Hold = SplitPlan{Pct: 25, Seed: 1}
	}
	inv.Estimate = Estimate{Tasks: tasks, ReadinessRuns: tasks * 6}
	inv.Next = fmt.Sprintf("Draft takes the first %d tasks; raise the limit on the plan to take more.", limit)
	return inv, nil
}

func (s importerSource) Draft(ctx context.Context, env *Env, in Input, plan Plan, set *DraftSet) error {
	from, p, err := materialize(ctx, env, in, filepath.Join(set.Dir(), "source"))
	if err != nil {
		return err
	}
	name := plan.Importer
	if name == "" {
		name, _, _ = guessImporter(p)
	}
	if name == "" {
		return errors.New("pick the benchmark format (plan.importer): the files did not name one")
	}
	return draftImporter(ctx, set, name, from, plan)
}

// draftImporter runs importer name on from into the set's benchmarks root.
func draftImporter(ctx context.Context, set *DraftSet, name, from string, plan Plan) error {
	im, ok := importers.Get(name)
	if !ok {
		return fmt.Errorf("the %s importer is not in this server build", name)
	}
	if !set.NewSuite {
		return fmt.Errorf("benchmark %s exists; an imported benchmark goes into a new benchmark id", set.Suite)
	}
	limit := plan.Limit
	if limit == 0 {
		limit = DefaultImportLimit
	}
	// The draft root holds only this suite: clear the empty folder NewSet made.
	_ = os.Remove(set.SuiteDir())
	res, err := im.Import(ctx, importers.Options{BenchRoot: set.Dir(), BenchmarksDir: set.BenchRoot(), From: from, SuiteID: set.Suite,
		Limit: limit, Extra: plan.Settings, Logger: schemas.NoopLogger{}})
	if err != nil {
		return fmt.Errorf("%s import: %w", name, err)
	}
	info := im.Info()
	set.License = info.License
	set.Public = true
	set.Plan.Importer = name
	for _, sk := range res.Skipped {
		set.Problems = append(set.Problems, Problem{Ref: "task " + sk.ID, Message: sk.Reason})
	}
	set.Notes = append(set.Notes, res.Notes...)
	for _, id := range res.Tasks {
		set.Tasks = append(set.Tasks, DraftTask{ID: id, Origin: Origin{Source: name + " import", Ref: "task " + id}})
	}
	return nil
}
