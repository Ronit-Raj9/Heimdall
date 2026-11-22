package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/db"
)

// Auth area (P2, docs/auth.md): accounts from the operator's shell, for the
// first owner of a closed-signup server and for password recovery.

func init() {
	register("user", "hosted-mode accounts from the shell: user create|set-password (password read from stdin)|verify", runUser)
}

const userUsage = `usage: bifrost-bench user create       -email <email> -org <id|slug|new name> [-role owner] [-name <name>] [-config deploy/bench.yaml] < password
       bifrost-bench user set-password -email <email> [-config deploy/bench.yaml] < password
       bifrost-bench user verify       -email <email> [-config deploy/bench.yaml]
The password is the first line of standard input, never a flag (flags show in
process lists and shell history): for example
  read -rs PW && echo "$PW" | bifrost-bench user create -email a@example.com -org acme -role owner
create makes the organization when no org has that id or slug, and works on an
empty install: it is how a server with auth.signup closed gets its first owner.
set-password also ends every session of the user. verify marks the account's
email as verified without a link (accounts made by create already are); use it
when the server sends no email and you know the address is the user's. All are
audited as system.`

func runUser(args []string) int {
	return userMain(args, os.Stdin, os.Stdout, os.Stderr)
}

func userMain(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "create" && args[0] != "set-password" && args[0] != "verify") {
		fmt.Fprintln(stderr, userUsage)
		return exitUsage
	}
	sub := args[0]
	fs := flag.NewFlagSet("user "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	config := fs.String("config", defaultConfig, "bench config")
	email := fs.String("email", "", "account email")
	org := fs.String("org", "", "create: organization id, slug, or the name of a new one")
	role := fs.String("role", "owner", "create: role in the organization (viewer, member, admin, owner)")
	name := fs.String("name", "", "create: display name")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 || *email == "" || (sub == "create" && *org == "") {
		fmt.Fprintln(stderr, userUsage)
		return exitUsage
	}
	r, err := auth.ParseRole(*role)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n%s\n", err, userUsage)
		return exitUsage
	}
	password := ""
	if sub != "verify" {
		if password, err = readPassword(stdin); err != nil {
			fmt.Fprintf(stderr, "%v\n%s\n", err, userUsage)
			return exitUsage
		}
	}
	cfg, err := loadConfig(*config)
	if err != nil {
		return fail("%v", err)
	}
	if !cfg.Auth.IsHosted() {
		fmt.Fprintln(stderr, "note: auth.mode is local; accounts take effect when the server runs with auth.mode hosted")
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
	case "create":
		c, err := auth.CreateUser(ctx, d, nil, *email, *name, password, *org, r)
		if err != nil {
			return fail("%v", err)
		}
		verb := "joined"
		if c.OrgCreated {
			verb = "created"
		}
		fmt.Fprintf(stdout, "user %s (%s) created; organization %s (%s) %s, role %s\n", c.User.Email, c.User.ID, c.Org.Slug, c.Org.ID, verb, c.Role)
	case "verify":
		u, err := auth.MarkEmailVerified(ctx, d, *email)
		if err != nil {
			return fail("%v", err)
		}
		fmt.Fprintf(stdout, "email %s of %s verified\n", u.Email, u.ID)
	default:
		u, err := auth.SetPassword(ctx, d, nil, *email, password)
		if err != nil {
			return fail("%v", err)
		}
		fmt.Fprintf(stdout, "password of %s (%s) set; every session of the user ended\n", u.Email, u.ID)
	}
	return exitOK
}

// readPassword reads the first line of r, without its line ending.
func readPassword(r io.Reader) (string, error) {
	line, err := bufio.NewReader(io.LimitReader(r, auth.MaxPasswordBytes+2)).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("read password from stdin: %w", err)
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return "", fmt.Errorf("no password on stdin")
	}
	return line, nil
}
