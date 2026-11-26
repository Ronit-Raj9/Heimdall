package handlers

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/plaintext"
)

// Served text of jobs, receipts and run steps (wave 4, F1.3): on a hosted
// server every message a browser reads passes through plaintext.Hosted. The
// stored records and files keep their full text.

// plainJob is j with its error cleaned for a browser.
func plainJob(j *jobs.Job) *jobs.Job {
	if j == nil || j.Error == "" || plaintext.DefaultRoots == nil {
		return j
	}
	v := *j
	v.Error = plaintext.Hosted(j.Error)
	return &v
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

// plainTextFields names the JSON fields of receipts and run steps that hold
// display text.
var plainTextFields = map[string]bool{"message": true, "summary": true, "detail": true, "error": true}

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
			t[k] = plainTree(x)
		}
	case []any:
		for i, x := range t {
			t[i] = plainTree(x)
		}
	}
	return v
}
