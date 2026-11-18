package main

import (
	"encoding/json"
	"os"
)

// printJSON writes v as indented JSON to stdout and returns the exit code;
// what names the command in the error. Shared by every command with a
// -json flag that prints one value (cost, migrate, canary, simulator).
func printJSON(v any, what string) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fail("%s: %v", what, err)
	}
	return exitOK
}
