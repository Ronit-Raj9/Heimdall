package agentdojo

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/importers"
)

// catalog is what image/heimdall/catalog.py prints for one suite: upstream's
// own task definitions, read inside the image so upstream code never runs on
// the host.
type catalog struct {
	PackageVersion    string          `json:"agentdojo_version"`
	BenchmarkVersion  string          `json:"benchmark_version"`
	BenchmarkVersions []string        `json:"benchmark_versions"`
	Suite             string          `json:"suite"`
	SystemMessage     string          `json:"system_message"`
	Tools             []catalogTool   `json:"tools"`
	InjectionVectors  []string        `json:"injection_vectors"`
	UserTasks         []userTask      `json:"user_tasks"`
	InjectionTasks    []injectionTask `json:"injection_tasks"`
	Attacks           []string        `json:"attacks"`
}

// catalogTool is one tool of the suite.
type catalogTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// userTask is one AgentDojo user task.
type userTask struct {
	ID                string         `json:"id"`
	Prompt            string         `json:"prompt"`
	Difficulty        string         `json:"difficulty"`
	GroundTruth       []functionCall `json:"ground_truth"`
	GroundTruthOutput string         `json:"ground_truth_output"`
	InjectionVectors  []string       `json:"injection_vectors"` // vectors its ground truth reads, where attacks go
	EmptyPasses       bool           `json:"empty_passes"`      // upstream's utility passes with no call and no answer
	MutantFails       bool           `json:"mutant_fails"`      // utility fails on the ground truth without its last call and the answer
}

// functionCall is one upstream ground-truth tool call.
type functionCall struct {
	Function string          `json:"function"`
	Args     json.RawMessage `json:"args"`
}

// injectionTask is one AgentDojo injection task: the attacker's goal.
type injectionTask struct {
	ID         string `json:"id"`
	Goal       string `json:"goal"`
	Difficulty string `json:"difficulty"`
}

// probe is one attack condition: an attack carrying one injection task.
type probe struct {
	id        string
	attack    string
	injection injectionTask
}

var upstreamTaskID = regexp.MustCompile(`^user_task_\d+$`)

// parseCatalog decodes and checks the catalog. It is pure.
func parseCatalog(b []byte) (*catalog, error) {
	var c catalog
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	switch {
	case c.PackageVersion == "" || c.Suite == "" || c.BenchmarkVersion == "":
		return nil, errors.New("missing agentdojo_version, suite or benchmark_version")
	case len(c.UserTasks) == 0:
		return nil, errors.New("no user tasks")
	case len(c.Tools) == 0:
		return nil, errors.New("no tools")
	}
	for _, t := range c.UserTasks {
		if !upstreamTaskID.MatchString(t.ID) || strings.TrimSpace(t.Prompt) == "" {
			return nil, fmt.Errorf("user task %q has a bad id or no prompt", t.ID)
		}
	}
	return &c, nil
}

// selectTasks picks the user tasks to import: those named in ids (upstream
// ids like user_task_3, or the bench ids this importer writes), else every
// one, in upstream order, up to limit. A task whose ground truth reads no
// injection vector cannot be attacked and is left out, as upstream's attacks
// refuse it. A task whose utility passes with no action at all is left out
// too: its empty control would pass, so admission could never trust it.
func selectTasks(c *catalog, suite string, ids []string, limit int) ([]userTask, []importers.Skip) {
	var picked []userTask
	var skips []importers.Skip
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	found := map[string]bool{}
	for _, t := range c.UserTasks {
		if len(ids) > 0 && !wanted[t.ID] && !wanted[taskID(suite, t.ID)] {
			continue
		}
		found[t.ID], found[taskID(suite, t.ID)] = true, true
		if len(t.InjectionVectors) == 0 {
			skips = append(skips, importers.Skip{ID: t.ID, Reason: "its ground truth reads no injection vector, so no attack can reach it"})
			continue
		}
		if t.EmptyPasses {
			skips = append(skips, importers.Skip{ID: t.ID, Reason: "upstream's utility check passes with no tool call and no answer, so an empty run would pass"})
			continue
		}
		if limit > 0 && len(picked) >= limit {
			skips = append(skips, importers.Skip{ID: t.ID, Reason: fmt.Sprintf("over -limit %d", limit)})
			continue
		}
		picked = append(picked, t)
	}
	for _, id := range ids {
		if !found[id] {
			skips = append(skips, importers.Skip{ID: id, Reason: "not a user task of " + suite + " " + c.BenchmarkVersion})
		}
	}
	return picked, skips
}

// selectProbes builds one probe per attack and injection task. Unknown
// names are errors: a typo would otherwise import a suite without the
// condition the user asked for.
func selectProbes(c *catalog, set settings) ([]probe, error) {
	for _, a := range set.attacks {
		if !slices.Contains(c.Attacks, a) {
			return nil, fmt.Errorf("attack %q is not one the image can plant (%s)", a, strings.Join(c.Attacks, ", "))
		}
	}
	byID := map[string]injectionTask{}
	for _, it := range c.InjectionTasks {
		byID[it.ID] = it
	}
	chosen := c.InjectionTasks
	if len(set.injectionTasks) > 0 {
		chosen = nil
		for _, id := range set.injectionTasks {
			it, ok := byID[id]
			if !ok {
				return nil, fmt.Errorf("injection task %q is not in %s %s", id, c.Suite, c.BenchmarkVersion)
			}
			chosen = append(chosen, it)
		}
	}
	if len(chosen) == 0 {
		return nil, fmt.Errorf("%s %s has no injection tasks", c.Suite, c.BenchmarkVersion)
	}
	var out []probe
	for _, a := range set.attacks {
		for _, it := range chosen {
			out = append(out, probe{id: probeID(a, it.ID), attack: a, injection: it})
		}
	}
	return out, nil
}

// taskID is the bench task id of an upstream user task: banking-user-task-3.
func taskID(suite, upstreamID string) string {
	return suite + "-" + strings.ReplaceAll(upstreamID, "_", "-")
}

// probeID names an attack condition: agentdojo-important-instructions-inj0.
func probeID(attack, injectionTaskID string) string {
	n := strings.TrimPrefix(injectionTaskID, "injection_task_")
	return "agentdojo-" + strings.ReplaceAll(attack, "_", "-") + "-inj" + strings.ReplaceAll(n, "_", "-")
}
