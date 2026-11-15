package main

import (
	"context"
	"flag"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/server"
	"github.com/maximhq/bifrost/bench/store"
)

func TestServeExtras(t *testing.T) {
	st, err := store.NewFSStore(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	a := &app{store: st, logger: schemas.NoopLogger{}}
	tests := []struct {
		name    string
		args    []string
		offline bool
		wantErr string
	}{
		{"trends only by default", nil, false, ""},
		{"daemon refused offline", []string{"-schedules"}, true, "-offline"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := flag.NewFlagSet("serve", flag.ContinueOnError)
			x := addServeExtras(fs)
			if err := fs.Parse(tt.args); err != nil {
				t.Fatal(err)
			}
			var deps server.Deps
			err := x.apply(context.Background(), a, &deps, tt.offline)
			if tt.wantErr == "" && err != nil || tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("apply = %v, want error containing %q", err, tt.wantErr)
			}
			if deps.Trends == nil {
				t.Fatal("trends source not wired")
			}
		})
	}
}
