package handlers

// A finding from a behaviour check (D4 10.1, source "check"): the ref is
// "<rule id>:<exp id>", and the finding holds the tasks and runs of the
// experiment where that rule fired, read with the same rules and run steps
// as GET /experiments/{id}/behaviour.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/findings"
	"github.com/maximhq/bifrost/bench/steprules"
	"github.com/valyala/fasthttp"
)

const findingsCheckNotFired = "This check did not fire on any run of that comparison, so there is nothing to track."

func (h *findingsHandler) fromCheck(ref string) (*findings.Finding, int, string) {
	ruleID, expID, ok := strings.Cut(ref, ":")
	if !ok || !validID(ruleID) || !validID(expID) {
		return nil, fasthttp.StatusBadRequest, "ref must be <check id>:<comparison id>"
	}
	idx := h.env.Services.Index
	if idx == nil {
		return nil, fasthttp.StatusServiceUnavailable, "verified run index is not available on this server"
	}
	if m, ok := idx.Manifest(expID); !ok || m == nil {
		return nil, fasthttp.StatusNotFound, "experiment not found: " + expID
	}
	root := ""
	if h.env.Cfg != nil {
		root = h.env.Cfg.Root
	}
	bh := &behaviourHandler{mode: h.env.Mode, root: root, index: idx}
	file, err := bh.load()
	if err != nil {
		return nil, fasthttp.StatusUnprocessableEntity, err.Error()
	}
	var rule *steprules.Compiled
	for _, c := range file.compiled() {
		if c.Rule().ID == ruleID {
			rule = c
			break
		}
	}
	if rule == nil {
		return nil, fasthttp.StatusNotFound, "behaviour check not found: " + ruleID
	}
	views := idx.Runs(expID)
	sort.SliceStable(views, func(i, j int) bool { return views[i].Run.RunID < views[j].Run.RunID })
	f := &findings.Finding{Key: fmt.Sprintf("check:%s:%s", ruleID, expID), Title: rule.Rule().Title, Source: "check", Origin: "rule"}
	if f.Title == "" {
		f.Title = ruleID
	}
	read := 0
	for _, v := range views {
		if v.Superseded || v.Run.RunID == "" {
			continue
		}
		if read >= maxBehaviourRuns {
			break
		}
		read++
		rb, err := bh.checkRun(v.Run.RunID, []*steprules.Compiled{rule})
		if err != nil {
			return nil, fasthttp.StatusServiceUnavailable, "the steps of run " + v.Run.RunID + " could not be read"
		}
		if len(rb.Results) == 0 || rb.Results[0].Status != steprules.StatusFired {
			continue
		}
		if v.Slot.Task != "" && v.Slot.Suite != "" {
			f.Tasks = addTask(f.Tasks, findings.TaskRef{Suite: suiteName(v.Slot.Suite), Task: v.Slot.Task})
		}
		f.Runs = append(f.Runs, v.Run.RunID)
	}
	if len(f.Runs) == 0 {
		return nil, fasthttp.StatusUnprocessableEntity, findingsCheckNotFired
	}
	return f, 0, ""
}
