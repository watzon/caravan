package api

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/cardigann"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/indexer/catalog"
	"github.com/watzon/caravan/internal/store"
)

func TestIndexerCatalogExposesTruthfulMetadataInventorySeparately(t *testing.T) {
	h, _, _ := newTestServer(t, WithLocalDefinitions(func(id string) (LocalDefinitionSchema, bool) {
		switch id {
		case "thepiratebay", "rutor", "nyaa", "tokyotosho",
			"builtin:thepiratebay", "builtin:rutor", "builtin:nyaa", "builtin:tokyotosho":
			return LocalDefinitionSchema{Settings: []string{"apiurl"}}, true
		}
		return LocalDefinitionSchema{}, false
	}))

	rec := do(t, h, http.MethodGet, "/api/v1/indexers/catalog", "")
	wantStatus(t, rec, http.StatusOK)
	var response struct {
		Definitions []catalog.Definition     `json:"definitions"`
		Inventory   []catalog.InventoryEntry `json:"inventory"`
	}
	decodeBody(t, rec, &response)
	if len(response.Inventory) != 542 {
		t.Fatalf("inventory = %d, want 542", len(response.Inventory))
	}
	foundOperational1337x := false
	for _, definition := range response.Definitions {
		if definition.ID == "1337x" {
			foundOperational1337x = true
		}
	}
	if foundOperational1337x {
		t.Fatal("1337x entered the operational definitions array")
	}
	x, ok := inventoryByMetadataID(response.Inventory, "1337x")
	if !ok || x.Addable || x.DefinitionID != "" || x.State != catalog.InventoryStateMetadataOnly || len(x.MetadataURLs) == 0 {
		t.Fatalf("1337x inventory = %+v", x)
	}
	tpb, ok := inventoryByMetadataID(response.Inventory, "thepiratebay")
	if !ok || !tpb.Addable || tpb.DefinitionID != "builtin:thepiratebay" || tpb.State != catalog.InventoryStateVerified {
		t.Fatalf("Pirate Bay inventory = %+v", tpb)
	}
	nyaa, ok := inventoryByMetadataID(response.Inventory, "nyaasi")
	if !ok || !nyaa.Addable || nyaa.DefinitionID != "builtin:nyaa" || nyaa.State != catalog.InventoryStateVerified {
		t.Fatalf("Nyaa inventory = %+v", nyaa)
	}
}

func TestIndexerCatalogProjectsManagedExecutableStatusWithoutPackLifecycle(t *testing.T) {
	const revision = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const digest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	h, _, _ := newTestServer(t, WithDefinitionInventoryStatuses([]catalog.ExecutionStatus{{
		MetadataID: "shanaproject", DefinitionID: "managed:shanaproject", Source: "managed", Revision: revision, Digest: digest,
		State: catalog.InventoryStateVerified, Addable: true, BaseURLs: []string{"https://www.shanaproject.com"}, Unsupported: []string{},
	}}))
	rec := do(t, h, http.MethodGet, "/api/v1/indexers/catalog?kind=torrent&q=shana", "")
	wantStatus(t, rec, http.StatusOK)
	var body struct {
		Inventory []catalog.InventoryEntry `json:"inventory"`
	}
	decodeBody(t, rec, &body)
	if len(body.Inventory) != 1 || !body.Inventory[0].Addable || len(body.Inventory[0].Definitions) != 1 {
		t.Fatalf("managed inventory = %+v", body.Inventory)
	}
	definition := body.Inventory[0].Definitions[0]
	if definition.DefinitionID != "managed:shanaproject" || definition.Source != "managed" || definition.Revision != revision || definition.Digest != digest || !definition.Addable {
		t.Fatalf("managed execution status = %+v", definition)
	}
}

func TestRuntimeDefinitionLookupMissIsAuthoritative(t *testing.T) {
	h, _, _ := newTestServer(t, WithLocalDefinitions(func(string) (LocalDefinitionSchema, bool) {
		return LocalDefinitionSchema{}, false
	}))

	rec := do(t, h, http.MethodGet, "/api/v1/indexers/catalog", "")
	wantStatus(t, rec, http.StatusOK)
	var response struct {
		Definitions []catalog.Definition `json:"definitions"`
	}
	decodeBody(t, rec, &response)
	for _, definition := range response.Definitions {
		if definition.DefinitionID != "" {
			t.Fatalf("runtime-missing local definition remained addable: %+v", definition)
		}
	}

	rec = do(t, h, http.MethodPost, "/api/v1/indexers", `{"name":"missing builtin","definition_id":"thepiratebay","url":"https://thepiratebay.org","type":"torznab","categories":[],"enabled":false}`)
	wantStatus(t, rec, http.StatusBadRequest)
}

func inventoryByMetadataID(inventory []catalog.InventoryEntry, id string) (catalog.InventoryEntry, bool) {
	for _, entry := range inventory {
		if entry.ID == id {
			return entry, true
		}
	}
	return catalog.InventoryEntry{}, false
}

func inventoryDigest(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return fmt.Sprintf("sha256:%x", sum[:])
}

func inventoryEntry(source, revision, id, metadataID, state string, unsupported ...string) core.DefinitionPackEntry {
	return core.DefinitionPackEntry{
		Source: source, Revision: revision, DefinitionRef: source + ":" + id, MetadataID: metadataID,
		Path: "definitions/" + id + ".yml", Digest: inventoryDigest(source, revision, id), State: state,
		Unsupported: append([]string(nil), unsupported...), ApprovedOrigins: []string{"https://tracker.example"},
	}
}

func installInventoryRevision(t *testing.T, st *store.Store, publicKey ed25519.PublicKey, source, revision string, installedAt time.Time, entries []core.DefinitionPackEntry) {
	t.Helper()
	fingerprint := inventoryDigest(string(publicKey))
	archiveDigest := inventoryDigest(source, revision, "archive")
	runnable := 0
	for _, entry := range entries {
		if entry.State == core.DefinitionPackEntryRunnableUnverified {
			runnable++
		}
	}
	receipt := core.DefinitionPackRevision{
		Source: source, Revision: revision,
		ManifestDigest: inventoryDigest(source, revision, "manifest"), ArchiveDigest: archiveDigest,
		ArchiveRelPath:    "archives/sha256/" + strings.TrimPrefix(archiveDigest, "sha256:") + ".zip",
		LicenseExpression: "MIT", LicensePath: "LICENSE", LicenseDigest: inventoryDigest(source, revision, "license"),
		Provenance: "invented inventory projection fixture", SignerKeyID: "inventory-key",
		SignerKeyFingerprint: fingerprint, SignerPublicKey: append(ed25519.PublicKey(nil), publicKey...),
		MinimumCaravanVersion: "0.1.0", InstallState: core.DefinitionPackInstalled,
		DefinitionCount: len(entries), RunnableCount: runnable, AcceptedAt: installedAt, AcceptedByUserID: 1, InstalledAt: installedAt,
	}
	if err := st.InstallDefinitionPackRevision(context.Background(), &receipt, entries); err != nil {
		t.Fatalf("install inventory revision %s: %v", revision, err)
	}
}

func seedInventoryPackStates(t *testing.T, st *store.Store, publicKey ed25519.PublicKey) map[string]string {
	t.Helper()
	const source = "inventory.test"
	base := time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC)
	entries := map[string][]core.DefinitionPackEntry{
		"r0": {inventoryEntry(source, "r0", "newest-only", "52bt", core.DefinitionPackEntryRunnableUnverified)},
		"r1": {
			inventoryEntry(source, "r1", "fixture", "1337x", core.DefinitionPackEntryRunnableUnverified),
			inventoryEntry(source, "r1", "runtime-missing", "0daykiev", core.DefinitionPackEntryRunnableUnverified),
		},
		"r2": {
			inventoryEntry(source, "r2", "fixture", "1337x", core.DefinitionPackEntryRunnableUnverified),
			inventoryEntry(source, "r2", "pending-only", "0magnet", core.DefinitionPackEntryRunnableUnverified),
		},
		"r3": {
			inventoryEntry(source, "r3", "pending-only", "0magnet", core.DefinitionPackEntryRunnableUnverified),
			inventoryEntry(source, "r3", "inactive-only", "13city", core.DefinitionPackEntryRunnableUnverified),
			inventoryEntry(source, "r3", "newest-only", "52bt", core.DefinitionPackEntryRunnableUnverified),
			inventoryEntry(source, "r3", "compiler-invalid", "1ptbar", core.DefinitionPackEntryUnsupported, "compiler.invalid"),
			inventoryEntry(source, "r3", "unsupported", "3dtorrents", core.DefinitionPackEntryUnsupported, "login.required"),
		},
		"r4": {inventoryEntry(source, "r4", "failed", "4thd", core.DefinitionPackEntryRunnableUnverified)},
	}
	for i, revision := range []string{"r0", "r1", "r2", "r3", "r4"} {
		installInventoryRevision(t, st, publicKey, source, revision, base.Add(time.Duration(i)*time.Hour), entries[revision])
	}
	if err := st.MarkDefinitionPackPending(context.Background(), source, "r1"); err != nil {
		t.Fatal(err)
	}
	if err := st.PromotePendingDefinitionPack(context.Background(), source, "r1"); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkDefinitionPackPending(context.Background(), source, "r2"); err != nil {
		t.Fatal(err)
	}
	if err := st.QuarantineDefinitionPack(context.Background(), source, "r4", "pack.synthetic.failed"); err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		"source":          source,
		"active_digest":   entries["r1"][0].Digest,
		"pending_digest":  entries["r2"][1].Digest,
		"inactive_digest": entries["r3"][1].Digest,
		"newest_digest":   entries["r3"][2].Digest,
	}
}

func inventoryVariant(t *testing.T, entry catalog.InventoryEntry, definitionID string) catalog.ExecutionStatus {
	t.Helper()
	for _, variant := range entry.Definitions {
		if variant.DefinitionID == definitionID {
			return variant
		}
	}
	t.Fatalf("inventory %q has no definition %q: %+v", entry.ID, definitionID, entry.Definitions)
	return catalog.ExecutionStatus{}
}

func TestIndexerInventoryProjectsPackLifecycleExactIdentityAndNoSecrets(t *testing.T) {
	fixture := makeAPISignedPack(t)
	exactCalls := make([][4]string, 0)
	exact := ExactLocalDefinitionLookup(func(id, source, revision, digest string) (LocalDefinitionSchema, bool) {
		exactCalls = append(exactCalls, [4]string{id, source, revision, digest})
		if id == "inventory.test:fixture" && source == "inventory.test" && revision == "r1" {
			return LocalDefinitionSchema{
				Settings: []string{"token", "passkey"},
				BaseURLs: []string{"https://definition-authority.example"},
			}, true
		}
		return LocalDefinitionSchema{}, false
	})
	local := LocalDefinitionLookup(func(id string) (LocalDefinitionSchema, bool) {
		switch id {
		case "thepiratebay", "rutor", "nyaa", "tokyotosho", "builtin:thepiratebay", "builtin:rutor", "builtin:nyaa", "builtin:tokyotosho":
			return LocalDefinitionSchema{Settings: []string{"apiurl"}}, true
		default:
			return LocalDefinitionSchema{}, false
		}
	})
	h, st, _, admin, _ := newDefinitionPackAPIServer(t, WithLocalDefinitions(local), WithExactLocalDefinitions(exact))
	identity := seedInventoryPackStates(t, st, fixture.publicKey)

	rec := doAuth(t, h, http.MethodGet, "/api/v1/indexers/catalog", "", withCookie(admin))
	wantStatus(t, rec, http.StatusOK)
	var response struct {
		Inventory []catalog.InventoryEntry `json:"inventory"`
	}
	decodeBody(t, rec, &response)
	if len(response.Inventory) != 542 {
		t.Fatalf("inventory rows = %d, want 542", len(response.Inventory))
	}
	entry := func(id string) catalog.InventoryEntry {
		t.Helper()
		got, ok := inventoryByMetadataID(response.Inventory, id)
		if !ok {
			t.Fatalf("inventory missing %q", id)
		}
		return got
	}

	active := inventoryVariant(t, entry("1337x"), "inventory.test:fixture")
	if !active.Addable || active.State != catalog.InventoryStateVerified || active.Source != identity["source"] || active.Revision != "r1" || active.Digest != identity["active_digest"] || len(active.Settings) != 2 || active.Settings[0].Name != "token" || !active.Settings[0].Secret || len(active.BaseURLs) != 1 || active.BaseURLs[0] != "https://definition-authority.example" {
		t.Fatalf("active exact projection = %+v", active)
	}
	missing := inventoryVariant(t, entry("0daykiev"), "inventory.test:runtime-missing")
	if missing.Addable || missing.State != catalog.InventoryStateRunnableUnverified || missing.BlockedCode != "pack.runtime.unavailable" || missing.Revision != "r1" {
		t.Fatalf("active runtime miss = %+v", missing)
	}
	pending := inventoryVariant(t, entry("0magnet"), "inventory.test:pending-only")
	if pending.Addable || pending.State != catalog.InventoryStateRunnableUnverified || pending.BlockedCode != "pack.restart_required" || pending.Revision != "r2" || pending.Digest != identity["pending_digest"] {
		t.Fatalf("pending projection/newer precedence = %+v", pending)
	}
	inactive := inventoryVariant(t, entry("13city"), "inventory.test:inactive-only")
	if inactive.Addable || inactive.State != catalog.InventoryStateRunnableUnverified || inactive.Revision != "r3" || inactive.Digest != identity["inactive_digest"] {
		t.Fatalf("inactive projection = %+v", inactive)
	}
	newest := inventoryVariant(t, entry("52bt"), "inventory.test:newest-only")
	if newest.Revision != "r3" || newest.Digest != identity["newest_digest"] {
		t.Fatalf("newest deterministic projection = %+v", newest)
	}
	quarantined := inventoryVariant(t, entry("1ptbar"), "inventory.test:compiler-invalid")
	if quarantined.State != catalog.InventoryStateQuarantined || quarantined.Addable {
		t.Fatalf("compiler.invalid projection = %+v", quarantined)
	}
	unsupported := inventoryVariant(t, entry("3dtorrents"), "inventory.test:unsupported")
	if unsupported.State != catalog.InventoryStateUnsupported || unsupported.Addable {
		t.Fatalf("unsupported projection = %+v", unsupported)
	}
	failed := inventoryVariant(t, entry("4thd"), "inventory.test:failed")
	if failed.State != catalog.InventoryStateUnsupported || failed.BlockedCode != "pack.synthetic.failed" || failed.Addable {
		t.Fatalf("failed projection = %+v", failed)
	}
	builtin := inventoryVariant(t, entry("thepiratebay"), "builtin:thepiratebay")
	if !builtin.Addable || builtin.State != catalog.InventoryStateVerified || builtin.Source != cardigann.BuiltinSource {
		t.Fatalf("builtin projection changed = %+v", builtin)
	}
	if len(exactCalls) != 2 {
		t.Fatalf("exact lookup calls = %+v, want only two active entries", exactCalls)
	}

	raw := rec.Body.String()
	keyJSON, _ := json.Marshal(fixture.publicKey)
	for _, leaked := range []string{string(keyJSON), `"signer_public_key"`, `"archive_rel_path"`, "archives/sha256/"} {
		if strings.Contains(raw, leaked) {
			t.Fatalf("inventory JSON leaked %q", leaked)
		}
	}
}

func TestPackCatalogExecutionStatusesUseRequestContext(t *testing.T) {
	fixture := makeAPISignedPack(t)
	_, st, service, _, _ := newDefinitionPackAPIServer(t)
	seedInventoryPackStates(t, st, fixture.publicKey)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := &server{definitionPacks: service, log: slog.Default()}
	if got := s.packCatalogExecutionStatuses(ctx); len(got) != 0 {
		t.Fatalf("canceled request produced %d pack statuses", len(got))
	}
}
