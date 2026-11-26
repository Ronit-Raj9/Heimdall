package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// categoriesFile is the task category sidecar at the suite root (written by
// package taskcat; the engine reads it with encoding/json only).
const categoriesFile = ".suite.categories.json"

// taskCategories reads the category sidecar of a suite: task id to its
// category, or "category (unsure)" for a label that is a guess. A missing or
// bad file is nil.
func taskCategories(root string) map[string]string {
	if root == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(root, categoriesFile))
	if err != nil {
		return nil
	}
	var labels map[string]struct {
		Category string `json:"category"`
		Sure     bool   `json:"sure"`
	}
	if json.Unmarshal(data, &labels) != nil {
		return nil
	}
	out := make(map[string]string, len(labels))
	for id, l := range labels {
		if l.Category == "" {
			continue
		}
		v := l.Category
		if !l.Sure {
			v += " (unsure)"
		}
		out[id] = v
	}
	return out
}
