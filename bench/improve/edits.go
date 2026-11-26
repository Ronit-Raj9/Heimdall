package improve

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

func init() { DefaultEditApplier = ApplyEdits }

// The managed block markers (A3.3).
const (
	blockBegin = "<!-- heimdall:rules begin -->"
	blockEnd   = "<!-- heimdall:rules end -->"
)

// Errors of a whole edit set.
var (
	ErrOverSizeLimit  = errors.New("over the size limit")
	ErrNothingApplied = errors.New("the improver changed nothing")
)

var (
	placeholderRe = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_.]+)\s*\}\}`)
	itemLineRe    = regexp.MustCompile(`^\s*\d+\.\s*(?:\[(pb-\d+)\]\s*)?(.*)$`)
	frontMatterRe = regexp.MustCompile(`\A---\n((?:.*\n)*?)---\n`)
)

// mergeJaccard is the shingle overlap at which an added item merges into
// an existing one (rule 3).
const mergeJaccard = 0.6

// ApplyEdits applies edits to a bundle and its playbook, deterministically
// (A3.3 rules 1 to 10). Each refused edit records why and the rest still
// apply. The whole set is refused with ErrOverSizeLimit when the result is
// over a size cap, and gives ErrNothingApplied when no edit applied; both
// return the parent bundle and playbook unchanged.
func ApplyEdits(b BundleObject, pb PlaybookObject, e EditsFile, ed Editable, lim Limits, kind string, round int) (BundleObject, PlaybookObject, []AppliedEdit, error) {
	lim = limitsOrDefault(lim)
	st := &editState{
		files: maps.Clone(b.Files),
		raw:   slices.Clone(b.Raw),
		items: slices.Clone(pb.Items),
		next:  max(pb.Next, 1),
		ed:    ed,
		lim:   lim,
		kind:  kind,
		round: round,
	}
	if st.files == nil {
		st.files = map[string]string{}
	}
	for _, it := range st.items {
		if n := itemNumber(it.ID); n >= st.next {
			st.next = n + 1
		}
	}
	touched := map[string]bool{}
	out := make([]AppliedEdit, len(e.Edits))
	applied := 0
	for i, x := range e.Edits {
		a := AppliedEdit{Edit: x}
		if i >= lim.MaxEdits {
			a.Result, a.Why = "refused", fmt.Sprintf("more than %d edits", lim.MaxEdits)
		} else if why := st.apply(&a, touched); why != "" {
			a.Result, a.Why = "refused", why
		} else {
			applied++
		}
		out[i] = a
	}
	if applied == 0 {
		return b, pb, out, ErrNothingApplied
	}
	for f := range touched {
		st.render(f)
	}
	nb := BundleObject{Files: st.files, Raw: st.raw}
	slices.Sort(nb.Raw)
	nb.Raw = slices.Compact(nb.Raw)
	if overSize(b, nb, pb.Items, st.items, lim) {
		for i := range out {
			if out[i].Result != "refused" {
				out[i].Result, out[i].Why = "refused", ErrOverSizeLimit.Error()
			}
		}
		return b, pb, out, ErrOverSizeLimit
	}
	return nb, PlaybookObject{Next: st.next, Items: st.items}, out, nil
}

type editState struct {
	files map[string]string
	raw   []string
	items []PlaybookItem
	next  int
	ed    Editable
	lim   Limits
	kind  string
	round int
}

func limitsOrDefault(l Limits) Limits {
	d := DefaultLimits
	set := func(v *int, x int) {
		if x > 0 {
			*v = x
		}
	}
	set(&d.MaxEdits, l.MaxEdits)
	set(&d.PlaybookWordsPerFile, l.PlaybookWordsPerFile)
	set(&d.BundleWords, l.BundleWords)
	set(&d.Skills, l.Skills)
	set(&d.SkillWords, l.SkillWords)
	set(&d.ItemWords, l.ItemWords)
	return d
}

// apply applies one edit, or returns why it is refused. On success it sets
// a.Result (and a.ID for a new item).
func (st *editState) apply(a *AppliedEdit, touched map[string]bool) string {
	if st.kind != "null" && !citesRun(a.Evidence) {
		return "the edit cites no practice run"
	}
	switch a.Op {
	case "add_item":
		return st.addItem(a, touched)
	case "edit_item":
		i := st.find(a.ID)
		if i < 0 {
			return "no rule " + a.ID
		}
		if why := itemTextWhy(a.Text, st.lim); why != "" {
			return why
		}
		if why := st.newPlaceholder(st.items[i].File, a.Text); why != "" {
			return why
		}
		st.items[i].Text = strings.TrimSpace(a.Text)
		st.items[i].Evidence = appendRefs(st.items[i].Evidence, a.Evidence)
		touched[st.items[i].File] = true
		a.Result = "applied"
	case "delete_item":
		i := st.find(a.ID)
		if i < 0 {
			return "no rule " + a.ID
		}
		touched[st.items[i].File] = true
		st.items = slices.Delete(st.items, i, i+1)
		a.Result = "applied"
	case "add_skill":
		if !st.skillPath(a.Path) {
			return "a skill must be a SKILL.md file under an editable folder"
		}
		if _, ok := st.files[a.Path]; ok {
			return "the skill already exists; use edit_skill"
		}
		if why := skillWhy(a.Text, st.lim); why != "" {
			return why
		}
		if distinctTasks(a.Evidence) < 2 && st.kind != "null" {
			return "a new skill must cite runs of at least 2 practice tasks"
		}
		if st.skillCount() >= st.lim.Skills {
			return fmt.Sprintf("more than %d skills", st.lim.Skills)
		}
		st.files[a.Path] = a.Text
		st.raw = append(st.raw, a.Path)
		a.Result = "applied"
	case "edit_skill":
		if _, ok := st.files[a.Path]; !ok || !st.skillPath(a.Path) {
			return "no skill " + a.Path
		}
		if why := skillWhy(a.Text, st.lim); why != "" {
			return why
		}
		if why := st.newPlaceholder(a.Path, a.Text); why != "" {
			return why
		}
		st.files[a.Path] = a.Text
		a.Result = "applied"
	case "retire_skill":
		if _, ok := st.files[a.Path]; !ok || !st.skillPath(a.Path) {
			return "no skill " + a.Path
		}
		delete(st.files, a.Path)
		st.raw = slices.DeleteFunc(st.raw, func(p string) bool { return p == a.Path })
		a.Result = "applied"
	case "rewrite_file":
		if st.kind != "restart" {
			return "rewrite_file is only allowed on a restart"
		}
		if !st.ed.Allows(a.File) {
			return "the file is not editable"
		}
		if why := st.newPlaceholder(a.File, a.Text); why != "" {
			return why
		}
		st.rewrite(a.File, a.Text)
		a.Result = "applied"
	default:
		return "unknown op " + a.Op
	}
	return ""
}

func (st *editState) addItem(a *AppliedEdit, touched map[string]bool) string {
	if !st.ed.Allows(a.File) {
		return "the file is not editable"
	}
	if why := itemTextWhy(a.Text, st.lim); why != "" {
		return why
	}
	if why := st.newPlaceholder(a.File, a.Text); why != "" {
		return why
	}
	text := strings.TrimSpace(a.Text)
	for i, it := range st.items {
		if nearDuplicate(it.Text, text) {
			st.items[i].Text = text
			st.items[i].Evidence = appendRefs(st.items[i].Evidence, a.Evidence)
			touched[it.File] = true
			a.Result = "merged_into:" + it.ID
			return ""
		}
	}
	section := strings.TrimSpace(a.Section)
	if section == "" {
		section = "Strategies"
	}
	id := fmt.Sprintf("pb-%04d", st.next)
	st.next++
	st.items = append(st.items, PlaybookItem{ID: id, File: a.File, Section: oneLine(section), Text: text, OriginRound: st.round, Evidence: appendRefs(nil, a.Evidence)})
	touched[a.File] = true
	a.ID = id
	a.Result = "applied"
	return ""
}

func (st *editState) find(id string) int {
	return slices.IndexFunc(st.items, func(it PlaybookItem) bool { return it.ID == id })
}

func (st *editState) skillPath(p string) bool {
	if !strings.HasSuffix(p, "/SKILL.md") || strings.Contains(p, "..") {
		return false
	}
	for _, d := range st.ed.Dirs {
		if strings.HasPrefix(p, d) && st.ed.Allows(p) {
			return true
		}
	}
	return false
}

func (st *editState) skillCount() int {
	n := 0
	for p := range st.files {
		if strings.HasSuffix(p, "/SKILL.md") {
			n++
		}
	}
	return n
}

// newPlaceholder refuses text that adds a {{name}} a templated file does
// not already have (rule 9). Raw files and standing instructions are never
// rendered, so braces there are plain text.
func (st *editState) newPlaceholder(file, text string) string {
	if file == InstructionsKey || slices.Contains(st.raw, file) {
		return ""
	}
	if _, ok := st.files[file]; !ok {
		return ""
	}
	have := map[string]bool{}
	for _, m := range placeholderRe.FindAllStringSubmatch(st.files[file], -1) {
		have[m[1]] = true
	}
	for _, it := range st.items {
		if it.File == file {
			for _, m := range placeholderRe.FindAllStringSubmatch(it.Text, -1) {
				have[m[1]] = true
			}
		}
	}
	for _, m := range placeholderRe.FindAllStringSubmatch(text, -1) {
		if !have[m[1]] {
			return fmt.Sprintf("the edit adds a new placeholder {{%s}} to a template file", m[1])
		}
	}
	return ""
}

// rewrite replaces a whole file; its block, if any, is parsed into items.
func (st *editState) rewrite(file, text string) {
	st.items = slices.DeleteFunc(st.items, func(it PlaybookItem) bool { return it.File == file })
	before, block, after, ok := splitBlock(text)
	if !ok {
		st.files[file] = text
		return
	}
	section := "Strategies"
	for _, line := range strings.Split(block, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if s, ok := strings.CutPrefix(t, "## "); ok {
			section = strings.TrimSpace(s)
			continue
		}
		m := itemLineRe.FindStringSubmatch(t)
		if m == nil || strings.TrimSpace(m[2]) == "" {
			continue
		}
		id := m[1]
		if id == "" || st.find(id) >= 0 {
			id = fmt.Sprintf("pb-%04d", st.next)
			st.next++
		} else if n := itemNumber(id); n >= st.next {
			st.next = n + 1
		}
		st.items = append(st.items, PlaybookItem{ID: id, File: file, Section: section, Text: strings.TrimSpace(m[2]), OriginRound: st.round})
	}
	st.files[file] = before + blockBegin + "\n" + blockEnd + after
	st.render(file)
}

// render writes the file's block from its items: in place of the old
// block, else at the end. A file with no items left loses its block.
func (st *editState) render(file string) {
	var its []PlaybookItem
	for _, it := range st.items {
		if it.File == file {
			its = append(its, it)
		}
	}
	cur := st.files[file]
	before, _, after, ok := splitBlock(cur)
	switch {
	case ok && len(its) == 0:
		st.files[file] = strings.TrimRight(before, "\n") + strings.TrimLeft(after, "\n")
		if before != "" && after != "" {
			st.files[file] = strings.TrimRight(before, "\n") + "\n\n" + strings.TrimLeft(after, "\n")
		} else if before != "" {
			st.files[file] = strings.TrimRight(before, "\n") + "\n"
		}
	case ok:
		st.files[file] = before + strings.TrimSuffix(renderBlock(its), "\n") + after
	case len(its) > 0:
		sep := ""
		if cur != "" {
			sep = "\n"
			if !strings.HasSuffix(cur, "\n") {
				sep = "\n\n"
			} else if !strings.HasSuffix(cur, "\n\n") {
				sep = "\n"
			} else {
				sep = ""
			}
		}
		st.files[file] = cur + sep + renderBlock(its)
	}
}

// splitBlock cuts text around its managed block (markers excluded).
func splitBlock(text string) (before, block, after string, ok bool) {
	i := strings.Index(text, blockBegin)
	if i < 0 {
		return text, "", "", false
	}
	j := strings.Index(text[i:], blockEnd)
	if j < 0 {
		return text, "", "", false
	}
	return text[:i], text[i+len(blockBegin) : i+j], text[i+j+len(blockEnd):], true
}

// renderBlock is the managed block of a file: sections in first-use order,
// items numbered 1..n across the block, each with its id in brackets.
func renderBlock(items []PlaybookItem) string {
	var order []string
	by := map[string][]PlaybookItem{}
	for _, it := range items {
		s := it.Section
		if s == "" {
			s = "Strategies"
		}
		if _, ok := by[s]; !ok {
			order = append(order, s)
		}
		by[s] = append(by[s], it)
	}
	var b strings.Builder
	b.WriteString(blockBegin + "\n")
	n := 0
	for _, s := range order {
		b.WriteString("## " + s + "\n")
		for _, it := range by[s] {
			n++
			fmt.Fprintf(&b, "%d. [%s] %s\n", n, it.ID, oneLine(it.Text))
		}
	}
	b.WriteString(blockEnd + "\n")
	return b.String()
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func words(s string) int { return len(strings.Fields(s)) }

func itemTextWhy(t string, lim Limits) string {
	if n := words(t); n < 1 || n > lim.ItemWords {
		return fmt.Sprintf("a rule must be 1 to %d words", lim.ItemWords)
	}
	return ""
}

// skillWhy checks a skill's front matter and size (rules 5 and 6).
func skillWhy(t string, lim Limits) string {
	m := frontMatterRe.FindStringSubmatch(t)
	if m == nil {
		return "a skill must start with a front matter block"
	}
	var name, desc bool
	for _, line := range strings.Split(m[1], "\n") {
		if v, ok := strings.CutPrefix(line, "name:"); ok && strings.TrimSpace(v) != "" {
			name = true
		}
		if v, ok := strings.CutPrefix(line, "description:"); ok {
			v = strings.TrimSpace(v)
			if v == "" || len(v) > 200 {
				return "a skill description must be one line of at most 200 characters"
			}
			desc = true
		}
	}
	if !name || !desc {
		return "a skill front matter must hold name and description"
	}
	if words(t) > lim.SkillWords {
		return fmt.Sprintf("a skill must be at most %d words", lim.SkillWords)
	}
	return ""
}

// citesRun reports whether refs hold at least one run ref of the evidence
// ("dev:<task>#<trial>/<arm>"). Whether each ref exists in this round's
// evidence.json is checked by the caller, which has the document.
func citesRun(refs []string) bool {
	return slices.ContainsFunc(refs, func(r string) bool { return runTask(r) != "" })
}

// runTask is the task of a run ref, "" when r is not one.
func runTask(r string) string {
	rest, ok := strings.CutPrefix(r, "dev:")
	if !ok {
		return ""
	}
	i := strings.LastIndexByte(rest, '#')
	if i <= 0 {
		return ""
	}
	return rest[:i]
}

func distinctTasks(refs []string) int {
	seen := map[string]bool{}
	for _, r := range refs {
		if t := runTask(r); t != "" {
			seen[t] = true
		}
	}
	return len(seen)
}

func appendRefs(have, add []string) []string {
	out := slices.Clone(have)
	for _, r := range add {
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out
}

func itemNumber(id string) int {
	var n int
	if _, err := fmt.Sscanf(id, "pb-%d", &n); err != nil {
		return 0
	}
	return n
}

// nearDuplicate: 8-word shingles with Jaccard at least 0.6, or for items
// under 8 words a lowercase exact match (rule 3).
func nearDuplicate(a, b string) bool {
	sa, sb := shingles(a), shingles(b)
	if len(sa) == 0 || len(sb) == 0 {
		return strings.EqualFold(oneLine(a), oneLine(b))
	}
	inter := 0
	for k := range sa {
		if sb[k] {
			inter++
		}
	}
	union := len(sa) + len(sb) - inter
	return float64(inter)/float64(union) >= mergeJaccard
}

// overSize applies rule 8: each file's block at most PlaybookWordsPerFile
// words, the bundle at most max(1.25 x parent, BundleWords) words, and when
// any cap was above 80% full before, the set must not add words.
func overSize(parent, cand BundleObject, before, after []PlaybookItem, lim Limits) bool {
	pw, cw := bundleWords(parent), bundleWords(cand)
	bundleCap := max(int(1.25*float64(pw)), lim.BundleWords)
	if cw > bundleCap {
		return true
	}
	ba, aa := blockWords(before), blockWords(after)
	full := float64(pw) > 0.8*float64(bundleCap)
	for f, w := range aa {
		if w > lim.PlaybookWordsPerFile {
			return true
		}
		_ = f
	}
	for _, w := range ba {
		if float64(w) > 0.8*float64(lim.PlaybookWordsPerFile) {
			full = true
		}
	}
	return full && cw > pw
}

func bundleWords(b BundleObject) int {
	n := 0
	for _, t := range b.Files {
		n += words(t)
	}
	return n
}

func blockWords(items []PlaybookItem) map[string]int {
	out := map[string]int{}
	for _, it := range items {
		out[it.File] += words(it.Text)
	}
	return out
}
