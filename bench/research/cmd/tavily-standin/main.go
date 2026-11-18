// Command tavily-standin serves the Tavily stand-in (research/standin) over
// HTTP: POST /search and POST /extract in Tavily's response shape, over the
// embedded Calder Coast corpus, with no network of its own.
//
//	go run ./research/cmd/tavily-standin -listen 127.0.0.1:8766
//
// With -key-env NAME every call must carry "Authorization: Bearer $NAME",
// so a live or record run through the tool proxy shows the key was
// injected. A container on a bridge network reaches the host's LAN address,
// not 127.0.0.1: listen on that address to use it as a task's base_url.
package main

import (
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/maximhq/bifrost/bench/research/standin"
)

func main() {
	fs := flag.NewFlagSet("tavily-standin", flag.ExitOnError)
	listen := fs.String("listen", "127.0.0.1:8766", "address to serve on")
	keyEnv := fs.String("key-env", "", "environment variable whose value every call must send as a Bearer key (default: no check)")
	_ = fs.Parse(os.Args[1:])
	c, err := standin.LoadDefault()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tavily-standin:", err)
		os.Exit(1)
	}
	key := ""
	if *keyEnv != "" {
		if key = os.Getenv(*keyEnv); key == "" {
			fmt.Fprintf(os.Stderr, "tavily-standin: %s is empty\n", *keyEnv)
			os.Exit(1)
		}
	}
	fmt.Printf("Tavily stand-in: corpus %s (%d pages) on http://%s (key check: %v)\n", c.Name, len(c.Docs), *listen, key != "")
	srv := &http.Server{Addr: *listen, Handler: standin.New(c, key), ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, "tavily-standin:", err)
		os.Exit(1)
	}
}
