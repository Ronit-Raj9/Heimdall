package traceexport

import (
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// commandNameCap is the most bytes of a program name kept in a command
// span's name.
const commandNameCap = 64

// stepSpans adds the process and file activity of the step timeline
// (Evidence.Steps rows of kind command and file_change) as child spans of
// the model turn each row follows (StepRecord.Turn, the logical step whose
// response came before it). A command runs from its start for its recorded
// duration, or is a zero-length span when the timeline has none. A file
// change is a zero-length span at the time the workspace observer saw it: it
// happens after the turn's response, outside the turn span's own window, so
// it is a child span rather than an event of that span. A row before the
// first response (turn 0), or one whose turn has no model span, goes under
// its session span, else the run. A run without a timeline adds nothing.
func (b *builder) stepSpans() {
	if len(b.in.Evidence.Steps) == 0 {
		return
	}
	turns := map[int]string{}
	for _, m := range b.models {
		if _, dup := turns[m.step]; m.step > 0 && !dup {
			turns[m.step] = m.spanID
		}
	}
	keys := map[string]bool{}
	for i, r := range b.in.Evidence.Steps {
		if r.TS.IsZero() || (r.Kind != schemas.StepCommand && r.Kind != schemas.StepFileChange) {
			continue
		}
		parent := turns[r.Turn]
		if r.Turn <= 0 || parent == "" {
			parent = b.sessionParent(r.TS, r.Session)
		}
		key := fmt.Sprintf("step:%d", r.Seq)
		if keys[key] {
			key = fmt.Sprintf("step:%d:%d", r.Seq, i)
		}
		keys[key] = true
		a := stepAttrs(r)
		w := window{start: r.TS, end: r.TS}
		if r.Kind == schemas.StepFileChange {
			a.str("heimdall.file.op", r.Op)
			a.str("file.path", clean(r.Path, textCap))
			b.add(key, parent, strings.TrimSpace("file_change "+r.Op), SpanKindInternal, w, a)
			continue
		}
		if r.DurationMS != nil && *r.DurationMS > 0 {
			w.end = r.TS.Add(time.Duration(*r.DurationMS) * time.Millisecond)
		}
		if r.PID != 0 {
			a.int("process.pid", int64(r.PID))
		}
		if r.PPID != 0 {
			a.int("process.parent_pid", int64(r.PPID))
		}
		if r.ExitCode != nil {
			a.int("process.exit.code", int64(*r.ExitCode))
		}
		idx := b.add(key, parent, commandName(r.Summary), SpanKindInternal, w, a)
		if r.ExitCode != nil && *r.ExitCode != 0 {
			b.spans[idx].Status = &Status{Code: StatusError, Message: fmt.Sprintf("exit code %d", *r.ExitCode)}
		}
	}
}

// stepAttrs are the attributes every timeline span shares. The summary is
// already redacted by the timeline; it is redacted and capped again here.
func stepAttrs(r schemas.StepRecord) attrs {
	a := attrs{}
	a.str("heimdall.step.kind", r.Kind)
	a.int("heimdall.step.seq", int64(r.Seq))
	a.int("heimdall.step.turn", int64(r.Turn))
	if r.Session > 0 {
		a.int("heimdall.session.index", int64(r.Session))
	}
	a.str("heimdall.step.source", r.Source)
	a.str("heimdall.step.proof", string(r.Proof))
	a.str("heimdall.step.summary", clean(r.Summary, textCap))
	if r.Counted {
		a.bool("heimdall.step.counted", true)
	}
	if r.WitnessOf > 0 {
		a.int("heimdall.step.witness_of", int64(r.WitnessOf))
	}
	a.str("heimdall.step.conflict", clean(r.Conflict, textCap))
	return a
}

// sessionParent returns the span a timeline row outside any model turn
// belongs under: the span of its 1-based session index when that session has
// one, else the session whose window holds t, else the run.
func (b *builder) sessionParent(t time.Time, session int) string {
	if session > 0 {
		id := SpanID(b.in.Run.RunID, fmt.Sprintf("session:%d", session))
		for _, s := range b.sessions {
			if s.spanID == id {
				return id
			}
		}
	}
	return b.parentAt(t, "")
}

// commandName is a command span's name: "command" and the program the row's
// summary starts with, without its directory.
func commandName(summary string) string {
	f := strings.Fields(summary)
	if len(f) == 0 {
		return "command"
	}
	return "command " + capText(path.Base(f[0]), commandNameCap)
}
