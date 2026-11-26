package handlers

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/plaintext"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Served text of jobs, receipts and run steps (wave 4, F1.3): on a hosted
// server every message a browser reads passes through plaintext.Hosted. The
// stored records and files keep their full text.

// plainJob is j with its error, and the display text inside its progress
// and result (the last error and last live event a comparison page shows),
// cleaned for a browser.
func plainJob(j *jobs.Job) *jobs.Job {
	if j == nil || plaintext.DefaultRoots == nil {
		return j
	}
	v := *j
	v.Error = plaintext.Hosted(j.Error)
	v.Progress = plainRaw(j.Progress)
	v.Result = plainRaw(j.Result)
	return &v
}

// plainRaw is raw with its display text fields cleaned (plainTree); raw
// itself when it is empty or does not decode.
func plainRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var tree any
	if err := dec.Decode(&tree); err != nil {
		return raw
	}
	out, err := json.Marshal(plainTree(tree))
	if err != nil {
		return raw
	}
	return out
}

// plainJobs is list with every job's error cleaned for a browser.
func plainJobs(list []jobs.Job) []jobs.Job {
	if plaintext.DefaultRoots == nil {
		return list
	}
	out := make([]jobs.Job, len(list))
	for i := range list {
		out[i] = *plainJob(&list[i])
	}
	return out
}

// plainLogPage is p with every log line cleaned for a browser.
func plainLogPage(p *jobs.LogPage) *jobs.LogPage {
	if p == nil || plaintext.DefaultRoots == nil {
		return p
	}
	v := *p
	v.Lines = make([]jobs.LogLine, len(p.Lines))
	for i, l := range p.Lines {
		l.Line = plaintext.Hosted(l.Line)
		v.Lines[i] = l
	}
	return &v
}

// plainLogsAPI is a JobsAPI whose log pages are cleaned for a browser (the
// job log event stream).
type plainLogsAPI struct{ JobsAPI }

func (a plainLogsAPI) Logs(ctx context.Context, orgID, id string, since int64, limit int) (*jobs.LogPage, error) {
	p, err := a.JobsAPI.Logs(ctx, orgID, id, since, limit)
	return plainLogPage(p), err
}

// plainTextFields names the JSON fields of served answers that hold display
// text from the engine, the planner or a worker: a string, or a list of
// strings (notes, errors).
var plainTextFields = map[string]bool{"message": true, "summary": true, "detail": true, "error": true, "last_error": true,
	"skipped": true, "problem": true, "notes": true, "reason": true, "basis": true, "errors": true,
	// The routes added later in wave 4: why an investigation or a replay
	// stopped, why an improvement idea was dropped or a tripwire fired, a
	// result's invalid reasons, why a weekly issue sync paused, and a
	// headline that repeats one of them.
	"stopped": true, "why": true, "dropped": true, "invalid_reasons": true, "paused_reason": true, "headline": true}

// plainEvent is ev with its message cleaned for a browser (live events of
// the experiment event poll and stream).
func plainEvent(ev schemas.LiveEvent) schemas.LiveEvent {
	ev.Message = plaintext.Hosted(ev.Message)
	return ev
}

// plainEvents is events with every message cleaned for a browser.
func plainEvents(events []schemas.LiveEvent) []schemas.LiveEvent {
	if plaintext.DefaultRoots == nil {
		return events
	}
	out := make([]schemas.LiveEvent, len(events))
	for i, ev := range events {
		out[i] = plainEvent(ev)
	}
	return out
}

// plainServed returns v with every message, summary, detail and error
// string cleaned for a browser, as a JSON tree; v itself when nothing is
// cleaned or v does not encode.
func plainServed(v any) any {
	if plaintext.DefaultRoots == nil {
		return v
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return v
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var tree any
	if err := dec.Decode(&tree); err != nil {
		return v
	}
	return plainTree(tree)
}

func plainTree(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			if s, ok := x.(string); ok && plainTextFields[k] {
				t[k] = plaintext.Hosted(s)
				continue
			}
			if list, ok := x.([]any); ok && plainTextFields[k] {
				for i, y := range list {
					if s, ok := y.(string); ok {
						list[i] = plaintext.Hosted(s)
					} else {
						list[i] = plainTree(y)
					}
				}
				continue
			}
			t[k] = plainTree(x)
		}
	case []any:
		for i, x := range t {
			t[i] = plainTree(x)
		}
	}
	return v
}
