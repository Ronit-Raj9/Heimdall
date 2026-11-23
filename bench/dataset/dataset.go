// Package dataset is the data side of a benchmark managed as code (lane
// Y15): task rows with chat inputs, metadata and attachments sent over the
// public API, the personal data policy applied to them, and a version log
// of every change. Each row becomes an answer task folder through the same
// writer the web rows import uses (scaffold.ImportRows); the row as stored
// (after masking or redaction) is kept content addressed, so every version
// can be listed and compared.
//
// Storage, under the org project folder:
//
//	.heimdall/datasets/<benchmark>/versions.jsonl  one Version per line
//	.heimdall/datasets/<benchmark>/v/<n>.json      task id -> row sha256 at version n
//	.heimdall/datasets/<benchmark>/rows/<sha>.json the row
package dataset

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/maximhq/bifrost/bench/datapolicy"
	"github.com/maximhq/bifrost/bench/pii"
	"github.com/maximhq/bifrost/bench/scaffold"
	"gopkg.in/yaml.v3"
)

// Limits of one row.
const (
	MaxTasksPerRequest = 500
	MaxMetadataKeys    = 50
	MaxMetadataValue   = 2000
	MaxAttachments     = 10
	MaxAttachmentBytes = 2 << 20
	MaxMessages        = 100
)

// ErrInvalid marks a refused row.
var ErrInvalid = errors.New("invalid dataset row")

// Strings is a list that also accepts a single JSON string.
type Strings []string

// UnmarshalJSON reads a string or a list of strings.
func (s *Strings) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '"' {
		var one string
		if err := json.Unmarshal(b, &one); err != nil {
			return err
		}
		if strings.TrimSpace(one) == "" {
			*s = nil
		} else {
			*s = Strings{one}
		}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return errors.New("want a string or a list of strings")
	}
	*s = many
	return nil
}

// Message is one chat message of a row's input.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Attachment is one file put in the task's workspace (repo/<name>): Text
// for a text file, ContentBase64 for any other.
type Attachment struct {
	Name          string `json:"name"`
	Text          string `json:"text,omitempty"`
	ContentBase64 string `json:"content_base64,omitempty"`
}

// Row is one task as code sends it.
type Row struct {
	// ID is the task id. A row with the id of a task made from rows
	// replaces it (a new version); without an id one is made from the title
	// or input.
	ID    string `json:"id,omitempty"`
	Title string `json:"title,omitempty"`
	// Input is the question or customer message; Messages is a chat input
	// instead (the conversation so far; the agent answers the last user
	// message).
	Input    string    `json:"input,omitempty"`
	Messages []Message `json:"messages,omitempty"`
	// Expected are accepted answers (answer_match) or the reference
	// answer (judge). Rubric is what the judge checks.
	Expected     Strings `json:"expected,omitempty"`
	Rubric       string  `json:"rubric,omitempty"`
	Reference    string  `json:"reference,omitempty"`
	WrongAnswers Strings `json:"wrong_answers,omitempty"`
	Match        string  `json:"match,omitempty"`
	// Ticket writes the input as a customer support ticket to answer.
	Ticket  bool `json:"ticket,omitempty"`
	HeldOut bool `json:"held_out,omitempty"`
	// Metadata are flat key and value pairs (string, number or boolean)
	// kept with the task, for example tier, language or intent.
	Metadata    map[string]any `json:"metadata,omitempty"`
	Attachments []Attachment   `json:"attachments,omitempty"`
}

// Problem is a row that could not become a task.
type Problem struct {
	Index   int    `json:"index"` // 1-based place in the request
	ID      string `json:"id,omitempty"`
	Message string `json:"message"`
}

// Finding says what the personal data scan saw in one row.
type Finding struct {
	Index  int            `json:"index"`
	ID     string         `json:"id,omitempty"`
	Fields []string       `json:"fields"`
	Kinds  map[string]int `json:"kinds"`
}

// PIIReport is what the scan saw over a request.
type PIIReport struct {
	Mode     string         `json:"mode"`
	Kinds    map[string]int `json:"kinds"`
	Rows     int            `json:"rows"` // rows with at least one finding
	Findings []Finding      `json:"findings"`
	Summary  string         `json:"summary"`
	// Unscanned names binary attachments the scan could not read.
	Unscanned []string `json:"unscanned,omitempty"`
}

// NewPIIReport is an empty report for mode.
func NewPIIReport(mode pii.Mode) *PIIReport {
	return &PIIReport{Mode: string(mode), Kinds: map[string]int{}, Findings: []Finding{}}
}

// Finish fills the summary.
func (r *PIIReport) Finish() {
	s := pii.Summary{}
	for k, n := range r.Kinds {
		s[pii.Kind(k)] = n
	}
	switch {
	case s.Total() == 0:
		r.Summary = "No personal data found."
	case r.Mode == string(pii.ModeMask):
		r.Summary = "Masked " + s.String() + "."
	case r.Mode == string(pii.ModeRedact):
		r.Summary = "Redacted " + s.String() + "."
	case r.Mode == string(pii.ModeBlock):
		r.Summary = "Refused rows holding " + s.String() + "."
	default:
		r.Summary = "Found " + s.String() + "; kept as is (the benchmark's policy is warn)."
	}
}

// Clean checks a row, applies the redactor to every text field and
// records findings. It returns the cleaned row, or a problem message (a
// refused row under block, or an invalid one).
func Clean(r Row, index int, red pii.Redactor, rep *PIIReport) (Row, string) {
	if msg := check(&r); msg != "" {
		return r, msg
	}
	found := pii.Summary{}
	var fields []string
	apply := func(name string, s *string) {
		res := red.Apply(*s)
		if len(res.Matches) > 0 {
			found.Add(res.Matches)
			fields = append(fields, name)
		}
		*s = res.Text
	}
	apply("title", &r.Title)
	apply("input", &r.Input)
	for i := range r.Messages {
		apply(fmt.Sprintf("messages[%d]", i), &r.Messages[i].Content)
	}
	for i := range r.Expected {
		apply(fmt.Sprintf("expected[%d]", i), &r.Expected[i])
	}
	apply("rubric", &r.Rubric)
	apply("reference", &r.Reference)
	for i := range r.WrongAnswers {
		apply(fmt.Sprintf("wrong_answers[%d]", i), &r.WrongAnswers[i])
	}
	keys := make([]string, 0, len(r.Metadata))
	for k := range r.Metadata {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if s, ok := r.Metadata[k].(string); ok {
			apply("metadata."+k, &s)
			r.Metadata[k] = s
		}
	}
	for i := range r.Attachments {
		a := &r.Attachments[i]
		if a.ContentBase64 != "" {
			if rep != nil && red.Policy.Effective() != pii.ModeOff {
				rep.Unscanned = append(rep.Unscanned, fmt.Sprintf("row %d: %s", index, a.Name))
			}
			continue
		}
		apply("attachments."+a.Name, &a.Text)
	}
	if found.Total() > 0 && rep != nil {
		f := Finding{Index: index, ID: r.ID, Fields: fields, Kinds: map[string]int{}}
		for k, n := range found {
			f.Kinds[string(k)] = n
			rep.Kinds[string(k)] += n
		}
		rep.Rows++
		rep.Findings = append(rep.Findings, f)
	}
	if found.Total() > 0 && red.Policy.Effective() == pii.ModeBlock {
		return r, "refused by the benchmark's personal data policy (block): " + found.String() + " in " + strings.Join(fields, ", ")
	}
	return r, ""
}

func check(r *Row) string {
	r.ID = strings.TrimSpace(r.ID)
	r.Title = strings.TrimSpace(r.Title)
	switch {
	case strings.TrimSpace(r.Input) == "" && len(r.Messages) == 0:
		return "input or messages is required"
	case r.Input != "" && len(r.Messages) > 0:
		return "send input or messages, not both"
	case len(r.Messages) > MaxMessages:
		return fmt.Sprintf("at most %d messages", MaxMessages)
	case len(r.Metadata) > MaxMetadataKeys:
		return fmt.Sprintf("at most %d metadata keys", MaxMetadataKeys)
	case len(r.Attachments) > MaxAttachments:
		return fmt.Sprintf("at most %d attachments", MaxAttachments)
	}
	lastUser := false
	for i, m := range r.Messages {
		switch m.Role {
		case "user", "assistant", "system", "tool":
		default:
			return fmt.Sprintf("messages[%d].role %q: want user, assistant, system or tool", i, m.Role)
		}
		if !utf8.ValidString(m.Content) {
			return fmt.Sprintf("messages[%d].content is not UTF-8 text", i)
		}
		lastUser = m.Role == "user"
	}
	if len(r.Messages) > 0 && !lastUser {
		return "the last message must be the user's: the agent answers it"
	}
	for k, v := range r.Metadata {
		if k == "" || len(k) > 64 || strings.ContainsAny(k, " \t\r\n.") {
			return fmt.Sprintf("metadata key %q: one word of at most 64 characters, no dots", k)
		}
		switch x := v.(type) {
		case string:
			if utf8.RuneCountInString(x) > MaxMetadataValue {
				return fmt.Sprintf("metadata %s: at most %d characters", k, MaxMetadataValue)
			}
		case float64, bool, json.Number, int:
		case nil:
			delete(r.Metadata, k)
		default:
			return fmt.Sprintf("metadata %s: a string, number or boolean (no nested objects or lists)", k)
		}
	}
	seen := map[string]bool{}
	for i := range r.Attachments {
		a := &r.Attachments[i]
		a.Name = strings.TrimSpace(a.Name)
		if a.Name == "" || a.Name != path.Base(a.Name) || strings.HasPrefix(a.Name, ".") || strings.ContainsAny(a.Name, "\\:\x00\r\n") || len(a.Name) > 120 {
			return fmt.Sprintf("attachments[%d].name %q: a plain file name (no folders, not hidden)", i, a.Name)
		}
		if strings.EqualFold(a.Name, "README.md") || seen[strings.ToLower(a.Name)] {
			return fmt.Sprintf("attachments[%d].name %q is taken", i, a.Name)
		}
		seen[strings.ToLower(a.Name)] = true
		if (a.Text == "") == (a.ContentBase64 == "") {
			return fmt.Sprintf("attachments[%d]: send text or content_base64, one of them", i)
		}
		if a.ContentBase64 != "" {
			b, err := base64.StdEncoding.DecodeString(a.ContentBase64)
			if err != nil {
				return fmt.Sprintf("attachments[%d].content_base64 is not base64", i)
			}
			if len(b) > MaxAttachmentBytes {
				return fmt.Sprintf("attachments[%d] is over %d MiB", i, MaxAttachmentBytes>>20)
			}
		} else if len(a.Text) > MaxAttachmentBytes || !utf8.ValidString(a.Text) {
			return fmt.Sprintf("attachments[%d].text: UTF-8 of at most %d MiB", i, MaxAttachmentBytes>>20)
		}
	}
	return ""
}

// Question is the task text of a row: Input, or the conversation with the
// last user message to answer.
func Question(r Row) string {
	if len(r.Messages) == 0 {
		return r.Input
	}
	if len(r.Messages) == 1 {
		return r.Messages[0].Content
	}
	var b strings.Builder
	b.WriteString("The conversation so far:\n\n")
	for _, m := range r.Messages[:len(r.Messages)-1] {
		b.WriteString("**" + m.Role + ":** " + strings.TrimSpace(m.Content) + "\n\n")
	}
	b.WriteString("Answer the user's last message:\n\n" + strings.TrimSpace(r.Messages[len(r.Messages)-1].Content))
	return b.String()
}

// ScaffoldRow is the rows-import row of r (line is its 1-based place).
func ScaffoldRow(r Row, line int) scaffold.Row {
	sr := scaffold.Row{Line: line, ID: r.ID, Title: r.Title, Question: Question(r), Expected: []string(r.Expected),
		Rubric: r.Rubric, Reference: r.Reference, Wrong: []string(r.WrongAnswers), Match: r.Match, HeldOut: r.HeldOut, Ticket: r.Ticket}
	if c, ok := r.Metadata["category"].(string); ok {
		sr.Category = c
	}
	return sr
}

// ExtraFiles are the files a row adds to its task folder beyond what
// scaffold writes: metadata.json (never copied into the workspace),
// messages.json for a chat input, and each attachment under repo/.
func ExtraFiles(r Row) ([]scaffold.File, error) {
	var out []scaffold.File
	if len(r.Metadata) > 0 {
		b, err := json.MarshalIndent(r.Metadata, "", "  ")
		if err != nil {
			return nil, err
		}
		out = append(out, scaffold.File{Path: "metadata.json", Data: append(b, '\n'), Mode: 0o644})
	}
	if len(r.Messages) > 0 {
		b, err := json.MarshalIndent(r.Messages, "", "  ")
		if err != nil {
			return nil, err
		}
		out = append(out, scaffold.File{Path: "messages.json", Data: append(b, '\n'), Mode: 0o644})
	}
	for _, a := range r.Attachments {
		data := []byte(a.Text)
		if a.ContentBase64 != "" {
			b, err := base64.StdEncoding.DecodeString(a.ContentBase64)
			if err != nil {
				return nil, err
			}
			data = b
		}
		out = append(out, scaffold.File{Path: "repo/" + a.Name, Data: data, Mode: 0o644})
	}
	return out, nil
}

// RowSHA is the content address of a stored row.
func RowSHA(r Row) (string, []byte, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return "", nil, err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), b, nil
}

// Version is one change to a benchmark's rows.
type Version struct {
	N       int       `json:"version"`
	At      time.Time `json:"at"`
	Actor   string    `json:"actor,omitempty"`
	Source  string    `json:"source,omitempty"`
	Added   []string  `json:"added"`
	Updated []string  `json:"updated"`
	Removed []string  `json:"removed"`
	Tasks   int       `json:"tasks"`
	// SHA256 is the hash of the sorted task to row list: two versions with
	// the same hash hold the same rows.
	SHA256 string `json:"sha256"`
}

// Snapshot is the rows of one version.
type Snapshot struct {
	Version int               `json:"version"`
	Tasks   map[string]string `json:"tasks"` // task id -> row sha256
}

// Store is the version log of one project root.
type Store struct{ dir string }

// Open returns the store of a project root.
func Open(root string) *Store { return &Store{dir: filepath.Join(root, ".heimdall", "datasets")} }

var locks sync.Map

// Lock serializes writes to one benchmark's rows; callers hold it across
// writing the task folders and recording the version.
func (s *Store) Lock(benchmark string) func() {
	m, _ := locks.LoadOrStore(filepath.Join(s.dir, benchmark), &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func (s *Store) bdir(b string) string { return filepath.Join(s.dir, b) }

// Versions reads the log, oldest first.
func (s *Store) Versions(benchmark string) ([]Version, error) {
	f, err := os.Open(filepath.Join(s.bdir(benchmark), "versions.jsonl"))
	if errors.Is(err, fs.ErrNotExist) {
		return []Version{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := []Version{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for sc.Scan() {
		var v Version
		if json.Unmarshal(sc.Bytes(), &v) == nil && v.N > 0 {
			out = append(out, v)
		}
	}
	return out, sc.Err()
}

// Snapshot reads the rows of version n; n 0 is the current version (an
// empty snapshot when there is none).
func (s *Store) Snapshot(benchmark string, n int) (*Snapshot, error) {
	if n == 0 {
		vs, err := s.Versions(benchmark)
		if err != nil {
			return nil, err
		}
		if len(vs) == 0 {
			return &Snapshot{Tasks: map[string]string{}}, nil
		}
		n = vs[len(vs)-1].N
	}
	b, err := os.ReadFile(filepath.Join(s.bdir(benchmark), "v", fmt.Sprintf("%d.json", n)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("version %d: %w", n, fs.ErrNotExist)
	}
	if err != nil {
		return nil, err
	}
	var snap Snapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		return nil, err
	}
	if snap.Tasks == nil {
		snap.Tasks = map[string]string{}
	}
	return &snap, nil
}

// Row reads a stored row.
func (s *Store) Row(benchmark, sha string) (*Row, error) {
	if len(sha) != 64 || strings.Trim(sha, "0123456789abcdef") != "" {
		return nil, fmt.Errorf("row %q: %w", sha, fs.ErrNotExist)
	}
	b, err := os.ReadFile(filepath.Join(s.bdir(benchmark), "rows", sha+".json"))
	if err != nil {
		return nil, err
	}
	var r Row
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// PutRow stores a row by its hash.
func (s *Store) PutRow(benchmark, sha string, data []byte) error {
	dir := filepath.Join(s.bdir(benchmark), "rows")
	p := filepath.Join(dir, sha+".json")
	if _, err := os.Stat(p); err == nil {
		return nil
	}
	return datapolicy.WriteAtomic(dir, p, data, 0o600)
}

// Record appends a version with the given changes over the current
// snapshot. rows maps changed task ids to their new row sha; removed tasks
// leave the snapshot. Nothing is recorded when nothing changed.
func (s *Store) Record(benchmark string, rows map[string]string, added, updated, removed []string, actor, source string, now time.Time) (*Version, error) {
	if len(added)+len(updated)+len(removed) == 0 {
		return nil, nil
	}
	cur, err := s.Snapshot(benchmark, 0)
	if err != nil {
		return nil, err
	}
	vs, err := s.Versions(benchmark)
	if err != nil {
		return nil, err
	}
	next := Snapshot{Version: 1, Tasks: map[string]string{}}
	if len(vs) > 0 {
		next.Version = vs[len(vs)-1].N + 1
	}
	for k, v := range cur.Tasks {
		next.Tasks[k] = v
	}
	for k, v := range rows {
		next.Tasks[k] = v
	}
	for _, k := range removed {
		delete(next.Tasks, k)
	}
	ids := make([]string, 0, len(next.Tasks))
	for k := range next.Tasks {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	h := sha256.New()
	for _, k := range ids {
		fmt.Fprintf(h, "%s %s\n", k, next.Tasks[k])
	}
	v := Version{N: next.Version, At: now.UTC().Truncate(time.Second), Actor: actor, Source: source,
		Added: nonNil(added), Updated: nonNil(updated), Removed: nonNil(removed), Tasks: len(ids), SHA256: hex.EncodeToString(h.Sum(nil))}
	snapB, err := json.Marshal(next)
	if err != nil {
		return nil, err
	}
	vdir := filepath.Join(s.bdir(benchmark), "v")
	if err := datapolicy.WriteAtomic(vdir, filepath.Join(vdir, fmt.Sprintf("%d.json", next.Version)), snapB, 0o600); err != nil {
		return nil, err
	}
	line, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.bdir(benchmark), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(s.bdir(benchmark), "versions.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return nil, err
	}
	return &v, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	sort.Strings(s)
	return s
}

// syncStateFile holds, per task, the row and folder hash of the last
// SyncSuite, so a task the dataset API recorded (a full row without a folder
// hash) is compared by its folder from then on. An entry counts only while
// the snapshot still holds the same row; a later dataset API write starts a
// new baseline.
const syncStateFile = "folders.json"

type syncSeen struct {
	Row   string `json:"row"`
	Files string `json:"files"`
}

// SyncSuite records the benchmark's current tasks as a new version when they
// differ from the latest one: every path that writes task folders or
// suite.yaml outside the dataset API calls it once after its write (rows
// import, scenario rows, policy and review accept, imports, the file editor
// and others), so the Data tab lists each change with source. The tasks are
// the ids suite.yaml lists (the folders under tasks/ when it lists none);
// each becomes one row with its title, instruction (clipped to 4 KiB), held
// out flag and the content hash of its folder. A task whose folder hash did
// not change keeps its stored row. Nothing is written when nothing changed;
// the first call on a benchmark with no version records every task as added.
func SyncSuite(root, suiteID, suiteDir, actor, source string, now time.Time) (*Version, error) {
	if root == "" {
		return nil, errors.New("no project folder")
	}
	ids, err := suiteTaskIDs(suiteDir)
	if err != nil {
		return nil, err
	}
	store := Open(root)
	unlock := store.Lock(suiteID)
	defer unlock()
	cur, err := store.Snapshot(suiteID, 0)
	if err != nil {
		return nil, err
	}
	state := map[string]syncSeen{}
	if b, err := os.ReadFile(filepath.Join(store.bdir(suiteID), syncStateFile)); err == nil {
		_ = json.Unmarshal(b, &state)
	}
	next := map[string]syncSeen{}
	rows := map[string]string{}
	var added, updated, removed []string
	for _, id := range ids {
		tdir := filepath.Join(suiteDir, "tasks", id)
		sum, err := taskFolderSHA(tdir)
		if err != nil {
			return nil, fmt.Errorf("hash task %s: %w", id, err)
		}
		old, had := cur.Tasks[id]
		next[id] = syncSeen{Row: old, Files: sum}
		if had {
			seen, known := state[id]
			base := seen.Files
			if known && seen.Row != old {
				known = false
			}
			if !known {
				if r, err := store.Row(suiteID, old); err == nil {
					if s, ok := r.Metadata["files_sha256"].(string); ok {
						base, known = s, true
					}
				}
			}
			// A row recorded elsewhere with no folder hash is taken as the
			// folder it has now.
			if !known || base == sum {
				continue
			}
		}
		r := taskRow(tdir, id, sum)
		sha, data, err := RowSHA(r)
		if err != nil {
			return nil, err
		}
		if had && sha == old {
			continue
		}
		if err := store.PutRow(suiteID, sha, data); err != nil {
			return nil, err
		}
		rows[id] = sha
		next[id] = syncSeen{Row: sha, Files: sum}
		if had {
			updated = append(updated, id)
		} else {
			added = append(added, id)
		}
	}
	for id := range cur.Tasks {
		if _, ok := next[id]; !ok {
			removed = append(removed, id)
		}
	}
	v, err := store.Record(suiteID, rows, added, updated, removed, actor, source, now)
	if err != nil {
		return nil, err
	}
	if b, err := json.Marshal(next); err == nil {
		_ = datapolicy.WriteAtomic(store.bdir(suiteID), filepath.Join(store.bdir(suiteID), syncStateFile), b, 0o600)
	}
	return v, nil
}

// suiteTaskIDs is the task list of suite.yaml, or the folders under tasks/
// when it lists none (as suite.LoadHead reads it).
func suiteTaskIDs(dir string) ([]string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "suite.yaml"))
	if err != nil {
		return nil, err
	}
	var head struct {
		Tasks []string `yaml:"tasks"`
	}
	if err := yaml.Unmarshal(raw, &head); err != nil {
		return nil, fmt.Errorf("suite.yaml: %w", err)
	}
	if len(head.Tasks) > 0 {
		return head.Tasks, nil
	}
	entries, err := os.ReadDir(filepath.Join(dir, "tasks"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			ids = append(ids, e.Name())
		}
	}
	return ids, nil
}

// taskRow is the stored row of a task folder.
func taskRow(dir, id, sum string) Row {
	var t struct {
		Title       string `yaml:"title"`
		Instruction string `yaml:"instruction"`
		Split       string `yaml:"split"`
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "task.yaml")); err == nil {
		_ = yaml.Unmarshal(raw, &t)
	}
	name := t.Instruction
	if name == "" || !filepath.IsLocal(name) {
		name = "instruction.md"
	}
	in, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if len(in) > 4<<10 {
		in = in[:4<<10]
		for len(in) > 0 && !utf8.Valid(in) {
			in = in[:len(in)-1]
		}
	}
	return Row{ID: id, Title: t.Title, Input: string(in), HeldOut: t.Split == "held_out",
		Metadata: map[string]any{"files_sha256": sum}}
}

// taskFolderSHA is the content hash of a task folder: every regular file's
// slash path and content hash, in path order. A missing folder hashes as
// empty.
func taskFolderSHA(dir string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == dir && errors.Is(err, fs.ErrNotExist) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if n := d.Name(); p != dir && (n == ".git" || n == "node_modules" || n == "__MACOSX") {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		fh := sha256.Sum256(data)
		fmt.Fprintf(h, "%s %s\n", filepath.ToSlash(rel), hex.EncodeToString(fh[:]))
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
