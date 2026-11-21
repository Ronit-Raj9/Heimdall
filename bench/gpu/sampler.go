package gpu

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// SamplesFile is the run folder file the sampler appends to, one
// schemas.GPUSample per line. The seal covers it like every run file.
const SamplesFile = "gpu.samples.jsonl"

// Sample phases (schemas.GPUSample.Phase).
const (
	PhaseBaseline = "baseline"
	PhaseAgent    = "agent"
)

// sampleQuery is the per-sample nvidia-smi query.
const sampleQuery = "uuid,utilization.gpu,memory.used,memory.total,power.draw,temperature.gpu"

// maxSampleLines bounds the samples file: at the default 500 ms that is
// over 11 hours of one GPU. Past it the numbers keep counting, the lines stop.
const maxSampleLines = 80000

// Sampler is the host-side nvidia-smi loop for one agent session of one
// run. It runs on the host, outside every container, so the agent cannot
// see, stop or forge it; and because a run holds its GPUs alone, every
// sample is attributed to that run exactly.
type Sampler struct {
	a       *schemas.GPUAssignment
	session int
	started time.Time

	cmd  *exec.Cmd
	done chan struct{}

	mu       sync.Mutex
	out      io.WriteCloser
	lines    int
	samples  int
	utilSum  float64
	baseline map[string]float64
	peak     map[string]float64
	errs     []string
}

// StartSampler takes a baseline reading of the assignment's GPUs and starts
// sampling them every a.SampleMS into dir/SamplesFile. It returns nil when
// the sampler is off (no assignment, or SampleMS 0). A sampler that cannot
// start still returns, with the reason in its summary, and never fails the
// run: its numbers are then unknown.
func StartSampler(ctx context.Context, a *schemas.GPUAssignment, dir string, session int) *Sampler {
	if a == nil || a.SampleMS <= 0 || len(a.GPUs) == 0 {
		return nil
	}
	s := &Sampler{a: a, session: session, baseline: map[string]float64{}, peak: map[string]float64{}, done: make(chan struct{})}
	f, err := os.OpenFile(dirFile(dir), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		s.fail("open samples file: " + err.Error())
		close(s.done)
		return s
	}
	s.out = f
	smi := a.SMI
	if smi == "" {
		smi = DefaultSMI
	}
	ids := uuids(a)
	bctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	raw, err := ExecRunner(bctx, smi, "--query-gpu="+sampleQuery, "--format=csv,noheader,nounits", "-i", ids)
	cancel()
	if err != nil {
		s.fail("baseline reading: " + err.Error())
	} else {
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			s.line(line, PhaseBaseline)
		}
	}
	s.started = time.Now()
	cmd := exec.Command(smi, "--query-gpu="+sampleQuery, "--format=csv,noheader,nounits", "-i", ids, "-lms", strconv.Itoa(a.SampleMS))
	stdout, err := cmd.StdoutPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		s.fail("start sampling: " + err.Error())
		close(s.done)
		return s
	}
	s.cmd = cmd
	go func() {
		defer close(s.done)
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			s.line(sc.Text(), PhaseAgent)
		}
	}()
	return s
}

func dirFile(dir string) string { return strings.TrimRight(dir, "/") + "/" + SamplesFile }

func uuids(a *schemas.GPUAssignment) string {
	ids := make([]string, 0, len(a.GPUs))
	for _, g := range a.GPUs {
		ids = append(ids, g.UUID)
	}
	return strings.Join(ids, ",")
}

// line parses one CSV line of sampleQuery and records it.
func (s *Sampler) line(text, phase string) {
	smp, err := ParseSample(text)
	if err != nil {
		if strings.TrimSpace(text) != "" {
			s.fail(err.Error())
		}
		return
	}
	smp.At, smp.Session, smp.Phase = time.Now().UTC(), s.session, phase
	s.mu.Lock()
	defer s.mu.Unlock()
	if phase == PhaseBaseline {
		s.baseline[smp.UUID] = smp.MemUsedMB
	} else {
		s.samples++
		s.utilSum += smp.UtilPct
		s.peak[smp.UUID] = max(s.peak[smp.UUID], smp.MemUsedMB)
	}
	if s.out != nil && s.lines < maxSampleLines {
		if b, err := json.Marshal(smp); err == nil {
			if _, err := s.out.Write(append(b, '\n')); err != nil {
				s.errs = append(s.errs, "write samples: "+err.Error())
				s.out = nil
			}
			s.lines++
		}
	}
}

// ParseSample reads one line of sampleQuery. Fields nvidia-smi cannot read
// on a card ("[N/A]", "[Not Supported]") are nil for power and temperature;
// utilization and memory must be numbers. It is pure.
func ParseSample(line string) (schemas.GPUSample, error) {
	f := splitCSV(strings.TrimSpace(line))
	if len(f) != 6 {
		return schemas.GPUSample{}, fmt.Errorf("nvidia-smi sample %q: %d fields, want 6", line, len(f))
	}
	util, err1 := strconv.ParseFloat(f[1], 64)
	used, err2 := strconv.ParseFloat(f[2], 64)
	total, err3 := strconv.ParseFloat(f[3], 64)
	if err := errors.Join(err1, err2, err3); err != nil {
		return schemas.GPUSample{}, fmt.Errorf("nvidia-smi sample %q: %w", line, err)
	}
	return schemas.GPUSample{UUID: f[0], UtilPct: util, MemUsedMB: used, MemTotal: total, PowerW: optional(f[4]), TempC: optional(f[5])}, nil
}

func optional(s string) *float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &v
}

func (s *Sampler) fail(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.errs) < 8 {
		s.errs = append(s.errs, msg)
	}
}

// Stop ends the session's sampling and returns what it saw. It is safe on a
// nil sampler (returns nil).
func (s *Sampler) Stop() *schemas.GPUSamplerSummary {
	if s == nil {
		return nil
	}
	held := time.Since(s.started)
	if s.started.IsZero() {
		held = 0
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		// The reader finishes before Wait closes its pipe (os/exec: Wait
		// must not run before reads from StdoutPipe are done), so the last
		// samples are not lost.
		<-s.done
		_ = s.cmd.Wait()
	} else {
		<-s.done
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.out != nil {
		if err := s.out.Close(); err != nil {
			s.errs = append(s.errs, "close samples: "+err.Error())
		}
		s.out = nil
	}
	sum := &schemas.GPUSamplerSummary{Samples: s.samples, IntervalMS: s.a.SampleMS, Sessions: 1,
		AgentSeconds: held.Seconds(), GPUSeconds: held.Seconds() * float64(len(s.a.GPUs))}
	for _, g := range s.a.GPUs {
		base, hasBase := s.baseline[g.UUID]
		peak, hasPeak := s.peak[g.UUID]
		if !hasBase || !hasPeak {
			continue
		}
		sum.BaselineMB = max(sum.BaselineMB, base)
		sum.PeakUsedMB = max(sum.PeakUsedMB, peak)
		sum.PeakVRAMMB = max(sum.PeakVRAMMB, max(peak-base, 0))
	}
	if s.samples > 0 {
		sum.MeanUtilPct = s.utilSum / float64(s.samples)
	}
	if len(s.baseline) == 0 {
		s.errs = append(s.errs, "no baseline reading")
	}
	if s.samples == 0 {
		s.errs = append(s.errs, "no sample while the agent ran (a session shorter than the interval, or nvidia-smi failed)")
	}
	sum.Error = strings.Join(s.errs, "; ")
	return sum
}

// Merge adds one session's summary to the run's. A session with an error
// carries it into the run's summary, so the run's numbers are read as
// incomplete. It is pure.
func Merge(run, session *schemas.GPUSamplerSummary) *schemas.GPUSamplerSummary {
	switch {
	case session == nil:
		return run
	case run == nil:
		c := *session
		return &c
	}
	out := *run
	total := out.Samples + session.Samples
	if total > 0 {
		out.MeanUtilPct = (out.MeanUtilPct*float64(out.Samples) + session.MeanUtilPct*float64(session.Samples)) / float64(total)
	}
	out.Samples = total
	out.AgentSeconds += session.AgentSeconds
	out.GPUSeconds += session.GPUSeconds
	out.BaselineMB = max(out.BaselineMB, session.BaselineMB)
	out.PeakUsedMB = max(out.PeakUsedMB, session.PeakUsedMB)
	out.PeakVRAMMB = max(out.PeakVRAMMB, session.PeakVRAMMB)
	out.Sessions += session.Sessions
	if session.Error != "" {
		out.Error = strings.TrimPrefix(out.Error+"; session "+strconv.Itoa(out.Sessions)+": "+session.Error, "; ")
	}
	return &out
}
