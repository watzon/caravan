package store

import (
	"github.com/uptrace/bun"
	"github.com/watzon/caravan/internal/core"
)

// userModel is the database representation of an account. Timestamps remain
// strings here so the store keeps its established formatTime/parseTime wire
// format without adding persistence concerns to core.User.
type userModel struct {
	bun.BaseModel `bun:"table:users,alias:user"`

	ID           int64  `bun:",pk,autoincrement"`
	Username     string `bun:",notnull"`
	PasswordHash string `bun:",notnull"`
	Role         string `bun:",notnull"`
	CreatedAt    string `bun:",notnull"`
	UpdatedAt    string `bun:",notnull"`
}

func userModelFromCore(u *core.User) *userModel {
	return &userModel{
		ID:           u.ID,
		Username:     u.Username,
		PasswordHash: u.PasswordHash,
		Role:         u.Role,
		CreatedAt:    formatTime(u.CreatedAt),
		UpdatedAt:    formatTime(u.UpdatedAt),
	}
}

func (u *userModel) toCore() *core.User {
	return &core.User{
		ID:           u.ID,
		Username:     u.Username,
		PasswordHash: u.PasswordHash,
		Role:         u.Role,
		CreatedAt:    parseTime(u.CreatedAt),
		UpdatedAt:    parseTime(u.UpdatedAt),
	}
}

// sessionModel is the database representation of a login session. As with
// userModel, expiry stays encoded until it crosses the store boundary.
type sessionModel struct {
	bun.BaseModel `bun:"table:sessions,alias:session"`

	TokenHash string `bun:",pk"`
	UserID    int64  `bun:",notnull"`
	ExpiresAt string `bun:",notnull"`
}
