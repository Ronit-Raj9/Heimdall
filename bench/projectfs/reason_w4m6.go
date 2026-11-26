package projectfs

import (
	"errors"
	"strings"
)

// InvalidReason returns the plain reason a write was refused, for a person
// to read: the innermost parser or validator message without the projectfs
// lead, the file path or the "yaml: " lead. A path outside the area gives
// "the file name is not allowed".
func InvalidReason(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrOutside) || errors.Is(err, ErrReserved) {
		return "the file name is not allowed"
	}
	s := err.Error()
	if i := strings.Index(s, ErrInvalid.Error()+": "); i >= 0 {
		s = s[i+len(ErrInvalid.Error())+2:]
	}
	if i := strings.Index(s, ": "); i > 0 && !strings.ContainsAny(s[:i], " \t") && strings.ContainsAny(s[:i], "/.") {
		s = s[i+2:]
	}
	return strings.TrimPrefix(s, "yaml: ")
}
