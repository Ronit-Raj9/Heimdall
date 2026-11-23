package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/utils"
)

// janitorLog keeps the info lines (schemas.Logger).
type janitorLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *janitorLog) Debug(string, ...any) {}
func (l *janitorLog) Info(msg string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, msg+fmt.Sprint(args...))
}
func (l *janitorLog) Warn(string, ...any)  {}
func (l *janitorLog) Error(string, ...any) {}

// TestServerJanitorNilWithoutDocker covers N218: a process without Docker
// (the hosted server image) builds no janitor, so no sweep runs "docker ps",
// and says so once at info level; a process with Docker keeps its janitor.
func TestServerJanitorNilWithoutDocker(t *testing.T) {
	noDockerOnce = sync.Once{} // another test may have logged it already
	log := &janitorLog{}
	none := utils.Docker{Binary: filepath.Join(t.TempDir(), "no-docker")}
	for i := 0; i < 3; i++ {
		if j := serverJanitor(nil, none, log); j != nil {
			t.Fatal("a janitor was built without Docker")
		}
	}
	n := 0
	for _, l := range log.lines {
		if strings.Contains(l, "no Docker on this server: leftover containers are cleaned on the workers") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("info lines %q: want the no-Docker line once", log.lines)
	}
	bin := filepath.Join(t.TempDir(), "docker")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if j := serverJanitor(nil, utils.Docker{Binary: bin}, log); j == nil {
		t.Fatal("no janitor although Docker is there")
	}
}
