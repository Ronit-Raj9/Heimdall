// Package taskgates holds the cheap quality checks a task set passes before
// it counts (INFRA_I3 section 4.5): duplicate tasks (exact and near, by
// MinHash), contamination against the built-in library (13-gram overlap),
// and the automatic held-out split with its canary. They read text and
// task files only: no model call, no container, no network.
package taskgates

import (
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/maximhq/bifrost/bench/graders"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Item is the text of one task the gates compare: what the agent is asked
// and what counts as the answer.
type Item struct {
	Suite       string `json:"suite"`
	Task        string `json:"task"`
	Instruction string `json:"-"`
	Expected    string `json:"-"`
}

// Key is "suite/task".
func (it Item) Key() string { return it.Suite + "/" + it.Task }

// Text is the instruction and the expected answer, the text the gates
// compare.
func (it Item) Text() string {
	if it.Expected == "" {
		return it.Instruction
	}
	return it.Instruction + "\n" + it.Expected
}

// maxInstructionBytes caps what is read of one instruction file.
const maxInstructionBytes = 256 << 10

// ItemsOf reads the gate text of every task of s: the instruction files
// (the task's and each session's, canary lines removed) and, for an answer
// task, the expected answers. A file that can not be read leaves its part
// empty: the gates compare what is there, and lint reports the missing file.
func ItemsOf(s *schemas.Suite) []Item {
	out := make([]Item, 0, len(s.Tasks))
	for _, t := range s.Tasks {
		out = append(out, ItemOf(s, t))
	}
	return out
}

// ItemOf is ItemsOf for one task.
func ItemOf(s *schemas.Suite, t *schemas.Task) Item {
	it := Item{Suite: s.ID, Task: t.ID}
	var parts []string
	rels := []string{t.Instruction}
	for _, sess := range t.Sessions {
		rels = append(rels, sess.Instruction)
	}
	for _, rel := range rels {
		if rel == "" || t.Dir == "" {
			continue
		}
		abs, err := utils.ResolveInside(t.Dir, rel)
		if err != nil {
			continue
		}
		data, err := readCapped(abs)
		if err != nil {
			continue
		}
		parts = append(parts, StripCanary(string(data)))
	}
	it.Instruction = strings.Join(parts, "\n")
	if utils.OutputKind(s, t) == schemas.OutputAnswer && t.Dir != "" {
		if ans, err := graders.LoadAnswerSpec(t); err == nil && ans != nil {
			it.Expected = strings.Join(ans.Expected, "\n")
		}
	}
	return it
}

func readCapped(path string) ([]byte, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, maxInstructionBytes)
	n, err := f.Read(buf)
	if n > 0 {
		return buf[:n], nil
	}
	return nil, err
}

// StripCanary removes canary lines, so a stamped and an unstamped copy of
// the same task compare equal.
func StripCanary(text string) string {
	var b strings.Builder
	for _, line := range strings.SplitAfter(text, "\n") {
		if _, ok := utils.CanaryLineGUID(line); ok {
			continue
		}
		b.WriteString(line)
	}
	return b.String()
}

// Words is text lower-cased with every run of non-letters and non-digits
// turned into one break: the normal form every gate compares.
func Words(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// Normalize is Words joined by single spaces.
func Normalize(text string) string { return strings.Join(Words(text), " ") }
