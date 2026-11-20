package main

// Tenancy area (P3): `bifrost-bench org create|list|import`. Organizations
// of the hosted platform (docs/platform-design.md, Tenancy). import copies
// sealed experiment folders into an org's evidence store, verifying every
// seal and each seals.log chain on the way in, then indexes them: this is
// how recorded runs become the public demo organization.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/maximhq/bifrost/bench/utils"
)

func init() {
	register("org", "hosted organizations: org create -slug <slug> [-owner <email>] [-public-read] | org list | org import -org <slug> -from <runs dir> | org member add -org <id|slug> -email <email> [-role owner]", runOrg)
}

func runOrg(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: bifrost-bench org create|list|import|member [flags]")
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch args[0] {
	case "create":
		return orgCreate(ctx, args[1:], os.Stdout)
	case "list":
		return orgList(ctx, args[1:], os.Stdout)
	case "import":
		return orgImport(ctx, args[1:], os.Stdout)
	case "member":
		if len(args) > 1 && args[1] == "add" {
			return orgMemberAdd(ctx, args[2:], os.Stdout)
		}
		fmt.Fprintln(os.Stderr, "usage: bifrost-bench org member add -org <id|slug> -email <email> [-role owner]")
		return exitUsage
	}
	// bootstrap.sh matches "unknown org command" to detect older builds.
	fmt.Fprintf(os.Stderr, "unknown org command %q: want create, list, import or member\n", args[0])
	return exitUsage
}

// openOrgDB loads the config and opens the migrated database.
func openOrgDB(ctx context.Context, config string) (*lib.Config, *db.DB, error) {
	cfg, err := loadConfig(config)
	if err != nil {
		return nil, nil, err
	}
	d, err := db.Open(ctx, cfg.DatabaseConfig())
	if err != nil {
		return nil, nil, fmt.Errorf("open database: %w", err)
	}
	if err := d.Migrate(ctx); err != nil {
		d.Close()
		return nil, nil, fmt.Errorf("migrate database: %w", err)
	}
	return cfg, d, nil
}

func orgCreate(ctx context.Context, args []string, w io.Writer) int {
	fs := flag.NewFlagSet("org create", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	slug := fs.String("slug", "", "organization slug (lowercase letters, digits, dashes)")
	name := fs.String("name", "", "display name (default: the slug)")
	owner := fs.String("owner", "", "email of an existing user who becomes the owner (optional)")
	public := fs.Bool("public-read", false, "let anonymous visitors read the organization (the demo)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *slug == "" {
		fmt.Fprintln(os.Stderr, "org create: -slug is required")
		return exitUsage
	}
	cfg, d, err := openOrgDB(ctx, *config)
	if err != nil {
		return fail("org create: %v", err)
	}
	defer d.Close()
	ownerID := ""
	if *owner != "" {
		u, err := d.UserByEmail(ctx, *owner)
		if err != nil {
			return fail("org create: owner %s: %v", *owner, err)
		}
		ownerID = u.ID
	}
	o, err := createOrg(ctx, d, hostedLayout(cfg), cfg, *slug, *name, ownerID, *public)
	if err != nil {
		return fail("org create: %v", err)
	}
	_, _ = d.AppendAudit(ctx, db.AuditEntry{OrgID: o.ID, ActorType: db.ActorSystem, ActorID: "cli", Action: "org.create",
		TargetType: "org", TargetID: o.ID, Details: db.AuditDetails(map[string]any{"slug": o.Slug, "public_read": o.PublicRead, "owner": *owner})})
	p, _ := hostedLayout(cfg).Paths(o.ID)
	fmt.Fprintf(w, "created org %s (%s)\n  evidence %s\n  project  %s\n", o.Slug, o.ID, p.StoreRoot, p.ProjectRoot)
	return exitOK
}

// orgMemberAdd grants an existing user a role in an organization: the
// operator's repair path for an organization nobody administers (a demo
// bootstrapped without --owner-email). Idempotent: a user who is already a
// member keeps their role and the command succeeds.
func orgMemberAdd(ctx context.Context, args []string, w io.Writer) int {
	fs := flag.NewFlagSet("org member add", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	org := fs.String("org", "", "organization id or slug")
	email := fs.String("email", "", "email of an existing user")
	role := fs.String("role", "owner", "role: owner, admin, member or viewer")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *org == "" || *email == "" {
		fmt.Fprintln(os.Stderr, "org member add: -org and -email are required")
		return exitUsage
	}
	_, d, err := openOrgDB(ctx, *config)
	if err != nil {
		return fail("org member add: %v", err)
	}
	defer d.Close()
	msg, err := addOrgMember(ctx, d, *org, *email, *role)
	if err != nil {
		return fail("org member add: %v", err)
	}
	fmt.Fprintln(w, msg)
	return exitOK
}

// addOrgMember is orgMemberAdd's work; it returns the line to print.
func addOrgMember(ctx context.Context, d *db.DB, org, email, role string) (string, error) {
	if !db.ValidRole(role) {
		return "", fmt.Errorf("role %q is not owner, admin, member or viewer", role)
	}
	o, err := d.OrgByID(ctx, org)
	if errors.Is(err, db.ErrNotFound) {
		o, err = d.OrgBySlug(ctx, org)
	}
	if err != nil {
		return "", fmt.Errorf("organization %s: %w", org, err)
	}
	u, err := d.UserByEmail(ctx, email)
	if err != nil {
		return "", fmt.Errorf("user %s: %w", email, err)
	}
	err = d.AddMember(ctx, o.ID, u.ID, role)
	if errors.Is(err, db.ErrConflict) {
		return fmt.Sprintf("%s is already a member of %s; role unchanged", email, o.Slug), nil
	}
	if err != nil {
		return "", err
	}
	if _, err := d.AppendAudit(ctx, db.AuditEntry{OrgID: o.ID, ActorType: db.ActorSystem, ActorID: "cli", Action: "org.member_add",
		TargetType: "user", TargetID: u.ID, Details: db.AuditDetails(map[string]any{"role": role, "via": "cli"})}); err != nil {
		return "", fmt.Errorf("%s added as %s, but its audit row was not written: %w", email, role, err)
	}
	return fmt.Sprintf("%s added to %s as %s", email, o.Slug, role), nil
}

func orgList(ctx context.Context, args []string, w io.Writer) int {
	fs := flag.NewFlagSet("org list", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	_, d, err := openOrgDB(ctx, *config)
	if err != nil {
		return fail("org list: %v", err)
	}
	defer d.Close()
	orgs, err := d.ListOrgs(ctx)
	if err != nil {
		return fail("org list: %v", err)
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "SLUG\tID\tNAME\tPUBLIC READ\tMEMBERS")
	for _, o := range orgs {
		ms, _ := d.Members(ctx, o.ID)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%v\t%d\n", o.Slug, o.ID, o.Name, o.PublicRead, len(ms))
	}
	return boolExit(tw.Flush() == nil)
}

func boolExit(ok bool) int {
	if ok {
		return exitOK
	}
	return 1
}

func orgImport(ctx context.Context, args []string, w io.Writer) int {
	fs := flag.NewFlagSet("org import", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	ref := fs.String("org", "", "organization slug or id")
	from := fs.String("from", "", "runs folder to import (one folder per experiment); only read")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *ref == "" || *from == "" {
		fmt.Fprintln(os.Stderr, "org import: -org and -from are required")
		return exitUsage
	}
	cfg, d, err := openOrgDB(ctx, *config)
	if err != nil {
		return fail("org import: %v", err)
	}
	defer d.Close()
	rep, org, err := importIntoOrg(ctx, cfg, d, *ref, *from, utils.NewStdLogger(os.Stderr, false))
	if err != nil {
		return fail("org import: %v", err)
	}
	printImport(w, org, rep)
	if rep.Refused > 0 {
		return 1
	}
	return exitOK
}

// importIntoOrg copies, verifies and indexes the sealed experiments of from
// into the org named by ref. Used by the CLI; the jobs area can run it as
// the org.import job (and then evict the org's runtime).
func importIntoOrg(ctx context.Context, cfg *lib.Config, d *db.DB, ref, from string, logger schemas.Logger) (*tenancy.ImportReport, tenancy.Org, error) {
	org, err := tenancy.FindOrg(ctx, tenancy.DBDirectory{DB: d}, ref)
	if err != nil {
		if errors.Is(err, tenancy.ErrOrgNotFound) {
			return nil, tenancy.Org{}, fmt.Errorf("organization %q not found (create it with org create)", ref)
		}
		return nil, tenancy.Org{}, err
	}
	p, err := hostedLayout(cfg).Paths(org.ID)
	if err != nil {
		return nil, org, err
	}
	if err := tenancy.EnsureProject(p, seedBenchmarks(cfg)); err != nil {
		return nil, org, err
	}
	rep, err := tenancy.ImportRuns(ctx, from, p.StoreRoot, logger)
	if err != nil {
		return rep, org, err
	}
	st, err := store.NewFSStore(p.StoreRoot, logger)
	if err != nil {
		return rep, org, err
	}
	if _, err := d.SyncOrg(ctx, org.ID, st); err != nil {
		return rep, org, fmt.Errorf("index org %s: %w", org.Slug, err)
	}
	_, _ = d.AppendAudit(ctx, db.AuditEntry{OrgID: org.ID, ActorType: db.ActorSystem, ActorID: "cli", Action: "org.import",
		TargetType: "org", TargetID: org.ID, Details: db.AuditDetails(map[string]any{
			"from": from, "imported": rep.Imported, "skipped": rep.Skipped, "refused": rep.Refused, "runs": rep.Runs})})
	return rep, org, nil
}

func printImport(w io.Writer, org tenancy.Org, rep *tenancy.ImportReport) {
	for _, e := range rep.Experiments {
		line := fmt.Sprintf("  %-10s %s", e.Status, e.ExpID)
		if e.Runs > 0 {
			line += fmt.Sprintf(" (%d sealed runs verified)", e.Runs)
		}
		if e.Reason != "" {
			line += ": " + e.Reason
		}
		fmt.Fprintln(w, strings.TrimRight(line, " "))
	}
	fmt.Fprintf(w, "org %s: %d imported (%d runs), %d skipped, %d refused\n", org.Slug, rep.Imported, rep.Runs, rep.Skipped, rep.Refused)
}
