package improve

import (
	"errors"
	"fmt"
	"slices"
)

// Explain is what changed in one version of an improvement run and why
// (A4.3): each file against the version it was made from, each edit with
// its reason and result, the gates and the round's decision. Evidence refs
// are given as counts only, so no run or task of another split is named.
type Explain struct {
	Ref       string        `json:"ref"`
	ParentRef string        `json:"parent_ref"`
	Round     int           `json:"round"`
	Source    string        `json:"source"`
	Improver  string        `json:"improver"`
	Model     string        `json:"model"`
	Files     []ExplainFile `json:"files"`
	Rationale string        `json:"rationale"`
	Gates     []GateResult  `json:"gates"`
	Decision  string        `json:"decision"`
	Why       string        `json:"why"`
}

// ExplainFile is one bundle path: its diff against the parent and the
// edits that named it. Status is added, removed, changed or unchanged (an
// edit that named a file whose text did not change).
type ExplainFile struct {
	Path   string        `json:"path"`
	Status string        `json:"status"`
	Lines  []DiffLine    `json:"lines"`
	Edits  []ExplainEdit `json:"edits"`
}

// ExplainEdit is one edit the improver proposed, with counts in place of
// its evidence refs and predicted task ids.
type ExplainEdit struct {
	Op            string `json:"op"`
	ID            string `json:"id,omitempty"`
	Reason        string `json:"reason"`
	EvidenceCount int    `json:"evidence_count"`
	PredictsFixed int    `json:"predicts_fixed"`
	Result        string `json:"result"`
}

// Explain builds the explain view of version n (the agent version number)
// of improvement id. It reads only the record and the candidate, edits and
// bundle objects. Version 0 of the run (the base) has no parent and no
// changes. A number no round produced is ErrNotFound.
func (r *Runner) Explain(id string, n int) (*Explain, error) {
	rec, err := r.load(id)
	if err != nil {
		return nil, err
	}
	if n == rec.Base.Number {
		return &Explain{Ref: rec.Base.Ref, Round: 0, Source: "v0", Improver: rec.Spec.Improver.Task, Files: []ExplainFile{}, Gates: []GateResult{}}, nil
	}
	var round *Round
	for _, rd := range rec.Rounds {
		if rd.Candidate != nil && rd.Candidate.Number == n {
			round = rd
		}
	}
	if round == nil {
		return nil, fmt.Errorf("version %d of %s: %w", n, id, ErrNotFound)
	}
	parent, err := r.bundleOf(round.Parent)
	if err != nil {
		return nil, err
	}
	cand, err := r.bundleOf(*round.Candidate)
	if err != nil {
		return nil, err
	}
	x := &Explain{
		Ref:       round.Candidate.Ref,
		ParentRef: round.Parent.Ref,
		Round:     round.N,
		Source:    "mutation",
		Improver:  rec.Spec.Improver.Task,
		Gates:     round.Gates,
		Decision:  round.Decision,
		Why:       round.Why,
	}
	if round.Improver != nil {
		x.Rationale = round.Improver.Summary
	}
	run := candidateRunOf(round, n)
	var edits []AppliedEdit
	if run != nil {
		if run.Kind != "" {
			x.Source = run.Kind
		}
		edits = run.Edits
		if len(run.Gates) > 0 {
			x.Gates = run.Gates
		}
		if run.Obj != "" && r.Objects.Root != "" {
			var obj CandidateObject
			if err := r.Objects.Get(run.Obj, ObjCandidate, &obj); err != nil && !errors.Is(err, ErrNotFound) {
				return nil, err
			} else if err == nil {
				x.Model = obj.Model
				if obj.Source != "" {
					x.Source = obj.Source
				}
				if obj.Edits != "" {
					var ef EditsFile
					if err := r.Objects.Get(obj.Edits, ObjEdits, &ef); err == nil {
						if ef.Rationale != "" {
							x.Rationale = ef.Rationale
						}
						if len(edits) == 0 {
							for _, e := range ef.Edits {
								edits = append(edits, AppliedEdit{Edit: e})
							}
						}
					}
				}
			}
		}
	}
	if x.Gates == nil {
		x.Gates = []GateResult{}
	}
	x.Files = explainFiles(Diff(parent, cand), edits)
	return x, nil
}

// bundleOf is a version's bundle: its bundle object when the run stores
// objects, else the bundle kept in the record.
func (r *Runner) bundleOf(v Version) (Bundle, error) {
	if v.BundleObj == "" || r.Objects.Root == "" {
		return v.Bundle, nil
	}
	var b BundleObject
	if err := r.Objects.Get(v.BundleObj, ObjBundle, &b); err != nil {
		if errors.Is(err, ErrNotFound) {
			return v.Bundle, nil
		}
		return nil, err
	}
	return Bundle(b.Files), nil
}

// candidateRunOf is the round's idea that became version n: the one whose
// version has that number, else the winner.
func candidateRunOf(round *Round, n int) *CandidateRun {
	for i := range round.Candidates {
		if v := round.Candidates[i].Version; v != nil && v.Number == n {
			return &round.Candidates[i]
		}
	}
	for i := range round.Candidates {
		if round.Candidates[i].ID == round.Winner && round.Winner != "" {
			return &round.Candidates[i]
		}
	}
	return nil
}

// explainFiles joins the diff and the edits by path. An edit names its
// file (File) or a skill path (Path); one naming no changed file gets an
// unchanged entry so its reason and result still show.
func explainFiles(diff []FileDiff, edits []AppliedEdit) []ExplainFile {
	out := make([]ExplainFile, 0, len(diff))
	at := map[string]int{}
	for _, d := range diff {
		at[d.Path] = len(out)
		lines := d.Lines
		if lines == nil {
			lines = []DiffLine{}
		}
		out = append(out, ExplainFile{Path: d.Path, Status: d.Status, Lines: lines, Edits: []ExplainEdit{}})
	}
	for _, e := range edits {
		path := e.File
		if path == "" {
			path = e.Path
		}
		i, ok := at[path]
		if !ok {
			at[path] = len(out)
			i = len(out)
			out = append(out, ExplainFile{Path: path, Status: "unchanged", Lines: []DiffLine{}, Edits: []ExplainEdit{}})
		}
		out[i].Edits = append(out[i].Edits, ExplainEdit{
			Op:            e.Op,
			ID:            e.ID,
			Reason:        e.Reason,
			EvidenceCount: len(e.Evidence),
			PredictsFixed: len(e.PredictsFixed),
			Result:        e.Result,
		})
	}
	slices.SortStableFunc(out, func(a, b ExplainFile) int {
		switch {
		case a.Path < b.Path:
			return -1
		case a.Path > b.Path:
			return 1
		}
		return 0
	})
	return out
}
