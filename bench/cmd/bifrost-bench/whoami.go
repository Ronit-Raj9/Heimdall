package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/connectlocal"
)

func init() {
	register("whoami", "show which server and organization this machine is signed in to: whoami [-url URL]", runWhoami)
}

// storedLoginToken is the token `login` stored for server, or "" (no
// server, no login for it, or an expired one). Commands that take -server
// and -token use it when neither -token nor a token variable is set.
func storedLoginToken(server string) string {
	if strings.TrimSpace(server) == "" {
		return ""
	}
	srv, tok, src := connectlocal.Resolve(server, "")
	if src != "login" || srv != connectlocal.NormalizeServer(server) {
		return ""
	}
	return tok
}

const whoamiUsage = `usage: heimdall whoami [-url URL]

Shows the server, organization, role and access of the stored login (or of
HEIMDALL_TOKEN), and checks the token with the server.`

func runWhoami(args []string) int {
	fs := flag.NewFlagSet("whoami", flag.ContinueOnError)
	server := fs.String("url", "", "Heimdall server address (env HEIMDALL_URL); default the last login")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, whoamiUsage)
		return exitUsage
	}
	return whoami(context.Background(), *server, os.Stdout)
}

func whoami(ctx context.Context, server string, out io.Writer) int {
	srv, tok, src := connectlocal.Resolve(server, "")
	if srv == "" || tok == "" {
		fmt.Fprintln(os.Stderr, "Not logged in. Run: heimdall login")
		return exitInfra
	}
	_, _ = fmt.Fprintf(out, "Server: %s\n", srv)
	if src == "login" {
		if c, err := connectlocal.LoadCredentials(); err == nil {
			if sc := c.Servers[srv]; sc != nil {
				if sc.Org != "" {
					_, _ = fmt.Fprintf(out, "Organization: %s\n", sc.Org)
				}
				if sc.Role != "" {
					_, _ = fmt.Fprintf(out, "Role: %s\n", sc.Role)
				}
				if len(sc.Scopes) > 0 {
					_, _ = fmt.Fprintf(out, "Access: %s\n", strings.Join(sc.Scopes, ", "))
				}
				if !sc.ExpiresAt.IsZero() {
					_, _ = fmt.Fprintf(out, "Token expires: %s\n", sc.ExpiresAt.Local().Format(time.RFC1123))
				}
			}
		}
		_, _ = fmt.Fprintln(out, "Token from: stored login")
	} else {
		_, _ = fmt.Fprintln(out, "Token from: HEIMDALL_TOKEN")
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, err := newOnboardAPI(srv, tok).call(cctx, http.MethodGet, "/api/bench/orgs", nil, nil); err != nil {
		fmt.Fprintf(os.Stderr, "The server refused the token: %v\n", err)
		return exitInfra
	}
	_, _ = fmt.Fprintln(out, "Token works.")
	return exitOK
}
