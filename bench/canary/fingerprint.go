package canary

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Fingerprint is the hash of what a canary run freezes and a baseline must
// share for the comparison to mean anything: the suite lock and evaluator
// fingerprints, each arm's resolved agent config hash and image, and the
// counted slots (task, condition, trial, arm) with their task hashes.
// Experiment id, creation time, seed, arm order and the spend cap are left
// out, so every run of an unchanged definition has the same fingerprint.
// rename maps an injected condition back to the watched one.
func Fingerprint(m *schemas.Manifest, rename map[string]string) string {
	type arm struct {
		ID         string `json:"id"`
		ConfigHash string `json:"config_hash"`
		ImageID    string `json:"image_id,omitempty"`
	}
	type slot struct {
		Task      string `json:"task"`
		TaskHash  string `json:"task_hash,omitempty"`
		Condition string `json:"condition"`
		Trial     int    `json:"trial"`
		Arm       string `json:"arm"`
	}
	v := struct {
		Suite           string `json:"suite"`
		SuiteLockHash   string `json:"suite_lock_hash"`
		FingerprintHash string `json:"fingerprint_hash,omitempty"`
		Arms            []arm  `json:"arms"`
		Slots           []slot `json:"slots"`
		Smoke           bool   `json:"smoke,omitempty"`
	}{Suite: m.SuiteID + "@" + m.SuiteVersion, SuiteLockHash: m.SuiteLockHash, FingerprintHash: m.FingerprintHash, Smoke: m.Experiment.Smoke}
	for _, id := range sortedKeys(m.Arms) {
		a := m.Arms[id]
		v.Arms = append(v.Arms, arm{ID: id, ConfigHash: a.ConfigHash, ImageID: a.ImageID})
	}
	for _, s := range m.Slots {
		if !s.Counted {
			continue
		}
		cond := s.Condition
		if to, ok := rename[cond]; ok {
			cond = to
		}
		v.Slots = append(v.Slots, slot{Task: s.Task, TaskHash: s.TaskHash, Condition: cond, Trial: s.Trial, Arm: s.Arm})
	}
	slices.SortFunc(v.Slots, func(a, b slot) int {
		return cmpStrings(a.Task+"|"+a.Condition+"|"+fmt.Sprint(a.Trial)+"|"+a.Arm, b.Task+"|"+b.Condition+"|"+fmt.Sprint(b.Trial)+"|"+b.Arm)
	})
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// CountedSlots counts a manifest's counted slots.
func CountedSlots(m *schemas.Manifest) int {
	n := 0
	for _, s := range m.Slots {
		if s.Counted {
			n++
		}
	}
	return n
}

func cmpStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
