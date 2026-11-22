package balrog

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/importers"
)

// catalog is what image/heimdall/catalog.py prints.
type catalog struct {
	Format   string    `json:"format"`
	Episodes []episode `json:"episodes"`
	Errors   []string  `json:"errors"`
}

// episode is one upstream evaluation episode: environment, task and seed.
type episode struct {
	Env           string   `json:"env"`
	Task          string   `json:"task"`
	Seed          int      `json:"seed"`
	Error         string   `json:"error"`
	Instruction   string   `json:"instruction"`
	MaxSteps      int      `json:"max_steps"`
	Actions       []string `json:"actions"`
	FirstObs      string   `json:"first_observation"`
	Reference     []string `json:"reference"`
	ReferenceNote string   `json:"reference_note"`
	MutantFails   bool     `json:"mutant_fails"`
}

func parseCatalog(b []byte) (*catalog, error) {
	var c catalog
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse catalog: %w", err)
	}
	if c.Format != "heimdall-balrog-catalog/1" {
		return nil, fmt.Errorf("catalog format %q is not heimdall-balrog-catalog/1", c.Format)
	}
	if len(c.Episodes) == 0 {
		return nil, errors.New("catalog lists no episodes")
	}
	return &c, nil
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

// taskID names an episode: balrog-babyai-mixedtrainlocal-v0-goto-s3.
func taskID(e episode) string {
	slug := strings.Trim(slugRE.ReplaceAllString(strings.ToLower(e.Task), "-"), "-")
	slug = strings.TrimPrefix(slug, "env-")
	slug = strings.TrimPrefix(slug, "babyai-")
	slug = strings.TrimPrefix(slug, "minihack-")
	return fmt.Sprintf("balrog-%s-%s-s%d", e.Env, slug, e.Seed)
}

// selectEpisodes keeps episodes whose env is wanted and whose id or task is
// asked for. An episode whose environment does not build is kept: it is
// written as a draft whose reference control refuses with the error.
func selectEpisodes(c *catalog, envs, ids []string, limit int) ([]episode, []importers.Skip) {
	var out []episode
	var skips []importers.Skip
	for _, e := range c.Episodes {
		if len(envs) > 0 && !slices.Contains(envs, e.Env) {
			continue
		}
		id := taskID(e)
		if len(ids) > 0 && !slices.Contains(ids, id) && !slices.Contains(ids, e.Task) {
			continue
		}
		if limit > 0 && len(out) >= limit {
			break
		}
		out = append(out, e)
	}
	return out, skips
}
