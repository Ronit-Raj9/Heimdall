//go:build integration

package db_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/db/dbtest"
)

// TestMain (integration builds only) gives the Postgres subtests a server:
// when HEIMDALL_TEST_PG_DSN is unset and docker is available, it starts a
// throwaway postgres:18 container named hd-db-pg-<rand> on a random
// loopback port, points HEIMDALL_TEST_PG_DSN at it, and removes it after.
//
//	go test -tags integration ./db/...
func TestMain(m *testing.M) {
	if dbtest.PostgresDSN() != "" {
		os.Exit(m.Run())
	}
	name, dsn, err := startPostgres()
	if err != nil {
		fmt.Fprintf(os.Stderr, "db integration: no Postgres (%v); Postgres subtests skip\n", err)
		os.Exit(m.Run())
	}
	os.Setenv(dbtest.EnvPostgresDSN, dsn)
	code := m.Run()
	_ = exec.Command("docker", "rm", "-f", "-v", name).Run()
	os.Exit(code)
}

func startPostgres() (string, string, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return "", "", err
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	name := "hd-db-pg-" + hex.EncodeToString(b)
	password := hex.EncodeToString(b) + "pw"
	out, err := exec.Command("docker", "run", "-d", "--rm", "--name", name,
		"-e", "POSTGRES_PASSWORD="+password, "-e", "POSTGRES_DB=heimdall",
		"-p", "127.0.0.1::5432", "postgres:18").CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("docker run: %v: %s", err, strings.TrimSpace(string(out)))
	}
	port := ""
	for i := 0; i < 50 && port == ""; i++ {
		out, err := exec.Command("docker", "port", name, "5432/tcp").Output()
		if err == nil {
			if line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0]); line != "" {
				port = line[strings.LastIndex(line, ":")+1:]
			}
		}
		if port == "" {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if port == "" {
		_ = exec.Command("docker", "rm", "-f", "-v", name).Run()
		return "", "", fmt.Errorf("no host port for %s", name)
	}
	dsn := fmt.Sprintf("postgres://postgres:%s@127.0.0.1:%s/heimdall?sslmode=disable", password, port)
	deadline := time.Now().Add(90 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		d, err := db.Open(ctx, db.Config{Driver: db.Postgres, DSN: dsn})
		cancel()
		if err == nil {
			d.Close()
			return name, dsn, nil
		}
		if time.Now().After(deadline) {
			_ = exec.Command("docker", "rm", "-f", "-v", name).Run()
			return "", "", fmt.Errorf("postgres in %s not ready: %v", name, err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// TestBackupPostgres runs pg_dump when it is on PATH.
func TestBackupPostgres(t *testing.T) {
	d := dbtest.OpenPostgres(t)
	if _, err := exec.LookPath("pg_dump"); err != nil {
		t.Skip("pg_dump not on PATH")
	}
	ctx := context.Background()
	if _, err := d.CreateOrg(ctx, db.NewOrg{Slug: "dumped"}); err != nil {
		t.Fatal(err)
	}
	// dbtest's per-test schema rides in search_path, which pg_dump does not
	// take; dump the whole database instead.
	out := t.TempDir() + "/pg.dump"
	whole, err := db.Open(ctx, db.Config{Driver: db.Postgres, DSN: dbtest.PostgresDSN()})
	if err != nil {
		t.Fatal(err)
	}
	defer whole.Close()
	if err := whole.Backup(ctx, out); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(out); err != nil || info.Size() == 0 {
		t.Fatalf("dump %v %v", info, err)
	}
}
