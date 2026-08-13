package store

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// createUser inserts an account and fails the test if it cannot.
func createUser(t *testing.T, st *Store, username, role string) *core.User {
	t.Helper()
	u := &core.User{Username: username, PasswordHash: "$argon2id$stub", Role: role}
	if err := st.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser(%q): %v", username, err)
	}
	return u
}

func TestUserRoundTrip(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()

	u := createUser(t, st, "Chris", core.RoleAdmin)
	if u.ID == 0 {
		t.Fatal("CreateUser did not write back an id")
	}
	if u.CreatedAt.IsZero() || u.UpdatedAt.IsZero() {
		t.Fatalf("CreateUser left timestamps unset: %+v", u)
	}

	got, err := st.GetUser(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if got.Username != "Chris" || got.Role != core.RoleAdmin || got.PasswordHash != "$argon2id$stub" {
		t.Fatalf("GetUser = %+v, want the row just written", got)
	}

	if _, err := st.GetUser(ctx, u.ID+1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetUser(absent) = %v, want ErrNotFound", err)
	}
}

func TestCreateFirstAdminIsAtomicAcrossStoreHandles(t *testing.T) {
	first, path := openTemp(t)
	second, err := Open(path)
	if err != nil {
		t.Fatalf("open second Store: %v", err)
	}
	t.Cleanup(func() { second.Close() })

	users := []*core.User{
		{Username: "alice", PasswordHash: "$argon2id$alice"},
		{Username: "bob", PasswordHash: "$argon2id$bob"},
	}
	stores := []*Store{first, second}
	start := make(chan struct{})
	errs := make(chan error, len(stores))
	var wg sync.WaitGroup
	for i := range stores {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- stores[i].CreateFirstAdmin(context.Background(), users[i])
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	created, rejected := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			created++
		case errors.Is(err, ErrFirstUserExists):
			rejected++
		default:
			t.Fatalf("CreateFirstAdmin returned unexpected error: %v", err)
		}
	}
	if created != 1 || rejected != 1 {
		t.Fatalf("CreateFirstAdmin results = %d created, %d rejected; want one each", created, rejected)
	}

	got, err := first.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(got) != 1 || got[0].Role != core.RoleAdmin {
		t.Fatalf("users = %+v, want exactly one administrator", got)
	}
	if got[0].Username != users[0].Username && got[0].Username != users[1].Username {
		t.Fatalf("created username = %q, want exactly one contender", got[0].Username)
	}
}

// The login form must not refuse someone for capitalising their own name, and
// two accounts that look identical in a list must not both exist.
func TestUsernamesAreCaseInsensitive(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()

	createUser(t, st, "Chris", core.RoleAdmin)

	for _, typed := range []string{"Chris", "chris", "CHRIS"} {
		got, err := st.GetUserByUsername(ctx, typed)
		if err != nil {
			t.Fatalf("GetUserByUsername(%q): %v", typed, err)
		}
		// Stored as typed at creation, not as looked up.
		if got.Username != "Chris" {
			t.Fatalf("GetUserByUsername(%q).Username = %q, want %q", typed, got.Username, "Chris")
		}
	}

	err := st.CreateUser(ctx, &core.User{Username: "chris", PasswordHash: "x", Role: core.RoleMember})
	if !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("CreateUser(\"chris\") alongside \"Chris\" = %v, want ErrUsernameTaken", err)
	}

	if _, err := st.GetUserByUsername(ctx, "nobody"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetUserByUsername(absent) = %v, want ErrNotFound", err)
	}
}

func TestListUsersAndCounts(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()

	users, err := st.ListUsers(ctx)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if users == nil || len(users) != 0 {
		t.Fatalf("ListUsers on a fresh database = %v, want an empty non-nil slice", users)
	}
	if n, err := st.CountUsers(ctx); err != nil || n != 0 {
		t.Fatalf("CountUsers = %d, %v; want 0, nil", n, err)
	}

	createUser(t, st, "zoe", core.RoleMember)
	createUser(t, st, "adam", core.RoleAdmin)
	createUser(t, st, "mel", core.RoleAdmin)

	users, err = st.ListUsers(ctx)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	want := []string{"adam", "mel", "zoe"}
	if len(users) != len(want) {
		t.Fatalf("ListUsers = %d rows, want %d", len(users), len(want))
	}
	for i, name := range want {
		if users[i].Username != name {
			t.Fatalf("ListUsers[%d] = %q, want %q (ordered by username)", i, users[i].Username, name)
		}
	}

	if n, err := st.CountUsers(ctx); err != nil || n != 3 {
		t.Fatalf("CountUsers = %d, %v; want 3, nil", n, err)
	}
	if n, err := st.CountAdmins(ctx); err != nil || n != 2 {
		t.Fatalf("CountAdmins = %d, %v; want 2, nil", n, err)
	}
}

func TestSetUserPasswordAndDelete(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()

	u := createUser(t, st, "chris", core.RoleAdmin)

	if err := st.SetUserPassword(ctx, u.ID, "$argon2id$new"); err != nil {
		t.Fatalf("SetUserPassword: %v", err)
	}
	got, err := st.GetUser(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if got.PasswordHash != "$argon2id$new" {
		t.Fatalf("password hash = %q, want the one just written", got.PasswordHash)
	}
	if err := st.SetUserPassword(ctx, u.ID+99, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetUserPassword(absent) = %v, want ErrNotFound", err)
	}

	if err := st.DeleteUser(ctx, u.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, err := st.GetUser(ctx, u.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetUser after delete = %v, want ErrNotFound", err)
	}
	if err := st.DeleteUser(ctx, u.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteUser(absent) = %v, want ErrNotFound", err)
	}
}

func TestUsernamesByID(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	chris := createUser(t, st, "Chris", core.RoleAdmin)
	housemate := createUser(t, st, "housemate", core.RoleMember)

	got, err := st.UsernamesByID(ctx, []int64{chris.ID, housemate.ID, 9999})
	if err != nil {
		t.Fatalf("UsernamesByID: %v", err)
	}
	want := map[int64]string{chris.ID: "Chris", housemate.ID: "housemate"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("UsernamesByID = %v, want %v — an unknown id names nobody", got, want)
	}

	// The common case on a server that runs open: nothing to look up at all.
	none, err := st.UsernamesByID(ctx, nil)
	if err != nil {
		t.Fatalf("UsernamesByID(nil): %v", err)
	}
	if len(none) != 0 {
		t.Errorf("UsernamesByID(nil) = %v, want empty", none)
	}
}
