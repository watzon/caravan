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

func TestSetupAdminSerializesConcurrentFirstRequests(t *testing.T) {
	h, st, _ := newTestServer(t)
	const requests = 2
	responses := make(chan int, requests)
	var wg sync.WaitGroup
	wg.Add(requests)
	for range requests {
		go func() {
			defer wg.Done()
			rec := do(t, h, http.MethodPost, "/api/v1/setup/admin", `{"username":"admin","password":"correct horse battery"}`)
			responses <- rec.Code
		}()
	}
	wg.Wait()
	close(responses)
	created, forbidden := 0, 0
	for status := range responses {
		switch status {
		case http.StatusCreated:
			created++
		case http.StatusForbidden:
			forbidden++
		default:
			t.Fatalf("concurrent setup status = %d, want 201 or 403", status)
		}
	}
	if created != 1 || forbidden != 1 {
		t.Fatalf("concurrent setup statuses = created %d, forbidden %d; want one each", created, forbidden)
	}
	if count, err := st.CountUsers(context.Background()); err != nil {
		t.Fatalf("CountUsers: %v", err)
	} else if count != 1 {
		t.Fatalf("CountUsers after concurrent setup = %d, want 1", count)
	}
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
