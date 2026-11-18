package calibration

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Perturbation names (CalibrationPerturbation.Name).
const (
	PerturbWhitespace = "whitespace"
	PerturbPreamble   = "preamble"
)

// Perturbations are the deterministic, meaning-preserving changes every
// calibration applies to each answer, in order.
var Perturbations = []string{PerturbWhitespace, PerturbPreamble}

// preamble is what the preamble perturbation puts before the answer.
const preamble = "Sure! I have looked into this carefully. Here is my answer:\n\n"

// Perturb applies one perturbation to an answer. Whitespace doubles every
// run of spaces between words and adds blank lines around the text;
// preamble adds a polite opening line. Neither changes what the answer
// says, so a judge whose verdict moves is reacting to form, not content.
func Perturb(name, answer string) string {
	switch name {
	case PerturbWhitespace:
		lines := strings.Split(answer, "\n")
		for i, l := range lines {
			lines[i] = strings.Join(strings.Fields(l), "  ")
		}
		return "\n\n" + strings.Join(lines, "\n\n") + "\n\n"
	case PerturbPreamble:
		return preamble + answer
	}
	return answer
}

// ItemRun is the judge's work on one item, as the runner hands it over.
type ItemRun struct {
	Item      Labelled
	Category  string
	Template  string
	Votes     []string            // first pass, each call's verdict (correct, incorrect, unparsable)
	Repeat    []string            // second pass
	Perturbed map[string][]string // perturbation -> votes
}

// Build assembles a record from the runs: statistics over the human items
// (what decides), seeds apart, per category, consistency and perturbation
// sensitivity. Everything about the judge and the set is filled by the
// caller; Build fills the statistics, the items and the counts.
func Build(rec *schemas.CalibrationRecord, runs []ItemRun, disputed int) {
	var hl, hv, hc []string
	var sl, sv []string
	var firstVotes [][]string
	var first, second []string
	pert := map[string][]string{}
	pertFirst := map[string][]string{}
	labellers := map[string]bool{}
	rec.Items = make([]schemas.CalibrationItemResult, 0, len(runs))
	for _, r := range runs {
		v1 := Majority(r.Votes)
		v2 := Majority(r.Repeat)
		res := schemas.CalibrationItemResult{
			ItemID: r.Item.Item.ID, Task: r.Item.Item.Task, Category: r.Category, Template: r.Template,
			Label: r.Item.Label, Source: r.Item.Item.Source, Votes: append([]string(nil), r.Votes...), Verdict: v1, Repeat: v2,
		}
		if r.Item.Seed {
			res.Source = schemas.CalibrationSourceControl
			sl, sv = append(sl, r.Item.Label), append(sv, v1)
		} else {
			hl, hv, hc = append(hl, r.Item.Label), append(hv, v1), append(hc, r.Category)
			for _, l := range r.Item.Labellers {
				labellers[l] = true
			}
		}
		firstVotes = append(firstVotes, r.Votes)
		first, second = append(first, v1), append(second, v2)
		for _, p := range Perturbations {
			votes, ok := r.Perturbed[p]
			if !ok {
				continue
			}
			if res.Perturbed == nil {
				res.Perturbed = map[string]string{}
			}
			res.Perturbed[p] = Majority(votes)
			pert[p] = append(pert[p], res.Perturbed[p])
			pertFirst[p] = append(pertFirst[p], v1)
		}
		rec.Items = append(rec.Items, res)
	}
	rec.HumanLabels, rec.SeedItems, rec.Disputed = len(hl), len(sl), disputed
	rec.Human = Compare(hl, hv)
	if len(sl) > 0 {
		s := Compare(sl, sv)
		rec.Seed = &s
	}
	if len(hl) > 0 {
		rec.Categories = ByCategory(hc, hl, hv)
	}
	rec.Consistency = Consistency(rec.Votes, firstVotes, first, second)
	rec.Perturbations = nil
	for _, p := range Perturbations {
		if len(pert[p]) > 0 {
			rec.Perturbations = append(rec.Perturbations, Flips(p, pertFirst[p], pert[p]))
		}
	}
	rec.Labellers = sortedSet(labellers)
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// PromptHash is the hash of a prompt version as a whole: the sha256 of its
// template names and hashes, one "name sha" line each, sorted.
func PromptHash(templates map[string]string) string {
	names := make([]string, 0, len(templates))
	for n := range templates {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		b.WriteString(n + " " + templates[n] + "\n")
	}
	return utils.SHA256Bytes([]byte(b.String()))
}

// RecordSet is a suite's calibration records as read from records.log and
// checked, oldest first: every record present, hashing to the hash on its
// line and chained to the line before. A record that fails a check is left
// out and named in Problems.
type RecordSet struct {
	Valid    []schemas.CalibrationRecord
	Problems []string
	Verified bool
}

var recordIDRe = regexp.MustCompile(`^cr_[a-z0-9]{6,32}$`)

// RecordHash is the sha256 of the record's canonical JSON with Hash empty.
func RecordHash(rec *schemas.CalibrationRecord) (string, error) {
	c := *rec
	c.Hash = ""
	return utils.HashJSON(&c)
}

// WriteRecord chains rec to the newest line of records.log, writes
// records/<id>/record.json read-only, then appends "<id> <hash>" with
// fsync, under the calibration folder's lock. The record is written before
// its line, so a crash between the two leaves a record that is not in the
// log, which readers report and ignore.
func WriteRecord(root string, rec *schemas.CalibrationRecord) error {
	if rec.RecordID == "" {
		rec.RecordID = utils.NewID("cr", 10)
	}
	if !recordIDRe.MatchString(rec.RecordID) {
		return fmt.Errorf("record id %q is not valid", rec.RecordID)
	}
	unlock, err := lock(root)
	if err != nil {
		return err
	}
	defer unlock()
	logPath := filepath.Join(root, Dir, RecordsLog)
	raw, err := os.ReadFile(logPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read %s: %w", RecordsLog, err)
	}
	_, last := checkRecords(root, raw)
	rec.PrevHash = last
	if rec.Hash, err = RecordHash(rec); err != nil {
		return err
	}
	dir := filepath.Join(root, Dir, RecordsDir, rec.RecordID)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		return fmt.Errorf("record %s: %w", rec.RecordID, err)
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	p := filepath.Join(dir, recordFile)
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(p, 0o444); err != nil {
		return err
	}
	lf, err := os.OpenFile(logPath, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	line := rec.RecordID + " " + rec.Hash + "\n"
	if len(raw) > 0 && raw[len(raw)-1] != '\n' {
		line = "\n" + line
	}
	if _, err := lf.WriteString(line); err != nil {
		_ = lf.Close()
		return err
	}
	if err := lf.Sync(); err != nil {
		_ = lf.Close()
		return err
	}
	return lf.Close()
}

// ReadRecords reads and checks the suite's records (RecordSet). A suite
// without records has an empty, verified set.
func ReadRecords(root string) (*RecordSet, error) {
	raw, err := os.ReadFile(filepath.Join(root, Dir, RecordsLog))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", RecordsLog, err)
	}
	set, _ := checkRecords(root, raw)
	return set, nil
}

// checkRecords parses records.log and checks every record it names; it also
// returns the hash on the last well-formed line, which a new record chains
// to. A malformed line breaks the chain: every record after it is
// reported. A changed record is ignored alone.
func checkRecords(root string, log []byte) (*RecordSet, string) {
	set := &RecordSet{}
	prev, broken := "", false
	logged := map[string]bool{}
	for i, line := range bytes.Split(log, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		f := strings.Fields(string(line))
		if len(f) != 2 || !recordIDRe.MatchString(f[0]) || len(f[1]) != 64 {
			set.Problems = append(set.Problems, fmt.Sprintf("%s line %d is malformed; the chain is broken there", RecordsLog, i+1))
			broken = true
			continue
		}
		id, hash := f[0], f[1]
		if logged[id] {
			set.Problems = append(set.Problems, fmt.Sprintf("%s line %d: record %s logged twice; the later line is ignored", RecordsLog, i+1, id))
			continue
		}
		logged[id] = true
		rec, err := checkRecord(root, id, hash, prev, broken)
		if err != nil {
			set.Problems = append(set.Problems, fmt.Sprintf("record %s (%s line %d) ignored: %v", id, RecordsLog, i+1, err))
		} else {
			set.Valid = append(set.Valid, *rec)
		}
		prev = hash
	}
	entries, _ := os.ReadDir(filepath.Join(root, Dir, RecordsDir))
	for _, e := range entries {
		if e.IsDir() && !logged[e.Name()] {
			set.Problems = append(set.Problems, fmt.Sprintf("record %s ignored: it is not in %s (interrupted before it was logged, or its line was removed)", e.Name(), RecordsLog))
		}
	}
	set.Verified = len(set.Problems) == 0
	return set, prev
}

func checkRecord(root, id, hash, prev string, broken bool) (*schemas.CalibrationRecord, error) {
	b, err := os.ReadFile(filepath.Join(root, Dir, RecordsDir, id, recordFile))
	if err != nil {
		return nil, errors.New("record.json is missing")
	}
	var rec schemas.CalibrationRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil, fmt.Errorf("record.json does not parse: %v", err)
	}
	h, err := RecordHash(&rec)
	if err != nil {
		return nil, err
	}
	switch {
	case rec.RecordID != id:
		return nil, fmt.Errorf("record names %s, the log says %s", rec.RecordID, id)
	case h != rec.Hash:
		return nil, fmt.Errorf("content hashes to %s, record says %s (the record was changed)", h, rec.Hash)
	case rec.Hash != hash:
		return nil, fmt.Errorf("record hash %s, %s says %s", rec.Hash, RecordsLog, hash)
	case broken:
		return nil, errors.New("an earlier line is malformed, so the chain cannot be checked")
	case rec.PrevHash != prev:
		return nil, fmt.Errorf("prev_hash %q, the line before says %q (the chain is broken)", rec.PrevHash, prev)
	}
	return &rec, nil
}

// Covering returns the newest valid record for the judge model that
// covers every template hash in templates, and, when there is none, the
// newest record for the model at all (to say what it lacks).
func (s *RecordSet) Covering(model string, templates []string) (covering, newest *schemas.CalibrationRecord) {
	if s == nil {
		return nil, nil
	}
	for i := len(s.Valid) - 1; i >= 0; i-- {
		r := &s.Valid[i]
		if r.Model != model {
			continue
		}
		if newest == nil {
			newest = r
		}
		if covers(r, templates) {
			return r, newest
		}
	}
	return nil, newest
}

// ForPrompt returns the newest valid record for the judge model and the
// prompt version's hash.
func (s *RecordSet) ForPrompt(model, promptSHA string) *schemas.CalibrationRecord {
	if s == nil {
		return nil
	}
	for i := len(s.Valid) - 1; i >= 0; i-- {
		if r := &s.Valid[i]; r.Model == model && r.PromptSHA256 == promptSHA {
			return r
		}
	}
	return nil
}

func covers(r *schemas.CalibrationRecord, templates []string) bool {
	have := map[string]bool{}
	for _, h := range r.Templates {
		have[h] = true
	}
	for _, t := range templates {
		if !have[t] {
			return false
		}
	}
	return true
}

// Ref is how a grade names the record it was graded under.
func Ref(r *schemas.CalibrationRecord) string {
	if r == nil {
		return ""
	}
	return r.RecordID + "@" + r.Hash
}

// Summary is a record without its per-item results, for listings.
func Summary(r *schemas.CalibrationRecord) *schemas.CalibrationRecord {
	if r == nil {
		return nil
	}
	c := *r
	c.Items = nil
	return &c
}

// Age is how old the record is at now, in whole days.
func Age(r *schemas.CalibrationRecord, now time.Time) int {
	return int(now.Sub(r.CreatedAt).Hours() / 24)
}
