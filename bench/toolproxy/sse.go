package toolproxy

import (
	"bufio"
	"bytes"
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

// sseReader splits a stream into events.
type sseReader struct {
	rd *bufio.Reader
}

// newSSEReader reads events from r.
func newSSEReader(r io.Reader) *sseReader {
	return &sseReader{rd: bufio.NewReaderSize(r, 64<<10)}
}

// next returns the next event. At the end of the stream it returns what it
// has (possibly a partial event) and the read error.
func (s *sseReader) next() (sseEvent, error) {
	var ev sseEvent
	for {
		line, err := s.rd.ReadBytes('\n')
		if len(line) > 0 {
			if len(bytes.TrimRight(line, "\r\n")) == 0 {
				ev.end = line
				return ev, nil
			}
			ev.lines = append(ev.lines, line)
		}
		if err != nil {
			return ev, err
		}
	}
}
