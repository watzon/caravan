package api

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

func TestExactDefinitionPinsRequireExactActiveSchema(t *testing.T) {
	const digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	lookup := ExactLocalDefinitionLookup(func(id, source, revision, gotDigest string) (LocalDefinitionSchema, bool) {
		if id == "community:fixture" && source == "community" && revision == "v1" && gotDigest == digest {
			return LocalDefinitionSchema{Settings: []string{"token"}}, true
		}
		return LocalDefinitionSchema{}, false
	})
	h, st, _ := newTestServer(t, WithExactLocalDefinitions(lookup))
	installExactPinFixture(t, st, digest)
	body := fmt.Sprintf(`{"name":"fixture","url":"https://tracker.example","type":"torznab","definition_id":"community:fixture","definition_source":"community","definition_revision":"v1","definition_digest":%q,"settings":{"token":"secret"},"enabled":false}`, digest)
	rec := do(t, h, http.MethodPost, "/api/v1/indexers", body)
	wantStatus(t, rec, http.StatusCreated)
	var dto indexerJSON
	decodeBody(t, rec, &dto)
	if dto.DefinitionSource != "community" || dto.DefinitionRevision != "v1" || dto.DefinitionDigest != digest {
		t.Fatalf("DTO lost exact definition pin: %+v", dto)
	}
	stored, err := st.GetIndexer(t.Context(), dto.ID)
	if err != nil || stored.DefinitionSource != "community" || stored.DefinitionRevision != "v1" || stored.DefinitionDigest != dto.DefinitionDigest {
		t.Fatalf("stored exact pin = %+v, err=%v", stored, err)
	}
	if dto.Type != core.IndexerTypeTorznab {
		t.Fatalf("type = %q", dto.Type)
	}
	rec = do(t, h, http.MethodGet, "/api/v1/indexers", "")
	wantStatus(t, rec, http.StatusOK)
	var listed struct {
		Indexers []indexerJSON `json:"indexers"`
	}
	decodeBody(t, rec, &listed)
	if len(listed.Indexers) != 1 || listed.Indexers[0].DefinitionSource != "community" || listed.Indexers[0].DefinitionRevision != "v1" || listed.Indexers[0].DefinitionDigest != digest {
		t.Fatalf("listed exact pin = %+v", listed.Indexers)
	}
}

func TestPackDefinitionCreateRejectsEveryNonExactIdentityEvenWhenDisabled(t *testing.T) {
	const digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const wrongDigest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	tests := []struct {
		name        string
		body        string
		installPack bool
		exact       ExactLocalDefinitionLookup
	}{
		{
			name:        "omitted pin cannot use legacy lookup",
			body:        `{"name":"fixture","url":"https://tracker.example","type":"torznab","definition_id":"community:fixture","settings":{"token":"secret"},"enabled":false}`,
			installPack: true,
		},
		{
			name:        "partial pin",
			body:        `{"name":"fixture","url":"https://tracker.example","type":"torznab","definition_id":"community:fixture","definition_source":"community","enabled":false}`,
			installPack: true,
		},
		{
			name:        "source ref mismatch",
			body:        fmt.Sprintf(`{"name":"fixture","url":"https://tracker.example","type":"torznab","definition_id":"community:fixture","definition_source":"other","definition_revision":"v1","definition_digest":%q,"enabled":false}`, digest),
			installPack: true,
		},
		{
			name:        "wrong digest",
			body:        fmt.Sprintf(`{"name":"fixture","url":"https://tracker.example","type":"torznab","definition_id":"community:fixture","definition_source":"community","definition_revision":"v1","definition_digest":%q,"enabled":false}`, wrongDigest),
			installPack: true,
		},
		{
			name:        "nonactive exact lookup",
			body:        fmt.Sprintf(`{"name":"fixture","url":"https://tracker.example","type":"torznab","definition_id":"community:fixture","definition_source":"community","definition_revision":"v1","definition_digest":%q,"enabled":false}`, digest),
			installPack: true,
			exact: func(string, string, string, string) (LocalDefinitionSchema, bool) {
				return LocalDefinitionSchema{}, false
			},
		},
		{
			name: "orphan runtime identity",
			body: fmt.Sprintf(`{"name":"fixture","url":"https://tracker.example","type":"torznab","definition_id":"community:fixture","definition_source":"community","definition_revision":"v1","definition_digest":%q,"enabled":false}`, digest),
			exact: func(string, string, string, string) (LocalDefinitionSchema, bool) {
				return LocalDefinitionSchema{Settings: []string{"token"}}, true
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			legacyCalls := 0
			legacy := LocalDefinitionLookup(func(id string) (LocalDefinitionSchema, bool) {
				legacyCalls++
				return LocalDefinitionSchema{Settings: []string{"token"}}, id == "community:fixture"
			})
			exact := test.exact
			if exact == nil {
				exact = func(id, source, revision, gotDigest string) (LocalDefinitionSchema, bool) {
					return LocalDefinitionSchema{Settings: []string{"token"}}, id == "community:fixture" && source == "community" && revision == "v1" && gotDigest == digest
				}
			}
			h, st, _ := newTestServer(t, WithLocalDefinitions(legacy), WithExactLocalDefinitions(exact))
			if test.installPack {
				installExactPinFixture(t, st, digest)
			}
			rec := do(t, h, http.MethodPost, "/api/v1/indexers", test.body)
			wantStatus(t, rec, http.StatusBadRequest)
			wantErrorBody(t, rec)
			if test.name == "omitted pin cannot use legacy lookup" && legacyCalls != 0 {
				t.Fatalf("legacy lookup called %d times for pack namespace", legacyCalls)
			}
			stored, err := st.ListIndexers(t.Context())
			if err != nil || len(stored) != 0 {
				t.Fatalf("rejected create stored indexers=%+v err=%v", stored, err)
			}
		})
	}
}

func TestPackDefinitionUpdateCannotRetainOrDropPinsAccidentally(t *testing.T) {
	const digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	lookup := ExactLocalDefinitionLookup(func(id, source, revision, gotDigest string) (LocalDefinitionSchema, bool) {
		return LocalDefinitionSchema{Settings: []string{"token"}}, id == "community:fixture" && source == "community" && revision == "v1" && gotDigest == digest
	})
	h, st, _ := newTestServer(t,
		WithLocalDefinitions(func(id string) (LocalDefinitionSchema, bool) {
			return LocalDefinitionSchema{Settings: []string{"token"}}, id == "community:fixture" || id == "user:fixture"
		}),
		WithExactLocalDefinitions(lookup),
	)
	installExactPinFixture(t, st, digest)
	created := do(t, h, http.MethodPost, "/api/v1/indexers", fmt.Sprintf(`{"name":"fixture","url":"https://tracker.example","type":"torznab","definition_id":"community:fixture","definition_source":"community","definition_revision":"v1","definition_digest":%q,"settings":{"token":"old"},"enabled":false}`, digest))
	wantStatus(t, created, http.StatusCreated)

	for _, test := range []struct {
		name string
		body string
	}{
		{name: "omitting all exact fields cannot drop pin", body: `{"name":"fixture","url":"https://tracker.example","type":"torznab","definition_id":"community:fixture","enabled":false}`},
		{name: "partial edit cannot retain hidden old fields", body: `{"name":"fixture","url":"https://tracker.example","type":"torznab","definition_id":"community:fixture","definition_source":"community","enabled":false}`},
		{name: "different pack ref cannot inherit old pin", body: `{"name":"fixture","url":"https://tracker.example","type":"torznab","definition_id":"other:fixture","enabled":false}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			rec := do(t, h, http.MethodPut, "/api/v1/indexers/1", test.body)
			wantStatus(t, rec, http.StatusBadRequest)
			stored, err := st.GetIndexer(t.Context(), 1)
			if err != nil || stored.DefinitionID != "community:fixture" || stored.DefinitionSource != "community" || stored.DefinitionRevision != "v1" || stored.DefinitionDigest != digest || stored.Settings["token"] != "old" {
				t.Fatalf("rejected edit changed stored pin/settings: %+v err=%v", stored, err)
			}
		})
	}

	updated := do(t, h, http.MethodPut, "/api/v1/indexers/1", `{"name":"fixture","url":"https://tracker.example","type":"torznab","definition_id":"user:fixture","settings":{"token":"local"},"enabled":false}`)
	wantStatus(t, updated, http.StatusOK)
	var dto indexerJSON
	decodeBody(t, updated, &dto)
	if dto.DefinitionID != "user:fixture" || dto.DefinitionSource != "" || dto.DefinitionRevision != "" || dto.DefinitionDigest != "" {
		t.Fatalf("explicit owner-local edit retained old pin: %+v", dto)
	}
	stored, err := st.GetIndexer(t.Context(), 1)
	if err != nil || stored.DefinitionID != "user:fixture" || stored.DefinitionSource != "" || stored.DefinitionRevision != "" || stored.DefinitionDigest != "" || stored.Settings["token"] != "local" {
		t.Fatalf("owner-local store roundtrip = %+v err=%v", stored, err)
	}
}

func installExactPinFixture(t *testing.T, st interface {
	InstallDefinitionPackRevision(context.Context, *core.DefinitionPackRevision, []core.DefinitionPackEntry) error
	MarkDefinitionPackPending(context.Context, string, string) error
	PromotePendingDefinitionPack(context.Context, string, string) error
}, digest string) {
	t.Helper()
	key := ed25519.PublicKey(make([]byte, ed25519.PublicKeySize))
	sum := sha256.Sum256(key)
	fingerprint := fmt.Sprintf("sha256:%x", sum[:])
	now := time.Now().UTC()
	revision := core.DefinitionPackRevision{
		Source: "community", Revision: "v1", ManifestDigest: digest, ArchiveDigest: digest,
		ArchiveRelPath:    "archives/sha256/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.zip",
		LicenseExpression: "MIT", LicensePath: "LICENSE", LicenseDigest: digest, Provenance: "test fixture",
		SignerKeyID: "test-key", SignerKeyFingerprint: fingerprint, SignerPublicKey: key,
		MinimumCaravanVersion: "0.1.0", InstallState: core.DefinitionPackInstalled, DefinitionCount: 1, RunnableCount: 1,
		AcceptedAt: now, InstalledAt: now,
	}
	entry := core.DefinitionPackEntry{Source: "community", Revision: "v1", DefinitionRef: "community:fixture", MetadataID: "1337x", Path: "definitions/fixture.yml", Digest: digest, State: core.DefinitionPackEntryRunnableUnverified, ApprovedOrigins: []string{"https://tracker.example"}}
	if err := st.InstallDefinitionPackRevision(context.Background(), &revision, []core.DefinitionPackEntry{entry}); err != nil {
		t.Fatalf("install definition pack fixture: %v", err)
	}
	if err := st.MarkDefinitionPackPending(context.Background(), revision.Source, revision.Revision); err != nil {
		t.Fatalf("mark definition pack fixture pending: %v", err)
	}
	if err := st.PromotePendingDefinitionPack(context.Background(), revision.Source, revision.Revision); err != nil {
		t.Fatalf("promote definition pack fixture: %v", err)
	}
}
