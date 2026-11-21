package suite

import (
	"fmt"
	"maps"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// LockTasks is LockWith for a few tasks: it starts from the suite's current
// lock and re-hashes and re-fingerprints only the tasks in ids, leaving every
// other task's lock entry and fingerprint exactly as it was, so admitting a
// handful of tasks in a large suite neither reads the other tasks' files nor
// makes their entries drift. It falls back to a full LockWith (and reports
// full = true) when there is no usable previous lock: none, another suite's,
// a legacy lock without fingerprints, a different fingerprints version, a
// changed base image, or changed suite-level settings, because each of those
// can invalidate every task's admission.
func LockTasks(s *schemas.Suite, ids []string, now time.Time, opts FingerprintOptions) (l *schemas.SuiteLock, full bool, err error) {
	prev := s.Lock
	if prev == nil || prev.Suite != s.ID || prev.Fingerprints == nil ||
		prev.Fingerprints.Version != schemas.LockFingerprintsVersion || !sameImage(prev.BaseImage, s.BaseImage) {
		l, err = LockWith(s, now, opts)
		return l, true, err
	}
	sfp, err := suiteFingerprint(s, opts)
	if err != nil {
		return nil, false, fmt.Errorf("fingerprint suite %s: %w", s.ID, err)
	}
	sfp.Images = carryImages(prev.Fingerprints.Suite.Images, sfp.Images, opts)
	if sfp.Settings != prev.Fingerprints.Suite.Settings || sfp.Files != prev.Fingerprints.Suite.Files ||
		calibrationSetHash(s.Root) != prev.Fingerprints.Calibration {
		l, err = LockWith(s, now, opts)
		return l, true, err
	}
	c := *prev
	c.Version = s.Version
	c.Tasks = maps.Clone(prev.Tasks)
	if c.Tasks == nil {
		c.Tasks = map[string]schemas.TaskLock{}
	}
	fps := *prev.Fingerprints
	fps.Tasks = maps.Clone(prev.Fingerprints.Tasks)
	if fps.Tasks == nil {
		fps.Tasks = map[string]schemas.Fingerprint{}
	}
	c.Fingerprints = &fps
	for _, id := range ids {
		t := s.TaskByID(id)
		if t == nil {
			return nil, false, fmt.Errorf("suite %s has no task %q", s.ID, id)
		}
		tl, err := lockTask(s, t)
		if err != nil {
			return nil, false, err
		}
		fp, err := taskFingerprint(s, t, opts)
		if err != nil {
			return nil, false, fmt.Errorf("fingerprint task %s: %w", id, err)
		}
		if old, ok := prev.Fingerprints.Tasks[id]; ok {
			fp.Images = carryImages(old.Images, fp.Images, opts)
		}
		fps.Tasks[id] = fp
		if old, ok := prevTask(prev, id); ok && sameContent(old, tl) && sameFingerprint(prev, &fps, id) {
			tl.Status = old.Status
			tl.Admission = old.Admission
		}
		c.Tasks[id] = tl
	}
	return &c, false, nil
}
