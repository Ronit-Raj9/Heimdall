package redteam

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/scaffold"
)

// Defaults and limits of a plan.
const (
	// DefaultSuite is the id of the benchmark a plan writes.
	DefaultSuite = "red-team"
	// RegressionsSuite is the org benchmark successful attacks become tasks of.
	RegressionsSuite = "regressions"
	// PlanFile marks a red-team benchmark and holds its plan (at the suite root).
	PlanFile           = "redteam.json"
	DefaultMaxAttempts = 6
	MaxMaxAttempts     = 20
	taskPrefix         = "rt."
	regressionPrefix   = "reg."
)

// Plan is what the org asks for: which attacks, against which channels,
// run by which model.
type Plan struct {
	Suite       string   `json:"suite,omitempty"`
	Categories  []string `json:"categories"`
	Strategies  []string `json:"strategies"`
	Channels    []string `json:"channels"`
	MaxAttempts int      `json:"max_attempts,omitempty"`
	// AttackerProvider and AttackerModel name the model that writes the
	// attacks, called through Bifrost on the org's own key.
	AttackerProvider string `json:"attacker_provider"`
	AttackerModel    string `json:"attacker_model"`
}

// Cell is one generated task: a category attacked with a strategy through
// a channel.
type Cell struct {
	Category string `json:"category"`
	Strategy string `json:"strategy"`
	Channel  string `json:"channel"`
}

// Normalize fills defaults (every category, strategy and channel when none
// is named, the default suite and attempt cap), drops duplicates and checks
// every id. It needs an attacker provider and model.
func (p *Plan) Normalize() error {
	p.Suite = strings.TrimSpace(p.Suite)
	if p.Suite == "" {
		p.Suite = DefaultSuite
	}
	if !scaffold.ValidID(p.Suite) || len(p.Suite) > 64 || p.Suite == RegressionsSuite {
		return fmt.Errorf("suite %q: use 1 to 64 lowercase letters, digits, '.', '_' or '-' (and not %q)", p.Suite, RegressionsSuite)
	}
	if len(p.Categories) == 0 {
		for _, c := range categories {
			p.Categories = append(p.Categories, c.ID)
		}
	}
	if len(p.Strategies) == 0 {
		for _, s := range strategies {
			p.Strategies = append(p.Strategies, s.ID)
		}
	}
	if len(p.Channels) == 0 {
		for _, c := range channels {
			p.Channels = append(p.Channels, c.ID)
		}
	}
	p.Categories, p.Strategies, p.Channels = dedupe(p.Categories), dedupe(p.Strategies), dedupe(p.Channels)
	for _, c := range p.Categories {
		if _, ok := category(c); !ok {
			return fmt.Errorf("category %q: want one of %s", c, strings.Join(ids(categories, func(c Category) string { return c.ID }), ", "))
		}
	}
	for _, s := range p.Strategies {
		if !knownStrategy(s) {
			return fmt.Errorf("strategy %q: want pair, tap or crescendo", s)
		}
	}
	for _, c := range p.Channels {
		if !knownChannel(c) {
			return fmt.Errorf("channel %q: want chat, tool_result, document or email", c)
		}
	}
	if p.MaxAttempts == 0 {
		p.MaxAttempts = DefaultMaxAttempts
	}
	if p.MaxAttempts < 1 || p.MaxAttempts > MaxMaxAttempts {
		return fmt.Errorf("max_attempts %d: want 1 to %d", p.MaxAttempts, MaxMaxAttempts)
	}
	p.AttackerProvider, p.AttackerModel = strings.TrimSpace(p.AttackerProvider), strings.TrimSpace(p.AttackerModel)
	if p.AttackerProvider == "" || p.AttackerModel == "" {
		return errors.New("attacker_provider and attacker_model are needed: the model that writes the attacks, on your own key")
	}
	if strings.ContainsAny(p.AttackerProvider+p.AttackerModel, " \t\n\"'`$") {
		return errors.New("attacker_provider and attacker_model must be plain names")
	}
	if len(p.Cells()) == 0 {
		return errors.New("no attack fits: the categories you picked are not offered on the channels you picked")
	}
	return nil
}

// Cells is every category, strategy and channel combination the plan
// covers, in catalog order; a category is only crossed with the channels it
// is offered on.
func (p Plan) Cells() []Cell {
	var out []Cell
	for _, c := range categories {
		if !slices.Contains(p.Categories, c.ID) {
			continue
		}
		for _, ch := range channels {
			if !slices.Contains(p.Channels, ch.ID) || !slices.Contains(c.Channels, ch.ID) {
				continue
			}
			for _, s := range strategies {
				if slices.Contains(p.Strategies, s.ID) {
					out = append(out, Cell{Category: c.ID, Strategy: s.ID, Channel: ch.ID})
				}
			}
		}
	}
	return out
}

// TaskID is the id of a cell's task: rt.<category>.<strategy>.<channel>.
func TaskID(c Cell) string {
	return taskPrefix + c.Category + "." + c.Strategy + "." + c.Channel
}

// ParseTaskID reads the cell back from a red-team task id, or from a
// regression task id (reg.<category>.<strategy>.<channel>.<hash>).
func ParseTaskID(id string) (Cell, bool) {
	var rest string
	switch {
	case strings.HasPrefix(id, taskPrefix):
		rest = strings.TrimPrefix(id, taskPrefix)
	case strings.HasPrefix(id, regressionPrefix):
		rest = strings.TrimPrefix(id, regressionPrefix)
		i := strings.LastIndex(rest, ".")
		if i < 0 {
			return Cell{}, false
		}
		rest = rest[:i]
	default:
		return Cell{}, false
	}
	parts := strings.Split(rest, ".")
	if len(parts) != 3 {
		return Cell{}, false
	}
	c := Cell{Category: parts[0], Strategy: parts[1], Channel: parts[2]}
	if _, ok := category(c.Category); !ok || !knownChannel(c.Channel) || (!knownStrategy(c.Strategy) && c.Strategy != "scripted") {
		return Cell{}, false
	}
	return c, true
}

// IsRegressionTask reports whether id names a regression task.
func IsRegressionTask(id string) bool {
	_, ok := ParseTaskID(id)
	return ok && strings.HasPrefix(id, regressionPrefix)
}

// IsSuite reports whether the suite folder root holds a red-team benchmark.
func IsSuite(root string) bool {
	if root == "" {
		return false
	}
	fi, err := os.Stat(filepath.Join(root, PlanFile))
	return err == nil && fi.Mode().IsRegular()
}

func dedupe(xs []string) []string {
	var out []string
	for _, x := range xs {
		x = strings.TrimSpace(x)
		if x != "" && !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

func ids[T any](xs []T, f func(T) string) []string {
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		out = append(out, f(x))
	}
	return out
}
