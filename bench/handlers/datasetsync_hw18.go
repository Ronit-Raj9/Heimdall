package handlers

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/dataset"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/suite"
)

// syncDatasetVersion records the benchmark's tasks as a new dataset version
// after a write outside the dataset API (fix wave 3, N268), so the Data tab
// lists the change with its source. A failure is logged and never fails the
// write that triggered it.
func syncDatasetVersion(root, suiteID, suiteDir, actor, source string) {
	if root == "" || suiteID == "" || suiteDir == "" {
		return
	}
	if _, err := dataset.SyncSuite(root, suiteID, suiteDir, actor, source, time.Now()); err != nil {
		slog.Warn("dataset version not recorded", "benchmark", suiteID, "source", source, "err", err)
	}
}

// syncEditedSuite records a dataset version after the file editor wrote,
// deleted or extracted p (a path inside benchmark id). Only suite.yaml and
// paths under tasks/ change the tasks; deleting a whole task folder is
// recorded as "Task deleted".
func syncEditedSuite(ps projectfs.Store, id, p, actor string) {
	if ps == nil {
		return
	}
	p = strings.Trim(path.Clean("/"+p), "/")
	if p != suite.SuiteFile && p != "tasks" && !strings.HasPrefix(p, "tasks/") {
		return
	}
	dir, err := ps.Resolve(projectfs.AreaBenchmarks, id)
	if err != nil {
		return
	}
	source := "File editor"
	if rest, ok := strings.CutPrefix(p, "tasks/"); ok && rest != "" && !strings.Contains(rest, "/") {
		if _, err := os.Stat(filepath.Join(dir, "tasks", rest)); errors.Is(err, fs.ErrNotExist) {
			source = "Task deleted"
		}
	}
	syncDatasetVersion(ps.Root(), id, dir, actor, source)
}
