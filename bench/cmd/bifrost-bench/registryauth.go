package main

// `registry htpasswd` (G3 B2): the bcrypt htpasswd line of one identity of
// the shared worker's image registry. deploy/hosted/lib.sh
// (ensure_registry_auth) runs it in a one-off server container when it
// generates the registry's credentials; the password comes on stdin, never
// on a command line.

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

func init() {
	register("registry", "credentials of the shared worker's image registry (registry htpasswd)", runRegistry)
}

const registryUsage = `usage:
  bifrost-bench registry htpasswd -user <name>    (password on stdin; prints name:<bcrypt hash>)
  bifrost-bench registry publisher -auth-file <file> -manifest <file> -token-file <file> [-listen addr] [-writer host:port] [-crane path] [-checkout dir] [-library dir] [-temp dir]
  bifrost-bench registry platform-images          (prints every platform image, one per line)
  bifrost-bench registry check [-manifest file] [-docker path] [-strict]    (warns about images missing from the registry)`

var registryUserRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

func runRegistry(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, registryUsage)
		return exitUsage
	}
	switch args[0] {
	case "htpasswd":
	case "publisher":
		return runRegistryPublisher(args[1:])
	case "platform-images":
		return runRegistryPlatformImages(args[1:])
	case "check":
		return runRegistryCheck(args[1:])
	default:
		fmt.Fprintln(os.Stderr, registryUsage)
		return exitUsage
	}
	line, err := registryHtpasswd(args[1:], os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "registry htpasswd: %v\n%s\n", err, registryUsage)
		return exitUsage
	}
	fmt.Println(line)
	return exitOK
}

// registryHtpasswd is the htpasswd line for -user and the password read
// from in (first line).
func registryHtpasswd(args []string, in io.Reader) (string, error) {
	fs := flag.NewFlagSet("registry htpasswd", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	user := fs.String("user", "", "registry user name")
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if !registryUserRe.MatchString(*user) {
		return "", errors.New("-user: 1 to 64 lower-case letters, digits, '_' or '-', starting with a letter")
	}
	pw, err := bufio.NewReader(io.LimitReader(in, 1024)).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	pw = strings.TrimRight(pw, "\r\n")
	if len(pw) < 16 || len(pw) > 72 {
		return "", errors.New("the password on stdin must be 16 to 72 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return *user + ":" + string(hash), nil
}
