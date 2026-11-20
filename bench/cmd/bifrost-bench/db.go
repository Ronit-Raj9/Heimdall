package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/maximhq/bifrost/bench/db"
)

// Database area (P1, docs/platform-design.md): the operational database's
// migrations and backup.

func init() {
	register("db", "operational database (users, orgs, tokens, jobs, index): db migrate|status|backup", runDB)
}

const dbUsage = `usage: bifrost-bench db migrate [-config deploy/bench.yaml]
       bifrost-bench db status  [-config deploy/bench.yaml] [-json]
       bifrost-bench db backup  [-config deploy/bench.yaml] -out <file>
The database is set by the database: section of the config (default SQLite at
<runs_root>/.heimdall/heimdall.db; Postgres reads its DSN from the variable named
by database.dsn_env, default HEIMDALL_DATABASE_URL). Sealed run folders stay the
evidence; the database holds operational state and a rebuildable index.
backup: SQLite is copied online (VACUUM INTO); Postgres runs pg_dump when it is
on PATH. The backup holds password and token hashes: keep it private.`

func runDB(args []string) int {
	if len(args) == 0 || (args[0] != "migrate" && args[0] != "status" && args[0] != "backup") {
		fmt.Fprintln(os.Stderr, dbUsage)
		return exitUsage
	}
	sub := args[0]
	fs := flag.NewFlagSet("db "+sub, flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	asJSON := fs.Bool("json", false, "status: print JSON")
	out := fs.String("out", "", "backup: output file (must not exist)")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, dbUsage)
		return exitUsage
	}
	if sub == "backup" && *out == "" {
		fmt.Fprintln(os.Stderr, "db backup needs -out <file>\n"+dbUsage)
		return exitUsage
	}
	cfg, err := loadConfig(*config)
	if err != nil {
		return fail("%v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	d, err := db.Open(ctx, cfg.DatabaseConfig())
	if err != nil {
		return fail("%v", err)
	}
	defer d.Close()
	switch sub {
	case "migrate":
		if err := d.Migrate(ctx); err != nil {
			return fail("%v", err)
		}
		fmt.Fprintf(os.Stdout, "database %s migrated\n", dbLocation(d))
		return printDBStatus(ctx, os.Stdout, d, false)
	case "status":
		return printDBStatus(ctx, os.Stdout, d, *asJSON)
	default:
		if err := d.Backup(ctx, *out); err != nil {
			if errors.Is(err, db.ErrNoPgDump) {
				fmt.Fprintln(os.Stderr, err)
				return exitInfra
			}
			return fail("%v", err)
		}
		fmt.Fprintf(os.Stdout, "backup of %s written to %s\n", dbLocation(d), *out)
		return exitOK
	}
}

func dbLocation(d *db.DB) string {
	if d.Driver() == db.SQLite {
		return "sqlite " + d.Path()
	}
	return "postgres"
}

// printDBStatus lists every migration; an applied migration whose file
// changed fails the command.
func printDBStatus(ctx context.Context, w io.Writer, d *db.DB, asJSON bool) int {
	st, err := d.MigrationStatus(ctx)
	if err != nil {
		return fail("%v", err)
	}
	bad, pending := 0, 0
	for _, m := range st {
		if m.Problem == "edited" {
			bad++
		}
		if !m.Applied {
			pending++
		}
	}
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(map[string]any{"driver": d.Driver(), "path": d.Path(), "migrations": st, "pending": pending, "edited": bad}); err != nil {
			return fail("%v", err)
		}
	} else {
		fmt.Fprintf(w, "database %s: %d migrations, %d pending, %d edited\n", dbLocation(d), len(st), pending, bad)
		tw := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "SET\tVERSION\tNAME\tAPPLIED\tPROBLEM")
		for _, m := range st {
			applied := "pending"
			if m.Applied {
				applied = db.Time(m.AppliedAt).Format(time.RFC3339)
			}
			fmt.Fprintf(tw, "%s\t%04d\t%s\t%s\t%s\n", m.Set, m.Version, m.Name, applied, m.Problem)
		}
		tw.Flush()
	}
	if bad > 0 {
		fmt.Fprintln(os.Stderr, "error: an applied migration was edited; restore its file or rebuild the database")
		return exitInfra
	}
	return exitOK
}
