package apptasks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/maximhq/bifrost/bench/scaffold"
)

// WriteBenchmark writes a generated draft's tasks into suiteDir, pending
// review. A new benchmark gets the app service, grader checks and (when
// the tasks keep reply rubrics) the pinned judge; an existing one must be
// a benchmark of the same app copy, and only gets new tasks. It records
// the written tasks in the draft; the caller saves it.
func WriteBenchmark(suiteDir string, st Store, d *Draft, judge *scaffold.JudgeConfig, heldOut bool) ([]Written, error) {
	switch {
	case d.Status == StatusWritten:
		return nil, errors.New("this draft's tasks are already in the benchmark")
	case d.Status != StatusGenerated:
		return nil, errors.New("generate the tasks first")
	case len(d.Plans) == 0:
		return nil, errors.New("no drafted task passed the checks, so there is nothing to write; see why each was dropped")
	}
	m, spec, err := st.Mock(d)
	if err != nil {
		return nil, err
	}
	suiteID := filepath.Base(suiteDir)
	if suiteID != d.Suite {
		return nil, fmt.Errorf("the draft is for benchmark %s, not %s", d.Suite, suiteID)
	}
	suitePath := filepath.Join(suiteDir, "suite.yaml")
	existing, err := os.ReadFile(suitePath)
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	o := WriteOptions{Suite: d.Suite, App: d.App, Title: d.Title, Rules: d.Rules, Seed: d.Seed, Judged: d.Judged, HeldOut: heldOut,
		Source: "OpenAPI " + orDefault(m.Source, d.SpecFile) + " and policy, draft " + d.ID}
	var have []string
	if exists {
		if err := sameApp(suiteDir, d.App, m); err != nil {
			return nil, err
		}
		if have, err = SuiteTasks(existing); err != nil {
			return nil, fmt.Errorf("suite.yaml: %v", err)
		}
		if d.Judged {
			var sj struct {
				Grading string `yaml:"grading"`
			}
			_ = yaml.Unmarshal(existing, &sj)
			if sj.Grading != "judged" {
				return nil, errors.New("the drafted tasks grade the reply with a judge, and this benchmark pins none: pin its judge first (Build > Grader)")
			}
		}
	} else if d.Judged && (judge == nil || judge.Provider == "" || judge.Model == "") {
		return nil, errors.New("the drafted tasks grade the reply with a judge: pick the judge model to pin in the new benchmark")
	}
	taken := map[string]bool{}
	for _, id := range have {
		taken[id] = true
	}
	o.Taken = func(id string) bool {
		if taken[id] {
			return true
		}
		_, err := os.Lstat(filepath.Join(suiteDir, "tasks", id))
		return err == nil
	}
	tasks, problems, err := TaskFiles(d.Plans, o)
	if err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		var why []string
		for _, p := range problems {
			why = append(why, p.Message)
		}
		return nil, fmt.Errorf("no task could be written: %s", strings.Join(why, "; "))
	}
	var files []scaffold.File
	var ids []string
	var written []Written
	for _, t := range tasks {
		for _, f := range t.Files {
			f.Path = "tasks/" + t.ID + "/" + f.Path
			files = append(files, f)
		}
		ids = append(ids, t.ID)
		p := d.Plans[t.Line-1]
		written = append(written, Written{ID: t.ID, Title: t.Title, RuleID: p.Scenario.RuleID, Checks: t.Checks, Summary: t.Check, Review: ReviewPending})
	}
	if !exists {
		service, err := AppServiceFiles(m, spec, "openapi"+filepath.Ext(d.SpecFile))
		if err != nil {
			return nil, err
		}
		var j *scaffold.JudgeConfig
		if d.Judged {
			j = judge
		}
		head, err := SuiteFiles(o, service, ids, j)
		if err != nil {
			return nil, err
		}
		if err := writeNew(suiteDir, append(head, files...)); err != nil {
			return nil, err
		}
	} else {
		for _, t := range tasks {
			var tf []scaffold.File
			for _, f := range t.Files {
				tf = append(tf, f)
			}
			if err := writeNew(filepath.Join(suiteDir, "tasks", t.ID), tf); err != nil {
				return nil, err
			}
		}
		y, err := SetSuiteTasks(existing, append(have, ids...))
		if err != nil {
			return nil, err
		}
		if err := writeFileAtomic(suitePath, y); err != nil {
			return nil, err
		}
	}
	d.Written = append(d.Written, written...)
	d.Status = StatusWritten
	return written, nil
}

// sameApp refuses to add tasks to a benchmark whose app copy is not the
// draft's.
func sameApp(suiteDir, app string, m *scaffold.MockSpec) error {
	have, err := scaffold.ReadMock(filepath.Join(suiteDir, "services", app))
	if err == nil {
		_, err = os.Stat(filepath.Join(suiteDir, "services", app, "simuser.py"))
	}
	if err != nil {
		return fmt.Errorf("the benchmark has no app copy services/%s from the generator; write into a new benchmark", app)
	}
	a, _ := json.Marshal(have.Operations)
	b, _ := json.Marshal(m.Operations)
	if !bytes.Equal(a, b) {
		return fmt.Errorf("the benchmark's app copy services/%s was made from another version of the OpenAPI file; write into a new benchmark", app)
	}
	return nil
}

// writeNew writes files into a hidden sibling of dir, then renames it into
// place; dir must not exist.
func writeNew(dir string, files []scaffold.File) error {
	if _, err := os.Lstat(dir); err == nil {
		return fmt.Errorf("%s already exists", filepath.Base(dir))
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), ".new-"+filepath.Base(dir)+"-*")
	if err != nil {
		return err
	}
	if err := scaffold.WriteFiles(tmp, files, false); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	if err := os.Rename(tmp, dir); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	return nil
}

func writeFileAtomic(p string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(p), "."+filepath.Base(p)+"-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	tmp.Close()
	_ = os.Chmod(tmp.Name(), 0o644)
	return os.Rename(tmp.Name(), p)
}

// Review accepts or rejects one written task. Accept marks the task
// reviewed (provenance instruction_reviewed: true); reject removes it from
// the benchmark. Only a pending task can be reviewed.
func Review(suiteDir string, d *Draft, taskID, decision, by string) error {
	i := slices.IndexFunc(d.Written, func(w Written) bool { return w.ID == taskID })
	if i < 0 {
		return fmt.Errorf("task %s was not written by this draft", taskID)
	}
	w := &d.Written[i]
	if w.Review != ReviewPending {
		return fmt.Errorf("task %s is already %s", taskID, w.Review)
	}
	taskDir := filepath.Join(suiteDir, "tasks", taskID)
	switch decision {
	case ReviewAccepted:
		p := filepath.Join(taskDir, "task.yaml")
		raw, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("task %s: %v", taskID, err)
		}
		out, changed, err := scaffold.SetInstructionReviewed(raw)
		if err != nil {
			return fmt.Errorf("task %s: %v", taskID, err)
		}
		if changed {
			if err := writeFileAtomic(p, out); err != nil {
				return err
			}
		}
	case ReviewRejected:
		sp := filepath.Join(suiteDir, "suite.yaml")
		raw, err := os.ReadFile(sp)
		if err != nil {
			return err
		}
		ids, err := SuiteTasks(raw)
		if err != nil {
			return err
		}
		ids = slices.DeleteFunc(ids, func(id string) bool { return id == taskID })
		y, err := SetSuiteTasks(raw, ids)
		if err != nil {
			return err
		}
		if err := writeFileAtomic(sp, y); err != nil {
			return err
		}
		if err := os.RemoveAll(taskDir); err != nil {
			return err
		}
	default:
		return fmt.Errorf("decision %q must be accepted or rejected", decision)
	}
	w.Review, w.ReviewedBy, w.ReviewedAt = decision, by, time.Now().UTC()
	return nil
}
