package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// maxUsernameLength is a display bound, not a security one: a username is a
// label in a list and a word on a login form, and one longer than this is
// somebody pasting a paragraph into the box.
const maxUsernameLength = 64

// userJSON is one account as the API reports it. There is deliberately no hash
// field: the argon2 string is the one column that never leaves the process
// (SPEC §12), and a DTO with nowhere to put it cannot leak it by accident.
type userJSON struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func userDTO(u core.User) userJSON {
	return userJSON{
		ID:        u.ID,
		Username:  u.Username,
		Role:      u.Role,
		CreatedAt: jsonTime(u.CreatedAt),
		UpdatedAt: jsonTime(u.UpdatedAt),
	}
}

// handleListUsers returns every account. Admin-only, like the rest of this
// file: memberAllowed names none of these routes, so requireAuth has already
// turned a member away with a 403 before any of them runs.
func (s *server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.st.ListUsers(r.Context())
	if err != nil {
		s.writeStoreError(w, "list users", err)
		return
	}
	out := make([]userJSON, 0, len(users))
	for _, u := range users {
		out = append(out, userDTO(u))
	}
	writeJSON(w, http.StatusOK, struct {
		Users []userJSON `json:"users"`
	}{out})
}

type userCreateRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

// handleCreateUser adds an account after first-run setup. An open server's
// implicit administrator is deliberately not an account administrator: only
// POST /setup/admin may create the first account and close the server.
func (s *server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if currentUser(r).Open {
		writeError(w, http.StatusForbidden, "administrator setup is required before users can be created")
		return
	}

	var body userCreateRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	username, ok := validUsername(w, body.Username)
	if !ok {
		return
	}
	if !core.ValidRole(body.Role) {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("role must be %q or %q", core.RoleAdmin, core.RoleMember))
		return
	}
	// Before the password is hashed: refusing after 19 MiB of argon2 would be
	// the same answer for more work.
	if !s.insistAnAdminRemains(w, r, body.Role) {
		return
	}
	hash, ok := hashNewPassword(w, s.log, body.Password)
	if !ok {
		return
	}

	user := &core.User{Username: username, PasswordHash: hash, Role: body.Role}
	if err := s.st.CreateUser(r.Context(), user); err != nil {
		if errors.Is(err, store.ErrUsernameTaken) {
			writeError(w, http.StatusConflict, "a user named "+username+" already exists")
			return
		}
		s.writeStoreError(w, "create user", err)
		return
	}
	writeJSON(w, http.StatusCreated, userDTO(*user))
}

// insistAnAdminRemains refuses a create that would leave the server gated with
// no admin, writing the failure itself.
//
// It is the same invariant the last-admin deletion guard keeps. First-run now
// creates its administrator only through POST /setup/admin, but this remains a
// defense against a legacy or manually edited database that already contains
// members and no administrator.
//
// 409, matching the deletion refusal: the body is well-formed and it is the
// state of the world that says no.
func (s *server) insistAnAdminRemains(w http.ResponseWriter, r *http.Request, role string) bool {
	if role == core.RoleAdmin {
		return true
	}
	admins, err := s.st.CountAdmins(r.Context())
	if err != nil {
		s.writeStoreError(w, "count admins", err)
		return false
	}
	if admins == 0 {
		writeError(w, http.StatusConflict,
			"the first account must be an admin, or nobody could administer this server; add yourself as an admin first")
		return false
	}
	return true
}

// handleDeleteUser removes an account and turns out every browser signed in as
// it.
//
// The last admin cannot be deleted while any other account exists: a server
// with members and no admin can never be administered again short of deleting
// the database. 409 because the request is well-formed and it is the state of
// the world that says no.
//
// When the admin is the ONLY account, the delete is allowed: removing the
// final account is the documented way to reopen the server (zero users = the
// open LAN default), and refusing it would make gating a one-way door. The
// caller deletes themself and the response's session is already revoked —
// which is fine, because the very next request runs open.
func (s *server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	user, err := s.st.GetUser(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "read user", err)
		return
	}
	if user.Role == core.RoleAdmin {
		admins, err := s.st.CountAdmins(r.Context())
		if err != nil {
			s.writeStoreError(w, "count admins", err)
			return
		}
		total, err := s.st.CountUsers(r.Context())
		if err != nil {
			s.writeStoreError(w, "count users", err)
			return
		}
		if admins <= 1 && total > 1 {
			// Both halves of this instruction are actually possible: there is
			// no role-change endpoint, so the only ways out are a second admin
			// account or an empty table.
			writeError(w, http.StatusConflict,
				"this is the only admin; create another admin account first, or delete every member account and then this one to reopen the server")
			return
		}
	}

	if err := s.st.DeleteUser(r.Context(), id); err != nil {
		s.writeStoreError(w, "delete user", err)
		return
	}
	s.sessions.revokeUser(id)
	w.WriteHeader(http.StatusNoContent)
}

type userPasswordRequest struct {
	NewPassword string `json:"new_password"`
}

// handleResetUserPassword sets another account's password without proving the
// old one — the point of an admin reset is that nobody knows it. It revokes
// that account's sessions, so a password handed out on a sticky note does not
// leave the previous holder logged in.
func (s *server) handleResetUserPassword(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body userPasswordRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	hash, ok := hashNewPassword(w, s.log, body.NewPassword)
	if !ok {
		return
	}

	if err := s.st.SetUserPassword(r.Context(), id, hash); err != nil {
		s.writeStoreError(w, "write password", err)
		return
	}
	s.sessions.revokeUser(id)
	w.WriteHeader(http.StatusNoContent)
}

// validUsername checks a submitted name, writing the failure itself when it
// will not do.
//
// Surrounding whitespace is rejected rather than trimmed: " chris" and "chris"
// would then be the same account under a name only one of them can type, and a
// name nobody can retype is a lockout. The length is counted in runes, so a
// name in a non-Latin script gets the same 64 characters a Latin one does.
func validUsername(w http.ResponseWriter, username string) (string, bool) {
	if strings.TrimSpace(username) != username {
		writeError(w, http.StatusBadRequest, "username must not start or end with a space")
		return "", false
	}
	if n := utf8.RuneCountInString(username); n < 1 || n > maxUsernameLength {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("username must be between 1 and %d characters", maxUsernameLength))
		return "", false
	}
	return username, true
}
