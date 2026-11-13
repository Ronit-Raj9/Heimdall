package main

import "fmt"

// benchVersion is the version of the bench binary, recorded in manifests.
const benchVersion = "0.1.0"

func init() {
	register("version", "print the bench version", func([]string) int {
		fmt.Println(benchVersion)
		return exitOK
	})
}
