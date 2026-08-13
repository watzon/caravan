package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PutSession records a live login. The token is stored only as a hash: the
// cookie value never crosses this package. Replacing an existing hash updates
// the pairing rather than failing, so a retry of the same issue is idempotent.
func (s *Store) PutSession(ctx context.Context, tokenHash string, userID int64, expiry time.Time) error {
	if tokenHash == "" {
		return fmt.Errorf("store: put session: empty token hash")
	}
	session := &sessionModel{TokenHash: tokenHash, UserID: userID, ExpiresAt: formatTime(expiry)}
	_, err := s.db.NewInsert().Model(session).
		On("CONFLICT (token_hash) DO UPDATE").
		Set("user_id = EXCLUDED.user_id").
		Set("expires_at = EXCLUDED.expires_at").
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("store: put session: %w", err)
	}
	return nil
}

// GetSession returns the account a hashed token belongs to, or ErrNotFound.
// Expiry is returned as stored; the caller decides whether it has lapsed so
// the same clock that issued the cookie is the one that retires it.
func (s *Store) GetSession(ctx context.Context, tokenHash string) (userID int64, expiry time.Time, err error) {
	var session sessionModel
	err = s.db.NewSelect().Model(&session).
		Column("user_id", "expires_at").
		Where("token_hash = ?", tokenHash).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, time.Time{}, fmt.Errorf("store: session: %w", ErrNotFound)
	}
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("store: get session: %w", err)
	}
	return session.UserID, parseTime(session.ExpiresAt), nil
}

// DeleteSession forgets one hashed token. Deleting an unknown hash is not an
// error: logout of an already-expired cookie must stay a 204.
func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	if _, err := s.db.NewDelete().Model((*sessionModel)(nil)).
		Where("token_hash = ?", tokenHash).
		Exec(ctx); err != nil {
		return fmt.Errorf("store: delete session: %w", err)
	}
	return nil
}

// DeleteSessionsForUser ends every login belonging to one account. A password
// change or a deleted housemate must not leave their other browsers signed in.
func (s *Store) DeleteSessionsForUser(ctx context.Context, userID int64) error {
	if _, err := s.db.NewDelete().Model((*sessionModel)(nil)).
		Where("user_id = ?", userID).
		Exec(ctx); err != nil {
		return fmt.Errorf("store: delete sessions for user %d: %w", userID, err)
	}
	return nil
}

// DeleteExpiredSessions drops rows whose expiry has passed. It is housekeeping,
// not a security boundary: GetSession still returns expired rows so the API
// can treat them as invalid rather than missing.
func (s *Store) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	if _, err := s.db.NewDelete().Model((*sessionModel)(nil)).
		Where("expires_at <= ?", formatTime(now)).
		Exec(ctx); err != nil {
		return fmt.Errorf("store: delete expired sessions: %w", err)
	}
	return nil
}
