package main

// `bifrost-bench org audit-verify` (lane X16, audit N11): checks the hash
// chain of an audit log, either an export downloaded from
// GET /orgs/{org}/audit/export (JSONL or CSV, offline, no server needed) or
// an org's rows in the database. Exit 0 when every link holds, 1 when the
// chain is broken.

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/handlers"
)

func orgAuditVerify(ctx context.Context, args []string, w io.Writer) int {
	fs := flag.NewFlagSet("org audit-verify", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config (with -org)")
	org := fs.String("org", "", "verify this org's rows in the database instead of a file (id or slug)")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	var rep db.AuditChainReport
	switch {
	case *org != "" && fs.NArg() == 0:
		_, d, err := openOrgDB(ctx, *config)
		if err != nil {
			return fail("org audit-verify: %v", err)
		}
		defer d.Close()
		id := *org
		if o, err := d.OrgBySlug(ctx, id); err == nil {
			id = o.ID
		}
		if rep, err = d.VerifyAuditChain(ctx, id); err != nil {
			return fail("org audit-verify: %v", err)
		}
	case *org == "" && fs.NArg() == 1:
		f, err := os.Open(fs.Arg(0))
		if err != nil {
			return fail("org audit-verify: %v", err)
		}
		defer f.Close()
		if rep, err = verifyAuditExport(f, strings.HasSuffix(strings.ToLower(fs.Arg(0)), ".csv")); err != nil {
			return fail("org audit-verify %s: %v", fs.Arg(0), err)
		}
	default:
		fmt.Fprintln(os.Stderr, "usage: bifrost-bench org audit-verify [-json] <export.jsonl|export.csv> | org audit-verify -org <id|slug> [-config bench.yaml]")
		return exitUsage
	}
	if *asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			return fail("encode: %v", err)
		}
	} else {
		printAuditReport(w, rep)
	}
	return boolExit(rep.OK)
}

func printAuditReport(w io.Writer, r db.AuditChainReport) {
	if r.OK {
		fmt.Fprintf(w, "audit log intact: %d entries, %d chained, head #%d %s\n", r.Entries, r.Chained, r.HeadSeq, r.HeadHash)
	} else {
		fmt.Fprintf(w, "audit log BROKEN at entry #%d: %s (%d entries read, %d verified before it)\n", r.BrokenAt, r.Reason, r.Entries, r.Chained)
	}
	if r.Legacy > 0 {
		fmt.Fprintf(w, "%d entries predate the hash chain and are not covered by it\n", r.Legacy)
	}
}

// verifyAuditExport checks an export file in its own row order.
func verifyAuditExport(r io.Reader, isCSV bool) (db.AuditChainReport, error) {
	var v db.AuditVerifier
	if isCSV {
		cr := csv.NewReader(r)
		head, err := cr.Read()
		if err != nil {
			return db.AuditChainReport{}, fmt.Errorf("read header: %w", err)
		}
		if !slices.Equal(head, handlers.AuditCSVHeader) {
			return db.AuditChainReport{}, errors.New("not an audit export: unexpected CSV header")
		}
		for line := 2; ; line++ {
			rec, err := cr.Read()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return db.AuditChainReport{}, fmt.Errorf("line %d: %w", line, err)
			}
			e, err := auditFromCSV(rec)
			if err != nil {
				return db.AuditChainReport{}, fmt.Errorf("line %d: %w", line, err)
			}
			v.Add(e)
		}
		return v.Report(), nil
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for line := 1; sc.Scan(); line++ {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var e db.AuditEntry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return db.AuditChainReport{}, fmt.Errorf("line %d: %w", line, err)
		}
		v.Add(e)
	}
	return v.Report(), sc.Err()
}

// auditFromCSV reads one row in handlers.AuditCSVHeader order.
func auditFromCSV(rec []string) (db.AuditEntry, error) {
	if len(rec) != len(handlers.AuditCSVHeader) {
		return db.AuditEntry{}, fmt.Errorf("want %d columns, got %d", len(handlers.AuditCSVHeader), len(rec))
	}
	seq, err := strconv.ParseInt(rec[0], 10, 64)
	if err != nil {
		return db.AuditEntry{}, fmt.Errorf("seq: %w", err)
	}
	at, err := time.Parse(time.RFC3339Nano, rec[3])
	if err != nil {
		return db.AuditEntry{}, fmt.Errorf("at: %w", err)
	}
	return db.AuditEntry{Seq: seq, ID: rec[1], OrgID: rec[2], At: at, ActorType: rec[4], ActorID: rec[5], Action: rec[6],
		TargetType: rec[7], TargetID: rec[8], IP: rec[9], Details: json.RawMessage(rec[10]), PrevHash: rec[11], Hash: rec[12]}, nil
}
