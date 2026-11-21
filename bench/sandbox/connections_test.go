package sandbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/keystore"
)

type checkPlatform struct {
	fakePlatform
	err error
}

func (c *checkPlatform) Check(context.Context) error { return c.err }

func TestConnectionsLifecycle(t *testing.T) {
	keys := keystore.NewMemory()
	var checkErr error
	var built []Credentials
	c := &Connections{Keys: keys, Build: func(name string, cr Credentials) (Platform, error) {
		built = append(built, cr)
		return &checkPlatform{err: checkErr}, nil
	}}
	ctx := context.Background()
	l, err := c.List(ctx, "org1")
	if err != nil || len(l.Platforms) < 3 || l.Default != "" {
		t.Fatalf("empty list = %+v %v", l, err)
	}
	checkErr = fmt.Errorf("%w (HTTP 401)", ErrUnauthorized)
	if _, err := c.Connect(ctx, "org1", PlatformDaytona, "dtn_refused_1234", "", "u1"); err == nil {
		t.Fatal("a refused token was kept")
	}
	if got, _ := keys.List(ctx, "org1", keystore.KindSecret); len(got) != 0 {
		t.Fatalf("refused token stored: %+v", got)
	}
	checkErr = nil
	v, err := c.Connect(ctx, "org1", PlatformDaytona, "dtn_good_token_abcd", "https://daytona.internal/api/", "u1")
	if err != nil {
		t.Fatal(err)
	}
	if !v.Connected || v.TokenLast4 != "abcd" || v.Endpoint != "https://daytona.internal/api" || v.Status != keystore.StatusOK || v.Default {
		t.Fatalf("view = %+v", v)
	}
	if _, err := c.SetDefault(ctx, "org1", PlatformE2B, "u1"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("default of an unconnected platform = %v", err)
	}
	l, err = c.SetDefault(ctx, "org1", PlatformDaytona, "u1")
	if err != nil || l.Default != PlatformDaytona {
		t.Fatalf("default = %+v %v", l, err)
	}
	r, err := c.Resolve(ctx, "org1")
	if err != nil || r == nil || r.Platform != PlatformDaytona || r.Credentials.Token != "dtn_good_token_abcd" {
		t.Fatalf("resolve = %+v %v", r, err)
	}
	if r2, _ := c.Resolve(ctx, "org2"); r2 != nil {
		t.Fatal("another org resolved this org's platform")
	}
	// Reconnecting replaces the token in place.
	if _, err := c.Connect(ctx, "org1", PlatformDaytona, "dtn_new_token_wxyz", "", "u1"); err != nil {
		t.Fatal(err)
	}
	if r, _ := c.Resolve(ctx, "org1"); r.Credentials.Token != "dtn_new_token_wxyz" {
		t.Fatalf("reconnect = %+v", r)
	}
	checkErr = fmt.Errorf("%w (revoked)", ErrUnauthorized)
	if v, err := c.Test(ctx, "org1", PlatformDaytona); err == nil || v.Status != keystore.StatusInvalid {
		t.Fatalf("test of a revoked token = %+v %v", v, err)
	}
	l, err = c.Disconnect(ctx, "org1", PlatformDaytona, "u1")
	if err != nil || l.Default != "" {
		t.Fatalf("disconnect = %+v %v", l, err)
	}
	if r, _ := c.Resolve(ctx, "org1"); r != nil {
		t.Fatal("disconnected platform still resolves")
	}
	if got, _ := keys.List(ctx, "org1", keystore.KindSecret); len(got) != 0 {
		t.Fatalf("secrets left after disconnect: %+v", got)
	}
	for _, b := range built {
		if strings.TrimSpace(b.Token) == "" {
			t.Fatal("client built without a token")
		}
	}
}
