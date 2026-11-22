package scaffold

// Row mapper (lane Y13): real datasets are nested (LangSmith inputs and
// outputs, OpenAI Evals input messages and ideal, Inspect samples with
// target, metadata and files), carry chat conversations and attachments,
// and label rows with metadata. A RowMapping picks, per row field, the
// column (a dotted path into the record) it is read from; fields the
// mapping leaves empty fall back to the aliases of rowAliases and the
// nested aliases below. Columns lists the paths a file holds so a person
// can map them.

import (
	"encoding/json"
	"fmt"
	"mime"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/maximhq/bifrost/bench/schemas"
)

// RowMapping names the column of each row field. A column is a dotted path
// into the record ("inputs.question", "metadata.tier", "messages"); a CSV
// header is a path as written ("metadata:tier" works too). "-" means the
// field is not read at all. Empty means auto-detect.
type RowMapping struct {
	ID          string `json:"id,omitempty" yaml:"id,omitempty"`
	Title       string `json:"title,omitempty" yaml:"title,omitempty"`
	Question    string `json:"question,omitempty" yaml:"question,omitempty"`
	Messages    string `json:"messages,omitempty" yaml:"messages,omitempty"`
	Expected    string `json:"expected,omitempty" yaml:"expected,omitempty"`
	Rubric      string `json:"rubric,omitempty" yaml:"rubric,omitempty"`
	Reference   string `json:"reference,omitempty" yaml:"reference,omitempty"`
	Wrong       string `json:"wrong_answers,omitempty" yaml:"wrong_answers,omitempty"`
	Match       string `json:"match,omitempty" yaml:"match,omitempty"`
	Category    string `json:"category,omitempty" yaml:"category,omitempty"`
	Split       string `json:"split,omitempty" yaml:"split,omitempty"`
	UserTurns   string `json:"user_turns,omitempty" yaml:"user_turns,omitempty"`
	Attachments string `json:"attachments,omitempty" yaml:"attachments,omitempty"`
	GradeOn     string `json:"grade_on,omitempty" yaml:"grade_on,omitempty"`
	// Metadata are columns copied into the task's metadata, keyed by the
	// column's last path segment. Columns named metadata.<key>,
	// metadata:<key> or inside a metadata object are always copied.
	Metadata []string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

// mappingFields are the single-column fields of a mapping, for validation.
func (m *RowMapping) fields() map[string]string {
	return map[string]string{"id": m.ID, "title": m.Title, "question": m.Question, "messages": m.Messages, "expected": m.Expected,
		"rubric": m.Rubric, "reference": m.Reference, "wrong_answers": m.Wrong, "match": m.Match, "category": m.Category,
		"split": m.Split, "user_turns": m.UserTurns, "attachments": m.Attachments, "grade_on": m.GradeOn}
}

var columnPathRE = regexp.MustCompile(`^[^\x00-\x1f]{1,200}$`)

// Validate checks every column path and the metadata list.
func (m *RowMapping) Validate() error {
	if m == nil {
		return nil
	}
	for name, p := range m.fields() {
		if p != "" && !columnPathRE.MatchString(p) {
			return fmt.Errorf("mapping.%s: a column path of at most 200 characters", name)
		}
	}
	if len(m.Metadata) > MaxRowMetadata {
		return fmt.Errorf("mapping.metadata: at most %d columns", MaxRowMetadata)
	}
	for _, p := range m.Metadata {
		if !columnPathRE.MatchString(p) || p == "-" {
			return fmt.Errorf("mapping.metadata: %q is not a column path", p)
		}
	}
	return nil
}

// Empty reports whether the mapping names no column.
func (m *RowMapping) Empty() bool {
	if m == nil {
		return true
	}
	for _, p := range m.fields() {
		if p != "" {
			return false
		}
	}
	return len(m.Metadata) == 0
}

// Row metadata and attachment limits.
const (
	MaxRowMetadata      = 32
	maxMetadataValue    = 200
	MaxRowAttachments   = 10
	MaxAttachmentBytes  = 5 << 20
	maxAttachmentName   = 100
	metadataKeyMaxRunes = 64
)

// nested aliases: paths auto-detection tries after the flat aliases.
var nestedAliases = map[string][]string{
	"question":    {"inputs.question", "inputs.input", "inputs.prompt", "inputs.query", "inputs.text", "inputs.instruction"},
	"messages":    {"messages", "inputs.messages", "conversation", "chat", "dialogue", "history", "inputs.conversation", "input"},
	"expected":    {"ideal", "target", "targets", "outputs.answer", "outputs.output", "outputs.expected", "outputs.expected_answer", "outputs.reference", "outputs.text", "outputs.result"},
	"user_turns":  {"user_turns", "turns", "follow_ups", "followups", "next_user_turns", "scripted_turns"},
	"attachments": {"attachments", "files", "images", "image", "image_url", "image_urls", "file_url", "file_urls", "inputs.attachments", "inputs.files", "inputs.images"},
	"grade_on":    {"grade_on", "grade"},
}

// lookup returns the value at path in rec: the literal key first (a CSV
// header such as "metadata.tier"), then the dotted path through nested
// objects and array indexes.
func lookup(rec map[string]any, p string) (any, bool) {
	p = strings.TrimPrefix(strings.TrimSpace(p), "$.")
	if p == "" || p == "-" {
		return nil, false
	}
	if v, ok := rec[strings.ToLower(p)]; ok {
		return v, true
	}
	var cur any = rec
	for _, seg := range strings.Split(p, ".") {
		switch x := cur.(type) {
		case map[string]any:
			v, ok := x[strings.ToLower(seg)]
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(x) {
				return nil, false
			}
			cur = x[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

// pick returns the value of field name: the mapped column when the mapping
// names one ("-" turns the field off), else the first present alias.
func pick(rec map[string]any, m *RowMapping, name string) (any, bool) {
	if m != nil {
		if p := m.fields()[name]; p == "-" {
			return nil, false
		} else if p != "" {
			return lookup(rec, p)
		}
	}
	for _, k := range rowAliases[name] {
		if v, ok := rec[k]; ok && !blank(v) {
			if name == "question" && k == "input" { // OpenAI Evals: input is the messages
				if _, arr := v.([]any); arr {
					continue
				}
			}
			if obj, ok := v.(map[string]any); ok && (k == "input" || k == "output") {
				if s, ok := singleText(obj); ok {
					return s, true
				}
				continue
			}
			return v, true
		}
	}
	for _, k := range nestedAliases[name] {
		if v, ok := lookup(rec, k); ok && !blank(v) {
			return v, true
		}
	}
	// LangSmith: inputs or outputs holding one text field.
	for _, k := range map[string][]string{"question": {"inputs"}, "expected": {"outputs"}}[name] {
		if obj, ok := rec[k].(map[string]any); ok {
			if s, ok := singleText(obj); ok {
				return s, true
			}
		} else if s, ok := rec[k].(string); ok && strings.TrimSpace(s) != "" {
			return s, true
		}
	}
	return nil, false
}

// singleText is the text of an object with exactly one text field.
func singleText(obj map[string]any) (string, bool) {
	var out string
	n := 0
	for _, v := range obj {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			out = s
			n++
		}
	}
	return out, n == 1
}

func blank(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(x) == ""
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

// pickText is pick as trimmed text.
func pickText(rec map[string]any, m *RowMapping, name string) string {
	v, ok := pick(rec, m, name)
	if !ok {
		return ""
	}
	return strings.TrimSpace(textOf(v))
}

// pickList is pick as a list: a JSON array, a JSON array in text, or text
// split on " | " or "||".
func pickList(rec map[string]any, m *RowMapping, name string) ([]string, bool) {
	v, ok := pick(rec, m, name)
	if !ok {
		return nil, false
	}
	return listOf(v)
}

// listOf turns a value into a list; arr reports whether it was an array.
func listOf(v any) (out []string, arr bool) {
	if a, ok := v.([]any); ok {
		for _, x := range a {
			if s := strings.TrimSpace(textOf(x)); s != "" {
				out = append(out, s)
			}
		}
		return out, true
	}
	s := textOf(v)
	if strings.HasPrefix(strings.TrimSpace(s), "[") {
		var a []any
		if json.Unmarshal([]byte(s), &a) == nil {
			return listOf(a)
		}
	}
	for _, x := range strings.Split(strings.ReplaceAll(s, "||", "|"), "|") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out, false
}

// chatRoles maps the role names datasets use to the three chat roles.
var chatRoles = map[string]string{
	"system": schemas.ChatRoleSystem, "developer": schemas.ChatRoleSystem,
	"user": schemas.ChatRoleUser, "human": schemas.ChatRoleUser, "customer": schemas.ChatRoleUser,
	"assistant": schemas.ChatRoleAssistant, "ai": schemas.ChatRoleAssistant, "bot": schemas.ChatRoleAssistant,
	"agent": schemas.ChatRoleAssistant, "model": schemas.ChatRoleAssistant, "gpt": schemas.ChatRoleAssistant,
}

// messagesOf reads a conversation: an array (or JSON text of one) of
// {role, content} objects. Content may be text or OpenAI-style parts; image
// and file parts become attachments by URL. ok is false when v is not a
// conversation.
func messagesOf(v any) (msgs []schemas.ChatMessage, urls []string, err error, ok bool) {
	if s, isText := v.(string); isText {
		var a []any
		if json.Unmarshal([]byte(strings.TrimSpace(s)), &a) != nil {
			return nil, nil, nil, false
		}
		v = a
	}
	a, isArr := v.([]any)
	if !isArr || len(a) == 0 {
		return nil, nil, nil, false
	}
	for i, it := range a {
		obj, isObj := it.(map[string]any)
		if !isObj {
			return nil, nil, nil, false
		}
		obj = lowerKeys(obj)
		rawRole := strings.ToLower(strings.TrimSpace(textOf(obj["role"])))
		if rawRole == "" {
			rawRole = strings.ToLower(strings.TrimSpace(textOf(obj["from"]))) // ShareGPT
		}
		role, known := chatRoles[rawRole]
		if !known {
			if rawRole == "" {
				return nil, nil, nil, false
			}
			return nil, nil, fmt.Errorf("message %d has role %q: want system, user or assistant", i+1, rawRole), true
		}
		content := obj["content"]
		if content == nil {
			content = obj["value"] // ShareGPT
		}
		text, parts := contentText(content)
		urls = append(urls, parts...)
		if strings.TrimSpace(text) == "" && len(parts) == 0 {
			continue // an empty turn, such as a tool-call placeholder
		}
		msgs = append(msgs, schemas.ChatMessage{Role: role, Content: text})
	}
	return msgs, urls, nil, len(msgs) > 0
}

// contentText is a message content as text, with the URLs of its image and
// file parts.
func contentText(c any) (string, []string) {
	switch x := c.(type) {
	case string:
		return x, nil
	case []any:
		var texts, urls []string
		for _, p := range x {
			part, ok := p.(map[string]any)
			if !ok {
				texts = append(texts, textOf(p))
				continue
			}
			switch textOf(part["type"]) {
			case "text", "input_text", "output_text", "":
				if t := textOf(part["text"]); t != "" {
					texts = append(texts, t)
				}
			case "image_url", "input_image":
				u := part["image_url"]
				if obj, ok := u.(map[string]any); ok {
					u = obj["url"]
				}
				if s := textOf(u); s != "" {
					urls = append(urls, s)
				}
			case "file", "input_file":
				if s := textOf(part["file_url"]); s != "" {
					urls = append(urls, s)
				}
			}
		}
		return strings.Join(texts, "\n"), urls
	}
	return textOf(c), nil
}

// metadataOf collects a row's metadata: a metadata object, columns named
// metadata.<key> or metadata:<key>, and the mapping's metadata columns.
func metadataOf(rec map[string]any, m *RowMapping) (map[string]string, string) {
	out := map[string]string{}
	add := func(k string, v any) string {
		key := metadataKey(k)
		if key == "" || blank(v) {
			return ""
		}
		if _, dup := out[key]; !dup && len(out) >= MaxRowMetadata {
			return fmt.Sprintf("more than %d metadata keys", MaxRowMetadata)
		}
		s := strings.Join(strings.Fields(textOf(v)), " ")
		if utf8.RuneCountInString(s) > maxMetadataValue {
			return fmt.Sprintf("metadata %s is longer than %d characters", key, maxMetadataValue)
		}
		out[key] = s
		return ""
	}
	keys := make([]string, 0, len(rec))
	for k := range rec {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := rec[k]
		switch {
		case k == "metadata":
			if obj, ok := v.(map[string]any); ok {
				sub := make([]string, 0, len(obj))
				for sk := range obj {
					sub = append(sub, sk)
				}
				sort.Strings(sub)
				for _, sk := range sub {
					if _, nested := obj[sk].(map[string]any); nested {
						continue
					}
					if msg := add(sk, obj[sk]); msg != "" {
						return nil, msg
					}
				}
			}
		case strings.HasPrefix(k, "metadata.") || strings.HasPrefix(k, "metadata:"):
			if msg := add(k[len("metadata."):], v); msg != "" {
				return nil, msg
			}
		}
	}
	if m != nil {
		for _, p := range m.Metadata {
			if v, ok := lookup(rec, p); ok {
				seg := p
				if i := strings.LastIndexAny(seg, ".:"); i >= 0 {
					seg = seg[i+1:]
				}
				if msg := add(seg, v); msg != "" {
					return nil, msg
				}
			}
		}
	}
	if len(out) == 0 {
		return nil, ""
	}
	return out, ""
}

var metadataKeyRE = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

// metadataKey is k as a metadata key: letters, digits, '_', '.', '-'.
func metadataKey(k string) string {
	k = strings.Trim(metadataKeyRE.ReplaceAllString(strings.TrimSpace(k), "_"), "_.-")
	if utf8.RuneCountInString(k) > metadataKeyMaxRunes {
		k = string([]rune(k)[:metadataKeyMaxRunes])
	}
	return k
}

// RowAttachment is a file or image a row names: a link, or the name of a
// file uploaded with the import.
type RowAttachment struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

// attachmentsOf reads a row's attachments: links or uploaded file names, as
// a list, objects {name, url} or {path}, or text split on "|".
func attachmentsOf(v any) []RowAttachment {
	var items []any
	switch x := v.(type) {
	case []any:
		items = x
	case map[string]any:
		items = []any{x}
	default:
		vals, _ := listOf(v)
		for _, s := range vals {
			items = append(items, s)
		}
	}
	var out []RowAttachment
	for _, it := range items {
		var name, u string
		switch x := it.(type) {
		case map[string]any:
			x = lowerKeys(x)
			u = strings.TrimSpace(textOf(x["url"]))
			name = strings.TrimSpace(textOf(x["name"]))
			if name == "" {
				name = strings.TrimSpace(textOf(x["path"]))
			}
		default:
			s := strings.TrimSpace(textOf(x))
			if isURL(s) {
				u = s
			} else {
				name = s
			}
		}
		if u == "" && name == "" {
			continue
		}
		if name == "" {
			name = path.Base(strings.SplitN(strings.SplitN(u, "?", 2)[0], "#", 2)[0])
		}
		out = append(out, RowAttachment{Name: name, URL: u})
	}
	return out
}

func isURL(s string) bool {
	return strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://")
}

var attachmentNameRE = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// AttachmentName is a safe file name for an attachment.
func AttachmentName(name string) string {
	name = path.Base(strings.ReplaceAll(strings.TrimSpace(name), "\\", "/"))
	name = strings.Trim(attachmentNameRE.ReplaceAllString(name, "_"), "._")
	if len(name) > maxAttachmentName {
		ext := path.Ext(name)
		if len(ext) > 10 {
			ext = ""
		}
		name = name[:maxAttachmentName-len(ext)] + ext
	}
	if name == "" || name == "." {
		return ""
	}
	return name
}

// MediaTypeOf guesses a media type from the file name.
func MediaTypeOf(name string) string {
	if t := mime.TypeByExtension(strings.ToLower(path.Ext(name))); t != "" {
		return strings.SplitN(t, ";", 2)[0]
	}
	return "application/octet-stream"
}

// RowColumn is one column a file holds: its path, an example value and its
// kind (text, number, list, messages or object).
type RowColumn struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Example string `json:"example,omitempty"`
	Filled  int    `json:"filled"` // rows (of those sampled) with a value
}

// maxColumns caps Columns.
const maxColumns = 200

// Columns lists the column paths of recs (the first 200 records), objects
// flattened to dotted paths up to three levels deep, in order of first
// appearance.
func Columns(recs []map[string]any) []RowColumn {
	by := map[string]*RowColumn{}
	var order []string
	var walk func(prefix string, v any, depth int)
	walk = func(prefix string, v any, depth int) {
		kind := "text"
		switch x := v.(type) {
		case map[string]any:
			if depth < 3 && len(x) > 0 {
				keys := make([]string, 0, len(x))
				for k := range x {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					walk(prefix+"."+strings.ToLower(k), x[k], depth+1)
				}
				return
			}
			kind = "object"
		case []any:
			kind = "list"
			if _, _, _, ok := messagesOf(x); ok {
				kind = "messages"
			}
		case float64, bool:
			kind = "number"
		case string:
			if _, _, _, ok := messagesOf(x); ok {
				kind = "messages"
			}
		}
		p := strings.TrimPrefix(prefix, ".")
		c := by[p]
		if c == nil {
			if len(by) >= maxColumns {
				return
			}
			c = &RowColumn{Path: p, Kind: kind}
			by[p] = c
			order = append(order, p)
		}
		if !blank(v) {
			c.Filled++
			if c.Example == "" {
				c.Example = preview(textOf(v))
				if len(c.Example) > 80 {
					c.Example = string([]rune(c.Example)[:79]) + "…"
				}
			}
		}
	}
	for i, rec := range recs {
		if i >= 200 {
			break
		}
		keys := make([]string, 0, len(rec))
		for k := range rec {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			walk("."+k, rec[k], 1)
		}
	}
	out := make([]RowColumn, 0, len(order))
	for _, p := range order {
		out = append(out, *by[p])
	}
	return out
}

// DetectMapping is the mapping auto-detection settled on for recs: for each
// field, the column the first record that has the field reads it from.
func DetectMapping(recs []map[string]any) RowMapping {
	var m RowMapping
	set := func(dst *string, name string) {
		for i, rec := range recs {
			if i >= 50 {
				return
			}
			if p := detectedPath(rec, name); p != "" {
				*dst = p
				return
			}
		}
	}
	set(&m.ID, "id")
	set(&m.Title, "title")
	set(&m.Messages, "messages")
	set(&m.Question, "question")
	set(&m.Expected, "expected")
	set(&m.Rubric, "rubric")
	set(&m.Reference, "reference")
	set(&m.Wrong, "wrong")
	set(&m.Match, "match")
	set(&m.Category, "category")
	set(&m.Split, "split")
	set(&m.UserTurns, "user_turns")
	set(&m.Attachments, "attachments")
	if m.Messages != "" && m.Messages == m.Question {
		m.Question = ""
	}
	return m
}

// detectedPath is the path pick would read field name from in rec.
func detectedPath(rec map[string]any, name string) string {
	for _, k := range rowAliases[name] {
		if v, ok := rec[k]; ok && !blank(v) {
			if name == "question" && k == "input" {
				if _, arr := v.([]any); arr {
					continue
				}
			}
			if obj, ok := v.(map[string]any); ok && (k == "input" || k == "output") {
				for sk, sv := range obj {
					if s, ok := sv.(string); ok && strings.TrimSpace(s) != "" {
						return k + "." + sk
					}
				}
				continue
			}
			return k
		}
	}
	for _, k := range nestedAliases[name] {
		if v, ok := lookup(rec, k); ok && !blank(v) {
			if name == "messages" {
				if _, _, _, ok := messagesOf(v); !ok {
					continue
				}
			}
			return k
		}
	}
	for _, k := range map[string][]string{"question": {"inputs"}, "expected": {"outputs"}}[name] {
		if obj, ok := rec[k].(map[string]any); ok {
			for sk, sv := range obj {
				if s, ok := sv.(string); ok && strings.TrimSpace(s) != "" {
					if _, one := singleText(obj); one {
						return k + "." + sk
					}
				}
			}
		} else if s, ok := rec[k].(string); ok && strings.TrimSpace(s) != "" {
			return k
		}
	}
	if name == "messages" { // OpenAI Evals: input is the conversation
		if v, ok := rec["input"]; ok {
			if _, _, _, ok := messagesOf(v); ok {
				return "input"
			}
		}
	}
	return ""
}

// sortedKeys of a string map.
func sortedMetaKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
