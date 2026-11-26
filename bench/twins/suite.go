package twins

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/maximhq/bifrost/bench/schemas"
	"gopkg.in/yaml.v3"
)

// SuiteSuffix ends the id of a twin benchmark.
const SuiteSuffix = "-twins"

// MaxTwins is the most twins one call makes.
const MaxTwins = 50

// Made is one twin a call made.
type Made struct {
	Task     string    `json:"task"`
	Twin     string    `json:"twin"`
	Mutation *Mutation `json:"mutation"`
}

// Skipped is a task that got no twin, with the reason in plain words.
type Skipped struct {
	Task   string `json:"task"`
	Reason string `json:"reason"`
}

// Result is what MakeSuite did.
type Result struct {
	Suite   string    `json:"suite"` // the twin benchmark
	Made    []Made    `json:"made"`
	Skipped []Skipped `json:"skipped"`
}

// MakeSuite writes twins of src's tasks (all, or the ones named in only, at
// most max) into benchmarksDir/<suite>-twins. The twin benchmark is made on
// first use from src's suite.yaml (same settings, grader and images); its
// task list is every twin folder it holds.
func MakeSuite(src *schemas.Suite, benchmarksDir string, only []string, max int) (*Result, error) {
	if max <= 0 || max > MaxTwins {
		max = MaxTwins
	}
	id := src.ID + SuiteSuffix
	dst := filepath.Join(benchmarksDir, id)
	if err := os.MkdirAll(filepath.Join(dst, "tasks"), 0o755); err != nil {
		return nil, err
	}
	res := &Result{Suite: id, Made: []Made{}, Skipped: []Skipped{}}
	for _, t := range src.Tasks {
		if len(only) > 0 && !slices.Contains(only, t.ID) {
			continue
		}
		if len(res.Made) >= max {
			res.Skipped = append(res.Skipped, Skipped{Task: t.ID, Reason: fmt.Sprintf("At most %d twins are made at a time.", max)})
			continue
		}
		m, err := Make(t, dst)
		if err != nil {
			reason := err.Error()
			if errors.Is(err, ErrNoMutation) {
				reason = "No test assertion with a fixed value was found to change."
			}
			res.Skipped = append(res.Skipped, Skipped{Task: t.ID, Reason: reason})
			continue
		}
		res.Made = append(res.Made, Made{Task: t.ID, Twin: t.ID + Suffix, Mutation: m})
	}
	if err := writeSuiteFile(filepath.Join(src.Root, "suite.yaml"), dst, id); err != nil {
		return nil, err
	}
	return res, nil
}

// writeSuiteFile writes dst/suite.yaml from the source's file with the new
// id and the task list of the twin folders in dst.
func writeSuiteFile(srcFile, dst, id string) error {
	b, err := os.ReadFile(srcFile)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return fmt.Errorf("parse suite.yaml: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return errors.New("suite.yaml is not a mapping")
	}
	root := doc.Content[0]
	setScalar(root, "id", id, "!!str")
	ents, err := os.ReadDir(filepath.Join(dst, "tasks"))
	if err != nil {
		return err
	}
	list := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, e := range ents {
		if e.IsDir() {
			list.Content = append(list.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: e.Name()})
		}
	}
	setNode(root, "tasks", list)
	setNode(root, "probes", &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"})
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dst, "suite.yaml"), out, 0o644)
}

func setNode(m *yaml.Node, key string, v *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = v
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, v)
}
