package toolproxy

import (
	"bufio"
	"bytes"
	"errors"
	"io"
)

// sseEvent is one server-sent event as it arrived: its field lines with
// their line endings, and the blank line that ended it (nil when the stream
// ended mid-event). An event that is not rewritten is relayed byte for byte.
type sseEvent struct {
	lines [][]byte
	end   []byte
}

// bytes returns the event as it is relayed.
func (e *sseEvent) bytes() []byte {
	var b bytes.Buffer
	for _, l := range e.lines {
		b.Write(l)
	}
	b.Write(e.end)
	return b.Bytes()
}

// data joins the event's data lines with newlines, as an SSE client does.
// ok is false when the event has no data line.
func (e *sseEvent) data() ([]byte, bool) {
	var parts [][]byte
	for _, l := range e.lines {
		if v, ok := dataValue(l); ok {
			parts = append(parts, v)
		}
	}
	if len(parts) == 0 {
		return nil, false
	}
	return bytes.Join(parts, []byte("\n")), true
}

// setData replaces the event's data lines with one line holding d, at the
// place of the first, keeping every other field (event, id, retry) as it
// was. d must hold no newline; compact JSON never does.
func (e *sseEvent) setData(d []byte) {
	var out [][]byte
	placed := false
	for _, l := range e.lines {
		if _, ok := dataValue(l); !ok {
			out = append(out, l)
			continue
		}
		if !placed {
			out = append(out, append(append([]byte("data: "), d...), '\n'))
			placed = true
		}
	}
	e.lines = out
	if e.end == nil {
		e.end = []byte("\n")
	}
}

// dataValue returns the value of a data line: the text after "data:" with
// one leading space removed and the line ending trimmed.
func dataValue(line []byte) ([]byte, bool) {
	l := bytes.TrimRight(line, "\r\n")
	if bytes.Equal(l, []byte("data")) {
		return nil, true
	}
	v, ok := bytes.CutPrefix(l, []byte("data:"))
	if !ok {
		return nil, false
	}
	return bytes.TrimPrefix(v, []byte(" ")), true
}

// sseReader splits a stream into events, keeping at most limit bytes of
// one event in memory.
type sseReader struct {
	rd      *bufio.Reader
	limit   int
	midLine bool // the last event returned over the limit ended inside a line
}

// newSSEReader reads events from r, at most limit bytes of one event.
func newSSEReader(r io.Reader, limit int) *sseReader {
	return &sseReader{rd: bufio.NewReaderSize(r, 64<<10), limit: limit}
}

// next returns the next event. At the end of the stream it returns what it
// has (possibly a partial event) and the read error. An event that grows
// over the limit comes back with big set as soon as it does, holding the
// bytes read so far (its last line may be cut); the caller relays them and
// then streams the rest of the event with rest.
func (s *sseReader) next() (ev sseEvent, big bool, err error) {
	size := 0
	var cur []byte
	for {
		frag, err := s.rd.ReadSlice('\n')
		cur = append(cur, frag...)
		size += len(frag)
		if errors.Is(err, bufio.ErrBufferFull) {
			if size > s.limit {
				ev.lines, s.midLine = append(ev.lines, cur), true
				return ev, true, nil
			}
			continue
		}
		if len(cur) > 0 {
			if err == nil && len(bytes.TrimRight(cur, "\r\n")) == 0 {
				ev.end = cur
				return ev, false, nil
			}
			ev.lines = append(ev.lines, cur)
			cur = nil
			if size > s.limit && err == nil {
				s.midLine = false
				return ev, true, nil
			}
		}
		if err != nil {
			return ev, false, err
		}
	}
}

// rest streams the remainder of an event next returned with big set, up to
// and including its blank line, to emit. It returns the read error that
// ended the stream, or nil at the end of the event.
func (s *sseReader) rest(emit func([]byte)) error {
	atStart := !s.midLine
	for {
		frag, err := s.rd.ReadSlice('\n')
		if len(frag) > 0 {
			blank := atStart && err == nil && len(bytes.TrimRight(frag, "\r\n")) == 0
			emit(frag)
			if blank {
				return nil
			}
		}
		switch {
		case err == nil:
			atStart = true
		case errors.Is(err, bufio.ErrBufferFull):
			atStart = false
		default:
			return err
		}
	}
}
