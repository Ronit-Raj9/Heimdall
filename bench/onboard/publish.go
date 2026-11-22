package onboard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"gopkg.in/yaml.v3"
)

// RecipeFile is a benchmark's onboarding recipe: the sources its tasks came
// from, so they can be pulled again into a new draft.
const RecipeFile = "onboard.yaml"

// Recipe is onboard.yaml.
type Recipe struct {
	Version int            `yaml:"version"`
	Suite   string         `yaml:"suite"`
	Sources []RecipeSource `yaml:"sources"`
}

// RecipeSource is one published draft's source and plan.
type RecipeSource struct {
	Kind      Kind           `yaml:"kind"`
	Source    string         `yaml:"source"`
	Input     map[string]any `yaml:"input"` // Input, as its JSON fields
	Plan      map[string]any `yaml:"plan"`  // Plan, as its JSON fields
	Draft     string         `yaml:"draft"`
	Tasks     []string       `yaml:"tasks"`
	Published time.Time      `yaml:"published"`
}

// PublishOptions choose what publish copies.
type PublishOptions struct {
	Tasks []string `json:"tasks,omitempty"` // empty: every accepted, ready task
	// IncludeUnready also publishes accepted tasks readiness did not admit
	// (or never ran on); they are not ready in the benchmark until its own
	// readiness run admits them.
	IncludeUnready bool   `json:"include_unready,omitempty"`
	Actor          string `json:"-"`
}

// PublishResult is what publish wrote.
type PublishResult struct {
	Suite     string                              `json:"suite"`
	Created   bool                                `json:"created"`
	Tasks     []string                            `json:"tasks"`
	Skipped   []Problem                           `json:"skipped"`
	HeldOut   int                                 `json:"held_out"`
	Canary    string                              `json:"canary,omitempty"`
	Recipe    string                              `json:"recipe"`
	Records   map[string]*schemas.AdmissionRecord `json:"-"` // the draft's readiness records of the published tasks
	Next      string                              `json:"next"`
	SuiteRoot string                              `json:"-"`
}

// ErrNothingToPublish is a publish with no task to copy.
var ErrNothingToPublish = errors.New("nothing to publish")

// Publish copies the chosen tasks of set into benchmarks/<suite> (creating
// the benchmark when it is new), stamps the canary when any published task
// is held out, appends the source to onboard.yaml and records the publish
// on the set. The caller carries the readiness records into the lock.
func Publish(ctx context.Context, env *Env, set *DraftSet, o PublishOptions) (*PublishResult, error) {
	if set.Published != nil {
		return nil, errors.New("this draft was published already; start a new draft to add more tasks")
	}
	tasks, skipped := set.Publishable(o.Tasks, o.IncludeUnready)
	res := &PublishResult{Suite: set.Suite, Skipped: skipped, Records: map[string]*schemas.AdmissionRecord{}}
	if res.Skipped == nil {
		res.Skipped = []Problem{}
	}
	if len(tasks) == 0 {
		return res, fmt.Errorf("%w: no task is accepted and ready (run the readiness checks, or publish not-ready tasks on purpose)", ErrNothingToPublish)
	}
	target := filepath.Join(env.Benchmarks(), set.Suite)
	res.SuiteRoot = target
	_, statErr := os.Stat(filepath.Join(target, suite.SuiteFile))
	exists := statErr == nil
	if suite.IsLibrarySuite(env.Benchmarks(), set.Suite) {
		return res, fmt.Errorf("benchmark %s: %w", set.Suite, ErrReadOnly)
	}
	if set.NewSuite && exists {
		return res, fmt.Errorf("benchmark %s was created after this draft started; start a new draft against it", set.Suite)
	}
	if !set.NewSuite && !exists {
		return res, fmt.Errorf("benchmark %s is gone; start a new draft", set.Suite)
	}
	if !exists {
		if err := createSuite(set, target); err != nil {
			return res, err
		}
		res.Created = true
	}
	raw, err := os.ReadFile(filepath.Join(target, suite.SuiteFile))
	if err != nil {
		return res, err
	}
	before := raw
	var written []string
	undo := func() {
		for _, d := range written {
			os.RemoveAll(d)
		}
		if res.Created {
			os.RemoveAll(target)
		}
	}
	for _, t := range tasks {
		dst := filepath.Join(target, "tasks", t.ID)
		if _, err := os.Lstat(dst); err == nil {
			res.Skipped = append(res.Skipped, Problem{Ref: t.ID, Message: "a task with this id was added to the benchmark meanwhile"})
			continue
		}
		if err := copyTree(filepath.Join(set.SuiteDir(), "tasks", t.ID), dst); err != nil {
			undo()
			return res, fmt.Errorf("copy task %s: %w", t.ID, err)
		}
		written = append(written, dst)
		next, _, err := scaffold.AddTaskID(raw, t.ID)
		if err != nil {
			undo()
			return res, fmt.Errorf("add task %s to suite.yaml: %w", t.ID, err)
		}
		raw = next
		res.Tasks = append(res.Tasks, t.ID)
		if t.HeldOut {
			res.HeldOut++
		}
		if t.Readiness != nil {
			res.Records[t.ID] = t.Readiness
		}
	}
	if len(res.Tasks) == 0 {
		undo()
		return res, fmt.Errorf("%w: every chosen task id is taken in the benchmark", ErrNothingToPublish)
	}
	note := fmt.Sprintf("add %d tasks from draft %s (%s)", len(res.Tasks), set.ID, set.Input.Label())
	if err := writeSuiteYAML(ctx, env, set.Suite, target, raw, before, note); err != nil {
		undo()
		return res, err
	}
	if res.HeldOut > 0 {
		want, err := draftCanaryOwn(set)
		if err != nil {
			return res, fmt.Errorf("canary: %w", err)
		}
		guid, err := ensureCanaryAs(target, want)
		if err != nil {
			return res, fmt.Errorf("canary: %w", err)
		}
		res.Canary = guid
		if loaders := suite.Builtin(env.Benchmarks()); len(loaders) > 0 {
			if s, err := loaders[0].Load(ctx, set.Suite); err == nil {
				if _, err := suite.StampCanary(s, guid); err != nil {
					return res, fmt.Errorf("stamp the canary: %w", err)
				}
			}
		}
	}
	res.Recipe = filepath.ToSlash(filepath.Join(set.Suite, RecipeFile))
	if err := appendRecipe(target, set, res.Tasks); err != nil {
		return res, fmt.Errorf("write %s: %w", RecipeFile, err)
	}
	set.Published = &Published{Suite: set.Suite, Tasks: res.Tasks, Created: res.Created, HeldOut: res.HeldOut, Canary: res.Canary, At: now(), By: o.Actor}
	if err := set.Save(); err != nil {
		return res, err
	}
	res.Next = "Published privately to your organization. Run a first evaluation on " + set.Suite + "."
	return res, nil
}

// createSuite writes the draft suite's own files (no tasks, no lock) as the
// new benchmark folder, with an empty task list.
func createSuite(set *DraftSet, target string) error {
	files, err := suiteFiles(set.SuiteDir())
	if err != nil {
		return err
	}
	if files, err = scaffold.BlankSuite(files); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(target), ".new-"+filepath.Base(target)+"-*")
	if err != nil {
		return err
	}
	if err := scaffold.WriteFiles(tmp, files, false); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	_ = os.Chmod(tmp, 0o755)
	if err := os.Rename(tmp, target); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	return nil
}

// copyTree copies a task folder (regular files and folders only) through a
// hidden sibling, renamed into place.
func copyTree(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dst), ".new-"+filepath.Base(dst)+"-*")
	if err != nil {
		return err
	}
	for _, rel := range listFiles(src, 100000) {
		p := filepath.Join(src, filepath.FromSlash(rel))
		st, err := os.Lstat(p)
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			os.RemoveAll(tmp)
			return err
		}
		out := filepath.Join(tmp, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			os.RemoveAll(tmp)
			return err
		}
		if err := os.WriteFile(out, data, st.Mode().Perm()); err != nil {
			os.RemoveAll(tmp)
			return err
		}
	}
	_ = os.Chmod(tmp, 0o755)
	if err := os.Rename(tmp, dst); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	return nil
}

func writeSuiteYAML(ctx context.Context, env *Env, id, target string, data, before []byte, note string) error {
	if env.Store != nil {
		if base, err := env.Store.Resolve(projectfs.AreaBenchmarks, "."); err == nil {
			if rel, err := filepath.Rel(base, filepath.Join(target, suite.SuiteFile)); err == nil && !strings.HasPrefix(rel, "..") {
				sum := sha256.Sum256(before)
				_, err := env.Store.Write(ctx, projectfs.AreaBenchmarks, filepath.ToSlash(rel), data,
					projectfs.WriteOpts{Actor: env.Actor, Note: note, IfMatch: hex.EncodeToString(sum[:])})
				if err == nil {
					_ = os.Chmod(filepath.Join(target, suite.SuiteFile), 0o644)
				}
				return err
			}
		}
	}
	return os.WriteFile(filepath.Join(target, suite.SuiteFile), data, 0o644)
}

func appendRecipe(target string, set *DraftSet, tasks []string) error {
	p := filepath.Join(target, RecipeFile)
	r := Recipe{Version: 1, Suite: set.Suite}
	if raw, err := os.ReadFile(p); err == nil {
		if err := yaml.Unmarshal(raw, &r); err != nil {
			return err
		}
	}
	r.Version, r.Suite = 1, set.Suite
	r.Sources = append(r.Sources, RecipeSource{Kind: set.Kind, Source: set.Source, Input: jsonMap(set.Input), Plan: jsonMap(set.Plan),
		Draft: set.ID, Tasks: tasks, Published: now()})
	data, err := yaml.Marshal(r)
	if err != nil {
		return err
	}
	head := "# Where this benchmark's tasks came from (Heimdall: Add benchmark). Each source can be drafted again with the same plan.\n"
	return os.WriteFile(p, append([]byte(head), data...), 0o644)
}

// jsonMap is v as its JSON object, so the recipe uses the API's field names.
func jsonMap(v any) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

// ReadRecipe reads a benchmark's onboard.yaml; nil when it has none.
func ReadRecipe(benchmarks, id string) (*Recipe, error) {
	raw, err := os.ReadFile(filepath.Join(benchmarks, id, RecipeFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var r Recipe
	if err := yaml.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	return &r, nil
}
