package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

func TestDownloadClientCRUD(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	if _, err := st.GetDownloadClient(ctx, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetDownloadClient(absent) = %v, want ErrNotFound", err)
	}
	list, err := st.ListDownloadClients(ctx)
	if err != nil {
		t.Fatalf("ListDownloadClients: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("ListDownloadClients on a fresh db = %v, want empty", list)
	}

	qbit := core.DownloadClientConfig{
		Type:     core.DownloadClientQBittorrent,
		Name:     "qBit",
		URL:      "http://127.0.0.1:8080",
		Username: "admin",
		Password: "adminadmin",
		Category: "caravan",
		Priority: 10,
		Enabled:  true,
	}
	if err := st.UpsertDownloadClient(ctx, &qbit); err != nil {
		t.Fatalf("UpsertDownloadClient: %v", err)
	}
	if qbit.ID == 0 {
		t.Fatal("UpsertDownloadClient did not write back an ID")
	}

	got, err := st.GetDownloadClient(ctx, qbit.ID)
	if err != nil {
		t.Fatalf("GetDownloadClient: %v", err)
	}
	if !reflect.DeepEqual(*got, qbit) {
		t.Errorf("GetDownloadClient = %+v, want %+v", *got, qbit)
	}

	// Update in place: same id, no second row, and the credential the caller
	// carried is what comes back.
	qbit.Name = "qBit (renamed)"
	qbit.Password = "rotated"
	qbit.Enabled = false
	if err := st.UpsertDownloadClient(ctx, &qbit); err != nil {
		t.Fatalf("UpsertDownloadClient update: %v", err)
	}
	list, err = st.ListDownloadClients(ctx)
	if err != nil {
		t.Fatalf("ListDownloadClients: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListDownloadClients = %+v, want one row after update", list)
	}
	if !reflect.DeepEqual(list[0], qbit) {
		t.Errorf("ListDownloadClients[0] = %+v, want %+v", list[0], qbit)
	}

	if err := st.DeleteDownloadClient(ctx, qbit.ID); err != nil {
		t.Fatalf("DeleteDownloadClient: %v", err)
	}
	if _, err := st.GetDownloadClient(ctx, qbit.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetDownloadClient after delete = %v, want ErrNotFound", err)
	}
}

// Updating a row that is not there must not quietly insert one: the HTTP layer
// turns ErrNotFound into a 404.
func TestUpsertDownloadClientMissingIDIsNotFound(t *testing.T) {
	st, _ := openTemp(t)

	cfg := core.DownloadClientConfig{
		ID:     404,
		Type:   core.DownloadClientSABnzbd,
		Name:   "ghost",
		URL:    "http://127.0.0.1:8085",
		APIKey: "k",
	}
	if err := st.UpsertDownloadClient(context.Background(), &cfg); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpsertDownloadClient(missing id) = %v, want ErrNotFound", err)
	}
}

// Routing takes the first enabled client that can carry a release, so the
// order the store hands them back is part of the contract: lowest priority
// first, name breaking the tie, disabled rows absent entirely.
func TestListEnabledDownloadClientsOrdersByPriority(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	for _, c := range []core.DownloadClientConfig{
		{Type: core.DownloadClientQBittorrent, Name: "second", URL: "http://a", Priority: 50, Enabled: true},
		{Type: core.DownloadClientSABnzbd, Name: "first", URL: "http://b", Priority: 5, Enabled: true},
		{Type: core.DownloadClientNZBGet, Name: "off", URL: "http://c", Priority: 1, Enabled: false},
		{Type: core.DownloadClientQBittorrent, Name: "aaa-tie", URL: "http://d", Priority: 50, Enabled: true},
	} {
		cfg := c
		if err := st.UpsertDownloadClient(ctx, &cfg); err != nil {
			t.Fatalf("UpsertDownloadClient(%s): %v", c.Name, err)
		}
	}

	enabled, err := st.ListEnabledDownloadClients(ctx)
	if err != nil {
		t.Fatalf("ListEnabledDownloadClients: %v", err)
	}
	var names []string
	for _, c := range enabled {
		names = append(names, c.Name)
	}
	want := []string{"first", "aaa-tie", "second"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("enabled clients = %v, want %v", names, want)
	}

	// The disabled row keeps its configuration; it is only skipped.
	all, err := st.ListDownloadClients(ctx)
	if err != nil {
		t.Fatalf("ListDownloadClients: %v", err)
	}
	if len(all) != 4 {
		t.Errorf("ListDownloadClients = %d rows, want all 4 including the disabled one", len(all))
	}
}

// 0007 reshapes a table 0001 already created, on databases that may already
// hold rows. Upgrading is the normal path (SPEC §7), so the reshape has to
// survive a row written under the old schema rather than only a fresh install.
func TestMigration0007UpgradesAnExistingTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "caravan.db")
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	st := &Store{db: db}

	migrations, err := loadMigrations(migrationFiles, "migrations")
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatalf("create schema_version: %v", err)
	}

	// Everything up to, but not including, the migration under test.
	const target = 7
	for _, m := range migrations {
		if m.version >= target {
			break
		}
		if err := st.applyMigration(m); err != nil {
			t.Fatalf("applyMigration %d: %v", m.version, err)
		}
	}

	// A row in the pre-0007 shape, host/port and all.
	ts := formatTime(now())
	if _, err := db.Exec(`INSERT INTO download_clients
		(name, kind, host, port, username, password, category, enabled, created_at, updated_at)
		VALUES ('legacy', 'qbittorrent', '127.0.0.1', 8080, 'admin', 'adminadmin', 'caravan', 1, ?, ?)`,
		ts, ts); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	for _, m := range migrations {
		if m.version < target {
			continue
		}
		if err := st.applyMigration(m); err != nil {
			t.Fatalf("applyMigration %d: %v", m.version, err)
		}
	}

	got, err := st.ListDownloadClients(context.Background())
	if err != nil {
		t.Fatalf("ListDownloadClients after upgrade: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("clients after upgrade = %+v, want the legacy row kept", got)
	}
	want := core.DownloadClientConfig{
		ID: got[0].ID, Type: core.DownloadClientQBittorrent, Name: "legacy",
		// The host/port pair does not survive: it is replaced by a base URL
		// the user re-enters, which is what makes this settings-class config.
		URL: "", Username: "admin", Password: "adminadmin", Category: "caravan",
		Priority: 25, Enabled: true,
	}
	if !reflect.DeepEqual(got[0], want) {
		t.Errorf("upgraded row = %+v, want %+v", got[0], want)
	}
}

// download_clients.name is unique, which is what lets the HTTP layer answer a
// duplicate with 409 instead of a 500.
func TestUpsertDownloadClientRejectsDuplicateName(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	first := core.DownloadClientConfig{
		Type: core.DownloadClientQBittorrent, Name: "dup", URL: "http://a", Username: "u", Enabled: true,
	}
	if err := st.UpsertDownloadClient(ctx, &first); err != nil {
		t.Fatalf("UpsertDownloadClient: %v", err)
	}
	second := core.DownloadClientConfig{
		Type: core.DownloadClientSABnzbd, Name: "dup", URL: "http://b", APIKey: "k", Enabled: true,
	}
	if err := st.UpsertDownloadClient(ctx, &second); err == nil {
		t.Fatal("UpsertDownloadClient with a duplicate name = nil, want a constraint failure")
	}
}
