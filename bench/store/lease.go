package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// leaseAttempts bounds the create, inspect, reclaim loop in Lease. Each pass
// either decides or observes another party change the lease; three passes is
// plenty under the slot lock.
const leaseAttempts = 3

// leaseLockWait bounds how long one lease change waits for the slot's lock
// before it reports schemas.ErrLeaseBusy. A process suspended while it holds
// the flock then delays others by this much, not forever. A variable for tests.
var leaseLockWait = 10 * time.Second

// Lease takes the slot for owner with an O_EXCL-created lease.json in the
// slot folder. It returns true when owner now holds the lease (also when it
// already did; the heartbeat is refreshed) and false when another owner holds
// a live one. A lease whose heartbeat is older than staleAfter is reclaimed:
// it is renamed to a unique tomb name first, and only the party whose rename
// moved exactly the stale bytes it inspected goes on to create the new lease,
// so two reclaimers cannot both win. All lease changes also hold the slot's
// flock, which makes the protocol exact between processes on one host.
func (s *FSStore) Lease(ctx context.Context, slotID, owner string, staleAfter time.Duration) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if owner == "" {
		return false, fmt.Errorf("lease slot %s: empty owner", slotID)
	}
	if staleAfter <= 0 {
		return false, fmt.Errorf("lease slot %s: stale_after must be positive", slotID)
	}
	dir, err := s.slotDir(slotID)
	if err != nil {
		return false, fmt.Errorf("lease: %w", err)
	}
	unlock, err := s.lockSlot(ctx, slotID, dir)
	if err != nil {
		return false, fmt.Errorf("lease slot %s: %w", slotID, err)
	}
	defer unlock()
	p := filepath.Join(dir, leaseFile)
	var stale *schemas.Lease
	for range leaseAttempts {
		now := s.now().UTC()
		created, err := createLease(p, schemas.Lease{SlotID: slotID, Owner: owner, HeartbeatAt: now})
		if err != nil {
			return false, fmt.Errorf("lease slot %s: %w", slotID, err)
		}
		if created {
			if stale != nil {
				s.logger.Info("stale lease reclaimed", "slot_id", slotID, "previous_owner", stale.Owner,
					"previous_heartbeat", stale.HeartbeatAt.Format(time.RFC3339Nano), "owner", owner)
			}
			return true, nil
		}
		cur, raw, err := s.readLease(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("lease slot %s: %w", slotID, err)
		}
		if cur.Owner == owner {
			cur.HeartbeatAt = now
			if err := writeJSONAtomic(p, cur); err != nil {
				return false, fmt.Errorf("lease slot %s: refresh: %w", slotID, err)
			}
			return true, nil
		}
		if now.Sub(cur.HeartbeatAt) <= staleAfter {
			return false, nil
		}
		moved, err := reclaim(p, raw)
		if err != nil {
			return false, fmt.Errorf("lease slot %s: %w", slotID, err)
		}
		if !moved {
			return false, nil
		}
		stale = &cur
	}
	return false, nil
}

// Heartbeat rewrites HeartbeatAt for the current owner. It returns
// schemas.ErrLeaseHeld when another owner holds the lease and
// schemas.ErrNotFound when there is no lease; either way the caller has lost
// the slot and must stop.
func (s *FSStore) Heartbeat(ctx context.Context, slotID, owner string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := s.slotDir(slotID)
	if err != nil {
		return fmt.Errorf("heartbeat: %w", err)
	}
	unlock, err := s.lockSlot(ctx, slotID, dir)
	if err != nil {
		return fmt.Errorf("heartbeat slot %s: %w", slotID, err)
	}
	defer unlock()
	p := filepath.Join(dir, leaseFile)
	cur, _, err := s.readLease(p)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("heartbeat slot %s: no lease: %w", slotID, schemas.ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("heartbeat slot %s: %w", slotID, err)
	}
	if cur.Owner != owner {
		return fmt.Errorf("heartbeat slot %s: owner %s: %w", slotID, cur.Owner, schemas.ErrLeaseHeld)
	}
	cur.HeartbeatAt = s.now().UTC()
	if err := writeJSONAtomic(p, cur); err != nil {
		return fmt.Errorf("heartbeat slot %s: %w", slotID, err)
	}
	return nil
}

// Release removes owner's lease. Releasing a slot with no lease is a no-op;
// releasing another owner's lease returns schemas.ErrLeaseHeld.
func (s *FSStore) Release(ctx context.Context, slotID, owner string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := s.slotDir(slotID)
	if err != nil {
		return fmt.Errorf("release: %w", err)
	}
	unlock, err := s.lockSlot(ctx, slotID, dir)
	if err != nil {
		return fmt.Errorf("release slot %s: %w", slotID, err)
	}
	defer unlock()
	p := filepath.Join(dir, leaseFile)
	cur, _, err := s.readLease(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("release slot %s: %w", slotID, err)
	}
	if cur.Owner != owner {
		return fmt.Errorf("release slot %s: owner %s: %w", slotID, cur.Owner, schemas.ErrLeaseHeld)
	}
	if err := os.Remove(p); err != nil {
		return fmt.Errorf("release slot %s: %w", slotID, err)
	}
	return syncDir(dir)
}

// lockSlot serializes lease changes for one slot: a mutex for goroutines of
// this store, then flock on lease.lock for other stores and processes. Both
// waits end with ctx, and give up after leaseLockWait with
// schemas.ErrLeaseBusy, which is contention, not a judgement of the lease.
func (s *FSStore) lockSlot(ctx context.Context, slotID, dir string) (func(), error) {
	wctx, cancel := context.WithTimeoutCause(ctx, leaseLockWait,
		fmt.Errorf("waited %s for the slot lock: %w", leaseLockWait, schemas.ErrLeaseBusy))
	defer cancel()
	unlockMu, err := s.slotLock.lockCtx(wctx, slotID)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, leaseLockFile), os.O_RDWR|os.O_CREATE, filePerm)
	if err != nil {
		unlockMu()
		return nil, fmt.Errorf("open lease lock: %w", err)
	}
	unlockFile, err := lockFileCtx(wctx, f)
	if err != nil {
		unlockMu()
		return nil, errors.Join(err, f.Close())
	}
	return func() {
		if err := unlockFile(); err != nil {
			s.logger.Warn("lease unlock failed", "slot_id", slotID, "error", err.Error())
		}
		unlockMu()
	}, nil
}

// reclaim renames the stale lease whose bytes were raw out of the way and
// reports whether the caller may go on to create a new lease. If the file it
// moved is not those bytes, someone renewed or replaced the lease in between;
// it is linked back (never over a newer lease) and reclaim reports false. A
// lease that vanished before the rename leaves the slot free.
func reclaim(p string, raw []byte) (bool, error) {
	tomb := p + "." + utils.NewID("stale", 10)
	if err := os.Rename(p, tomb); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return true, nil
		}
		return false, fmt.Errorf("reclaim: %w", err)
	}
	got, err := os.ReadFile(tomb)
	if err != nil {
		return false, fmt.Errorf("reclaim: read tomb: %w", err)
	}
	if !bytes.Equal(got, raw) {
		linkErr := os.Link(tomb, p)
		if err := os.Remove(tomb); err != nil {
			return false, fmt.Errorf("reclaim: remove tomb: %w", err)
		}
		if linkErr != nil && !errors.Is(linkErr, fs.ErrExist) {
			return false, fmt.Errorf("reclaim: restore lease: %w", linkErr)
		}
		return false, nil
	}
	if err := os.Remove(tomb); err != nil {
		return false, fmt.Errorf("reclaim: remove tomb: %w", err)
	}
	return true, nil
}

// readLease reads lease.json and its raw bytes. A lease that does not decode
// is one being created right now (O_EXCL create, then write): it is treated
// as owned by nobody with the file's mtime as heartbeat, so it is never
// reclaimed early.
func (s *FSStore) readLease(p string) (schemas.Lease, []byte, error) {
	raw, err := os.ReadFile(p)
	if err != nil {
		return schemas.Lease{}, nil, err
	}
	var l schemas.Lease
	if jerr := json.Unmarshal(raw, &l); jerr != nil {
		info, err := os.Stat(p)
		if err != nil {
			return schemas.Lease{}, nil, err
		}
		s.logger.Warn("lease.json unreadable, using mtime", "path", p, "error", jerr.Error())
		return schemas.Lease{HeartbeatAt: info.ModTime().UTC()}, raw, nil
	}
	return l, raw, nil
}

// createLease creates lease.json with O_EXCL. It returns false, nil when the
// file already exists.
func createLease(p string, l schemas.Lease) (bool, error) {
	b, err := marshalIndent(l)
	if err != nil {
		return false, fmt.Errorf("marshal lease: %w", err)
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, filePerm)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return false, nil
		}
		return false, fmt.Errorf("create lease: %w", err)
	}
	_, werr := f.Write(b)
	if werr == nil {
		werr = f.Sync()
	}
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		return false, errors.Join(fmt.Errorf("write lease: %w", err), os.Remove(p))
	}
	if err := syncDir(filepath.Dir(p)); err != nil {
		return false, err
	}
	return true, nil
}
