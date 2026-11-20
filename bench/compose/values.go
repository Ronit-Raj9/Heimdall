package compose

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// splitShell splits a command string into words the way compose does for
// the string form of command and entrypoint: POSIX shell quoting (single
// quotes, double quotes with backslash escapes, backslash outside quotes),
// no expansion. It is pure.
func splitShell(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	inWord := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return nil, errors.New("unterminated single quote")
			}
			cur.WriteString(s[i+1 : i+1+j])
			i += j + 1
			inWord = true
		case c == '"':
			i++
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) && strings.IndexByte("\\\"$`\n", s[i+1]) >= 0 {
					i++
				}
				cur.WriteByte(s[i])
			}
			if i >= len(s) {
				return nil, errors.New("unterminated double quote")
			}
			inWord = true
		case c == '\\':
			if i+1 < len(s) {
				i++
				cur.WriteByte(s[i])
			}
			inWord = true
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteByte(c)
			inWord = true
		}
	}
	if inWord {
		out = append(out, cur.String())
	}
	return out, nil
}

// parseDuration reads a compose duration ("1m30s", "10s", "500ms", "1h").
// It is pure.
func parseDuration(s string) (time.Duration, error) {
	d, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("duration %q: want a value such as 10s or 1m30s", s)
	}
	if d < 0 {
		return 0, fmt.Errorf("duration %q is negative", s)
	}
	return d, nil
}

// parseBytes reads a compose byte value: a plain number of bytes, or a
// number with a unit b, k, m or g (kb, mb, gb and upper case accepted).
// It is pure.
func parseBytes(s string) (int64, error) {
	v := strings.ToLower(strings.TrimSpace(s))
	mult := float64(1)
	for _, u := range []struct {
		suffix string
		mult   float64
	}{{"gb", 1 << 30}, {"mb", 1 << 20}, {"kb", 1 << 10}, {"g", 1 << 30}, {"m", 1 << 20}, {"k", 1 << 10}, {"b", 1}} {
		if strings.HasSuffix(v, u.suffix) {
			v, mult = strings.TrimSuffix(v, u.suffix), u.mult
			break
		}
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || n < 0 || math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, fmt.Errorf("size %q: want a number with an optional unit b, k, m or g", s)
	}
	return int64(n * mult), nil
}

// portRange parses "4000", "4000/tcp" or "4000-4002" into its ports. Only
// tcp is accepted: the bench's readiness check and the run network are tcp.
// It is pure.
func portRange(s string) ([]int, error) {
	v, proto, _ := strings.Cut(strings.TrimSpace(s), "/")
	if proto != "" && proto != "tcp" {
		return nil, fmt.Errorf("port %q: only tcp ports are supported", s)
	}
	lo, hi, isRange := strings.Cut(v, "-")
	a, err := strconv.Atoi(lo)
	if err != nil || a < 1 || a > 65535 {
		return nil, fmt.Errorf("port %q is not a port number", s)
	}
	b := a
	if isRange {
		if b, err = strconv.Atoi(hi); err != nil || b < a || b > 65535 {
			return nil, fmt.Errorf("port range %q is invalid", s)
		}
	}
	if b-a > 64 {
		return nil, fmt.Errorf("port range %q is wider than 64 ports", s)
	}
	out := make([]int, 0, b-a+1)
	for p := a; p <= b; p++ {
		out = append(out, p)
	}
	return out, nil
}

// portMapping is one entry of a service's ports: the container side (what
// the bench keeps, as an internal port) and the published host side (what it
// drops).
type portMapping struct {
	container []int
	published string
}

// parseShortPort reads the short ports syntax: "3000", "3000:3000",
// "127.0.0.1:3000:3000", "3000-3001:3000-3001", "3000/tcp". It is pure.
func parseShortPort(s string) (portMapping, error) {
	v := strings.TrimSpace(s)
	proto := ""
	if i := strings.LastIndexByte(v, '/'); i >= 0 {
		v, proto = v[:i], v[i:]
	}
	// An IPv6 host address is bracketed; drop it before splitting.
	if strings.HasPrefix(v, "[") {
		if j := strings.Index(v, "]:"); j >= 0 {
			v = v[j+2:]
		}
	}
	parts := strings.Split(v, ":")
	var m portMapping
	containerSide := parts[len(parts)-1]
	if len(parts) > 1 {
		m.published = strings.Join(parts[:len(parts)-1], ":")
	}
	ports, err := portRange(containerSide + proto)
	if err != nil {
		return m, err
	}
	m.container = ports
	return m, nil
}

// envName reports whether s is a valid environment variable name. It is
// pure.
func envName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_', r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// parseDotenv reads a .env or env_file: KEY=VALUE lines, # comments, an
// optional "export " prefix, values optionally in single or double quotes.
// A line with a key and no "=" is refused: compose would read it from the
// host. It is pure.
func parseDotenv(data string) (map[string]string, []string, error) {
	return parseDotenvMode(data, false)
}

// parseDotenvMode is parseDotenv with compose's env_file format: raw. A raw
// value is everything after the first "=", taken literally: no quote
// stripping, no escapes and no inline comments.
func parseDotenvMode(data string, raw bool) (map[string]string, []string, error) {
	out := map[string]string{}
	var order []string
	for n, line := range strings.Split(strings.ReplaceAll(data, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		if !ok {
			return nil, nil, fmt.Errorf("line %d: %s has no value; a run never passes host variables in", n+1, k)
		}
		if !envName(k) {
			return nil, nil, fmt.Errorf("line %d: %q is not a variable name", n+1, k)
		}
		if raw {
			if _, seen := out[k]; !seen {
				order = append(order, k)
			}
			out[k] = v
			continue
		}
		v = strings.TrimSpace(v)
		switch {
		case len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'':
			v = v[1 : len(v)-1]
		case len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"':
			v = strings.NewReplacer(`\n`, "\n", `\"`, `"`, `\\`, `\`).Replace(v[1 : len(v)-1])
		default:
			if i := strings.Index(v, " #"); i >= 0 {
				v = strings.TrimSpace(v[:i])
			}
		}
		if _, seen := out[k]; !seen {
			order = append(order, k)
		}
		out[k] = v
	}
	return out, order, nil
}
