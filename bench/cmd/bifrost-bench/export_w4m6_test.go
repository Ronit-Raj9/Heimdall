package main

import (
	"errors"
	"testing"

	"github.com/maximhq/bifrost/bench/handlers"
)

func TestExportRefusalPlain(t *testing.T) {
	err := harborRefusal()
	if !errors.Is(err, handlers.ErrExportRefused) {
		t.Fatalf("not a refusal: %v", err)
	}
	if err.Error() != "Nothing to export yet: no task of this benchmark is both ready and public." {
		t.Fatalf("refusal text: %q", err.Error())
	}
}
