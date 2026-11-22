package utils

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestHideAddressesKeepsTheErrorChain(t *testing.T) {
	if HideAddresses(nil) != nil {
		t.Fatal("nil must stay nil")
	}
	err := HideAddresses(&wrapped{io.ErrUnexpectedEOF})
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("errors.Is lost the cause: %v", err)
	}
	if msg := err.Error(); strings.Contains(msg, "10.0.0.7") || !strings.Contains(msg, "dial tcp a private address:443") {
		t.Fatalf("text = %q", msg)
	}
}

type wrapped struct{ err error }

func (w *wrapped) Error() string { return "dial tcp 10.0.0.7:443: " + w.err.Error() }
func (w *wrapped) Unwrap() error { return w.err }
