package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"
)

// bifrostBenchCommand matches the old CLI name followed by a subcommand.
var bifrostBenchCommand = regexp.MustCompile(`bifrost-bench [a-z]`)

// TestNoBifrostBenchInUserText (N78, decision D1): the CLI's user-facing name
// is heimdall, so no English UI string tells a person to run
// "bifrost-bench <subcommand>". Image names and file paths are not UI text.
func TestNoBifrostBenchInUserText(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "ui", "locales", "en", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no locale files: %v", err)
	}
	var bad []string
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var doc any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		walkStrings(doc, filepath.Base(f), func(key, value string) {
			if bifrostBenchCommand.MatchString(value) {
				bad = append(bad, key+": "+value)
			}
		})
	}
	sort.Strings(bad)
	for _, b := range bad {
		t.Errorf("user text names the old CLI (use heimdall): %s", b)
	}
}

// walkStrings calls fn with the dotted key and value of every string in v.
func walkStrings(v any, key string, fn func(key, value string)) {
	switch x := v.(type) {
	case string:
		fn(key, x)
	case map[string]any:
		for k, c := range x {
			walkStrings(c, key+"."+k, fn)
		}
	case []any:
		for _, c := range x {
			walkStrings(c, key, fn)
		}
	}
}
