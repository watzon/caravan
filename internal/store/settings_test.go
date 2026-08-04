package store

import (
	"context"
	"testing"
)

func TestSetSettingsWritesEveryPair(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()

	if err := st.SetSettings(ctx, map[string]string{
		SettingStashboxEndpoint: "https://stashdb.org/graphql",
		SettingStashboxAPIKey:   "k",
	}); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}

	settings, err := st.AllSettings(ctx)
	if err != nil {
		t.Fatalf("AllSettings: %v", err)
	}
	if settings[SettingStashboxEndpoint] != "https://stashdb.org/graphql" ||
		settings[SettingStashboxAPIKey] != "k" {
		t.Fatalf("settings = %v, want both pairs written", settings)
	}
}

// The reason SetSettings exists: the stash-box endpoint and its key are one
// credential, and half of a new pair beside half of the old one is a
// combination nothing ever validated running behind a module that is already on
// (SPEC §10.2). A failure part way through must leave the previous pair whole.
func TestSetSettingsIsAllOrNothing(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()

	if err := st.SetSettings(ctx, map[string]string{
		SettingStashboxEndpoint: "https://stashdb.org/graphql",
		SettingStashboxAPIKey:   "old",
	}); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}

	// Fail the second write the way a disk or a lock would. SetSettings takes
	// the keys in sorted order, so "stashbox_api_key" is already written when
	// the refusal lands on "stashbox_endpoint" — which is exactly the half-a-
	// credential state a transaction is here to prevent.
	if _, err := st.db.ExecContext(ctx, `
		CREATE TRIGGER refuse_the_endpoint BEFORE UPDATE ON settings
		WHEN NEW.key = '`+SettingStashboxEndpoint+`'
		BEGIN SELECT RAISE(ABORT, 'disk full'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	err := st.SetSettings(ctx, map[string]string{
		SettingStashboxEndpoint: "https://fansdb.cc/graphql",
		SettingStashboxAPIKey:   "new",
	})
	if err == nil {
		t.Fatal("SetSettings succeeded with a refusing store")
	}

	settings, allErr := st.AllSettings(ctx)
	if allErr != nil {
		t.Fatalf("AllSettings: %v", allErr)
	}
	if settings[SettingStashboxEndpoint] != "https://stashdb.org/graphql" ||
		settings[SettingStashboxAPIKey] != "old" {
		t.Fatalf("settings = %v, want the previous pair intact after a failed write", settings)
	}
}
