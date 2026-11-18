package suite

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/maximhq/bifrost/bench/utils"
)

// Secret scan limits.
const (
	maxScanBytes    = 5 << 20 // larger files are not scanned
	minEntropyBits  = 3.5     // per character, for *_TOKEN= / *_KEY= values
	minSecretLength = 16
)

// SecretFinding is one secret-looking string in a task file. The matched
// value itself is never kept: Masked shows only the fixed prefix of the
// pattern (or the variable name) followed by asterisks.
type SecretFinding struct {
	Path   string `json:"path"` // slash-separated, relative to the scanned folder
	Line   int    `json:"line"`
	Kind   string `json:"kind"`
	Masked string `json:"masked"`
}

// String renders the finding as "path:line: possible <kind> (<masked>)".
func (f SecretFinding) String() string {
	return fmt.Sprintf("%s:%d: possible %s (%s)", f.Path, f.Line, f.Kind, f.Masked)
}

// secretPatterns are the patterns of docs/bring-your-own-design.md, shared
// with the run seal (utils.SecretTokenPatterns). High-entropy *_TOKEN= and
// *_KEY= assignments are matched separately.
var secretPatterns = utils.SecretTokenPatterns

var (
	assignRE      = regexp.MustCompile(`(?i)\b([a-z0-9_]*(?:_token|_key))["']?\s*[=:]\s*["']?([^\s"'#,;]+)`)
	placeholderRE = regexp.MustCompile(`(?i)replace|example|changeme|change-me|your[_-]|placeholder|dummy|\$\{|\{\{|^<`)
)

// ScanSecrets walks dir and reports every line that matches a secret
// pattern. Symlinks are not followed, .git folders and binary files are
// skipped, and files over 5 MB are not read.
func ScanSecrets(dir string) ([]SecretFinding, error) {
	var out []SecretFinding
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > maxScanBytes {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out = append(out, ScanSecretsIn(rel(dir, path), data)...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan %s for secrets: %w", dir, err)
	}
	return out, nil
}

// ScanSecretsIn reports the secret-looking strings in one file's content.
// Binary content (a NUL byte in the first 8 KB) is skipped. It is pure.
func ScanSecretsIn(name string, data []byte) []SecretFinding {
	if bytes.IndexByte(data[:min(len(data), 8192)], 0) >= 0 {
		return nil
	}
	var out []SecretFinding
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), maxScanBytes)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		for _, p := range secretPatterns {
			for _, m := range p.RE.FindAllString(line, -1) {
				out = append(out, SecretFinding{Path: name, Line: n, Kind: p.Kind, Masked: mask(m, p.Keep)})
			}
		}
		for _, m := range assignRE.FindAllStringSubmatch(line, -1) {
			if v := m[2]; len(v) >= minSecretLength && !placeholderRE.MatchString(v) && entropy(v) >= minEntropyBits {
				out = append(out, SecretFinding{Path: name, Line: n, Kind: "high-entropy " + strings.ToUpper(m[1]) + " value", Masked: m[1] + "=****"})
			}
		}
	}
	return out
}

func mask(s string, keep int) string {
	keep = min(keep, len(s))
	return s[:keep] + strings.Repeat("*", 4)
}

// entropy is the Shannon entropy of s in bits per character.
func entropy(s string) float64 {
	counts := map[rune]int{}
	n := 0
	for _, r := range s {
		counts[r]++
		n++
	}
	var h float64
	for _, c := range counts {
		p := float64(c) / float64(n)
		h -= p * math.Log2(p)
	}
	return h
}
