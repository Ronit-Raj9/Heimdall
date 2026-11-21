package keystore

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"time"

	"github.com/maximhq/bifrost/bench/db"
)

//go:embed migrations/*.sql
var migrations embed.FS

func init() { db.Register("keystore", migrations) }

type dbStore struct {
	d      *db.DB
	master []byte
}

func newDBStore(d *db.DB, master []byte) (Store, error) {
	m := make([]byte, len(master))
	copy(m, master)
	return &dbStore{d: d, master: m}, nil
}

const keyCols = `id, org_id, kind, provider, label, base_url, last4, status, created_by, models,
	budget_usd, month_spent_usd, spend_month, created_at, updated_at, last_tested_at, models_available, price`

type scanner interface{ Scan(dest ...any) error }

func scanKey(s scanner) (*Key, error) {
	var (
		k                   Key
		kind, models, month string
		available, price    string
		created, updated    int64
		tested              sql.NullInt64
	)
	if err := s.Scan(&k.ID, &k.OrgID, &kind, &k.Provider, &k.Label, &k.BaseURL, &k.Last4, &k.Status, &k.CreatedBy,
		&models, &k.BudgetUSD, &k.MonthSpentUSD, &month, &created, &updated, &tested, &available, &price); err != nil {
		return nil, err
	}
	k.Kind = Kind(kind)
	k.Price = decodePrice(price)
	if err := json.Unmarshal([]byte(models), &k.Models); err != nil || k.Models == nil {
		k.Models = []string{}
	}
	if err := json.Unmarshal([]byte(available), &k.ModelsAvailable); err != nil || k.ModelsAvailable == nil {
		k.ModelsAvailable = []string{}
	}
	if month != spendMonth(time.Now()) {
		k.MonthSpentUSD = 0 // a new month starts from zero
	}
	k.CreatedAt, k.UpdatedAt = time.UnixMilli(created).UTC(), time.UnixMilli(updated).UTC()
	if tested.Valid {
		t := time.UnixMilli(tested.Int64).UTC()
		k.LastTestedAt = &t
	}
	return &k, nil
}

func (s *dbStore) List(ctx context.Context, orgID string, k Kind) ([]Key, error) {
	q := `SELECT ` + keyCols + ` FROM model_keys WHERE org_id = ?`
	args := []any{orgID}
	if k != "" {
		q += ` AND kind = ?`
		args = append(args, string(k))
	}
	q += ` ORDER BY created_at, id`
	rows, err := s.d.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Key{}
	for rows.Next() {
		key, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *key)
	}
	return out, rows.Err()
}

func (s *dbStore) Get(ctx context.Context, orgID, id string) (*Key, error) {
	k, err := scanKey(s.d.QueryRow(ctx, `SELECT `+keyCols+` FROM model_keys WHERE org_id = ? AND id = ?`, orgID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return k, err
}

func (s *dbStore) Create(ctx context.Context, orgID string, k Kind, in Put, actor string) (*Key, error) {
	in, err := normalize(k, in, true)
	if err != nil {
		return nil, err
	}
	id := newID()
	enc, err := seal(s.master, orgID, id, in.Value)
	if err != nil {
		return nil, err
	}
	models, _ := json.Marshal(in.Models)
	now := time.Now().UTC().UnixMilli()
	_, err = s.d.Exec(ctx, `INSERT INTO model_keys (id, org_id, kind, provider, label, base_url, last4, status, created_by, models,
		budget_usd, month_spent_usd, spend_month, value_enc, created_at, updated_at, price) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?)`,
		id, orgID, string(k), in.Provider, in.Label, in.BaseURL, last4(in.Value), StatusUntested, actor, string(models),
		in.BudgetUSD, spendMonth(time.Now()), enc, now, now, encodePrice(in.Price))
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, orgID, id)
}

func (s *dbStore) Update(ctx context.Context, orgID, id string, in Put, actor string) (*Key, error) {
	cur, err := s.Get(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if in, err = normalize(cur.Kind, in, false); err != nil {
		return nil, err
	}
	models, _ := json.Marshal(in.Models)
	now := time.Now().UTC().UnixMilli()
	if in.Value == "" {
		_, err = s.d.Exec(ctx, `UPDATE model_keys SET provider = ?, label = ?, base_url = ?, models = ?, budget_usd = ?, updated_at = ?, price = ?
			WHERE org_id = ? AND id = ?`, in.Provider, in.Label, in.BaseURL, string(models), in.BudgetUSD, now, encodePrice(in.Price), orgID, id)
	} else {
		enc, serr := seal(s.master, orgID, id, in.Value)
		if serr != nil {
			return nil, serr
		}
		_, err = s.d.Exec(ctx, `UPDATE model_keys SET provider = ?, label = ?, base_url = ?, models = ?, budget_usd = ?, updated_at = ?,
			value_enc = ?, last4 = ?, status = ?, last_tested_at = NULL, price = ? WHERE org_id = ? AND id = ?`,
			in.Provider, in.Label, in.BaseURL, string(models), in.BudgetUSD, now, enc, last4(in.Value), StatusUntested, encodePrice(in.Price), orgID, id)
	}
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, orgID, id)
}

func (s *dbStore) Delete(ctx context.Context, orgID, id, actor string) error {
	res, err := s.d.Exec(ctx, `DELETE FROM model_keys WHERE org_id = ? AND id = ?`, orgID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *dbStore) Reveal(ctx context.Context, orgID, id string) (string, error) {
	var enc string
	err := s.d.QueryRow(ctx, `SELECT value_enc FROM model_keys WHERE org_id = ? AND id = ?`, orgID, id).Scan(&enc)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return open(s.master, orgID, id, enc)
}

func (s *dbStore) SetStatus(ctx context.Context, orgID, id, status string, at time.Time) error {
	res, err := s.d.Exec(ctx, `UPDATE model_keys SET status = ?, last_tested_at = ? WHERE org_id = ? AND id = ?`,
		status, at.UTC().UnixMilli(), orgID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AddSpend adds usd to this month's spend; the first spend of a new month
// starts the counter from zero.
func (s *dbStore) AddSpend(ctx context.Context, orgID, id string, usd float64) error {
	if usd < 0 || usd != usd {
		return ErrInvalid
	}
	month := spendMonth(time.Now())
	res, err := s.d.Exec(ctx, `UPDATE model_keys SET
		month_spent_usd = CASE WHEN spend_month = ? THEN month_spent_usd + ? ELSE ? END,
		spend_month = ? WHERE org_id = ? AND id = ?`, month, usd, usd, month, orgID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetModelsAvailable stores the model ids a key's provider listed (sorted,
// deduplicated, at most MaxModelsAvailable).
func (s *dbStore) SetModelsAvailable(ctx context.Context, orgID, id string, models []string) error {
	b, _ := json.Marshal(normalizeAvailable(models))
	res, err := s.d.Exec(ctx, `UPDATE model_keys SET models_available = ? WHERE org_id = ? AND id = ?`, string(b), orgID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
