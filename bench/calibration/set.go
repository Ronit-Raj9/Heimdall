// Package calibration measures a judged suite's judge against human labels
// (docs/judge-calibration.md). It owns the calibration set
// (benchmarks/<suite>/calibration/labels.jsonl), the statistics that compare
// the judge's verdicts with the labels, the hash-chained calibration records
// and the judge_calibration instrument a judged experiment's verdict
// carries. It never calls a model: graders/judgecalibration.go runs the
// pinned judge and hands the verdicts here.
package calibration

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Layout of a suite's calibration folder.
const (
	Dir        = "calibration"  // under the suite root
	LabelsFile = "labels.jsonl" // the working set, one item per line, append-only
	SetsDir    = "sets"         // content-addressed snapshots: sets/<sha256>.jsonl
	RecordsDir = "records"      // records/<record_id>/record.json, read-only
	RecordsLog = "records.log"  // one line per record: "<record_id> <hash>"
	recordFile = "record.json"
	lockFile   = ".calibration.lock"
)

// Limits on one label.
const (
	maxAnswerRunes   = 64000
	maxNoteRunes     = 4000
	maxLabellerRunes = 200
	maxRefRunes      = 500
	maxTaskRunes     = 200
)

// ErrInput marks a label that cannot be written as asked; the API answers
// it with 400.
var ErrInput = errors.New("invalid calibration label")

// ItemID is the content address of an answer to a task: the first 16 hex
// of sha256(task NUL answer). The same answer labelled twice is one item.
func ItemID(task, answer string) string {
	return utils.SHA256Bytes([]byte(task + "\x00" + answer))[:16]
}

// NewItem validates a label request and returns the item to append.
func NewItem(req schemas.CalibrationLabelRequest, source string, at time.Time) (schemas.CalibrationItem, error) {
	it := schemas.CalibrationItem{
		Task: strings.TrimSpace(req.Task), Answer: req.Answer, Label: strings.ToLower(strings.TrimSpace(req.Label)),
		Labeller: strings.TrimSpace(req.Labeller), Note: strings.TrimSpace(req.Note), Source: source,
		Ref: strings.TrimSpace(req.Ref), At: at.UTC().Truncate(time.Second),
	}
	it.ID = ItemID(it.Task, it.Answer)
	return it, ValidateItem(it)
}

// ValidateItem checks one item: a task id, a non-empty answer, one of the
// two labels, a one-line labeller, a known source, within their lengths.
// Errors wrap ErrInput.
func ValidateItem(it schemas.CalibrationItem) error {
	oneLine := func(s string, n int) bool {
		return s != "" && utf8.ValidString(s) && utf8.RuneCountInString(s) <= n && !strings.ContainsAny(s, "\r\n")
	}
	switch {
	case !oneLine(it.Task, maxTaskRunes):
		return fmt.Errorf("task must be one line of at most %d characters: %w", maxTaskRunes, ErrInput)
	case (strings.TrimSpace(it.Answer) == "" && it.Image == "") || !utf8.ValidString(it.Answer) || utf8.RuneCountInString(it.Answer) > maxAnswerRunes: // X1 vision grading: an image item may have no answer text
		return fmt.Errorf("answer must be non-empty text of at most %d characters: %w", maxAnswerRunes, ErrInput)
	case it.Image != "" && !imageSHARe.MatchString(it.Image): // X1 vision grading (image.go)
		return fmt.Errorf("image %q is not a lowercase sha256: %w", it.Image, ErrInput)
	case it.Label != schemas.CalibrationCorrect && it.Label != schemas.CalibrationIncorrect:
		return fmt.Errorf("label %q is not %s or %s: %w", it.Label, schemas.CalibrationCorrect, schemas.CalibrationIncorrect, ErrInput)
	case !oneLine(it.Labeller, maxLabellerRunes):
		return fmt.Errorf("labeller is required, one line of at most %d characters: %w", maxLabellerRunes, ErrInput)
	case !utf8.ValidString(it.Note) || utf8.RuneCountInString(it.Note) > maxNoteRunes:
		return fmt.Errorf("note must be text of at most %d characters: %w", maxNoteRunes, ErrInput)
	case it.Ref != "" && !oneLine(it.Ref, maxRefRunes):
		return fmt.Errorf("ref must be one line of at most %d characters: %w", maxRefRunes, ErrInput)
	case !knownSource(it.Source):
		return fmt.Errorf("source %q is not known: %w", it.Source, ErrInput)
	case it.ID != ItemIDOf(it): // X1 vision grading: ItemIDOf is ItemID unless the item has an image (image.go)
		return fmt.Errorf("id %s is not the content address of task and answer (%s): %w", it.ID, ItemIDOf(it), ErrInput)
	}
	return nil
}

func knownSource(s string) bool {
	switch s {
	case schemas.CalibrationSourceReview, schemas.CalibrationSourceLabel, schemas.CalibrationSourceImport, schemas.CalibrationSourceControl:
		return true
	}
	return false
}

// Set is a suite's labels.jsonl as read: its raw bytes, their sha256 (the
// set hash; "" when the file does not exist) and every row.
type Set struct {
	Root   string // the suite root
	Raw    []byte
	SHA256 string
	Items  []schemas.CalibrationItem
}

// LabelsPath is the labels file of the suite at root.
func LabelsPath(root string) string { return filepath.Join(root, Dir, LabelsFile) }

// LoadSet reads the suite's labels.jsonl. A missing file is an empty set
// with no hash. Every line must be a valid item; a bad line is an error
// naming it, never skipped.
func LoadSet(root string) (*Set, error) {
	s := &Set{Root: root}
	raw, err := os.ReadFile(LabelsPath(root))
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read calibration labels: %w", err)
	}
	items, err := ParseJSONL(raw, LabelsFile, false)
	if err != nil {
		return nil, err
	}
	if err := ValidateImages(root, items); err != nil { // X1 vision grading: every labelled image is present and intact (image.go)
		return nil, err
	}
	s.Raw, s.SHA256, s.Items = raw, utils.SHA256Bytes(raw), items
	return s, nil
}

// ParseJSONL reads items, one JSON object per line. With fill, a line may
// leave out id, source and at (an import file): they are filled in.
func ParseJSONL(raw []byte, file string, fill bool) ([]schemas.CalibrationItem, error) {
	var out []schemas.CalibrationItem
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 4*maxAnswerRunes+64*1024)
	for n := 1; sc.Scan(); n++ {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var it schemas.CalibrationItem
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&it); err != nil {
			return nil, fmt.Errorf("%s line %d: %v: %w", file, n, err, ErrInput)
		}
		if fill {
			it = filled(it, schemas.CalibrationSourceImport, file)
		}
		if err := ValidateItem(it); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", file, n, err)
		}
		out = append(out, it)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return out, nil
}

// ParseCSV reads an import file with a header row naming at least task,
// answer, label and labeller (note and ref optional, any order).
func ParseCSV(raw []byte, file string) ([]schemas.CalibrationItem, error) {
	r := csv.NewReader(bytes.NewReader(raw))
	r.FieldsPerRecord = -1
	head, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("%s: header: %v: %w", file, err, ErrInput)
	}
	col := map[string]int{}
	for i, h := range head {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	for _, need := range []string{"task", "answer", "label", "labeller"} {
		if _, ok := col[need]; !ok {
			return nil, fmt.Errorf("%s: header has no %s column (want task, answer, label, labeller, optional note, ref): %w", file, need, ErrInput)
		}
	}
	get := func(rec []string, k string) string {
		if i, ok := col[k]; ok && i < len(rec) {
			return rec[i]
		}
		return ""
	}
	var out []schemas.CalibrationItem
	for n := 2; ; n++ {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s row %d: %v: %w", file, n, err, ErrInput)
		}
		it := filled(schemas.CalibrationItem{
			Task: strings.TrimSpace(get(rec, "task")), Answer: get(rec, "answer"), Label: strings.ToLower(strings.TrimSpace(get(rec, "label"))),
			Labeller: strings.TrimSpace(get(rec, "labeller")), Note: strings.TrimSpace(get(rec, "note")), Ref: strings.TrimSpace(get(rec, "ref")),
		}, schemas.CalibrationSourceImport, file)
		if err := ValidateItem(it); err != nil {
			return nil, fmt.Errorf("%s row %d: %w", file, n, err)
		}
		out = append(out, it)
	}
	return out, nil
}

// filled sets an import row's id, source, ref and time when it left them
// out.
func filled(it schemas.CalibrationItem, source, file string) schemas.CalibrationItem {
	if it.ID == "" {
		it.ID = ItemIDOf(it) // X1 vision grading: ItemID unless the row names an image
	}
	if it.Source == "" {
		it.Source = source
	}
	if it.Ref == "" && source == schemas.CalibrationSourceImport {
		it.Ref = filepath.Base(file)
	}
	if it.At.IsZero() {
		it.At = time.Now().UTC().Truncate(time.Second)
	}
	return it
}

// setMu serializes writers in this process; the lock file serializes
// processes.
var setMu sync.Mutex

// Append adds items to the suite's labels.jsonl under a lock and returns
// how many it wrote. A row equal to an existing one in id, label, labeller,
// source and ref is skipped, so importing the same reviews or file twice
// adds nothing. Rows are only ever appended.
func Append(root string, items []schemas.CalibrationItem) (int, error) {
	for _, it := range items {
		if err := ValidateItem(it); err != nil {
			return 0, err
		}
	}
	if err := ValidateImages(root, items); err != nil { // X1 vision grading (image.go)
		return 0, err
	}
	unlock, err := lock(root)
	if err != nil {
		return 0, err
	}
	defer unlock()
	cur, err := LoadSet(root)
	if err != nil {
		return 0, err
	}
	seen := map[string]bool{}
	for _, it := range cur.Items {
		seen[rowKey(it)] = true
	}
	var buf bytes.Buffer
	n := 0
	for _, it := range items {
		k := rowKey(it)
		if seen[k] {
			continue
		}
		seen[k] = true
		b, err := json.Marshal(it)
		if err != nil {
			return 0, err
		}
		buf.Write(b)
		buf.WriteByte('\n')
		n++
	}
	if n == 0 {
		return 0, nil
	}
	if len(cur.Raw) > 0 && cur.Raw[len(cur.Raw)-1] != '\n' {
		buf = *bytes.NewBuffer(append([]byte{'\n'}, buf.Bytes()...))
	}
	f, err := os.OpenFile(LabelsPath(root), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return 0, fmt.Errorf("open calibration labels: %w", err)
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		_ = f.Close()
		return 0, fmt.Errorf("append calibration labels: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return 0, fmt.Errorf("sync calibration labels: %w", err)
	}
	return n, f.Close()
}

func rowKey(it schemas.CalibrationItem) string {
	return strings.Join([]string{it.ID, it.Label, it.Labeller, it.Source, it.Ref}, "\x00")
}

// Snapshot writes the set's bytes to sets/<sha256>.jsonl (read-only) unless
// it is there, and returns its path relative to the suite root. The record
// names the hash, so the exact set a calibration used can be read back.
func Snapshot(s *Set) (string, error) {
	if s.SHA256 == "" {
		return "", errors.New("no calibration labels to snapshot")
	}
	rel := filepath.Join(Dir, SetsDir, s.SHA256+".jsonl")
	abs := filepath.Join(s.Root, rel)
	if b, err := os.ReadFile(abs); err == nil {
		if utils.SHA256Bytes(b) != s.SHA256 {
			return "", fmt.Errorf("snapshot %s does not hash to its name", rel)
		}
		return rel, nil
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(abs, s.Raw, 0o444); err != nil {
		return "", fmt.Errorf("write set snapshot: %w", err)
	}
	return rel, nil
}

// Labelled is one item as the statistics read it: the item's first row,
// the label every labelling row agrees on, and who labelled it.
type Labelled struct {
	Item      schemas.CalibrationItem
	Label     string
	Labellers []string
	Seed      bool // only admission-control rows: weak, reported apart
}

// Resolve groups rows by item. Rows from people (reviews, labels, imports)
// decide an item; admission-control rows decide it only when no person
// labelled it, and the item is then a seed. An item whose deciding rows
// disagree is disputed: left out and counted, because a judge cannot be
// measured against labels people do not agree on. Items keep the order of
// their first row.
func Resolve(rows []schemas.CalibrationItem) (human, seed []Labelled, disputed int) {
	type group struct {
		first          schemas.CalibrationItem
		people, seeds  map[string]bool
		peopleLabelers []string
		seedLabellers  []string
	}
	groups := map[string]*group{}
	var order []string
	for _, r := range rows {
		g, ok := groups[r.ID]
		if !ok {
			g = &group{first: r, people: map[string]bool{}, seeds: map[string]bool{}}
			groups[r.ID] = g
			order = append(order, r.ID)
		}
		if r.Source == schemas.CalibrationSourceControl {
			g.seeds[r.Label] = true
			g.seedLabellers = appendNew(g.seedLabellers, r.Labeller)
			continue
		}
		g.people[r.Label] = true
		g.peopleLabelers = appendNew(g.peopleLabelers, r.Labeller)
	}
	for _, id := range order {
		g := groups[id]
		labels, labellers, isSeed := g.people, g.peopleLabelers, false
		if len(labels) == 0 {
			labels, labellers, isSeed = g.seeds, g.seedLabellers, true
		}
		if len(labels) != 1 {
			disputed++
			continue
		}
		var label string
		for l := range labels {
			label = l
		}
		l := Labelled{Item: g.first, Label: label, Labellers: labellers, Seed: isSeed}
		if isSeed {
			seed = append(seed, l)
		} else {
			human = append(human, l)
		}
	}
	return human, seed, disputed
}

func appendNew(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

// SeedItems turns one task's answer spec into admission-control seed rows:
// each expected answer labelled correct and each wrong answer incorrect, by
// the suite author. They are a weak, clearly labelled start, never a
// substitute for people labelling agents' answers.
func SeedItems(task string, expected, wrong []string, at time.Time) []schemas.CalibrationItem {
	var out []schemas.CalibrationItem
	add := func(ans, label, note string) {
		if strings.TrimSpace(ans) == "" {
			return
		}
		it := schemas.CalibrationItem{
			ID: ItemID(task, ans), Task: task, Answer: ans, Label: label, Labeller: "suite admission controls",
			Note: note, Source: schemas.CalibrationSourceControl, Ref: "answer.yaml", At: at.UTC().Truncate(time.Second),
		}
		if ValidateItem(it) == nil {
			out = append(out, it)
		}
	}
	for _, e := range expected {
		add(e, schemas.CalibrationCorrect, "expected answer (admission control, weak seed)")
	}
	for _, w := range wrong {
		add(w, schemas.CalibrationIncorrect, "wrong answer (admission control, weak seed)")
	}
	return out
}

// lock takes the calibration folder's lock: the process mutex, then a lock
// file created exclusively. A lock file older than staleLock is left by a
// crashed writer and is removed.
func lock(root string) (func(), error) {
	setMu.Lock()
	dir := filepath.Join(root, Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		setMu.Unlock()
		return nil, fmt.Errorf("create calibration folder: %w", err)
	}
	p := filepath.Join(dir, lockFile)
	deadline := time.Now().Add(lockWait)
	for {
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(p); setMu.Unlock() }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			setMu.Unlock()
			return nil, fmt.Errorf("lock calibration folder: %w", err)
		}
		if fi, serr := os.Stat(p); serr == nil && time.Since(fi.ModTime()) > staleLock {
			_ = os.Remove(p)
			continue
		}
		if time.Now().After(deadline) {
			setMu.Unlock()
			return nil, fmt.Errorf("calibration folder %s is locked by another writer", dir)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

const (
	lockWait  = 10 * time.Second
	staleLock = 2 * time.Minute
)
