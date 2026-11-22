package main

// Lane Z14: `bifrost-bench status` writes the public status page's incident
// notes. The server reads the same file (HEIMDALL_STATUS_FILE, else
// <data>/status/incidents.json) and shows a change within a few seconds;
// nothing needs a restart. On the hosted stack:
//
//	docker compose exec heimdall bifrost-bench status incident open \
//	  -title "Evaluations start late" -impact major -components workers,queue \
//	  -message "We are looking into it."

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/maximhq/bifrost/bench/statuspage"
)

func init() {
	register("status", "write the public status page's incident notes (status incident open|update|resolve|list|remove)", runStatus)
}

const statusUsage = `usage:
  status incident list    [-all] [-json]
  status incident open    -title T -message M [-impact none|minor|major|critical|maintenance]
                          [-components api,database,storage,workers,queue,gateway] [-scheduled-for RFC3339]
  status incident update  -id ID -message M [-status investigating|identified|monitoring|scheduled|resolved] [-impact I]
  status incident resolve -id ID -message M
  status incident remove  -id ID
common flags: -config bench.yaml, -file incidents.json (default $HEIMDALL_STATUS_FILE, else <data>/status/incidents.json)`

func runStatus(args []string) int {
	if len(args) < 2 || args[0] != "incident" {
		fmt.Fprintln(os.Stderr, statusUsage)
		return exitUsage
	}
	verb, rest := args[1], args[2:]
	fs := flag.NewFlagSet("status incident "+verb, flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config (finds the data folder)")
	file := fs.String("file", "", "incident file (overrides "+StatusFileEnv+" and the config)")
	id := fs.String("id", "", "incident id")
	title := fs.String("title", "", "short title, shown publicly")
	message := fs.String("message", "", "the note, shown publicly as plain text")
	impact := fs.String("impact", "", "none, minor, major, critical or maintenance")
	status := fs.String("status", "", "investigating, identified, monitoring, scheduled or resolved")
	components := fs.String("components", "", "component ids, comma separated; empty means the whole platform")
	scheduledFor := fs.String("scheduled-for", "", "maintenance start (RFC 3339), for -status scheduled")
	all := fs.Bool("all", false, "list: resolved incidents too")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(rest); err != nil {
		return exitUsage
	}
	path := *file
	if path == "" {
		path = strings.TrimSpace(os.Getenv(StatusFileEnv))
	}
	if path == "" {
		cfg, err := loadConfig(*config)
		if err != nil {
			return fail("%v (or pass -file)", err)
		}
		path = statusFilePath(cfg)
	}
	st := statuspage.NewStore(path)
	show := func(in statuspage.Incident) int {
		if *asJSON {
			b, _ := json.MarshalIndent(in, "", "  ")
			fmt.Println(string(b))
		} else {
			fmt.Printf("%s  %s  %s  %s\n", in.ID, in.Status, in.Impact, in.Title)
		}
		return exitOK
	}
	switch verb {
	case "list":
		list, err := st.List()
		if err != nil {
			return fail("%v", err)
		}
		if !*all {
			open := list[:0]
			for _, in := range list {
				if in.Status != statuspage.StatusResolved {
					open = append(open, in)
				}
			}
			list = open
		}
		if *asJSON {
			b, _ := json.MarshalIndent(list, "", "  ")
			fmt.Println(string(b))
			return exitOK
		}
		if len(list) == 0 {
			fmt.Println("no incidents (" + path + ")")
			return exitOK
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tSTATUS\tIMPACT\tSTARTED\tCOMPONENTS\tTITLE")
		for _, in := range list {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", in.ID, in.Status, in.Impact, in.StartedAt.Format(time.RFC3339), strings.Join(in.Components, ","), in.Title)
		}
		_ = tw.Flush()
		return exitOK
	case "open":
		o := statuspage.Open{Title: *title, Message: *message, Impact: statuspage.Impact(*impact), Status: *status, Components: splitList(*components)}
		if *scheduledFor != "" {
			t, err := time.Parse(time.RFC3339, *scheduledFor)
			if err != nil {
				return fail("-scheduled-for: %v", err)
			}
			o.ScheduledFor = &t
			if o.Status == "" {
				o.Status = statuspage.StatusScheduled
			}
			if o.Impact == "" {
				o.Impact = statuspage.ImpactMaintenance
			}
		}
		in, err := st.Open(o)
		if err != nil {
			return fail("%v", err)
		}
		return show(in)
	case "update", "resolve":
		if *id == "" {
			return fail("-id is required")
		}
		c := statuspage.Change{Status: *status, Impact: statuspage.Impact(*impact), Message: *message}
		if verb == "resolve" {
			c.Status = statuspage.StatusResolved
		}
		in, err := st.Update(*id, c)
		if err != nil {
			return fail("%v", err)
		}
		return show(in)
	case "remove":
		if *id == "" {
			return fail("-id is required")
		}
		if err := st.Remove(*id); err != nil {
			return fail("%v", err)
		}
		fmt.Println("removed " + *id)
		return exitOK
	}
	fmt.Fprintln(os.Stderr, statusUsage)
	return exitUsage
}
