// Package leasebook is the database lease book of hosted coordinators
// (Z11 durable leases, engine/remotebook.go): the remote_leases table.
package leasebook

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"time"

	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/engine"
)

//go:embed migrations/*.sql
var migrations embed.FS

func init() { db.Register("leasebook", migrations) }

// keepEnded is how long an ended row stays for an operator to read.
const keepEnded = 7 * 24 * time.Hour

// Book implements engine.LeaseBook over the server database.
type Book struct {
	DB  *db.DB
	Now func() time.Time
}

var _ engine.LeaseBook = (*Book)(nil)

func (b *Book) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

// PutLease implements engine.LeaseBook.
func (b *Book) PutLease(ctx context.Context, rec engine.LeaseRecord) error {
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	gpus, err := json.Marshal(append([]string{}, rec.GPUIDs...))
	if err != nil {
		return err
	}
	_, err = b.DB.Exec(ctx, `INSERT INTO remote_leases (id, org_id, exp_id, slot_id, worker_id, owner, attempt, reserved_usd, gpu_ids, last_beat, state, record)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'open', ?)
		ON CONFLICT (id) DO UPDATE SET last_beat = excluded.last_beat, record = excluded.record, state = 'open'`,
		rec.ID, rec.OrgID, rec.ExpID, rec.SlotID, rec.WorkerID, rec.Owner, rec.Attempt, rec.ReservedUSD, string(gpus),
		rec.LastBeat.UnixMilli(), string(raw))
	if err != nil {
		return fmt.Errorf("write lease %s: %w", rec.ID, err)
	}
	return nil
}

// TouchLease implements engine.LeaseBook: only an open row moves.
func (b *Book) TouchLease(ctx context.Context, id string, at time.Time) error {
	if _, err := b.DB.Exec(ctx, `UPDATE remote_leases SET last_beat = ? WHERE id = ? AND state = 'open'`, at.UnixMilli(), id); err != nil {
		return fmt.Errorf("touch lease %s: %w", id, err)
	}
	return nil
}

// EndLease implements engine.LeaseBook.
func (b *Book) EndLease(ctx context.Context, id, why string) error {
	if len(why) > 500 {
		why = why[:500]
	}
	if _, err := b.DB.Exec(ctx, `UPDATE remote_leases SET state = 'ended', ended_why = ?, ended_at = ? WHERE id = ? AND state = 'open'`,
		why, b.now().UnixMilli(), id); err != nil {
		return fmt.Errorf("end lease %s: %w", id, err)
	}
	return nil
}

// OpenLeases implements engine.LeaseBook. It also removes the org's rows
// that ended more than a week ago.
func (b *Book) OpenLeases(ctx context.Context, orgID string) ([]engine.LeaseRecord, error) {
	if _, err := b.DB.Exec(ctx, `DELETE FROM remote_leases WHERE org_id = ? AND state = 'ended' AND ended_at < ?`,
		orgID, b.now().Add(-keepEnded).UnixMilli()); err != nil {
		return nil, fmt.Errorf("prune ended leases: %w", err)
	}
	rows, err := b.DB.Query(ctx, `SELECT record, last_beat FROM remote_leases WHERE org_id = ? AND state = 'open' ORDER BY id`, orgID)
	if err != nil {
		return nil, fmt.Errorf("read open leases: %w", err)
	}
	defer rows.Close()
	var out []engine.LeaseRecord
	for rows.Next() {
		var raw string
		var beat int64
		if err := rows.Scan(&raw, &beat); err != nil {
			return nil, err
		}
		var rec engine.LeaseRecord
		if json.Unmarshal([]byte(raw), &rec) != nil || rec.ID == "" {
			continue // unreadable: not re-attached; the journal charges its reservation
		}
		rec.LastBeat = time.UnixMilli(beat).UTC()
		out = append(out, rec)
	}
	return out, rows.Err()
}
