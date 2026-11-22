package agentinfer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/agentevents"
)

// StoreDir is where an org's incoming traces are kept, under its project
// root.
const StoreDir = ".heimdall/connect/traces"

// storeFile holds the kept spans.
const storeFile = "spans.json"

// MaxKeptSpans caps the spans kept per org; the oldest go first.
const MaxKeptSpans = 4000

// maxAttr caps one kept attribute value.
const maxAttr = 512

// keptAttrs are the span attributes inference reads. Nothing else is kept:
// no prompt, no answer, no tool arguments or results.
var keptAttrs = map[string]bool{
	"gen_ai.operation.name": true, "openinference.span.kind": true,
	"gen_ai.agent.name": true, "gen_ai.agent.id": true, "agent.name": true,
	"gen_ai.request.model": true, "gen_ai.response.model": true, "llm.model_name": true,
	"gen_ai.provider.name": true, "gen_ai.system": true, "llm.provider": true, "llm.system": true,
	"gen_ai.tool.name": true, "tool.name": true, "tool_name": true,
	"gen_ai.conversation.id": true, "session.id": true, "conversation.id": true,
	"gen_ai.framework": true, "agent.framework": true,
	"http.request.method": true, "http.method": true, "http.route": true, "url.path": true, "http.target": true,
	"url.scheme": true, "http.scheme": true, "server.address": true, "server.port": true,
	"http.host": true, "net.host.name": true, "net.host.port": true,
}

// keptResource are the resource attributes kept.
var keptResource = map[string]bool{"service.name": true, "service.version": true, "telemetry.sdk.language": true, "telemetry.sdk.name": true}

// Compact keeps only what inference reads from a span. url.full keeps its
// scheme, host and path (never the query). Declared tool names and the
// presence of system instructions are kept as markers, never their text.
func Compact(sp agentevents.Span) agentevents.Span {
	a := map[string]string{}
	for k, v := range sp.Attributes {
		if keptAttrs[k] {
			a[k] = clip(v)
		}
	}
	for _, k := range []string{"url.full", "http.url"} {
		if v := sp.Attributes[k]; v != "" {
			if i := strings.IndexAny(v, "?#"); i >= 0 {
				v = v[:i]
			}
			a[k] = clip(v)
		}
	}
	for i, n := range declaredTools(sp.Attributes) {
		if i >= 64 {
			break
		}
		a["heimdall.tool_declared."+clip(n)] = "1"
	}
	if sp.Attributes["gen_ai.system_instructions"] != "" || sp.Attributes["heimdall.system_instructions_seen"] != "" {
		a["heimdall.system_instructions_seen"] = "1"
	}
	r := map[string]string{}
	for k, v := range sp.Resource {
		if keptResource[k] {
			r[k] = clip(v)
		}
	}
	sp.Attributes, sp.Resource = a, r
	if len(r) == 0 {
		sp.Resource = nil
	}
	sp.Name = clip(sp.Name)
	return sp
}

func clip(s string) string {
	if len(s) > maxAttr {
		return s[:maxAttr]
	}
	return s
}

// State is what an org's store holds.
type State struct {
	Spans     []agentevents.Span `json:"spans"`
	Exports   int                `json:"exports"` // OTLP requests received
	Dropped   int                `json:"dropped"` // spans dropped by the cap
	FirstSeen time.Time          `json:"first_received,omitzero"`
	LastSeen  time.Time          `json:"last_received,omitzero"`
}

// Store keeps an org's incoming spans in one bounded file.
type Store struct {
	Dir string
	Max int // 0 is MaxKeptSpans
}

var storeLocks sync.Map // dir -> *sync.Mutex

func (s Store) lock() func() {
	m, _ := storeLocks.LoadOrStore(filepath.Clean(s.Dir), &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// NewStore is the store under a project root.
func NewStore(projectRoot string) Store {
	return Store{Dir: filepath.Join(projectRoot, filepath.FromSlash(StoreDir))}
}

// Load reads the store; an empty state when nothing arrived yet.
func (s Store) Load() (State, error) {
	defer s.lock()()
	return s.load()
}

func (s Store) load() (State, error) {
	b, err := os.ReadFile(filepath.Join(s.Dir, storeFile))
	if errors.Is(err, fs.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return State{}, fmt.Errorf("trace store %s: %w", s.Dir, err)
	}
	return st, nil
}

// Add keeps spans (compacted) and returns the new state.
func (s Store) Add(spans []agentevents.Span, at time.Time) (State, error) {
	defer s.lock()()
	st, err := s.load()
	if err != nil {
		return State{}, err
	}
	for _, sp := range spans {
		st.Spans = append(st.Spans, Compact(sp))
	}
	max := s.Max
	if max <= 0 {
		max = MaxKeptSpans
	}
	if over := len(st.Spans) - max; over > 0 {
		st.Spans = append([]agentevents.Span(nil), st.Spans[over:]...)
		st.Dropped += over
	}
	st.Exports++
	at = at.UTC()
	if st.FirstSeen.IsZero() {
		st.FirstSeen = at
	}
	st.LastSeen = at
	if err := s.save(st); err != nil {
		return State{}, err
	}
	return st, nil
}

// Clear forgets every kept span.
func (s Store) Clear() error {
	defer s.lock()()
	err := os.Remove(filepath.Join(s.Dir, storeFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func (s Store) save(st State) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.Dir, storeFile+".*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(s.Dir, storeFile))
}
