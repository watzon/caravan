package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// tpdbEndpoint is the preset the settings pair used to spell as "". It is
// written out here rather than imported from internal/stashbox: the migration
// holds the same literal, and a test that reads it from the same constant the
// code does would agree with a typo.
const tpdbEndpoint = "https://theporndb.net/graphql"

// seedStashboxSettings builds a database at the last schema that knew the
// settings pair and writes the given rows into it.
func seedStashboxSettings(t *testing.T, path string, kv map[string]string) {
	t.Helper()
	openAtSchemaVersion(t, path, 25)

	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("open seeded database: %v", err)
	}
	defer db.Close()
	for k, v := range kv {
		exec(t, db, `INSERT INTO settings (key, value, updated_at)
			VALUES (?, ?, '2024-01-01T00:00:00Z')`, k, v)
	}
}

// The carry-in: an install that had configured the module comes out the other
// side describing the same box it was already talking to, and the settings the
// description was assembled from are gone rather than left behind as a second
// copy of a credential.
func TestMigrate0026CarriesTheConfiguredEndpointIn(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "caravan.db")
	seedStashboxSettings(t, path, map[string]string{
		SettingStashboxEndpoint: "https://stashdb.org/graphql",
		SettingStashboxAPIKey:   "secret",
		SettingAdultEnabled:     "true",
	})

	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open after upgrade: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	instances, err := st.ListStashboxInstances(ctx)
	if err != nil {
		t.Fatalf("ListStashboxInstances: %v", err)
	}
	if len(instances) != 1 {
		t.Fatalf("instances = %+v, want exactly one", instances)
	}
	in := instances[0]
	if in.ProviderID != core.ProviderStashbox {
		t.Errorf("provider id = %q, want the bare %q — every adult row on disk is pinned to it",
			in.ProviderID, core.ProviderStashbox)
	}
	if in.Endpoint != "https://stashdb.org/graphql" || in.APIKey != "secret" {
		t.Errorf("carried-in instance = %q/%q, want the configured endpoint and key",
			in.Endpoint, in.APIKey)
	}
	// A custom endpoint is a box only its owner can name.
	if in.Name != "Stash-box" {
		t.Errorf("name = %q, want %q for a non-preset endpoint", in.Name, "Stash-box")
	}
	if in.CreatedAt.IsZero() || in.UpdatedAt.IsZero() {
		t.Errorf("timestamps = %v/%v, want the migration's clock", in.CreatedAt, in.UpdatedAt)
	}

	settings, err := st.AllSettings(ctx)
	if err != nil {
		t.Fatalf("AllSettings: %v", err)
	}
	for _, key := range []string{SettingStashboxEndpoint, SettingStashboxAPIKey} {
		if _, ok := settings[key]; ok {
			t.Errorf("%s survived the migration; the credential now has two homes", key)
		}
	}
}

// An empty endpoint meant "the TPDB preset", and the sentinel cannot survive the
// plural: with several rows it would let two instances be silently the same box.
// It resolves to the URL it always stood for, and the row is named accordingly.
func TestMigrate0026ResolvesTheBlankEndpointToTPDB(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "caravan.db")
	seedStashboxSettings(t, path, map[string]string{
		SettingStashboxEndpoint: "",
		SettingStashboxAPIKey:   "tpdb-key",
	})

	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open after upgrade: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	in, err := st.GetStashboxInstanceByProviderID(ctx, core.ProviderStashbox)
	if err != nil {
		t.Fatalf("GetStashboxInstanceByProviderID: %v", err)
	}
	if in.Endpoint != tpdbEndpoint {
		t.Errorf("endpoint = %q, want the preset %q spelled out", in.Endpoint, tpdbEndpoint)
	}
	if in.Name != "ThePornDB" {
		t.Errorf("name = %q, want ThePornDB", in.Name)
	}
	if in.APIKey != "tpdb-key" {
		t.Errorf("api key = %q, want the stored one", in.APIKey)
	}
}

// The preset spelled out is the same box as the preset implied, and gets the
// same name — otherwise which of the two the owner happened to store decides
// what their instance is called.
func TestMigrate0026NamesTheExplicitPresetToo(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "caravan.db")
	seedStashboxSettings(t, path, map[string]string{
		SettingStashboxEndpoint: tpdbEndpoint,
		SettingStashboxAPIKey:   "k",
	})

	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open after upgrade: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	in, err := st.GetStashboxInstanceByProviderID(ctx, core.ProviderStashbox)
	if err != nil {
		t.Fatalf("GetStashboxInstanceByProviderID: %v", err)
	}
	if in.Name != "ThePornDB" {
		t.Errorf("name = %q, want ThePornDB", in.Name)
	}
}

// The enable flow writes the switch before the credential form is submitted, so
// a server with the module on and no key is a real state — and it still has a
// box to point at. The empty key rides in with it.
func TestMigrate0026CarriesAnEnabledModuleWithNoKey(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "caravan.db")
	seedStashboxSettings(t, path, map[string]string{SettingAdultEnabled: "true"})

	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open after upgrade: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	in, err := st.GetStashboxInstanceByProviderID(ctx, core.ProviderStashbox)
	if err != nil {
		t.Fatalf("GetStashboxInstanceByProviderID: %v", err)
	}
	if in.APIKey != "" {
		t.Errorf("api key = %q, want empty — nothing was ever entered", in.APIKey)
	}
	if in.Endpoint != tpdbEndpoint {
		t.Errorf("endpoint = %q, want the preset", in.Endpoint)
	}
}

// An install that never touched the module gets no row. This is the same
// promise 0013 makes by not seeding the Adult library: a description of a box
// nobody configured is a trace of a module that was never switched on.
func TestMigrate0026LeavesAnUnconfiguredInstallEmpty(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "caravan.db")
	// A module explicitly switched off, and no credential ever submitted.
	seedStashboxSettings(t, path, map[string]string{SettingAdultEnabled: "false"})

	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open after upgrade: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	instances, err := st.ListStashboxInstances(ctx)
	if err != nil {
		t.Fatalf("ListStashboxInstances: %v", err)
	}
	if len(instances) != 0 {
		t.Errorf("instances = %+v, want none on an install that never configured the module", instances)
	}
	if _, err := st.GetStashboxInstanceByProviderID(ctx, core.ProviderStashbox); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetStashboxInstanceByProviderID = %v, want ErrNotFound", err)
	}
}

// A fresh install is the unconfigured case too: nothing seeds an instance, and
// the enable flow is what mints the first one.
func TestFreshInstallHasNoStashboxInstances(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	instances, err := st.ListStashboxInstances(ctx)
	if err != nil {
		t.Fatalf("ListStashboxInstances: %v", err)
	}
	if len(instances) != 0 {
		t.Errorf("instances = %+v, want none", instances)
	}
}

// The reason 0026 demotes the bare stash_id indexes: the public boxes are forks
// of one another and mint identical UUIDs, so the same site catalogued on two
// instances is two rows — and under the old global unique index the second one
// simply could not be written.
func TestTwoInstancesMayHoldTheSameStashID(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	a := &core.Series{Kind: core.SeriesKindAdult, StashID: "shared-uuid", Title: "Site (A)",
		Provider: core.ProviderStashbox, ProviderRef: "shared-uuid"}
	if err := st.UpsertSeries(ctx, a); err != nil {
		t.Fatalf("UpsertSeries(instance A): %v", err)
	}
	b := &core.Series{Kind: core.SeriesKindAdult, StashID: "shared-uuid", Title: "Site (B)",
		Provider: core.ProviderStashbox + ":stashdb", ProviderRef: "shared-uuid"}
	if err := st.UpsertSeries(ctx, b); err != nil {
		t.Fatalf("UpsertSeries(instance B): %v", err)
	}
	if a.ID == b.ID {
		t.Fatal("the second instance's site collapsed into the first's row")
	}

	// And each still refreshes onto its own row rather than the other's.
	again := &core.Series{Kind: core.SeriesKindAdult, StashID: "shared-uuid", Title: "Site (B, renamed)",
		Provider: core.ProviderStashbox + ":stashdb", ProviderRef: "shared-uuid"}
	if err := st.UpsertSeries(ctx, again); err != nil {
		t.Fatalf("UpsertSeries(refresh B): %v", err)
	}
	if again.ID != b.ID {
		t.Errorf("refresh of instance B landed on series %d, want %d", again.ID, b.ID)
	}
}

// The episode index keeps a uniqueness, narrowed to the scope where it is still
// true: a scene belongs to one site, so one series may not hold it twice. Across
// series it was never a real constraint, only an artefact of there being one box.
func TestEpisodeStashIDIsUniquePerSeries(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	insert := func(seriesID int64, episode int, stashID string) error {
		_, err := st.DB().ExecContext(ctx, `
			INSERT INTO episodes (series_id, season_number, episode_number, stash_id, title)
			VALUES (?, 2024, ?, ?, 'A Scene')`, seriesID, episode, stashID)
		return err
	}

	for _, id := range []int64{1, 2} {
		_, err := st.DB().ExecContext(ctx, `
			INSERT INTO series (id, kind, title, added_at, updated_at)
			VALUES (?, 'adult', 'Site', '', '')`, id)
		if err != nil {
			t.Fatalf("seed series %d: %v", id, err)
		}
	}

	if err := insert(1, 1, "scene-uuid"); err != nil {
		t.Fatalf("first scene: %v", err)
	}
	// The same UUID under another site is the two-boxes case, and it inserts.
	if err := insert(2, 1, "scene-uuid"); err != nil {
		t.Errorf("the same scene uuid under a second site was refused: %v", err)
	}
	// Twice under one site is the duplicate the import path relies on being
	// refused.
	if err := insert(1, 2, "scene-uuid"); err == nil {
		t.Error("one site held the same scene twice")
	}
	// Unmatched scenes stay unconstrained, as they were before.
	if err := insert(1, 3, ""); err != nil {
		t.Fatalf("first unmatched scene: %v", err)
	}
	if err := insert(1, 4, ""); err != nil {
		t.Errorf("a second unmatched scene was refused: %v", err)
	}
}

func TestStashboxInstanceCRUD(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	in := &core.StashboxInstance{
		ProviderID: core.ProviderStashbox, Name: "ThePornDB",
		Endpoint: tpdbEndpoint, APIKey: "first",
	}
	if err := st.UpsertStashboxInstance(ctx, in); err != nil {
		t.Fatalf("UpsertStashboxInstance: %v", err)
	}
	if in.ID == 0 {
		t.Fatal("insert did not write back an id")
	}

	got, err := st.GetStashboxInstance(ctx, in.ID)
	if err != nil {
		t.Fatalf("GetStashboxInstance: %v", err)
	}
	if got.ProviderID != in.ProviderID || got.Name != in.Name ||
		got.Endpoint != in.Endpoint || got.APIKey != in.APIKey {
		t.Errorf("round-trip = %+v, want %+v", got, in)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Errorf("timestamps = %v/%v, want both set", got.CreatedAt, got.UpdatedAt)
	}

	got.Name = "TPDB"
	got.APIKey = "rotated"
	if err := st.UpsertStashboxInstance(ctx, got); err != nil {
		t.Fatalf("UpsertStashboxInstance(update): %v", err)
	}
	after, err := st.GetStashboxInstanceByProviderID(ctx, core.ProviderStashbox)
	if err != nil {
		t.Fatalf("GetStashboxInstanceByProviderID: %v", err)
	}
	if after.ID != in.ID {
		t.Errorf("update made row %d, want an update of %d", after.ID, in.ID)
	}
	if after.Name != "TPDB" || after.APIKey != "rotated" {
		t.Errorf("after update = %q/%q, want the new name and key", after.Name, after.APIKey)
	}
	// The provider id is identity, not a field: the update never carries it, so
	// no rename can re-point the rows pinned to this instance.
	if after.ProviderID != core.ProviderStashbox {
		t.Errorf("provider id = %q, want it unchanged by an update", after.ProviderID)
	}

	second := &core.StashboxInstance{
		ProviderID: core.ProviderStashbox + ":stashdb", Name: "StashDB",
		Endpoint: "https://stashdb.org/graphql",
	}
	if err := st.UpsertStashboxInstance(ctx, second); err != nil {
		t.Fatalf("UpsertStashboxInstance(second): %v", err)
	}
	list, err := st.ListStashboxInstances(ctx)
	if err != nil {
		t.Fatalf("ListStashboxInstances: %v", err)
	}
	if len(list) != 2 || list[0].ID != in.ID || list[1].ID != second.ID {
		t.Errorf("list = %+v, want both instances in id order", list)
	}

	if err := st.DeleteStashboxInstance(ctx, second.ID); err != nil {
		t.Fatalf("DeleteStashboxInstance: %v", err)
	}
	if _, err := st.GetStashboxInstance(ctx, second.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetStashboxInstance after delete = %v, want ErrNotFound", err)
	}
	if _, err := st.GetStashboxInstanceByProviderID(ctx, "stashbox:nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetStashboxInstanceByProviderID(unknown) = %v, want ErrNotFound", err)
	}
	// An update aimed at a row that is gone is a mistake, not a silent insert.
	if err := st.UpsertStashboxInstance(ctx, &core.StashboxInstance{ID: 9999, Name: "Ghost"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpsertStashboxInstance(missing id) = %v, want ErrNotFound", err)
	}
}

// Both uniquenesses are load-bearing: the provider id because it is what rows
// and chains store, and the name because it is what the owner picks an instance
// by on screen.
func TestStashboxInstanceUniqueness(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	first := &core.StashboxInstance{ProviderID: core.ProviderStashbox, Name: "ThePornDB",
		Endpoint: tpdbEndpoint}
	if err := st.UpsertStashboxInstance(ctx, first); err != nil {
		t.Fatalf("UpsertStashboxInstance: %v", err)
	}

	sameID := &core.StashboxInstance{ProviderID: core.ProviderStashbox, Name: "Another",
		Endpoint: "https://other.test/graphql"}
	if err := st.UpsertStashboxInstance(ctx, sameID); err == nil {
		t.Error("a second instance claimed the same provider id")
	}
	sameName := &core.StashboxInstance{ProviderID: core.ProviderStashbox + ":other", Name: "ThePornDB",
		Endpoint: "https://other.test/graphql"}
	if err := st.UpsertStashboxInstance(ctx, sameName); err == nil {
		t.Error("a second instance claimed the same name")
	}
	// Two accounts on one box is legitimate: nothing constrains the endpoint.
	sameEndpoint := &core.StashboxInstance{ProviderID: core.ProviderStashbox + ":shared", Name: "TPDB (shared)",
		Endpoint: tpdbEndpoint, APIKey: "another-account"}
	if err := st.UpsertStashboxInstance(ctx, sameEndpoint); err != nil {
		t.Errorf("a second account on the same box was refused: %v", err)
	}
}

// The counters behind the delete guard. Both have to see an instance that is
// only ever named in the tail of a chain — a library identifying through
// ["stashbox:stashdb", "stashbox"] uses both, and only its head is a column.
func TestProviderUsageCounters(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	lib := &core.Library{Kind: core.LibraryKindAdult, Name: "Adult", RootPath: "library/Adult",
		Providers: []string{core.ProviderStashbox + ":stashdb", core.ProviderStashbox}}
	if err := st.CreateLibrary(ctx, lib); err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}

	for id, want := range map[string]int{
		core.ProviderStashbox + ":stashdb": 1,
		// The tail, counted once. The base does NOT also claim its instance's
		// library twice over: the match is on the whole quoted id, and
		// "stashbox" does not occur inside "stashbox:stashdb".
		core.ProviderStashbox: 1,
		// The two libraries 0012 seeds, both chained to TMDB.
		core.ProviderTMDB: 2,
		// Nor is the match a prefix: a truncated id claims nothing.
		"tmd": 0,
	} {
		got, err := st.CountLibrariesUsingProvider(ctx, id)
		if err != nil {
			t.Fatalf("CountLibrariesUsingProvider(%q): %v", id, err)
		}
		if got != want {
			t.Errorf("CountLibrariesUsingProvider(%q) = %d, want %d", id, got, want)
		}
	}

	pinned := &core.Series{Kind: core.SeriesKindAdult, Title: "Site",
		Provider: core.ProviderStashbox + ":stashdb", ProviderRef: "uuid-a"}
	if err := st.UpsertSeries(ctx, pinned); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	mv := &core.Movie{Title: "A Film", SortTitle: "a film",
		Provider: core.ProviderStashbox + ":stashdb", ProviderRef: "uuid-b"}
	if err := st.UpsertMovie(ctx, mv); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}

	for id, want := range map[string]int{
		core.ProviderStashbox + ":stashdb": 2, // movies and series counted together
		core.ProviderStashbox:              0,
	} {
		got, err := st.CountItemsPinnedToProvider(ctx, id)
		if err != nil {
			t.Fatalf("CountItemsPinnedToProvider(%q): %v", id, err)
		}
		if got != want {
			t.Errorf("CountItemsPinnedToProvider(%q) = %d, want %d", id, got, want)
		}
	}
}
