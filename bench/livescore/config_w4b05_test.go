package livescore

import (
	"errors"
	"testing"
)

// p13-9: a template with only its placeholders left is marked ErrUnedited.
func TestUneditedTemplateIsMarked(t *testing.T) {
	text, err := Template(InitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseConfig([]byte(text), "config.yaml"); !errors.Is(err, ErrUnedited) {
		t.Fatalf("got %v, want ErrUnedited", err)
	}
	bad := "versions:\n  - name: baseline\n    virtual_keys: [EDIT ME]\n  - name: BAD NAME\n    virtual_keys: [x]\n"
	if _, err := ParseConfig([]byte(bad), "config.yaml"); err == nil || errors.Is(err, ErrUnedited) {
		t.Fatalf("a file with a real problem must not be ErrUnedited: %v", err)
	}
}
