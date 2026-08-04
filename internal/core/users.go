package core

import "time"

// User roles. They are stored verbatim in users.role and constrained by a
// CHECK in migration 0011.
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
	// AdultAccess is the admin-granted permission to see the adult module
	// (PLAN phase 9 task 5). It is the one permission that does live on this
	// row rather than in the API's allowlist, and the exception proves the rule
	// above it: every other flag would WIDEN what an account may reach, which
	// is why they are not stored. This one only ever narrows — it is checked in
	// addition to the allowlist and to the server-wide adult_enabled setting
	// (see AdultVisible), so a row somebody managed to flip still opens nothing
	// the owner has not turned on server-wide.
	//
	// Meaningless on an admin, who is implicitly granted; it is stored anyway
	// so a demoted admin does not silently keep access.
	AdultAccess bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
