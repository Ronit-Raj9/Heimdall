package policybench

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// DocsDir is the folder of a benchmark that holds its policy documents:
// <benchmark>/policy-docs/<doc>/{doc.json, source.txt, drafts/<task>/}.
// Nothing in it is a task: drafts become tasks only when accepted.
const DocsDir = "policy-docs"

// Draft states.
const (
	StatusPending  = "pending"
	StatusAccepted = "accepted"
	StatusRejected = "rejected"
)

// Doc is one uploaded policy and what was drafted from it.
type Doc struct {
	ID         string `json:"id"`
	Name       string `json:"name"` // the uploaded file name
	Title      string `json:"title"`
	Format     string `json:"format"`
	SHA256     string `json:"sha256"`
	Bytes      int    `json:"bytes"`
	UploadedAt string `json:"uploaded_at"`
	UploadedBy string `json:"uploaded_by,omitempty"`
	// PolicyIn is where the tasks give the agent the policy: "seed" (the
	// tool service's policy record, read with a tool such as get_policy)
	// or "instruction" (appended to the instruction).
	PolicyIn    string       `json:"policy_in,omitempty"`
	Clauses     []Clause     `json:"clauses"`
	NotTestable []NoTest     `json:"not_testable,omitempty"`
	Drafts      []Draft      `json:"drafts"`
	Generations []Generation `json:"generations,omitempty"`
}

// NoTest is a clause the model found nothing to test in.
type NoTest struct {
	Clause string `json:"clause"`
	Reason string `json:"reason"`
}

// Draft is one generated task and its review state.
type Draft struct {
	Task      string   `json:"task"`
	Clause    string   `json:"clause"`
	Kind      string   `json:"kind"`
	Title     string   `json:"title"`
	Opening   string   `json:"opening"`
	Check     string   `json:"check"`
	Violation string   `json:"violation"`
	Warnings  []string `json:"warnings,omitempty"`
	Status    string   `json:"status"`
	Model     string   `json:"model"`
	PromptSHA string   `json:"prompt_sha"`
	CostUSD   float64  `json:"cost_usd"`
	At        string   `json:"at"`
	DecidedBy string   `json:"decided_by,omitempty"`
	DecidedAt string   `json:"decided_at,omitempty"`
}

// Generation records one generation job.
type Generation struct {
	At        string   `json:"at"`
	Model     string   `json:"model"`
	PromptSHA string   `json:"prompt_sha"`
	BudgetUSD float64  `json:"budget_usd"`
	SpentUSD  float64  `json:"spent_usd"`
	Calls     int      `json:"calls"`
	Clauses   []string `json:"clauses"`
	Drafted   int      `json:"drafted"`
	Stopped   bool     `json:"stopped,omitempty"`
	By        string   `json:"by,omitempty"`
}

var docIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)

// ValidDocID reports whether id can name a policy document.
func ValidDocID(id string) bool { return docIDRe.MatchString(id) }

// NewDoc makes a document record from an upload and its text.
func NewDoc(name, format string, data []byte, text, by string, now time.Time) (*Doc, error) {
	clauses, err := SplitClauses(text)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	base := slugify(strings.TrimSuffix(filepath.Base(name), filepath.Ext(name)), 32)
	if base == "" {
		base = "policy"
	}
	title := titleOf(text, name)
	return &Doc{ID: base + "-" + sha[:6], Name: filepath.Base(name), Title: title, Format: format, SHA256: sha, Bytes: len(data),
		UploadedAt: now.UTC().Format(time.RFC3339), UploadedBy: by, Clauses: clauses, Drafts: []Draft{}}, nil
}

func titleOf(text, name string) string {
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(l), "#"))
		if l == "" {
			continue
		}
		if len(l) <= 120 {
			return l
		}
		break
	}
	return strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
}

// Dir is a document's folder in a benchmark.
func Dir(suiteRoot, id string) string { return filepath.Join(suiteRoot, DocsDir, id) }

// DraftDir is a draft task's folder.
func DraftDir(suiteRoot, id, task string) string { return filepath.Join(Dir(suiteRoot, id), "drafts", task) }

// Load reads a document record.
func Load(suiteRoot, id string) (*Doc, error) {
	if !ValidDocID(id) {
		return nil, fmt.Errorf("%q is not a policy document id", id)
	}
	raw, err := os.ReadFile(filepath.Join(Dir(suiteRoot, id), "doc.json"))
	if err != nil {
		return nil, err
	}
	var d Doc
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("policy document %s: doc.json: %w", id, err)
	}
	if d.Drafts == nil {
		d.Drafts = []Draft{}
	}
	return &d, nil
}

// Save writes doc.json atomically (and source.txt when text is set).
func Save(suiteRoot string, d *Doc, text string) error {
	dir := Dir(suiteRoot, d.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if text != "" {
		if err := writeAtomic(filepath.Join(dir, "source.txt"), []byte(text)); err != nil {
			return err
		}
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, "doc.json"), append(b, '\n'))
}

// Source reads the extracted text of a document.
func Source(suiteRoot, id string) (string, error) {
	b, err := os.ReadFile(filepath.Join(Dir(suiteRoot, id), "source.txt"))
	return string(b), err
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// List reads every document record of a benchmark, newest first.
func List(suiteRoot string) ([]*Doc, error) {
	ents, err := os.ReadDir(filepath.Join(suiteRoot, DocsDir))
	if errors.Is(err, os.ErrNotExist) {
		return []*Doc{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []*Doc{}
	for _, e := range ents {
		if !e.IsDir() || !ValidDocID(e.Name()) {
			continue
		}
		d, err := Load(suiteRoot, e.Name())
		if err != nil {
			continue
		}
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UploadedAt > out[j].UploadedAt })
	return out, nil
}

// Clause returns the clause with number n.
func (d *Doc) Clause(n string) (Clause, bool) {
	for _, c := range d.Clauses {
		if c.Number == n {
			return c, true
		}
	}
	return Clause{}, false
}

// DraftByTask returns the draft of a task.
func (d *Doc) DraftByTask(task string) *Draft {
	for i := range d.Drafts {
		if d.Drafts[i].Task == task {
			return &d.Drafts[i]
		}
	}
	return nil
}

// Counts are the drafts per status.
func (d *Doc) Counts() map[string]int {
	out := map[string]int{StatusPending: 0, StatusAccepted: 0, StatusRejected: 0}
	for _, x := range d.Drafts {
		out[x.Status]++
	}
	return out
}

// SetClauses replaces the clause list after a person edits it: numbers
// must be unique and non-empty, and a clause that already has drafts keeps
// its number.
func (d *Doc) SetClauses(cs []Clause) error {
	if len(cs) == 0 {
		return errors.New("a policy needs at least one clause")
	}
	if len(cs) > MaxClauses {
		return fmt.Errorf("at most %d clauses", MaxClauses)
	}
	seen := map[string]bool{}
	for i := range cs {
		c := &cs[i]
		c.Number, c.Title, c.Text = strings.TrimSpace(c.Number), strings.TrimSpace(c.Title), strings.TrimSpace(c.Text)
		switch {
		case c.Number == "" || len(c.Number) > 20 || strings.ContainsAny(c.Number, "\n\r"):
			return fmt.Errorf("clause %d: the number must be 1 to 20 characters on one line", i+1)
		case seen[c.Number]:
			return fmt.Errorf("clause number %s appears twice", c.Number)
		case c.Text == "":
			return fmt.Errorf("clause %s has no text", c.Number)
		case len(c.Text) > MaxClauseText:
			return fmt.Errorf("clause %s is over %d characters", c.Number, MaxClauseText)
		}
		seen[c.Number] = true
	}
	for _, x := range d.Drafts {
		if x.Status != StatusRejected && !seen[x.Clause] {
			return fmt.Errorf("clause %s has drafted tasks (%s); keep its number, or reject them first", x.Clause, x.Task)
		}
	}
	d.Clauses = cs
	return nil
}
