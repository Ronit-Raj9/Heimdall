package improve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/stats"
	"github.com/maximhq/bifrost/bench/utils"
)

// A study runs the whole loop several times from version 0 and tests the
// pooled gain on one shared held-out split (A4.1).

// StudyDir is the folder of studies under the improvements root.
const (
	StudyDir   = "studies"
	StudyFile  = "study.json"
	MaxLoops   = 5
	StudyAlpha = 0.05
	// PhaseStudyFinal is the study's one experiment on the held-out tasks.
	PhaseStudyFinal = "study_final"
)

var studyIDRe = regexp.MustCompile(`^ims_[a-z0-9-]{1,64}$`)

// ValidStudyID reports whether id is a study id.
func ValidStudyID(id string) bool { return studyIDRe.MatchString(id) }

// Study is a whole-loop replication as stored in study.json. Sealed holds
// the held-out task ids and is never served (StudyView).
type Study struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Spec      Spec          `json:"spec"`
	SpecHash  string        `json:"spec_hash"`
	Loops     []string      `json:"loops"`
	Sealed    []string      `json:"sealed"`
	Status    string        `json:"status"`
	Reason    string        `json:"reason"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
	CreatedBy string        `json:"created_by"`
	Final     *StudyVerdict `json:"final,omitempty"`
	CostUSD   float64       `json:"cost_usd,omitempty"`
}

// StudyVerdict is the pooled test of a study's final experiment.
type StudyVerdict struct {
	State     string      `json:"state"`
	Headline  string      `json:"headline"`
	N         int         `json:"n"`
	NNonzero  int         `json:"n_nonzero"`
	Gain      float64     `json:"gain"`
	CI95      [2]float64  `json:"ci95"`
	PUpper    float64     `json:"p_upper"`
	LoopGains []float64   `json:"loop_gains"`
	Better    int         `json:"better"` // loops that ended better than version 0
	Loops     int         `json:"loops"`
	LoopCI80  *[2]float64 `json:"loop_ci80,omitempty"`
}

// StudyView is a study as served: no held-out task id.
type StudyView struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Agent     string        `json:"agent"`
	Suite     string        `json:"suite"`
	Status    string        `json:"status"`
	Reason    string        `json:"reason"`
	Loops     []string      `json:"loops"`
	LoopsMax  int           `json:"loops_max"`
	HeldOut   int           `json:"held_out"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
	CreatedBy string        `json:"created_by"`
	Final     *StudyVerdict `json:"final,omitempty"`
	CostUSD   float64       `json:"cost_usd"`
	Running   bool          `json:"running"`
}

// StudyList is GET /improvement-studies.
type StudyList struct {
	Studies []StudyView `json:"studies"`
}

// ViewStudy is a study as served.
func (r *Runner) ViewStudy(st *Study) StudyView {
	return StudyView{ID: st.ID, Name: st.Name, Agent: st.Spec.Agent, Suite: st.Spec.Suite, Status: st.Status, Reason: st.Reason,
		Loops: append([]string{}, st.Loops...), LoopsMax: st.Spec.Replicate.Loops, HeldOut: len(st.Sealed), CreatedAt: st.CreatedAt,
		UpdatedAt: st.UpdatedAt, CreatedBy: st.CreatedBy, Final: st.Final, CostUSD: st.CostUSD, Running: r.Running(st.ID)}
}

func (r *Runner) studyDir(id string) string { return filepath.Join(r.Root, StudyDir, id) }

// GetStudy loads a stored study.
func (r *Runner) GetStudy(id string) (*Study, error) {
	if !ValidStudyID(id) {
		return nil, ErrNotFound
	}
	b, err := os.ReadFile(filepath.Join(r.studyDir(id), StudyFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var st Study
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("study %s: %w", id, err)
	}
	return &st, nil
}

// ListStudies returns every stored study, newest first.
func (r *Runner) ListStudies() ([]*Study, error) {
	ents, err := os.ReadDir(filepath.Join(r.Root, StudyDir))
	if errors.Is(err, fs.ErrNotExist) {
		return []*Study{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []*Study{}
	for _, e := range ents {
		if !e.IsDir() || !ValidStudyID(e.Name()) {
			continue
		}
		st, err := r.GetStudy(e.Name())
		if err != nil {
			continue
		}
		out = append(out, st)
	}
	slices.SortFunc(out, func(a, b *Study) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return out, nil
}

func (r *Runner) saveStudy(st *Study) error {
	st.UpdatedAt = r.now()
	if err := os.MkdirAll(r.studyDir(st.ID), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(r.studyDir(st.ID), StudyFile), append(b, '\n'), 0o644)
}

func newStudyID(name string) string {
	slug := name
	if len(slug) > 40 {
		slug = slug[:40]
	}
	return "ims_" + slug + "-" + strings.TrimPrefix(utils.NewID("", 6), "_")
}

// CreateStudy freezes a study spec (replicate.loops 2 to 5) and draws the
// held-out split every loop shares. Nothing runs yet.
func (r *Runner) CreateStudy(ctx context.Context, spec *Spec, actor string) (*Study, error) {
	spec.Defaults()
	if l := spec.Replicate.Loops; l < 2 || l > MaxLoops {
		return nil, fmt.Errorf("a study needs replicate loops 2 to %d", MaxLoops)
	}
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	hash, err := spec.Hash()
	if err != nil {
		return nil, err
	}
	pool, err := r.studyPool(ctx, spec)
	if err != nil {
		return nil, err
	}
	splits, err := Partition(pool, spec)
	if err != nil {
		return nil, err
	}
	now := r.now()
	st := &Study{ID: newStudyID(spec.Name), Name: spec.Name, Spec: *spec, SpecHash: hash, Loops: []string{}, Sealed: splits.Sealed,
		Status: StatusPlanned, Reason: "created; nothing has run yet", CreatedAt: now, CreatedBy: actor}
	return st, r.saveStudy(st)
}

func (r *Runner) studyPool(ctx context.Context, spec *Spec) ([]string, error) {
	infos, err := r.Exec.Tasks(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("list tasks of %s: %w", spec.Suite, err)
	}
	pool := []string{}
	for _, t := range infos {
		if t.Impossible && !slices.Contains(spec.Splits.Honeypots, t.ID) {
			continue
		}
		pool = append(pool, t.ID)
	}
	return pool, nil
}

// loopSpec is loop l's spec: one loop, its own seed when repartitioning.
func loopSpec(st *Study, l int) Spec {
	s := st.Spec
	s.Replicate = ReplicationPlan{Loops: 1}
	s.Name = fmt.Sprintf("%s-loop-%d", st.Name, l+1)
	if st.Spec.Replicate.Repartition {
		s.Seed = st.Spec.Seed + int64(1000*l)
	}
	return s
}

// StopStudy stops a study and its running loop.
func (r *Runner) StopStudy(id, actor string) (*Study, error) {
	r.mu.Lock()
	cancel, running := r.running[id]
	r.mu.Unlock()
	if running {
		cancel()
	}
	st, err := r.GetStudy(id)
	if err != nil {
		return nil, err
	}
	if st.Status == StatusDone || st.Status == StatusStopped {
		return st, resumeRefusal{fmt.Sprintf("This study is %s.", st.Status), ErrState}
	}
	if n := len(st.Loops); n > 0 {
		if rec, err := r.load(st.Loops[n-1]); err == nil && rec.Status != StatusDone && rec.Status != StatusStopped {
			_, _ = r.Stop(rec.ID, actor)
		}
	}
	st.Status, st.Reason = StatusStopped, "stopped by "+actor
	return st, r.saveStudy(st)
}

// RunStudy drives a study: its loops one after another, each a normal run
// on the shared held-out split that skips its own final experiment, then
// the study's final experiment and the pooled test.
func (r *Runner) RunStudy(ctx context.Context, id string) (*Study, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	r.mu.Lock()
	if r.running == nil {
		r.running = map[string]context.CancelFunc{}
	}
	if _, busy := r.running[id]; busy {
		r.mu.Unlock()
		return nil, ErrBusy
	}
	r.running[id] = cancel
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.running, id)
		r.mu.Unlock()
	}()
	st, err := r.GetStudy(id)
	if err != nil {
		return nil, err
	}
	if st.Status == StatusDone || st.Status == StatusStopped {
		return st, resumeRefusal{fmt.Sprintf("This study is %s.", st.Status), ErrState}
	}
	fail := func(err error) (*Study, error) {
		st.Status, st.Reason = StatusFailed, err.Error()
		return st, errors.Join(err, r.saveStudy(st))
	}
	st.Status, st.Reason = StatusRunning, "running"
	if err := r.saveStudy(st); err != nil {
		return st, err
	}
	pool, err := r.studyPool(ctx, &st.Spec)
	if err != nil {
		return fail(err)
	}
	for l := 0; l < st.Spec.Replicate.Loops; l++ {
		if l == len(st.Loops) {
			spec := loopSpec(st, l)
			rec, err := r.Create(ctx, &spec, st.CreatedBy)
			if err != nil {
				return fail(err)
			}
			if rec.Splits, err = PartitionWithSealed(pool, st.Sealed, &spec); err != nil {
				return fail(err)
			}
			prot, err := r.Exec.Protect(ctx, &spec, slices.Concat(rec.Splits.Audit, rec.Splits.Sealed))
			if err != nil {
				return fail(fmt.Errorf("read protected task content: %w", err))
			}
			if err := r.saveProtected(rec.ID, prot); err != nil {
				return fail(err)
			}
			rec.Study, rec.InStudy = st.ID, true
			if err := r.save(rec); err != nil {
				return fail(err)
			}
			st.Loops = append(st.Loops, rec.ID)
			if err := r.saveStudy(st); err != nil {
				return st, err
			}
		}
		rec, err := r.load(st.Loops[l])
		if err != nil {
			return fail(err)
		}
		if rec.Status != StatusDone {
			rec, err = r.Run(ctx, rec.ID)
			if rec == nil {
				return fail(err)
			}
		}
		switch rec.Status {
		case StatusDone:
			continue
		case StatusAwaiting:
			st.Status, st.Reason = StatusAwaiting, fmt.Sprintf("loop %d is paused for approval", l+1)
		case StatusStopped:
			st.Status, st.Reason = StatusStopped, fmt.Sprintf("loop %d was stopped", l+1)
		default:
			st.Status, st.Reason = StatusFailed, fmt.Sprintf("loop %d ended %s: %s", l+1, rec.Status, rec.Reason)
		}
		return st, r.saveStudy(st)
	}
	recs := make([]*Record, len(st.Loops))
	arms := []Version{}
	for i, lid := range st.Loops {
		rec, err := r.load(lid)
		if err != nil {
			return fail(err)
		}
		recs[i] = rec
		if i == 0 {
			arms = append(arms, rec.Base)
		}
		arms = append(arms, rec.Incumbent)
	}
	res, err := r.Exec.Score(ctx, ScoreRequest{ImprovementID: st.ID, Spec: &st.Spec, Phase: PhaseStudyFinal, Arms: arms,
		Tasks: st.Sealed, Trials: max(1, st.Spec.Sealed.Trials)})
	if err != nil {
		return fail(fmt.Errorf("the study's held-out experiment failed: %w", err))
	}
	committed := make([]bool, len(recs))
	for i, rec := range recs {
		committed[i] = rec.Incumbent.Ref != rec.Base.Ref
	}
	st.Final = StudyPooled(res.Runs, committed, st.Sealed)
	for _, rec := range recs {
		st.CostUSD += totalUSD(rec)
	}
	st.CostUSD += res.CostUSD
	st.Status, st.Reason = StatusDone, "finished"
	return st, r.saveStudy(st)
}

// StudyPooled is the pooled test of a study's final experiment: arm 0 is
// version 0 and arm l+1 is loop l's final. A loop with no commit contributes
// version 0 (a difference of 0). Per task d_t is the mean over loops of
// final minus version 0, tested one-sided at StudyAlpha.
func StudyPooled(runs []RunScore, committed []bool, tasks []string) *StudyVerdict {
	v0, _ := TaskMeans(runs, 0, tasks)
	L := len(committed)
	sum := map[string]float64{}
	cnt := map[string]int{}
	gains := make([]float64, L)
	for l, ok := range committed {
		fl := v0
		if ok {
			fl, _ = TaskMeans(runs, l+1, tasks)
		}
		d := TaskDiffs(v0, fl, tasks)
		gains[l] = mean(d)
		for t, b := range fl {
			if a, ok := v0[t]; ok {
				sum[t] += b - a
				cnt[t]++
			}
		}
	}
	b := map[string]float64{}
	for t, a := range v0 {
		if cnt[t] == L && L > 0 {
			b[t] = a + sum[t]/float64(L)
		}
	}
	spec := &Spec{Claim: ClaimSpec{Alpha: StudyAlpha}}
	anyCommit := slices.Contains(committed, true)
	v := signFlipClaim{}.Decide(ClaimInput{Spec: spec, A: v0, B: b, Tasks: tasks, Committed: anyCommit, Level: StudyAlpha})
	sv := &StudyVerdict{State: v.State, N: v.N, NNonzero: v.NNonzero, Gain: v.Gain, CI95: v.CI95, PUpper: v.PUpper, LoopGains: gains, Loops: L}
	for _, g := range gains {
		if g > 0 {
			sv.Better++
		}
	}
	if L >= 3 {
		if lo, hi, ok := stats.PairedT(gains, 0.80); ok {
			sv.LoopCI80 = &[2]float64{lo, hi}
		}
	}
	sv.Headline = fmt.Sprintf("Across %d runs of the loop from version 0: %s points on %d held-out tasks (95%% interval %s to %s, p %s). %d of %d runs ended better than version 0.",
		L, formatPoints(sv.Gain), sv.N, formatPoints(sv.CI95[0]), formatPoints(sv.CI95[1]), formatP(sv.PUpper), sv.Better, L)
	return sv
}
