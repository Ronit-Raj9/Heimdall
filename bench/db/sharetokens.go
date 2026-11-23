package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
)

// ---- share link index (fix wave 3, N293) ----

// ShareTokenHash is the key a share token is stored under: the hex SHA-256
// of the token. The token itself is never stored.
func ShareTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// PutShareToken records that token opens share shareID of orgID. Writing a
// token that is already recorded changes nothing.
func (d *DB) PutShareToken(ctx context.Context, token, orgID, shareID string) error {
	if token == "" || orgID == "" || shareID == "" {
		return errors.New("db: share token, org and share id are required")
	}
	_, err := d.Exec(ctx, `INSERT INTO share_tokens (token_hash, org_id, share_id, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (token_hash) DO NOTHING`, ShareTokenHash(token), orgID, shareID, Now())
	return err
}

// ShareToken returns the org and share id token opens, or ErrNotFound.
func (d *DB) ShareToken(ctx context.Context, token string) (orgID, shareID string, err error) {
	err = d.QueryRow(ctx, `SELECT org_id, share_id FROM share_tokens WHERE token_hash = ?`, ShareTokenHash(token)).Scan(&orgID, &shareID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return orgID, shareID, err
}
