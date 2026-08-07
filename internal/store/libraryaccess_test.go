package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// newLibrary inserts a library through the write door and returns it, so a test
// reads back exactly what a create form would have produced.
func newLibrary(t *testing.T, st *Store, kind, name, root string) *core.Library {
	t.Helper()
	lib := &core.Library{Kind: kind, Name: name, RootPath: root, DLNAVisible: true}
	if err := st.CreateLibrary(context.Background(), lib); err != nil {
		t.Fatalf("CreateLibrary(%q): %v", name, err)
	}
	return lib
}

func newMember(t *testing.T, st *Store, username string) *core.User {
	t.Helper()
	u := &core.User{Username: username, PasswordHash: "hash", Role: core.RoleMember}
	if err := st.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser(%q): %v", username, err)
	}
	return u
}

// A library is born active and open. Every caller written before the columns
// existed builds a core.Library without them, and a zero value that meant "off"
// would have the create form produce libraries nobody can see.
func TestCreateLibraryIsBornActiveAndOpen(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	lib := newLibrary(t, st, core.LibraryKindTV, "Anime", "library/Anime")
	if !lib.Active || lib.Restricted {
		t.Errorf("created library = {active:%t, restricted:%t}, want active and open",
			lib.Active, lib.Restricted)
	}
	stored, err := st.GetLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatalf("GetLibrary: %v", err)
	}
	if !stored.Active || stored.Restricted {
		t.Errorf("stored library = {active:%t, restricted:%t}, want active and open",
			stored.Active, stored.Restricted)
	}
}

// The roster is replaced wholesale, not merged: the submitted list IS the
// answer, so an account left off it loses the grant in the same write that
// hands one to whoever was added.
func TestSetLibraryAccessReplacesTheRosterWholesale(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	lib := newLibrary(t, st, core.LibraryKindTV, "Kids", "library/Kids")
	alice := newMember(t, st, "alice")
	bob := newMember(t, st, "bob")

	if err := st.SetLibraryAccess(ctx, lib.ID, true, []int64{alice.ID, bob.ID}); err != nil {
		t.Fatalf("SetLibraryAccess: %v", err)
	}
	got, err := st.ListLibraryAccess(ctx, lib.ID)
	if err != nil {
		t.Fatalf("ListLibraryAccess: %v", err)
	}
	if want := []int64{alice.ID, bob.ID}; !reflect.DeepEqual(got, want) {
		t.Fatalf("roster = %v, want %v", got, want)
	}

	// Bob is taken off. Nothing about alice is re-submitted, and nothing about
	// alice changes.
	if err := st.SetLibraryAccess(ctx, lib.ID, true, []int64{alice.ID}); err != nil {
		t.Fatalf("SetLibraryAccess: %v", err)
	}
	got, err = st.ListLibraryAccess(ctx, lib.ID)
	if err != nil {
		t.Fatalf("ListLibraryAccess: %v", err)
	}
	if want := []int64{alice.ID}; !reflect.DeepEqual(got, want) {
		t.Errorf("roster = %v, want %v — the list submitted is the whole answer", got, want)
	}

	stored, err := st.GetLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatalf("GetLibrary: %v", err)
	}
	if !stored.Restricted {
		t.Error("library is not restricted after a restricting write")
	}
}

// Restriction and LAN sharing cannot both be true: DLNA has no accounts, so
// anything on the tree is readable by every device in the house. Of the two, it
// is the restriction that was just asked for.
func TestSetLibraryAccessClearsDLNAWhenRestricting(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	lib := newLibrary(t, st, core.LibraryKindTV, "Kids", "library/Kids")
	if !lib.DLNAVisible {
		t.Fatalf("fixture library is not shared; the test proves nothing")
	}
	before, err := st.GetSetting(ctx, SettingDLNAUpdateID)
	if err != nil {
		t.Fatalf("GetSetting(dlna update id): %v", err)
	}

	if err := st.SetLibraryAccess(ctx, lib.ID, true, nil); err != nil {
		t.Fatalf("SetLibraryAccess: %v", err)
	}
	stored, err := st.GetLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatalf("GetLibrary: %v", err)
	}
	if stored.DLNAVisible {
		t.Error("restricted library is still advertised on the LAN")
	}
	// A cached client has to learn the container is gone.
	after, err := st.GetSetting(ctx, SettingDLNAUpdateID)
	if err != nil {
		t.Fatalf("GetSetting(dlna update id): %v", err)
	}
	if after == before {
		t.Errorf("dlna update id stayed %q; a television keeps showing the container", before)
	}

	// Unrestricting does NOT put the flag back. Nobody asked for the LAN to see
	// it again, and re-advertising silently would be exactly the surprise
	// clearing it was there to prevent — re-sharing is a second act on the Reach
	// card.
	if err := st.SetLibraryAccess(ctx, lib.ID, false, nil); err != nil {
		t.Fatalf("SetLibraryAccess: %v", err)
	}
	stored, err = st.GetLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatalf("GetLibrary: %v", err)
	}
	if stored.DLNAVisible {
		t.Error("unrestricting re-advertised the library on the LAN by itself")
	}
	if stored.Restricted {
		t.Error("library is still restricted after an unrestricting write")
	}
}

// A grant is a live permission, not a record of one: when the account or the
// library goes, so does the row. A permission that outlived what it named would
// be waiting for whoever next lands on that id.
func TestLibraryAccessCascades(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	kids := newLibrary(t, st, core.LibraryKindTV, "Kids", "library/Kids")
	docs := newLibrary(t, st, core.LibraryKindTV, "Docs", "library/Docs")
	alice := newMember(t, st, "alice")

	for _, lib := range []*core.Library{kids, docs} {
		if err := st.SetLibraryAccess(ctx, lib.ID, true, []int64{alice.ID}); err != nil {
			t.Fatalf("SetLibraryAccess(%d): %v", lib.ID, err)
		}
	}

	// A deleted library takes its grants with it. Deletion needs the library
	// empty and not its kind's default, which Docs is not.
	if err := st.DeleteLibrary(ctx, docs.ID); err != nil {
		t.Fatalf("DeleteLibrary(%d): %v", docs.ID, err)
	}
	grants, err := st.ListLibraryAccessForUser(ctx, alice.ID)
	if err != nil {
		t.Fatalf("ListLibraryAccessForUser: %v", err)
	}
	if want := map[int64]bool{kids.ID: true}; !reflect.DeepEqual(grants, want) {
		t.Errorf("grants after library delete = %v, want %v", grants, want)
	}

	// A deleted account takes its grants with it.
	if err := st.DeleteUser(ctx, alice.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	roster, err := st.ListLibraryAccess(ctx, kids.ID)
	if err != nil {
		t.Fatalf("ListLibraryAccess: %v", err)
	}
	if len(roster) != 0 {
		t.Errorf("roster = %v after the account was deleted, want empty", roster)
	}
}

// ListLibraryAccessForUser answers per account, not per library: it is the
// query a session runs once to learn every grant it holds.
func TestListLibraryAccessForUser(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	kids := newLibrary(t, st, core.LibraryKindTV, "Kids", "library/Kids")
	docs := newLibrary(t, st, core.LibraryKindTV, "Docs", "library/Docs")
	alice := newMember(t, st, "alice")
	bob := newMember(t, st, "bob")

	if err := st.SetLibraryAccess(ctx, kids.ID, true, []int64{alice.ID, bob.ID}); err != nil {
		t.Fatalf("SetLibraryAccess(kids): %v", err)
	}
	if err := st.SetLibraryAccess(ctx, docs.ID, true, []int64{alice.ID}); err != nil {
		t.Fatalf("SetLibraryAccess(docs): %v", err)
	}

	for _, tt := range []struct {
		user *core.User
		want map[int64]bool
	}{
		{user: alice, want: map[int64]bool{kids.ID: true, docs.ID: true}},
		{user: bob, want: map[int64]bool{kids.ID: true}},
	} {
		got, err := st.ListLibraryAccessForUser(ctx, tt.user.ID)
		if err != nil {
			t.Fatalf("ListLibraryAccessForUser(%q): %v", tt.user.Username, err)
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("grants of %q = %v, want %v", tt.user.Username, got, tt.want)
		}
	}

	// The API-key credential and the open install both authenticate as an admin
	// with user id 0, and there is no users row 0 to grant.
	got, err := st.ListLibraryAccessForUser(ctx, 0)
	if err != nil {
		t.Fatalf("ListLibraryAccessForUser(0): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("user 0 holds %v, want nothing", got)
	}
}

// SetLibraryActive is the master switch, and it deletes nothing — the grants and
// the roster are exactly where they were when it comes back on.
func TestSetLibraryActive(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	lib := newLibrary(t, st, core.LibraryKindTV, "Kids", "library/Kids")
	alice := newMember(t, st, "alice")
	if err := st.SetLibraryAccess(ctx, lib.ID, true, []int64{alice.ID}); err != nil {
		t.Fatalf("SetLibraryAccess: %v", err)
	}

	if err := st.SetLibraryActive(ctx, lib.ID, false); err != nil {
		t.Fatalf("SetLibraryActive(false): %v", err)
	}
	stored, err := st.GetLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatalf("GetLibrary: %v", err)
	}
	if stored.Active {
		t.Error("library is still active after being switched off")
	}
	if !stored.Restricted {
		t.Error("switching a library off changed its restriction")
	}
	roster, err := st.ListLibraryAccess(ctx, lib.ID)
	if err != nil {
		t.Fatalf("ListLibraryAccess: %v", err)
	}
	if want := []int64{alice.ID}; !reflect.DeepEqual(roster, want) {
		t.Errorf("roster = %v after a deactivation, want %v — off deletes nothing", roster, want)
	}

	if err := st.SetLibraryActive(ctx, lib.ID, true); err != nil {
		t.Fatalf("SetLibraryActive(true): %v", err)
	}
	stored, err = st.GetLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatalf("GetLibrary: %v", err)
	}
	if !stored.Active {
		t.Error("library did not come back on")
	}
}

// The DLNA tree carries a container while `active AND dlna_visible`, so the
// switch is a tree change exactly for a library the LAN was being shown.
func TestSetLibraryActiveBumpsDLNAUpdateIDOnlyForSharedLibraries(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	shared := newLibrary(t, st, core.LibraryKindTV, "Kids", "library/Kids")
	private := &core.Library{Kind: core.LibraryKindTV, Name: "Docs",
		RootPath: "library/Docs", DLNAVisible: false}
	if err := st.CreateLibrary(ctx, private); err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}

	updateID := func() string {
		t.Helper()
		v, err := st.GetSetting(ctx, SettingDLNAUpdateID)
		if err != nil {
			t.Fatalf("GetSetting(dlna update id): %v", err)
		}
		return v
	}

	before := updateID()
	if err := st.SetLibraryActive(ctx, private.ID, false); err != nil {
		t.Fatalf("SetLibraryActive(private): %v", err)
	}
	if got := updateID(); got != before {
		t.Errorf("dlna update id moved to %q for a library the tree never held", got)
	}

	if err := st.SetLibraryActive(ctx, shared.ID, false); err != nil {
		t.Fatalf("SetLibraryActive(shared): %v", err)
	}
	if got := updateID(); got == before {
		t.Errorf("dlna update id stayed %q; the container left the tree unannounced", before)
	}
}

// UpdateLibrary is the other door onto the same flag, and it must announce the
// same tree change.
func TestUpdateLibraryBumpsDLNAUpdateIDWhenActiveChanges(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	lib := newLibrary(t, st, core.LibraryKindTV, "Kids", "library/Kids")
	before, err := st.GetSetting(ctx, SettingDLNAUpdateID)
	if err != nil {
		t.Fatalf("GetSetting(dlna update id): %v", err)
	}

	lib.Active = false
	if err := st.UpdateLibrary(ctx, lib); err != nil {
		t.Fatalf("UpdateLibrary: %v", err)
	}
	stored, err := st.GetLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatalf("GetLibrary: %v", err)
	}
	if stored.Active {
		t.Error("UpdateLibrary did not carry the active flag")
	}
	after, err := st.GetSetting(ctx, SettingDLNAUpdateID)
	if err != nil {
		t.Fatalf("GetSetting(dlna update id): %v", err)
	}
	if after == before {
		t.Errorf("dlna update id stayed %q after a shared library was switched off", before)
	}
}

// The zero-traffic guard in its general form: "is this kind reachable at all".
func TestAnyActiveLibraryOfKind(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	for _, kind := range []string{core.LibraryKindMovie, core.LibraryKindTV} {
		got, err := st.AnyActiveLibraryOfKind(ctx, kind)
		if err != nil {
			t.Fatalf("AnyActiveLibraryOfKind(%q): %v", kind, err)
		}
		if !got {
			t.Errorf("AnyActiveLibraryOfKind(%q) = false on a seeded install", kind)
		}
	}

	// A kind with no library at all is unreachable, which is what an install
	// that never enabled the adult module looks like.
	got, err := st.AnyActiveLibraryOfKind(ctx, core.LibraryKindAdult)
	if err != nil {
		t.Fatalf("AnyActiveLibraryOfKind(adult): %v", err)
	}
	if got {
		t.Error("AnyActiveLibraryOfKind(adult) = true with no adult library on the install")
	}

	// One library of a kind switched off, another still on: the kind is still
	// reachable, because the question is about the install and not the row.
	extra := newLibrary(t, st, core.LibraryKindTV, "Anime", "library/Anime")
	tv, err := st.GetLibraryByKind(ctx, core.LibraryKindTV)
	if err != nil {
		t.Fatalf("GetLibraryByKind(tv): %v", err)
	}
	if err := st.SetLibraryActive(ctx, tv.ID, false); err != nil {
		t.Fatalf("SetLibraryActive: %v", err)
	}
	got, err = st.AnyActiveLibraryOfKind(ctx, core.LibraryKindTV)
	if err != nil {
		t.Fatalf("AnyActiveLibraryOfKind(tv): %v", err)
	}
	if !got {
		t.Error("AnyActiveLibraryOfKind(tv) = false with one of two tv libraries still on")
	}

	if err := st.SetLibraryActive(ctx, extra.ID, false); err != nil {
		t.Fatalf("SetLibraryActive: %v", err)
	}
	got, err = st.AnyActiveLibraryOfKind(ctx, core.LibraryKindTV)
	if err != nil {
		t.Fatalf("AnyActiveLibraryOfKind(tv): %v", err)
	}
	if got {
		t.Error("AnyActiveLibraryOfKind(tv) = true with every tv library switched off")
	}
}

// ---------------------------------------------------------------------------
// 0027, upgrade in place.
// ---------------------------------------------------------------------------

// atSchema26 builds a populated database frozen one migration before the access
// columns and hands the caller the raw handle to seed it with. The seeding has
// to be raw SQL for atSchema12's reason: the store's own writers speak the
// columns 0027 adds.
func atSchema26(t *testing.T, seed func(*sql.DB)) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "caravan.db")
	openAtSchemaVersion(t, path, 26)

	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("open seeded database: %v", err)
	}
	seed(db)
	if err := db.Close(); err != nil {
		t.Fatalf("close seeded database: %v", err)
	}
	return path
}

// seedAdultInstall writes the pre-0027 shape of an install with the module
// configured: an adult library row, the switch, and two housemates of whom one
// was granted.
func seedAdultInstall(db *sql.DB, t *testing.T, enabled string) {
	t.Helper()
	exec(t, db, `INSERT INTO libraries (kind, name, root_path, dlna_visible, provider, providers, is_default)
		VALUES ('adult', 'Adult', 'library/Adult', 0, 'stashbox', '["stashbox"]', 1)`)
	if enabled != "" {
		exec(t, db, `INSERT INTO settings (key, value, updated_at)
			VALUES ('adult_enabled', ?, '2024-01-01T00:00:00Z')`, enabled)
	}
	exec(t, db, `INSERT INTO users (id, username, password_hash, role, adult_access, created_at, updated_at)
		VALUES (4, 'granted', 'hash', 'member', 1, '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z')`)
	exec(t, db, `INSERT INTO users (id, username, password_hash, role, adult_access, created_at, updated_at)
		VALUES (5, 'ungranted', 'hash', 'member', 0, '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z')`)
}

func upgraded(t *testing.T, path string) *Store {
	t.Helper()
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open after upgrade: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// (a) The module was on. The library comes out active, restricted, and granted
// to exactly the account that held the flag.
func TestMigrate0027CarriesAnEnabledModuleAcross(t *testing.T) {
	ctx := context.Background()
	path := atSchema26(t, func(db *sql.DB) { seedAdultInstall(db, t, "true") })
	st := upgraded(t, path)

	lib, err := st.GetLibraryByKind(ctx, core.LibraryKindAdult)
	if err != nil {
		t.Fatalf("GetLibraryByKind(adult): %v", err)
	}
	if !lib.Active {
		t.Error("adult library came out inactive under a module that was switched on")
	}
	if !lib.Restricted {
		t.Error("adult library came out open; it was only ever reachable by a granted account")
	}

	roster, err := st.ListLibraryAccess(ctx, lib.ID)
	if err != nil {
		t.Fatalf("ListLibraryAccess: %v", err)
	}
	if want := []int64{4}; !reflect.DeepEqual(roster, want) {
		t.Errorf("roster = %v, want %v — exactly the account that held adult_access", roster, want)
	}
}

// (b) The switch absent or off parses as off, which is store.AdultEnabled's own
// rule. The grants are still written: they describe who the owner named, and
// they are what a re-enable has to find intact.
func TestMigrate0027ReadsAnAbsentOrOffSwitchAsOff(t *testing.T) {
	ctx := context.Background()
	for _, tt := range []struct{ name, enabled string }{
		{name: "absent", enabled: ""},
		{name: "false", enabled: "false"},
		{name: "unparseable", enabled: "yes-please"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := atSchema26(t, func(db *sql.DB) { seedAdultInstall(db, t, tt.enabled) })
			st := upgraded(t, path)

			lib, err := st.GetLibraryByKind(ctx, core.LibraryKindAdult)
			if err != nil {
				t.Fatalf("GetLibraryByKind(adult): %v", err)
			}
			if lib.Active {
				t.Errorf("adult library is active with adult_enabled %q", tt.enabled)
			}
			if !lib.Restricted {
				t.Error("adult library came out open")
			}
			roster, err := st.ListLibraryAccess(ctx, lib.ID)
			if err != nil {
				t.Fatalf("ListLibraryAccess: %v", err)
			}
			if want := []int64{4}; !reflect.DeepEqual(roster, want) {
				t.Errorf("roster = %v, want %v — a re-enable must find the grants intact",
					roster, want)
			}
		})
	}
}

// (c) Two adult libraries: the backfill keys on the kind, so both are restricted
// and both carry the grant. Nothing keys on a provider id — 0026 made those
// instance-qualified, so a library talking to a second box would have been
// missed.
func TestMigrate0027CarriesEveryAdultLibrary(t *testing.T) {
	ctx := context.Background()
	path := atSchema26(t, func(db *sql.DB) {
		seedAdultInstall(db, t, "true")
		exec(t, db, `INSERT INTO libraries (kind, name, root_path, dlna_visible, provider, providers, is_default)
			VALUES ('adult', 'Scenes', 'library/Scenes', 0, 'stashbox:stashdb',
			        '["stashbox:stashdb"]', 0)`)
	})
	st := upgraded(t, path)

	libs, err := st.ListLibrariesByKind(ctx, core.LibraryKindAdult)
	if err != nil {
		t.Fatalf("ListLibrariesByKind(adult): %v", err)
	}
	if len(libs) != 2 {
		t.Fatalf("adult libraries = %+v, want two", libs)
	}
	for _, lib := range libs {
		if !lib.Active || !lib.Restricted {
			t.Errorf("library %q = {active:%t, restricted:%t}, want active and restricted",
				lib.Name, lib.Active, lib.Restricted)
		}
		roster, err := st.ListLibraryAccess(ctx, lib.ID)
		if err != nil {
			t.Fatalf("ListLibraryAccess(%d): %v", lib.ID, err)
		}
		if want := []int64{4}; !reflect.DeepEqual(roster, want) {
			t.Errorf("roster of %q = %v, want %v", lib.Name, roster, want)
		}
	}
}

// (d) An install that never enabled the module: nothing is written anywhere, and
// the libraries it does have come out in the state they were already in.
func TestMigrate0027LeavesANonAdultInstallAlone(t *testing.T) {
	ctx := context.Background()
	path := atSchema26(t, func(db *sql.DB) {
		exec(t, db, `INSERT INTO users (id, username, password_hash, role, adult_access, created_at, updated_at)
			VALUES (4, 'housemate', 'hash', 'member', 0, '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z')`)
	})
	st := upgraded(t, path)

	libs, err := st.ListLibraries(ctx)
	if err != nil {
		t.Fatalf("ListLibraries: %v", err)
	}
	if len(libs) != 2 {
		t.Fatalf("libraries = %+v, want the two seeded ones", libs)
	}
	for _, lib := range libs {
		if !lib.Active || lib.Restricted {
			t.Errorf("library %q = {active:%t, restricted:%t}, want active and open",
				lib.Name, lib.Active, lib.Restricted)
		}
		roster, err := st.ListLibraryAccess(ctx, lib.ID)
		if err != nil {
			t.Fatalf("ListLibraryAccess(%d): %v", lib.ID, err)
		}
		if len(roster) != 0 {
			t.Errorf("library %q gained a roster of %v on upgrade", lib.Name, roster)
		}
	}
	grants, err := st.ListLibraryAccessForUser(ctx, 4)
	if err != nil {
		t.Fatalf("ListLibraryAccessForUser: %v", err)
	}
	if len(grants) != 0 {
		t.Errorf("housemate gained %v on upgrade; a permission an upgrade switches on is one nobody granted",
			grants)
	}
}

// The migration reads users.adult_access and settings.adult_enabled and removes
// neither. An old backup re-migrates from its own version on every open, so
// 0027 has to be able to read them forever — retirement is 0028's, and folding
// the two together would make this file unable to run against the backups it
// exists to upgrade.
func TestMigrate0027LeavesTheOldColumnsInPlace(t *testing.T) {
	ctx := context.Background()
	path := atSchema26(t, func(db *sql.DB) { seedAdultInstall(db, t, "true") })
	st := upgraded(t, path)

	enabled, err := st.AdultEnabled(ctx)
	if err != nil {
		t.Fatalf("AdultEnabled: %v", err)
	}
	if !enabled {
		t.Error("adult_enabled did not survive 0027")
	}
	u, err := st.GetUser(ctx, 4)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if !u.AdultAccess {
		t.Error("users.adult_access did not survive 0027")
	}
}
