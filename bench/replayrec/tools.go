package replayrec

import (
	"bytes"
	"encoding/json"
	"errors"
)

// Tape answers tool calls from a recording. Strict: a call is answered only
// by a recorded result of the same tool name with byte-equal canonical
// arguments that was not used yet; anything else is a miss. Never a nearest
// answer, never a live call.
type Tape struct {
	left map[string][]ToolResult // name + "\x00" + args hash -> unused results, in order
}

// NewTape indexes the tool results of a recording.
func NewTape(rec *Recording) *Tape {
	t := &Tape{left: map[string][]ToolResult{}}
	if rec == nil {
		return t
	}
	for _, r := range rec.Tools {
		k := r.Name + "\x00" + r.ArgsHash
		t.left[k] = append(t.left[k], r)
	}
	return t
}

// Answer returns the next unused recorded result of this exact call. The
// arguments must already be canonical (Canonical); arguments that are not
// are a miss.
func (t *Tape) Answer(name, canonicalArgs string) (res ToolResult, ok bool) {
	if c, err := Canonical(canonicalArgs); err != nil || c != canonicalArgs {
		return ToolResult{}, false
	}
	k := name + "\x00" + argsHash(canonicalArgs)
	q := t.left[k]
	if len(q) == 0 {
		return ToolResult{}, false
	}
	res, t.left[k] = q[0], q[1:]
	return res, true
}

// Canonical re-encodes JSON arguments with sorted keys and no spaces;
// numbers keep their text. Invalid JSON is an error.
func Canonical(args string) (string, error) {
	dec := json.NewDecoder(bytes.NewReader([]byte(args)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "", err
	}
	if dec.More() {
		return "", errors.New("trailing data after the JSON value")
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return string(bytes.TrimRight(buf.Bytes(), "\n")), nil
}
