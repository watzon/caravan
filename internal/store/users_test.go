package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
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

// The upgrade promise: the password that opened the server yesterday opens it
// today, as the 'admin' account, and the setting it lived in is gone.
func TestMigrationFoldsTheLegacyPasswordIntoAnAdmin(t *testing.T) {
	ctx := context.Background()
	st := openPreUsers(t)

	if err := st.SetSetting(ctx, SettingPasswordHash, "$argon2id$legacy"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	// A request made before accounts existed, to prove requested_by backfills
	// to the "nobody in particular" zero rather than failing the migration. It
	// is written in SQL rather than through CreateRequest because CreateRequest
	// speaks the current schema, and the whole point of the row is that it
	// predates the column.
	if _, err := st.DB().ExecContext(ctx, `
		INSERT INTO requests (media_type, tmdb_id, title, status, created_at, updated_at)
		VALUES ('movie', 27205, 'Inception', 'pending', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z')`,
	); err != nil {
		t.Fatalf("insert pre-RBAC request: %v", err)
	}

	applyRemainingMigrations(t, st)

	var requestedBy int64
	if err := st.DB().QueryRowContext(ctx,
		"SELECT requested_by FROM requests WHERE tmdb_id = 27205").Scan(&requestedBy); err != nil {
		t.Fatalf("read requested_by: %v", err)
	}
	if requestedBy != 0 {
		t.Fatalf("pre-RBAC request requested_by = %d, want 0", requestedBy)
	}

	admin, err := st.GetUserByUsername(ctx, "admin")
	if err != nil {
		t.Fatalf("GetUserByUsername(admin): %v", err)
	}
	if admin.PasswordHash != "$argon2id$legacy" {
		t.Fatalf("folded hash = %q, want the legacy one", admin.PasswordHash)
	}
	if admin.Role != core.RoleAdmin {
		t.Fatalf("folded role = %q, want admin", admin.Role)
	}
	// The timestamps the migration wrote in SQL must read back through the
	// store's own parser, or every account would look like it was never made.
	if admin.CreatedAt.IsZero() || admin.UpdatedAt.IsZero() {
		t.Fatalf("folded timestamps are unparseable: %+v", admin)
	}

	// The setting is gone rather than merely ignored: a stale hash there would
	// be a second, unreachable credential.
	if _, err := st.GetSetting(ctx, SettingPasswordHash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("password_hash setting = %v, want ErrNotFound after the fold", err)
	}
}

// A server that never had a password stays open across the upgrade: no user is
// invented for it.
func TestMigrationLeavesAPasswordlessServerOpen(t *testing.T) {
	st := openPreUsers(t)
	applyRemainingMigrations(t, st)

	n, err := st.CountUsers(context.Background())
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if n != 0 {
		t.Fatalf("CountUsers after upgrading a passwordless server = %d, want 0", n)
	}
}

// usersMigration is the migration under test; the two migration tests above
// build a database at the version just below it and then step over it.
const usersMigration = 11

// openPreUsers opens a store migrated to the version just before
// usersMigration, which is the shape a pre-RBAC Caravan has on disk.
func openPreUsers(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", dsn(filepath.Join(t.TempDir(), "caravan.db")))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	st := &Store{db: db}

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatalf("create schema_version: %v", err)
	}
	for _, m := range loadEmbedded(t) {
		if m.version >= usersMigration {
			break
		}
		if err := st.applyMigration(m); err != nil {
			t.Fatalf("applyMigration %d: %v", m.version, err)
		}
	}
	return st
}

// applyRemainingMigrations steps the store from the pre-usersMigration shape up
// to head.
func applyRemainingMigrations(t *testing.T, st *Store) {
	t.Helper()
	for _, m := range loadEmbedded(t) {
		if m.version < usersMigration {
			continue
		}
		if err := st.applyMigration(m); err != nil {
			t.Fatalf("applyMigration %d: %v", m.version, err)
		}
	}
}

func loadEmbedded(t *testing.T) []migration {
	t.Helper()
	migrations, err := loadMigrations(migrationFiles, "migrations")
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	return migrations
}
