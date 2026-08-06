package api

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

func setupAdmin(t *testing.T, h http.Handler, username, password string) *http.Cookie {
	t.Helper()
	rec := do(t, h, http.MethodPost, "/api/v1/setup/admin", `{"username":`+quote(username)+`,"password":`+quote(password)+`}`)
	wantStatus(t, rec, http.StatusCreated)
	cookie := sessionCookieFrom(rec)
	if cookie == nil {
		t.Fatal("setup did not issue a session cookie")
	}
	return cookie
}

func TestSetupAdminCreatesOnlyTheFirstAdministrator(t *testing.T) {
	h, st, _ := newTestServer(t)
	cookie := setupAdmin(t, h, "admin", testPassword)

	rec := doAuth(t, h, http.MethodGet, "/api/v1/auth/me", "", withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)
	var me meResponse
	decodeBody(t, rec, &me)
	if me.Username != "admin" || me.Role != core.RoleAdmin || me.Open {
		t.Fatalf("setup auth/me = %+v, want the created administrator", me)
	}

	rec = do(t, h, http.MethodPost, "/api/v1/setup/admin", `{"username":"second","password":"another password"}`)
	wantStatus(t, rec, http.StatusForbidden)
	if count, err := st.CountUsers(context.Background()); err != nil {
		t.Fatalf("CountUsers: %v", err)
	} else if count != 1 {
		t.Fatalf("CountUsers after repeated setup = %d, want 1", count)
	}
}

func TestSetupAdminRejectsWeakInput(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"empty username", `{"username":"","password":"correct horse"}`},
		{"short password", `{"username":"admin","password":"short"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, st, _ := newTestServer(t)
			rec := do(t, h, http.MethodPost, "/api/v1/setup/admin", tc.body)
			wantStatus(t, rec, http.StatusBadRequest)
			wantErrorBody(t, rec)
			if count, err := st.CountUsers(context.Background()); err != nil {
				t.Fatalf("CountUsers: %v", err)
			} else if count != 0 {
				t.Fatalf("CountUsers after rejected setup = %d, want 0", count)
			}
		})
	}
}

func TestSetupAdminSerializesDistinctConcurrentFirstRequests(t *testing.T) {
	h, st, _ := newTestServer(t)
	usernames := []string{"alice", "bob"}
	type result struct {
		username string
		status   int
	}
	responses := make(chan result, len(usernames))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, username := range usernames {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			rec := do(t, h, http.MethodPost, "/api/v1/setup/admin",
				`{"username":`+quote(username)+`,"password":"correct horse battery"}`)
			responses <- result{username: username, status: rec.Code}
		}()
	}
	close(start)
	wg.Wait()
	close(responses)

	created, forbidden := 0, 0
	var winner string
	for response := range responses {
		switch response.status {
		case http.StatusCreated:
			created++
			winner = response.username
		case http.StatusForbidden:
			forbidden++
		default:
			t.Fatalf("concurrent setup status = %d, want 201 or 403", response.status)
		}
	}
	if created != 1 || forbidden != 1 {
		t.Fatalf("concurrent setup statuses = created %d, forbidden %d; want one each", created, forbidden)
	}
	users, err := st.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 1 || users[0].Username != winner || users[0].Role != core.RoleAdmin {
		t.Fatalf("users = %+v, want only winning administrator %q", users, winner)
	}
}

func TestSetupAdminWinsRaceWithOpenCreateUser(t *testing.T) {
	h, st, _ := newTestServer(t)
	start := make(chan struct{})
	type result struct {
		endpoint string
		status   int
	}
	responses := make(chan result, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		rec := do(t, h, http.MethodPost, "/api/v1/setup/admin",
			`{"username":"owner","password":"correct horse battery"}`)
		responses <- result{endpoint: "setup", status: rec.Code}
	}()
	go func() {
		defer wg.Done()
		<-start
		rec := do(t, h, http.MethodPost, "/api/v1/users",
			`{"username":"attacker","password":"correct horse battery","role":"admin"}`)
		responses <- result{endpoint: "users", status: rec.Code}
	}()
	close(start)
	wg.Wait()
	close(responses)

	for response := range responses {
		switch response.endpoint {
		case "setup":
			if response.status != http.StatusCreated {
				t.Fatalf("POST /setup/admin status = %d, want 201", response.status)
			}
		case "users":
			if response.status != http.StatusForbidden && response.status != http.StatusUnauthorized {
				t.Fatalf("racing POST /users status = %d, want 403 before setup commits or 401 after", response.status)
			}
		}
	}

	users, err := st.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 1 || users[0].Username != "owner" || users[0].Role != core.RoleAdmin {
		t.Fatalf("users = %+v, want exactly the setup administrator and no attacker account", users)
	}
	if admins, err := st.CountAdmins(context.Background()); err != nil {
		t.Fatalf("CountAdmins: %v", err)
	} else if admins != 1 {
		t.Fatalf("CountAdmins = %d, want 1", admins)
	}
}

func TestSetupAdminIsRateLimitedAtCapacity(t *testing.T) {
	s := &server{logins: newLoginGuard()}
	for range loginConcurrency {
		s.logins.slots <- struct{}{}
	}

	rec := do(t, http.HandlerFunc(s.handleSetupAdmin), http.MethodPost, "/api/v1/setup/admin",
		`{"username":"admin","password":"correct horse battery"}`)
	wantStatus(t, rec, http.StatusTooManyRequests)
	wantErrorBody(t, rec)
}

func TestNeedsSetupWaitsForStorageAfterAdministratorCreation(t *testing.T) {
	h, st, _ := newTestServer(t)
	rec := do(t, h, http.MethodGet, "/api/v1/system/status", "")
	wantStatus(t, rec, http.StatusOK)
	var status statusResponse
	decodeBody(t, rec, &status)
	if !status.NeedsSetup {
		t.Fatal("fresh server needs_setup = false, want true")
	}

	cookie := setupAdmin(t, h, "admin", testPassword)
	rec = doAuth(t, h, http.MethodGet, "/api/v1/system/status", "", withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)
	decodeBody(t, rec, &status)
	if !status.NeedsSetup {
		t.Fatal("needs_setup = false before storage root is configured")
	}

	if err := st.SetSetting(context.Background(), store.SettingStorageRoot, t.TempDir()); err != nil {
		t.Fatalf("SetSetting storage root: %v", err)
	}
	rec = doAuth(t, h, http.MethodGet, "/api/v1/system/status", "", withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)
	decodeBody(t, rec, &status)
	if status.NeedsSetup {
		t.Fatal("needs_setup = true after administrator and storage root are configured")
	}
}
