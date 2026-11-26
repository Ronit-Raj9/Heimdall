package imagepub

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/maximhq/bifrost/bench/utils"
)

var digestRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// SetPin records "ref digest" in the registry manifest, replacing an older
// line for ref and keeping every other line, the same result as
// registry-sync.sh set_pin. It holds an exclusive lock on manifestPath +
// ".lock" (registry-sync.sh takes the same lock) and replaces the file by a
// rename, so readers never see half a file. The file is mode 0640.
func SetPin(manifestPath, ref, digest string) error {
	if !ValidRef(ref) || !digestRe.MatchString(digest) {
		return fmt.Errorf("manifest: bad pin %q %q", ref, digest)
	}
	lock, err := os.OpenFile(manifestPath+".lock", os.O_CREATE|os.O_RDWR, 0o640)
	if err != nil {
		return fmt.Errorf("manifest lock: %w", err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("manifest lock: %w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	// A manifest that does not parse is never rewritten.
	if _, err := os.Stat(manifestPath); err == nil {
		if _, err := utils.ReadRegistryManifest(manifestPath); err != nil {
			return err
		}
	}
	var keep []string
	if f, err := os.Open(manifestPath); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := sc.Text()
			if fields := strings.Fields(line); len(fields) > 0 && fields[0] == ref {
				continue
			}
			keep = append(keep, line)
		}
		err = sc.Err()
		f.Close()
		if err != nil {
			return fmt.Errorf("manifest: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("manifest: %w", err)
	}
	keep = append(keep, ref+" "+digest)

	tmp, err := os.CreateTemp(filepath.Dir(manifestPath), ".manifest-*")
	if err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(strings.Join(keep, "\n") + "\n"); err != nil {
		tmp.Close()
		return fmt.Errorf("manifest: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o640); err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	// Keep the old file's group (the Docker socket's group the shared
	// runner reads it with), as set_pin's chgrp does.
	if fi, err := os.Stat(manifestPath); err == nil {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			_ = os.Chown(tmp.Name(), -1, int(st.Gid))
		}
	}
	if err := os.Rename(tmp.Name(), manifestPath); err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	return nil
}
