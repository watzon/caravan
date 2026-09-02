package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// usersBody is the envelope GET /users answers with.
type usersBody struct {
	Users []userJSON `json:"users"`
}

func TestOnlySetupAdminCanCreateFirstAccount(t *testing.T) {
	h, st, _ := newTestServer(t)

	for _, role := range []string{core.RoleAdmin, core.RoleMember} {
		rec := do(t, h, http.MethodPost, "/api/v1/users",
			`{"username":"attacker","password":`+quote(testPassword)+`,"role":`+quote(role)+`}`)
		wantStatus(t, rec, http.StatusForbidden)
		wantErrorBody(t, rec)
	}

	users, err := st.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 0 {
		t.Fatalf("users = %+v, want open-server POST /users to create none", users)
	}
	wantStatus(t, do(t, h, http.MethodGet, "/api/v1/library/movies", ""), http.StatusOK)

	cookie := setupAdmin(t, h, "chris", testPassword)
	users, err = st.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("ListUsers after setup: %v", err)
	}
	if len(users) != 1 || users[0].Username != "chris" || users[0].Role != core.RoleAdmin {
		t.Fatalf("users after setup = %+v, want only the setup administrator", users)
	}

	rec := doAuth(t, h, http.MethodPost, "/api/v1/users",
		`{"username":"housemate","password":`+quote(testPassword)+`,"role":"member"}`,
		withCookie(cookie))
	wantStatus(t, rec, http.StatusCreated)
}

func TestListUsersCarriesNoHash(t *testing.T) {
	h, st, _ := newTestServer(t)
	setPassword(t, st, testPassword)
	createUser(t, st, testMember, testPassword, core.RoleMember)
	cookie := login(t, h, testAdmin, testPassword)

	rec := doAuth(t, h, http.MethodGet, "/api/v1/users", "", withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "$argon2id$") ||
		strings.Contains(rec.Body.String(), "password") {
		t.Fatalf("GET /users mentions a password: %s", rec.Body.String())
	}

	var body usersBody
	decodeBody(t, rec, &body)
	if len(body.Users) != 2 {
		t.Fatalf("users = %d, want 2", len(body.Users))
	}
	// Ordered by username: "admin" before "housemate".
	if body.Users[0].Username != testAdmin || body.Users[1].Username != testMember {
		t.Fatalf("users = %+v, want them ordered by username", body.Users)
	}
	if body.Users[0].Role != core.RoleAdmin || body.Users[1].Role != core.RoleMember {
		t.Fatalf("users = %+v, want the roles they were created with", body.Users)
	}
	if body.Users[0].CreatedAt == "" || body.Users[0].UpdatedAt == "" {
		t.Fatalf("user = %+v, want timestamps", body.Users[0])
	}
}

func TestCreateUserValidation(t *testing.T) {
	h, st, _ := newTestServer(t)
	setPassword(t, st, testPassword)
	cookie := login(t, h, testAdmin, testPassword)

	tests := []struct {
		name string
		body string
		want int
	}{
		{"empty username", `{"username":"","password":"a password","role":"member"}`, http.StatusBadRequest},
		{"leading space", `{"username":" chris","password":"a password","role":"member"}`, http.StatusBadRequest},
		{"trailing space", `{"username":"chris ","password":"a password","role":"member"}`, http.StatusBadRequest},
		{"overlong username", `{"username":"` + strings.Repeat("c", maxUsernameLength+1) +
			`","password":"a password","role":"member"}`, http.StatusBadRequest},
		{"unknown role", `{"username":"chris","password":"a password","role":"owner"}`, http.StatusBadRequest},
		{"no role", `{"username":"chris","password":"a password"}`, http.StatusBadRequest},
		{"short password", `{"username":"chris","password":"short","role":"member"}`, http.StatusBadRequest},
		{"long password", `{"username":"chris","password":"` + strings.Repeat("p", maxPasswordLength+1) +
			`","role":"member"}`, http.StatusBadRequest},
		// Uniqueness is the database's, compared case-insensitively.
		{"duplicate username", `{"username":"ADMIN","password":"a password","role":"member"}`, http.StatusConflict},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := doAuth(t, h, http.MethodPost, "/api/v1/users", tc.body, withCookie(cookie))
			wantStatus(t, rec, tc.want)
			wantErrorBody(t, rec)
		})
	}

	// A name at the limit is accepted: the bound is inclusive.
	rec := doAuth(t, h, http.MethodPost, "/api/v1/users",
		`{"username":"`+strings.Repeat("c", maxUsernameLength)+
			`","password":"a long enough password","role":"member"}`, withCookie(cookie))
	wantStatus(t, rec, http.StatusCreated)

	users, err := st.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("users = %d, want only the admin and the one accepted create", len(users))
	}
}

// A server with members and no admin can never be administered again, so the
// last admin cannot be deleted while any other account exists. 409, not 403:
// the request is well-formed and it is the state of the world that says no.
// The one exception is the final account of all, whose deletion is how the
// server is reopened.
func TestDeletingTheLastAdminIsRefused(t *testing.T) {
	h, st, _ := newTestServer(t)
	admin := setPassword(t, st, testPassword)
	member := createUser(t, st, testMember, testPassword, core.RoleMember)
	cookie := login(t, h, testAdmin, testPassword)

	rec := doAuth(t, h, http.MethodDelete, "/api/v1/users/"+itoa(admin.ID), "", withCookie(cookie))
	wantStatus(t, rec, http.StatusConflict)
	wantErrorBody(t, rec)

	// Having a member is not having an admin.
	if _, err := st.GetUser(context.Background(), admin.ID); err != nil {
		t.Fatalf("the last admin was deleted anyway: %v", err)
	}

	// A second admin makes the first deletable.
	second := createUser(t, st, "second", testPassword, core.RoleAdmin)
	rec = doAuth(t, h, http.MethodDelete, "/api/v1/users/"+itoa(admin.ID), "", withCookie(cookie))
	wantStatus(t, rec, http.StatusNoContent)

	// ...and now the second admin is the last one.
	secondCookie := login(t, h, "second", testPassword)
	rec = doAuth(t, h, http.MethodDelete, "/api/v1/users/"+itoa(second.ID), "", withCookie(secondCookie))
	wantStatus(t, rec, http.StatusConflict)

	// A member is never the last admin.
	rec = doAuth(t, h, http.MethodDelete, "/api/v1/users/"+itoa(member.ID), "", withCookie(secondCookie))
	wantStatus(t, rec, http.StatusNoContent)

	// The second admin is now the only account of any kind, and deleting the
	// final account is the documented way to reopen the server: it must be
	// allowed, and the very next unauthenticated request runs open.
	rec = doAuth(t, h, http.MethodDelete, "/api/v1/users/"+itoa(second.ID), "", withCookie(secondCookie))
	wantStatus(t, rec, http.StatusNoContent)
	wantStatus(t, do(t, h, http.MethodGet, "/api/v1/library/movies", ""), http.StatusOK)
}

// Deleting somebody must not leave their browser logged in.
func TestDeletingAUserRevokesTheirSessions(t *testing.T) {
	h, st, _ := newTestServer(t)
	setPassword(t, st, testPassword)
	member := createUser(t, st, testMember, testPassword, core.RoleMember)
	admin := login(t, h, testAdmin, testPassword)
	theirs := login(t, h, testMember, testPassword)

	wantStatus(t, doAuth(t, h, http.MethodGet, "/api/v1/requests", "", withCookie(theirs)),
		http.StatusOK)

	rec := doAuth(t, h, http.MethodDelete, "/api/v1/users/"+itoa(member.ID), "", withCookie(admin))
	wantStatus(t, rec, http.StatusNoContent)

	wantStatus(t, doAuth(t, h, http.MethodGet, "/api/v1/requests", "", withCookie(theirs)),
		http.StatusUnauthorized)
	// The admin who did it is untouched.
	wantStatus(t, doAuth(t, h, http.MethodGet, "/api/v1/library/movies", "", withCookie(admin)),
		http.StatusOK)
}

func TestDeleteUserRejectsBadIDs(t *testing.T) {
	h, st, _ := newTestServer(t)
	setPassword(t, st, testPassword)
	cookie := login(t, h, testAdmin, testPassword)

	rec := doAuth(t, h, http.MethodDelete, "/api/v1/users/nope", "", withCookie(cookie))
	wantStatus(t, rec, http.StatusBadRequest)
	rec = doAuth(t, h, http.MethodDelete, "/api/v1/users/9999", "", withCookie(cookie))
	wantStatus(t, rec, http.StatusNotFound)
}

// An admin reset proves nothing about the old password (that is the point) and
// turns out whoever was holding it.
func TestAdminResetsAPassword(t *testing.T) {
	h, st, _ := newTestServer(t)
	setPassword(t, st, testPassword)
	member := createUser(t, st, testMember, testPassword, core.RoleMember)
	admin := login(t, h, testAdmin, testPassword)
	theirs := login(t, h, testMember, testPassword)

	rec := doAuth(t, h, http.MethodPost, "/api/v1/users/"+itoa(member.ID)+"/password",
		`{"new_password":"a fresh password"}`, withCookie(admin))
	wantStatus(t, rec, http.StatusNoContent)

	// The old session is gone and the old password no longer works.
	wantStatus(t, doAuth(t, h, http.MethodGet, "/api/v1/requests", "", withCookie(theirs)),
		http.StatusUnauthorized)
	rec = do(t, h, http.MethodPost, "/api/v1/auth/login",
		`{"username":`+quote(testMember)+`,"password":`+quote(testPassword)+`}`)
	wantStatus(t, rec, http.StatusUnauthorized)

	login(t, h, testMember, "a fresh password")

	// A short reset is refused, and the account is left as it was.
	rec = doAuth(t, h, http.MethodPost, "/api/v1/users/"+itoa(member.ID)+"/password",
		`{"new_password":"short"}`, withCookie(admin))
	wantStatus(t, rec, http.StatusBadRequest)
	wantErrorBody(t, rec)
	login(t, h, testMember, "a fresh password")

	rec = doAuth(t, h, http.MethodPost, "/api/v1/users/9999/password",
		`{"new_password":"a fresh password"}`, withCookie(admin))
	wantStatus(t, rec, http.StatusNotFound)
}

// Managing accounts is an admin's job. A member is turned away by the gate, so
// none of these handlers ever runs for them.
func TestUserManagementIsAdminOnly(t *testing.T) {
	h, st, _ := newTestServer(t)
	setPassword(t, st, testPassword)
	member := createUser(t, st, testMember, testPassword, core.RoleMember)
	cookie := login(t, h, testMember, testPassword)

	tests := []struct{ method, target, body string }{
		{http.MethodGet, "/api/v1/users", ""},
		{http.MethodPost, "/api/v1/users", `{"username":"mine","password":"a password","role":"admin"}`},
		{http.MethodDelete, "/api/v1/users/" + itoa(member.ID), ""},
		{http.MethodPost, "/api/v1/users/" + itoa(member.ID) + "/password", `{"new_password":"my own choice"}`},
	}
	for _, tc := range tests {
		rec := doAuth(t, h, tc.method, tc.target, tc.body, withCookie(cookie))
		wantStatus(t, rec, http.StatusForbidden)
		wantErrorBody(t, rec)
	}

	// Nothing landed: the member did not promote themselves and is still there.
	users, err := st.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("users = %d, want the two that existed before", len(users))
	}
}
