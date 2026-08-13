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
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (token_hash, user_id, expires_at)
		VALUES (?, ?, ?)
		ON CONFLICT(token_hash) DO UPDATE SET
			user_id = excluded.user_id,
			expires_at = excluded.expires_at`,
		tokenHash, userID, formatTime(expiry))
	if err != nil {
		return fmt.Errorf("store: put session: %w", err)
	}
	return nil
}

// GetSession returns the account a hashed token belongs to, or ErrNotFound.
// Expiry is returned as stored; the caller decides whether it has lapsed so
// the same clock that issued the cookie is the one that retires it.
func (s *Store) GetSession(ctx context.Context, tokenHash string) (userID int64, expiry time.Time, err error) {
	var expires string
	err = s.db.QueryRowContext(ctx,
		`SELECT user_id, expires_at FROM sessions WHERE token_hash = ?`, tokenHash,
	).Scan(&userID, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, time.Time{}, fmt.Errorf("store: session: %w", ErrNotFound)
	}
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("store: get session: %w", err)
	}
	return userID, parseTime(expires), nil
}

// DeleteSession forgets one hashed token. Deleting an unknown hash is not an
// error: logout of an already-expired cookie must stay a 204.
func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash); err != nil {
		return fmt.Errorf("store: delete session: %w", err)
	}
	return nil
}

// DeleteSessionsForUser ends every login belonging to one account. A password
// change or a deleted housemate must not leave their other browsers signed in.
func (s *Store) DeleteSessionsForUser(ctx context.Context, userID int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("store: delete sessions for user %d: %w", userID, err)
	}
	return nil
}

// DeleteExpiredSessions drops rows whose expiry has passed. It is housekeeping,
// not a security boundary: GetSession still returns expired rows so the API
// can treat them as invalid rather than missing.
func (s *Store) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, formatTime(now)); err != nil {
		return fmt.Errorf("store: delete expired sessions: %w", err)
	}
	return nil
}
