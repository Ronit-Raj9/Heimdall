package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/livefeed"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Live screen and human takeover (Z03, schemas/recordings.go). While the
// agent runs, a browser or desktop service with a live block is sampled
// every LiveFrameEvery and its newest frame published to the run's sink:
// the in-process hub for a local run, the coordinator for a leased one. A
// debug run's sink also hands back the inputs a person queued; each is
// performed on the service, written to human_actions.jsonl and marks the
// run human_assisted. Frames are display only and never written.

// LiveSink takes a run's live frames and returns the person's inputs queued
// for it since the last frame.
type LiveSink interface {
	Publish(ctx context.Context, f schemas.LiveFrame) []schemas.HumanAction
	End(runID string)
}

// hubSink is the in-process sink: local runs publish straight to the local
// org's hub, which the run page reads.
type hubSink struct{ h *livefeed.Hub }

func (s hubSink) Publish(_ context.Context, f schemas.LiveFrame) []schemas.HumanAction {
	s.h.Publish(f)
	return s.h.Take(f.RunID)
}

func (s hubSink) End(runID string) { s.h.End(runID) }

// liveEvery is the sampling interval; a variable for tests.
var liveEvery = schemas.LiveFrameEvery

// startLive starts sampling the run's live services and returns the stop
// function, which waits for the sampler, forgets the run in the sink and
// marks the run human_assisted when a person's input was performed.
func (p *Pipeline) startLive(ctx context.Context, st *runState) func() {
	ls, ok := st.env.(schemas.LiveScreen)
	if !ok || st.rc == nil || st.rc.Run == nil {
		return func() {}
	}
	svcs := ls.LiveServices(st.rc)
	if len(svcs) == 0 {
		return func() {}
	}
	sink := st.in.Live
	if sink == nil {
		sink = hubSink{h: livefeed.For("")}
	}
	debug := debugSpec(st.rc) != nil
	svc := svcs[0] // the first live service is the run's screen
	runID := st.rc.Run.RunID
	lctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var actions []schemas.HumanAction
	wg.Add(1)
	go func() {
		defer wg.Done()
		next := time.Duration(0)
		for {
			select {
			case <-lctx.Done():
				return
			case <-time.After(next):
			}
			next = liveEvery
			data, mime, err := ls.LiveFrame(lctx, st.rc, svc.ID)
			if err != nil {
				p.logger.Debug("live frame not read", "run", runID, "service", svc.ID, "error", err.Error())
				continue
			}
			inputs := sink.Publish(lctx, schemas.LiveFrame{RunID: runID, Service: svc.ID, Mime: mime, At: p.now().UTC(), Data: data,
				Debug: debug, TakeoverOK: debug && svc.Takeover})
			if len(inputs) == 0 {
				continue
			}
			if !debug || !svc.Takeover {
				p.logger.Warn("inputs for a run that cannot be taken over were dropped", "run", runID, "inputs", len(inputs))
				continue
			}
			for _, a := range inputs {
				a.Service = svc.ID
				if err := ls.LiveInput(lctx, st.rc, svc.ID, a); err != nil {
					a.Error = err.Error()
				}
				mu.Lock()
				actions = append(actions, a)
				snapshot := append([]schemas.HumanAction(nil), actions...)
				mu.Unlock()
				p.writeHumanActions(lctx, st, snapshot)
			}
			next = 300 * time.Millisecond // show the result of the input soon
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			wg.Wait()
			sink.End(runID)
			mu.Lock()
			defer mu.Unlock()
			if len(actions) > 0 {
				st.rc.Run.HumanAssisted = true
				p.writeHumanActions(context.WithoutCancel(ctx), st, actions)
			}
		})
	}
}

// writeHumanActions rewrites human_actions.jsonl with every input so far.
func (p *Pipeline) writeHumanActions(ctx context.Context, st *runState, actions []schemas.HumanAction) {
	var buf bytes.Buffer
	for _, a := range actions {
		b, err := json.Marshal(a)
		if err != nil {
			continue
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	if err := p.reg.Store.WriteRunFile(ctx, st.rc.Run, schemas.HumanActionsFile, buf.Bytes()); err != nil {
		p.logger.Warn("human actions not written", "run", st.rc.Run.RunID, "error", err.Error())
	}
}

// workerLiveSink posts a leased run's frames to the coordinator and takes
// the queued inputs from its answer. A failed post drops the frame: it is
// display only.
type workerLiveSink struct {
	w       *Worker
	leaseID string
}

func (s workerLiveSink) Publish(ctx context.Context, f schemas.LiveFrame) []schemas.HumanAction {
	var reply schemas.LiveFrameReply
	body := struct {
		WorkerID string `json:"worker_id"`
		schemas.LiveFrame
	}{s.w.opts.WorkerID, f}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.w.call(cctx, http.MethodPost, workerAPI+"/leases/"+url.PathEscape(s.leaseID)+"/live-frame", body, &reply); err != nil {
		s.w.logger.Debug("live frame not delivered", "lease", s.leaseID, "error", err.Error())
		return nil
	}
	return reply.Inputs
}

func (s workerLiveSink) End(string) {}
