package main

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/traceimport"
	"github.com/maximhq/bifrost/bench/tracedraft"
	"github.com/maximhq/bifrost/bench/traceinbox"
)

// Wave 4 (W4-45): trace.draft asks a model to draft what should have
// happened in one inbox item (the expected answer, or the right and the
// forbidden tool calls) and writes inbox/items/<item>/proposal.json for the
// review screen. One call, two with the quote repair round; the session text
// is redacted before it is put in the prompt.

const jobTraceDraft = "trace.draft"

// traceDraftMaxUSD caps one draft's spend.
const traceDraftMaxUSD = 2.0

func init() {
	registerJobKind(jobKindSpec{Kind: jobTraceDraft, Local: true, Hosted: true, Opts: []jobs.KindOption{jobs.WithLimit(4, 1)},
		Validate: validateTraceDraft, Run: runTraceDraft})
}

// traceDraftParams are the params of trace.draft.
type traceDraftParams struct {
	Item        string `json:"item"`  // inbox item id, traceinbox.ValidItemID
	Suite       string `json:"suite"` // the benchmark the task will join
	Kind        string `json:"kind"`  // qa | tool-use | mcp | memory; empty takes the suite's template
	modelParams        // provider, model, key_id, budget_usd (at most 2.00), yes
}

func validateTraceDraft(_ context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
	var p traceDraftParams
	if err := liveDecode(raw, &p); err != nil {
		return nil, "", err
	}
	if err := checkTraceDraft(env, &p); err != nil {
		return nil, "", err
	}
	return p, "inbox/" + p.Item, nil
}

// checkTraceDraft checks and normalizes the params: the item exists, the
// kind is known (or comes from the suite), a model is named, the cost is
// confirmed and the cap is above zero and at most traceDraftMaxUSD.
func checkTraceDraft(env jobEnv, p *traceDraftParams) error {
	switch {
	case !traceinbox.ValidItemID(p.Item):
		return liveInvalid("item %q is not an inbox item id", p.Item)
	case !scaffold.ValidID(p.Suite):
		return liveInvalid("Pick a benchmark first.")
	case strings.TrimSpace(p.Provider) == "" || strings.TrimSpace(p.Model) == "":
		return liveInvalid("Pick a model first.")
	case !p.Yes:
		return liveInvalid("Confirm the cost first.")
	case p.BudgetUSD <= 0 || math.IsNaN(p.BudgetUSD) || math.IsInf(p.BudgetUSD, 0):
		return liveInvalid("Set a spending cap above zero.")
	}
	p.BudgetUSD = math.Min(p.BudgetUSD, traceDraftMaxUSD)
	if p.Kind == "" {
		p.Kind = suiteTemplate(env, p.Suite)
	}
	if !slices.Contains(traceimport.Kinds(), p.Kind) {
		return liveInvalid("kind must be one of %s", strings.Join(traceimport.Kinds(), ", "))
	}
	if _, err := os.Stat(filepath.Join(traceDraftInboxDir(env), traceinbox.ItemsDir, p.Item)); err != nil {
		return liveInvalid("No inbox item %s.", p.Item)
	}
	return nil
}

// suiteTemplate is the template of the suite's suite.yaml, or "".
func suiteTemplate(env jobEnv, suiteID string) string {
	if env.App == nil || env.App.cfg == nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(env.App.cfg.Path(env.App.cfg.BenchmarksRoot), suiteID, "suite.yaml"))
	if err != nil {
		return ""
	}
	return scaffold.DetectTemplate(data)
}

func traceDraftInboxDir(env jobEnv) string {
	return filepath.Join(env.ProjectRoot, traceinbox.Dir)
}

func runTraceDraft(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, _ func(any)) (any, error) {
	var p traceDraftParams
	if err := liveDecode(j.Params, &p); err != nil {
		return nil, err
	}
	if err := checkTraceDraft(env, &p); err != nil {
		return nil, err
	}
	store := traceinbox.NewFileStore(traceDraftInboxDir(env))
	sess, err := store.Session(p.Item)
	if err != nil {
		return nil, err
	}
	if openCaller == nil {
		return nil, errors.New("this server makes no model calls")
	}
	c, done, err := openCaller(ctx, env, j, p.modelParams)
	if err != nil {
		return nil, err
	}
	defer done()
	prop, err := tracedraft.Draft(ctx, c, tracedraft.Input{Item: p.Item, Kind: p.Kind, Session: sess, Now: time.Now().UTC()})
	if err != nil {
		return nil, err
	}
	if err := writeProposal(filepath.Join(store.Dir(), traceinbox.ItemsDir, p.Item, handlers.ProposalFile), prop); err != nil {
		return nil, err
	}
	if log != nil {
		log.Printf("drafted %s with %s for $%.4f; %d unverified suggestions dropped", p.Item, prop.Model, prop.CostUSD, prop.Dropped)
	}
	return prop, nil
}

// writeProposal writes the proposal atomically (a temp file renamed).
func writeProposal(path string, p *tracedraft.Proposal) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".proposal-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
