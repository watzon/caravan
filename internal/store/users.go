package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/watzon/caravan/internal/core"
)

// ErrUsernameTaken is returned by CreateUser when the name is already an
// account's. It is a sentinel rather than a raw uniqueness error because the
// name is the caller's to fix: the API turns it into a 409 naming the
// collision, not a 500.
var ErrUsernameTaken = errors.New("store: username already taken")

const userColumns = `id, username, password_hash, role, adult_access, created_at, updated_at`

// CreateUser inserts a new account and writes back the assigned ID. The
// username is compared case-insensitively by the column's collation, so a
// second "Admin" alongside "admin" is ErrUsernameTaken rather than a duplicate.
//
// The hash is stored verbatim: hashing is the API's job, because this package
// must never see a plaintext password.
func (s *Store) CreateUser(ctx context.Context, u *core.User) error {
	ts := now()
	u.CreatedAt = ts
	u.UpdatedAt = ts

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO users (username, password_hash, role, adult_access, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		u.Username, u.PasswordHash, u.Role, u.AdultAccess, formatTime(ts), formatTime(ts))
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("store: create user %q: %w", u.Username, ErrUsernameTaken)
		}
		return fmt.Errorf("store: create user %q: %w", u.Username, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: create user %q: %w", u.Username, err)
	}
	u.ID = id
	return nil
}

// GetUser returns one account, or ErrNotFound.
func (s *Store) GetUser(ctx context.Context, id int64) (*core.User, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users WHERE id = ?", id)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: user %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get user %d: %w", id, err)
	}
	return u, nil
}

// GetUserByUsername returns the account with that name, or ErrNotFound. The
// match is case-insensitive: the column's NOCASE collation is what the login
// form relies on, so nobody is refused for capitalising their own name.
func (s *Store) GetUserByUsername(ctx context.Context, username string) (*core.User, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users WHERE username = ?", username)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: user %q: %w", username, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get user %q: %w", username, err)
	}
	return u, nil
}

// ListUsers returns every account by username. The list is empty, never nil,
// on a server that runs open.
func (s *Store) ListUsers(ctx context.Context) ([]core.User, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+userColumns+" FROM users ORDER BY username COLLATE NOCASE")
	if err != nil {
		return nil, fmt.Errorf("store: list users: %w", err)
	}
	defer rows.Close()

	out := []core.User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan user: %w", err)
		}
		out = append(out, *u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list users: %w", err)
	}
	return out, nil
}

// UsernamesByID maps account ids onto usernames, omitting the ids that name
// nobody. It exists for the requests screen, which stores the id of whoever
// asked and would otherwise either load every account or issue a query per row
// — the same trade MovieIDsByTMDBID makes for the discover screens.
//
// An id with no account — a housemate who has since been deleted, or the zero
// that means "no account at all" — is simply absent from the map, so the caller
// renders it however it renders an unknown asker rather than being handed a
// wrong name.
func (s *Store) UsernamesByID(ctx context.Context, ids []int64) (map[int64]string, error) {
	out := map[int64]string{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}

	rows, err := s.db.QueryContext(ctx,
		"SELECT id, username FROM users WHERE id IN ("+placeholders(len(ids))+")", args...)
	if err != nil {
		return nil, fmt.Errorf("store: usernames by id: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id       int64
			username string
		)
		if err := rows.Scan(&id, &username); err != nil {
			return nil, fmt.Errorf("store: scan username: %w", err)
		}
		out[id] = username
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: usernames by id: %w", err)
	}
	return out, nil
}

// DeleteUser removes an account. Deleting an absent one is ErrNotFound.
//
// Nothing cascades: requests keep the id of whoever asked (requests.requested_by
// is deliberately not a foreign key), so deleting a housemate leaves the record
// of what they asked for intact rather than quietly rewriting history.
func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM users WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("store: delete user %d: %w", id, err)
	}
	return affectedOne(res, "delete user", id)
}

// SetUserPassword replaces one account's hash. Setting the password of an
// absent account is ErrNotFound.
func (s *Store) SetUserPassword(ctx context.Context, id int64, hash string) error {
	res, err := s.db.ExecContext(ctx,
		"UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?",
		hash, formatTime(now()), id)
	if err != nil {
		return fmt.Errorf("store: set user %d password: %w", id, err)
	}
	return affectedOne(res, "set user password", id)
}

// CountUsers reports how many accounts exist. Zero is what makes the API open:
// it is read on every gated request, so it is a COUNT rather than a list.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	return s.countUsers(ctx, "SELECT COUNT(*) FROM users")
}

// CountAdmins reports how many accounts hold RoleAdmin. It is what stops the
// last admin being deleted or demoted: a server with members and no admin can
// never be administered again short of deleting the database.
func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	return s.countUsers(ctx, "SELECT COUNT(*) FROM users WHERE role = ?", core.RoleAdmin)
}

func (s *Store) countUsers(ctx context.Context, query string, args ...any) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count users: %w", err)
	}
	return n, nil
}

func scanUser(sc scanner) (*core.User, error) {
	var (
		u       core.User
		created string
		updated string
	)
	if err := sc.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.AdultAccess, &created, &updated); err != nil {
		return nil, err
	}
	u.CreatedAt = parseTime(created)
	u.UpdatedAt = parseTime(updated)
	return &u, nil
}
