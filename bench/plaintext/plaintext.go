// Package plaintext cleans text the hosted server sends to a browser: no
// server path, command line flag or internal organization id leaves the
// server (wave 4, F1.3). It is applied once, at the sinks (API errors, served
// job errors and log lines, live events, served receipts and run steps).
// Producers keep their full text for server logs and the command line.
package plaintext

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// DefaultRoots lists the server folders whose paths are cut from served
// text. It is nil in local mode and in the command line tool, where nothing
// is cleaned; the hosted server sets it at start.
var DefaultRoots func() []string

// Hosted is s cleaned with DefaultRoots, or s unchanged when DefaultRoots is
// nil.
func Hosted(s string) string {
	if DefaultRoots == nil {
		return s
	}
	return Clean(s, DefaultRoots()...)
}

// CleanErr is Clean of err's text.
func CleanErr(err error, roots ...string) string {
	if err == nil {
		return ""
	}
	return Clean(err.Error(), roots...)
}

var (
	// absPathRe is an absolute path under a usual server folder, after the
	// start of the text, a space, a quote, a bracket, "=" or ":".
	absPathRe = regexp.MustCompile(`(^|[\s"'(=:])(/(?:var|tmp|home|opt|srv|data|run|usr|etc|root)/[^\s"'),;]*)`)
	// orgFolderRe finds an organization folder inside a path.
	orgFolderRe = regexp.MustCompile(`(?:^|/)(?:projects|orgs)/[^/]+/`)

	slotRe  = regexp.MustCompile(`slot org_org[0-9a-z]{26}_([a-z0-9-]+)_([0-9]+)`)
	orgIDRe = regexp.MustCompile(`org_(?:org)?[0-9a-z]{26}`)

	flagHintRe = regexp.MustCompile(`pass -(\w[\w-]*) (\S+) or leave -(\w[\w-]*) out`)
	// flagWordRe is a flag after pass, use, with or run, with its value and
	// any flags that follow it ("run -resume e_x -total-usd 5").
	flagWordRe = regexp.MustCompile(`\s?\b(?:pass|use|with|run) -[a-z][a-z-]+(?: [^\s-]\S*)?(?: -[a-z][a-z-]+(?: [^\s-]\S*)?)*`)
	// commandRe is a command line; a word joined to the name by "-", "." or
	// "/" (the compose key x-heimdall, a path) is not one.
	commandRe = regexp.MustCompile(`(^|[^\w./-])(?:bifrost-bench|heimdall) [a-z-]+(?: [a-z-]+)*(?: -\S+)*`)

	stutterRe  = regexp.MustCompile(`^(\w[\w ]{0,30}): `)
	planIDRe   = regexp.MustCompile(`^plan [^:]+: `)
	repeatOpRe = regexp.MustCompile(`(\S+): (?:open|stat|lstat) (\S+): `)
	notExistRe = regexp.MustCompile(`\b(?:open|stat|lstat) (\S+): no such file or directory`)
)

// Clean applies, in order: known roots, other absolute paths, organization
// ids and slot names, command line words, repeated prefixes, and the
// operating system's file error words.
func Clean(s string, roots ...string) string {
	if s == "" {
		return s
	}
	s = cutRoots(s, roots)
	s = absPathRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := absPathRe.FindStringSubmatch(m)
		return sub[1] + shortPath(sub[2])
	})
	s = slotRe.ReplaceAllString(s, "provider slot $1 $2")
	s = orgIDRe.ReplaceAllString(s, "your organization")
	s = flagHintRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := flagHintRe.FindStringSubmatch(m)
		if sub[1] != sub[3] {
			return m
		}
		return "choose " + sub[2] + " or leave the setting on Automatic"
	})
	s = flagWordRe.ReplaceAllString(s, "")
	s = commandRe.ReplaceAllString(s, "${1}the command line tool")
	s = collapseStutter(s)
	s = planIDRe.ReplaceAllString(s, "")
	s = repeatOpRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := repeatOpRe.FindStringSubmatch(m)
		if sub[1] != sub[2] {
			return m
		}
		return sub[1] + ": "
	})
	s = notExistRe.ReplaceAllString(s, "$1 does not exist")
	s = strings.ReplaceAll(s, ": not a directory", ": that is a file, not a folder")
	return s
}

// cutRoots removes every known root and a following organization folder,
// longest root first.
func cutRoots(s string, roots []string) string {
	clean := make([]string, 0, len(roots))
	for _, r := range roots {
		if r == "" || !filepath.IsAbs(r) {
			continue
		}
		r = filepath.ToSlash(filepath.Clean(r))
		if r == "/" {
			continue
		}
		clean = append(clean, r)
	}
	sort.SliceStable(clean, func(i, j int) bool { return len(clean[i]) > len(clean[j]) })
	for _, r := range clean {
		re := regexp.MustCompile(`(^|[^\w./-])` + regexp.QuoteMeta(r) + `/(?:(?:projects|orgs)/[^/\s"']+/)?`)
		s = re.ReplaceAllString(s, "${1}")
	}
	return s
}

// shortPath keeps the last two parts of a path inside an organization
// folder; any other server path becomes "a file on the server".
func shortPath(p string) string {
	if !orgFolderRe.MatchString(p) {
		return "a file on the server"
	}
	parts := strings.Split(strings.TrimSuffix(p, "/"), "/")
	if len(parts) > 2 {
		parts = parts[len(parts)-2:]
	}
	return strings.Join(parts, "/")
}

// collapseStutter drops a leading "word: " while the rest starts with the
// same word, so "plan: plan: plan e_x: ..." becomes "plan e_x: ...".
func collapseStutter(s string) string {
	for {
		m := stutterRe.FindStringSubmatch(s)
		if m == nil {
			return s
		}
		rest := s[len(m[0]):]
		if !strings.HasPrefix(rest, m[1]+": ") && !strings.HasPrefix(rest, m[1]+" ") {
			return s
		}
		s = rest
	}
}
