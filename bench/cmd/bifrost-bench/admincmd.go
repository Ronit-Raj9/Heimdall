package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/db"
)

// Server admins from the operator's shell (Wave A, audit B1): the bootstrap
// path for the first server admin, bound by user id. Run it on the server,
// against the server's database.

func init() {
	register("admin", "server admins by user id: admin grant|revoke -email <email>, admin list", runAdmin)
}

const adminUsage = `usage: bifrost-bench admin grant  -email <email> [-config deploy/bench.yaml]
       bifrost-bench admin revoke -email <email> [-config deploy/bench.yaml]
       bifrost-bench admin list   [-config deploy/bench.yaml]
grant makes the account with that email a server admin by its user id: it
works whether or not the email is verified, so check the account is the right
person's first (list shows each admin's user id and verification). revoke drops
the grant; also remove the address from HEIMDALL_SERVER_ADMINS, or it binds
again at the user's next verified sign-in. Run on the server; audited as system.`

func runAdmin(args []string) int { return adminMain(args, os.Stdout, os.Stderr) }

func adminMain(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "grant" && args[0] != "revoke" && args[0] != "list") {
		fmt.Fprintln(stderr, adminUsage)
		return exitUsage
	}
	sub := args[0]
	fs := flag.NewFlagSet("admin "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	config := fs.String("config", defaultConfig, "bench config")
	email := fs.String("email", "", "account email")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 || (sub != "list" && *email == "") {
		fmt.Fprintln(stderr, adminUsage)
		return exitUsage
	}
	cfg, err := loadConfig(*config)
	if err != nil {
		return fail("%v", err)
	}
	if !cfg.Auth.IsHosted() {
		fmt.Fprintln(stderr, "note: auth.mode is local; server admins exist only when the server runs with auth.mode hosted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	d, err := db.Open(ctx, cfg.DatabaseConfig())
	if err != nil {
		return fail("%v", err)
	}
	defer d.Close()
	if err := d.Migrate(ctx); err != nil {
		return fail("%v", err)
	}
	switch sub {
	case "grant":
		u, err := auth.GrantServerAdmin(ctx, d, *email)
		if err != nil {
			return fail("%v", err)
		}
		fmt.Fprintf(stdout, "%s (%s) is a server admin by user id; it applies at their next request\n", u.Email, u.ID)
	case "revoke":
		u, had, err := auth.RevokeServerAdmin(ctx, d, *email)
		if err != nil {
			return fail("%v", err)
		}
		if !had {
			fmt.Fprintf(stdout, "%s (%s) had no grant\n", u.Email, u.ID)
		} else {
			fmt.Fprintf(stdout, "grant of %s (%s) revoked\n", u.Email, u.ID)
		}
		if auth.ListedServerAdminEmail(u.Email) {
			fmt.Fprintf(stdout, "note: %s is still listed in %s; remove it there too, or it binds again at the next verified sign-in\n", u.Email, auth.ServerAdminsEnv)
		}
	default:
		list, err := auth.ServerAdmins(ctx, d)
		if err != nil {
			return fail("%v", err)
		}
		if len(list) == 0 {
			fmt.Fprintln(stdout, "no server admins; grant one with: bifrost-bench admin grant -email you@example.com")
			return exitOK
		}
		for _, e := range list {
			verified := "unverified"
			if e.Verified {
				verified = "verified"
			}
			fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", e.UserID, e.Email, adminSourceText(e.Source), verified)
		}
	}
	return exitOK
}

func adminSourceText(source string) string {
	switch source {
	case auth.GrantCLI:
		return "granted (admin grant)"
	case auth.GrantEmail:
		return "bound from " + auth.ServerAdminsEnv
	case "ids_env":
		return "listed in " + auth.ServerAdminIDsEnv
	case "email_env":
		return "listed in " + auth.ServerAdminsEnv + " (binds at next request)"
	}
	return source
}
