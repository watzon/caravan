package core

import "testing"

// A ref round-trips through the int64 vocabulary the legacy call sites still
// speak, and an absent id stays absent rather than becoming a ref to "0" —
// which would be an identity claim about a title nobody has identified.
func TestTMDBRefRoundTrip(t *testing.T) {
	cases := []struct {
		id       int64
		wantRef  string
		wantBack int64
	}{
		{603, "603", 603},
		{1, "1", 1},
		{0, "", 0},
		{-7, "", 0},
	}
	for _, c := range cases {
		ref := TMDBRef(c.id)
		if ref.Ref != c.wantRef {
			t.Errorf("TMDBRef(%d).Ref = %q, want %q", c.id, ref.Ref, c.wantRef)
		}
		if got := ref.TMDBID(); got != c.wantBack {
			t.Errorf("TMDBRef(%d).TMDBID() = %d, want %d", c.id, got, c.wantBack)
		}
		if want := c.wantRef != ""; ref.Valid() != want {
			t.Errorf("TMDBRef(%d).Valid() = %v, want %v", c.id, ref.Valid(), want)
		}
	}
}

// TMDBID answers only for TMDB. Reading another provider's ref as a TMDB id is
// how a stash-box UUID would end up in a uniqueid tag or a discover lookup.
func TestTMDBIDIsProviderScoped(t *testing.T) {
	cases := []ItemRef{
		{Provider: ProviderStashbox, Ref: "603"},
		{Provider: ProviderTMDB, Ref: "9f3b-not-a-number"},
		{Provider: ProviderTMDB, Ref: ""},
		{},
	}
	for _, ref := range cases {
		if got := ref.TMDBID(); got != 0 {
			t.Errorf("ItemRef%+v.TMDBID() = %d, want 0", ref, got)
		}
	}
}

func TestItemRefValidNeedsBothHalves(t *testing.T) {
	cases := []struct {
		ref  ItemRef
		want bool
	}{
		{ItemRef{Provider: ProviderTMDB, Ref: "603"}, true},
		{ItemRef{Provider: ProviderTMDB}, false},
		{ItemRef{Ref: "603"}, false},
		{ItemRef{}, false},
	}
	for _, c := range cases {
		if got := c.ref.Valid(); got != c.want {
			t.Errorf("ItemRef%+v.Valid() = %v, want %v", c.ref, got, c.want)
		}
	}
}

// The meta structs are what a provider hands back, so their Ref() is the seam's
// answer to "who identified this and as what".
func TestMetaRef(t *testing.T) {
	m := MovieMeta{Provider: ProviderTMDB, ProviderRef: "603", TMDBID: 603}
	if got := m.Ref(); got != (ItemRef{Provider: ProviderTMDB, Ref: "603"}) {
		t.Errorf("MovieMeta.Ref() = %+v", got)
	}
	s := SeriesMeta{Provider: ProviderStashbox, ProviderRef: "site-uuid"}
	if got := s.Ref(); got != (ItemRef{Provider: ProviderStashbox, Ref: "site-uuid"}) {
		t.Errorf("SeriesMeta.Ref() = %+v", got)
	}
}
