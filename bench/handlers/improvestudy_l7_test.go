package handlers

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/improve"
	"github.com/maximhq/bifrost/bench/improve/improvetest"
)

// webStudyBody is the exact body the New improvement form posts for a
// study (ui newImprovementDialog.ufl7.test.tsx WEB_STUDY_BODY).
const webStudyBody = `{"kind":"improvement","name":"s-study","agent":"agents/a.yaml","suite":"hd-harbor-bfcl@1.0.0","preset":"small",` +
	`"editable":{"instructions":true,"files":[]},"improver":{"task":"heimdall-improver@1.1.0:reflect-edit"},"budget":{"total_usd":12},` +
	`"replicate":{"loops":3}}`

// l7StudySource records the spec the study route hands on.
type l7StudySource struct {
	runnerSource
	got     *improve.Spec
	stopped string
}

func (s *l7StudySource) CreateStudy(_ context.Context, spec *improve.Spec, _ string) (*improve.StudyView, error) {
	s.got = spec
	return &improve.StudyView{ID: "ims_s-study-1", Name: spec.Name, LoopsMax: spec.Replicate.Loops, Loops: []string{},
		Limit: improve.StudyLimitText(spec.Budget.TotalUSD, spec.Replicate.Loops)}, nil
}

func (s *l7StudySource) ListStudies(context.Context) (*improve.StudyList, error) {
	return &improve.StudyList{Studies: []improve.StudyView{{ID: "ims_s-study-1", Name: "s-study", Loops: []string{}}}}, nil
}

func (s *l7StudySource) GetStudy(_ context.Context, id string) (*improve.StudyView, error) {
	return &improve.StudyView{ID: id, Name: "s-study", Loops: []string{}}, nil
}

func (s *l7StudySource) StopStudy(_ context.Context, id, _ string) (*improve.StudyView, error) {
	s.stopped = id
	return &improve.StudyView{ID: id, Status: improve.StatusStopped, Loops: []string{}}, nil
}

// The form's study body passes the route's checks with its loops and limit,
// and the list, open and stop routes answer what the Studies tab reads.
func TestWebStudyBodyThroughStudyRoutes(t *testing.T) {
	x := &improvetest.Exec{Behavior: improvetest.Quiet, Base: 1, NTasks: 40}
	src := &l7StudySource{runnerSource: runnerSource{r: &improve.Runner{Root: filepath.Join(t.TempDir(), improve.DefaultDir), Exec: x}}}
	c := newImproveClient(t, src)

	resp := c.do(t, "POST", APIPrefix+"/improvement-studies", []byte(webStudyBody), authed(""))
	if resp.StatusCode() != 202 {
		t.Fatalf("create: %d %s", resp.StatusCode(), resp.Body())
	}
	if src.got == nil || src.got.Replicate.Loops != 3 || src.got.Budget.TotalUSD != 12 || src.got.Preset != "small" {
		t.Fatalf("spec handed on: %+v", src.got)
	}
	var v improve.StudyView
	if err := json.Unmarshal(resp.Body(), &v); err != nil || v.Limit != "Total for 3 loops: at most $12.00 ($4.00 for each loop)" {
		t.Fatalf("view %+v %v", v, err)
	}
	// One loop is a plain run: the study route refuses it in words.
	one := []byte(`{"kind":"improvement","name":"s-one","agent":"agents/a.yaml","suite":"x@1.0.0","preset":"small",` +
		`"editable":{"instructions":true,"files":[]},"improver":{"task":"heimdall-improver@1.1.0:reflect-edit"},"budget":{"total_usd":2}}`)
	if resp = c.do(t, "POST", APIPrefix+"/improvement-studies", one, authed("")); resp.StatusCode() != 422 {
		t.Fatalf("one loop: %d %s", resp.StatusCode(), resp.Body())
	}
	if resp = c.do(t, "GET", APIPrefix+"/improvement-studies", nil, nil); resp.StatusCode() != 200 {
		t.Fatalf("list: %d", resp.StatusCode())
	}
	if resp = c.do(t, "POST", APIPrefix+"/improvement-studies/ims_s-study-1/stop", nil, nil); resp.StatusCode() != 401 {
		t.Fatalf("stop without token: %d", resp.StatusCode())
	}
	if resp = c.do(t, "POST", APIPrefix+"/improvement-studies/ims_s-study-1/stop", nil, authed("")); resp.StatusCode() != 200 || src.stopped != "ims_s-study-1" {
		t.Fatalf("stop: %d %s", resp.StatusCode(), resp.Body())
	}
}
