package store

import (
	"context"
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

	lib := newLibrary(t, st, core.LibraryKindTV, "Kids", "library/Kids")
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

// The roster is replaced wholesale, not merged: the submitted list is the
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

	// Unrestricting does not put the flag back. Nobody asked for the LAN to see
	// it again, and re-advertising silently would be exactly the surprise
	// clearing it was there to prevent, re-sharing is a second act on the Reach
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

// SetLibraryActive is the master switch, and it deletes nothing. The grants and
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
	extra := newLibrary(t, st, core.LibraryKindTV, "Kids", "library/Kids")
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
