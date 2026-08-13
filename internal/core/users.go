package core

import "time"

// User roles. They are stored verbatim in users.role and constrained by a
// CHECK on users.role.
const (
	// RoleAdmin is whoever runs the box: the whole API, including settings,
	// the library, the queue and other people's accounts.
	RoleAdmin = "admin"
	// RoleMember is a housemate: discover titles and ask for them, and nothing
	// else. What a member may reach is an allowlist in the API's auth
	// middleware, not a set of flags on this row — a permission that lives in
	// the database is a permission somebody can grant themselves.
	RoleMember = "member"
)

// ValidRole reports whether role is one Caravan stores. An unknown role is a
// caller mistake, not a role with no permissions: it is rejected at the edge
// rather than defaulted, because defaulting it either locks an admin out or
// hands a stranger the box.
func ValidRole(role string) bool {
	return role == RoleAdmin || role == RoleMember
}

// User is an account. Zero users means the server runs open — the same trusted
// LAN default the optional password always had (SPEC §11) — so this table
// being empty is a supported state, not an unconfigured one.
type User struct {
	ID int64
	// Username is compared case-insensitively (the column is COLLATE NOCASE)
	// but stored as it was typed, so a person's own capitalisation survives.
	Username string
	// PasswordHash is an argon2id PHC string. It must never reach a response
	// body or a log line (SPEC §12); the API's user DTO has no field for it.
	PasswordHash string
	// Role is RoleAdmin or RoleMember.
	Role string
	// There is deliberately no permission flag here, not even a narrowing one.
	// A legacy `adult_access` flag was the single exception before per-library access:
	// a per-account grant on the row, checked in addition to the allowlist. What
	// replaced it lives in `library_access`, keyed by the library it grants — a
	// grant names a thing, and a boolean on a person can only name a category
	// somebody has to keep in sync with the libraries in it.
	//
	// The rule above therefore has no exception left. A permission that lives on
	// this row is a permission somebody can grant themselves.
	CreatedAt time.Time
	UpdatedAt time.Time
}
