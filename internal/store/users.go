package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/uptrace/bun"
	"github.com/watzon/caravan/internal/core"
)

// ErrUsernameTaken is returned by CreateUser when the name is already an
// account's. It is a sentinel rather than a raw uniqueness error because the
// name is the caller's to fix: the API turns it into a 409 naming the
// collision, not a 500.
var ErrUsernameTaken = errors.New("store: username already taken")

// ErrFirstUserExists is returned by CreateFirstAdmin when any account already
// exists. The setup endpoint maps it to its stable "already complete" reply.
var ErrFirstUserExists = errors.New("store: first user already exists")

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

	model := userModelFromCore(u)
	_, err := s.db.NewInsert().Model(model).Exec(ctx)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("store: create user %q: %w", u.Username, ErrUsernameTaken)
		}
		return fmt.Errorf("store: create user %q: %w", u.Username, err)
	}
	u.ID = model.ID
	return nil
}

// CreateFirstAdmin inserts the administrator that closes an open server. The
// emptiness check and insert are one SQLite statement, so concurrent callers
// using different Store handles and different usernames cannot both observe an
// empty table and commit. Exactly one row can be inserted; every loser receives
// ErrFirstUserExists.
//
// The hash is stored verbatim. As with CreateUser, plaintext passwords never
// cross the store boundary.
func (s *Store) CreateFirstAdmin(ctx context.Context, u *core.User) error {
	ts := now()
	u.Role = core.RoleAdmin
	u.CreatedAt = ts
	u.UpdatedAt = ts

	res, err := s.db.NewRaw(`
		INSERT INTO users (username, password_hash, role, created_at, updated_at)
		SELECT ?, ?, ?, ?, ?
		WHERE NOT EXISTS (SELECT 1 FROM users)`,
		u.Username, u.PasswordHash, u.Role, formatTime(ts), formatTime(ts)).Exec(ctx)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("store: create first administrator %q: %w", u.Username, ErrFirstUserExists)
		}
		return fmt.Errorf("store: create first administrator %q: %w", u.Username, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: create first administrator %q: %w", u.Username, err)
	}
	if affected == 0 {
		return fmt.Errorf("store: create first administrator %q: %w", u.Username, ErrFirstUserExists)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: create first administrator %q: %w", u.Username, err)
	}
	u.ID = id
	return nil
}

// GetUser returns one account, or ErrNotFound.
func (s *Store) GetUser(ctx context.Context, id int64) (*core.User, error) {
	var user userModel
	err := s.db.NewSelect().Model(&user).Where("id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: user %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get user %d: %w", id, err)
	}
	return user.toCore(), nil
}

// GetUserByUsername returns the account with that name, or ErrNotFound. The
// match is case-insensitive: the column's NOCASE collation is what the login
// form relies on, so nobody is refused for capitalising their own name.
func (s *Store) GetUserByUsername(ctx context.Context, username string) (*core.User, error) {
	var user userModel
	err := s.db.NewSelect().Model(&user).Where("username = ?", username).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: user %q: %w", username, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get user %q: %w", username, err)
	}
	return user.toCore(), nil
}

// ListUsers returns every account by username. The list is empty, never nil,
// on a server that runs open.
func (s *Store) ListUsers(ctx context.Context) ([]core.User, error) {
	var users []userModel
	err := s.db.NewSelect().Model(&users).
		OrderExpr("username COLLATE NOCASE ASC").
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: list users: %w", err)
	}

	out := make([]core.User, 0, len(users))
	for i := range users {
		out = append(out, *users[i].toCore())
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
	var users []userModel
	err := s.db.NewSelect().Model(&users).
		Column("id", "username").
		Where("id IN (?)", bun.In(ids)).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: usernames by id: %w", err)
	}

	for _, user := range users {
		out[user.ID] = user.Username
	}
	return out, nil
}

// DeleteUser removes an account. Deleting an absent one is ErrNotFound.
//
// Nothing cascades: requests keep the id of whoever asked (requests.requested_by
// is deliberately not a foreign key), so deleting a housemate leaves the record
// of what they asked for intact rather than quietly rewriting history.
func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	res, err := s.db.NewDelete().Model((*userModel)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return fmt.Errorf("store: delete user %d: %w", id, err)
	}
	return affectedOne(res, "delete user", id)
}

// SetUserPassword replaces one account's hash. Setting the password of an
// absent account is ErrNotFound.
func (s *Store) SetUserPassword(ctx context.Context, id int64, hash string) error {
	res, err := s.db.NewUpdate().Model((*userModel)(nil)).
		Set("password_hash = ?", hash).
		Set("updated_at = ?", formatTime(now())).
		Where("id = ?", id).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("store: set user %d password: %w", id, err)
	}
	return affectedOne(res, "set user password", id)
}

// CountUsers reports how many accounts exist. Zero is what makes the API open:
// it is read on every gated request, so it is a COUNT rather than a list.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	n, err := s.db.NewSelect().Model((*userModel)(nil)).Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("store: count users: %w", err)
	}
	return n, nil
}

// CountAdmins reports how many accounts hold RoleAdmin. It is what stops the
// last admin being deleted or demoted: a server with members and no admin can
// never be administered again short of deleting the database.
func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	n, err := s.db.NewSelect().Model((*userModel)(nil)).
		Where("role = ?", core.RoleAdmin).
		Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("store: count users: %w", err)
	}
	return n, nil
}
