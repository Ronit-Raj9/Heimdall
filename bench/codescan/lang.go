package codescan

import (
	"path"
	"regexp"
	"strings"
)

// Languages the rules know. A file in none of them is still read by the
// secret rules, which apply to every text file.
const (
	LangPython     = "python"
	LangJavaScript = "javascript"
	LangTypeScript = "typescript"
	LangGo         = "go"
	LangShell      = "shell"
	LangDockerfile = "dockerfile"
	LangYAML       = "yaml"
	LangMake       = "make"
	LangConfig     = "config" // .env, .ini, .toml, .properties, .cfg, .conf
	langAny        = "any"
)

var shebangRE = regexp.MustCompile(`^#!\s*(?:/usr/bin/env\s+)?(?:/\S*/)?(sh|bash|zsh|dash|ksh|python[0-9.]*|node)\b`)

// langOf names the language of a file from its path, and for a file with no
// telling extension from the shebang on its first line.
func langOf(p, firstLine string) string {
	base := path.Base(p)
	lower := strings.ToLower(base)
	switch {
	case lower == "dockerfile" || strings.HasPrefix(lower, "dockerfile.") || strings.HasSuffix(lower, ".dockerfile") || lower == "containerfile":
		return LangDockerfile
	case lower == "makefile" || lower == "gnumakefile" || strings.HasSuffix(lower, ".mk"):
		return LangMake
	case lower == ".env" || strings.HasPrefix(lower, ".env."):
		return LangConfig
	}
	switch strings.ToLower(path.Ext(base)) {
	case ".py", ".pyw":
		return LangPython
	case ".js", ".mjs", ".cjs", ".jsx":
		return LangJavaScript
	case ".ts", ".tsx", ".mts", ".cts":
		return LangTypeScript
	case ".go":
		return LangGo
	case ".sh", ".bash", ".zsh", ".ksh":
		return LangShell
	case ".yml", ".yaml":
		return LangYAML
	case ".env", ".ini", ".toml", ".properties", ".cfg", ".conf":
		return LangConfig
	case "":
		if m := shebangRE.FindStringSubmatch(firstLine); m != nil {
			switch {
			case strings.HasPrefix(m[1], "python"):
				return LangPython
			case m[1] == "node":
				return LangJavaScript
			default:
				return LangShell
			}
		}
	}
	return ""
}

// testDirs are path segments that hold tests, fixtures and test data.
var testDirs = map[string]bool{
	"test": true, "tests": true, "__tests__": true, "testdata": true, "testing": true,
	"spec": true, "specs": true, "fixtures": true, "__fixtures__": true, "e2e": true,
}

// isTestPath reports whether p is a test file or lives in a test folder:
// test_*.py, *_test.py, conftest.py, *_test.go, *.test.ts, *.spec.js and the
// folders in testDirs.
func isTestPath(p string) bool {
	segs := strings.Split(p, "/")
	for _, s := range segs[:len(segs)-1] {
		if testDirs[strings.ToLower(s)] {
			return true
		}
	}
	base := strings.ToLower(segs[len(segs)-1])
	stem := strings.TrimSuffix(base, path.Ext(base))
	switch {
	case base == "conftest.py":
		return true
	case strings.HasSuffix(base, ".py") && (strings.HasPrefix(base, "test_") || strings.HasSuffix(stem, "_test")):
		return true
	case strings.HasSuffix(base, "_test.go"):
		return true
	case strings.Contains(base, ".test.") || strings.Contains(base, ".spec."):
		return true
	}
	return false
}

// hashComments are the languages whose comments start with #.
var hashComments = map[string]bool{LangPython: true, LangShell: true, LangDockerfile: true, LangYAML: true, LangMake: true, LangConfig: true}

// slashComments are the languages whose comments start with //.
var slashComments = map[string]bool{LangJavaScript: true, LangTypeScript: true, LangGo: true}

// codeOf returns the line with comments removed and the inside of every
// string literal replaced by underscores, byte for byte, so offsets in the
// result are offsets in the original line. Quotes are kept. It reads one line
// at a time: a string or comment that spans lines is not followed, which is a
// documented limit of the pattern rules. A line that is only a comment comes
// back blank.
func codeOf(lang, line string) string {
	code, _ := splitComment(lang, line)
	return code
}

// bareOf returns the line with its comment cut off and string literals kept.
func bareOf(lang, line string) string {
	_, cut := splitComment(lang, line)
	return line[:cut]
}

// splitComment returns codeOf's result and the offset where the line's
// comment starts (len(line) when it has none).
func splitComment(lang, line string) (string, int) {
	trim := strings.TrimSpace(line)
	if slashComments[lang] && (strings.HasPrefix(trim, "*") || strings.HasPrefix(trim, "/*")) {
		return strings.Repeat(" ", len(line)), 0
	}
	b := []byte(line)
	var quote byte
	backtick := lang == LangJavaScript || lang == LangTypeScript || lang == LangGo || lang == LangShell || lang == LangMake || lang == LangDockerfile
	for i := 0; i < len(b); i++ {
		c := b[i]
		if quote != 0 {
			switch {
			case c == '\\' && quote != '`' && i+1 < len(b):
				b[i], b[i+1] = '_', '_'
				i++
			case c == quote:
				quote = 0
			default:
				b[i] = '_'
			}
			continue
		}
		switch {
		case c == '"' || c == '\'' || (c == '`' && backtick):
			if c == '\'' && (lang == LangYAML || lang == LangConfig || lang == LangDockerfile || lang == LangShell || lang == LangMake) && i > 0 && isWordByte(b[i-1]) {
				continue // an apostrophe inside a bare word
			}
			quote = c
		case c == '#' && hashComments[lang] && (i == 0 || b[i-1] == ' ' || b[i-1] == '\t' || lang == LangPython):
			if lang == LangShell && i > 0 && b[i-1] == '$' {
				continue
			}
			return string(blankFrom(b, i)), i
		case c == '/' && slashComments[lang] && i+1 < len(b) && b[i+1] == '/':
			if i > 0 && b[i-1] == ':' { // a URL such as https://, not a comment
				continue
			}
			return string(blankFrom(b, i)), i
		}
	}
	return string(b), len(b)
}

// blankFrom replaces b[i:] with spaces.
func blankFrom(b []byte, i int) []byte {
	for j := i; j < len(b); j++ {
		b[j] = ' '
	}
	return b
}

func isWordByte(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
