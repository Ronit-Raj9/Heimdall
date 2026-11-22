package quota_test

import (
	"context"
	"errors"
	"testing"

	"github.com/maximhq/bifrost/bench/db/dbtest"
	"github.com/maximhq/bifrost/bench/quota"
)

func TestDBStoreLimitsAndKillSwitch(t *testing.T) {
	d := dbtest.OpenSQLite(t)
	ctx := context.Background()
	def := quota.DefaultLimits
	def.SharedPool = true
	s := quota.Open(d, quota.Options{Defaults: &def})
	l, err := s.Get(ctx, "org1")
	if err != nil || !l.SharedPool || l.Runtime != "runsc" {
		t.Fatalf("defaults: %+v %v", l, err)
	}
	l.Runtime, l.MaxConcurrentRuns, l.AllowCustomImages = "runc", 4, true
	if err := s.Set(ctx, "org1", l, "admin"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, "org1")
	if got != l {
		t.Fatalf("round trip: %+v != %+v", got, l)
	}
	if other, _ := s.Get(ctx, "org2"); other != quota.Normalize(def) {
		t.Fatalf("org2 should keep defaults: %+v", other)
	}
	bad := l
	bad.Runtime = "kata"
	if err := s.Set(ctx, "org1", bad, "admin"); !errors.Is(err, quota.ErrInvalid) {
		t.Fatalf("bad runtime: %v", err)
	}
	if on, _ := s.KillSwitch(ctx); on {
		t.Fatal("kill switch on by default")
	}
	if err := s.SetKillSwitch(ctx, true, "admin"); err != nil {
		t.Fatal(err)
	}
	s2 := quota.Open(d, quota.Options{})
	if on, _ := s2.KillSwitch(ctx); !on {
		t.Fatal("kill switch not persisted")
	}
}
