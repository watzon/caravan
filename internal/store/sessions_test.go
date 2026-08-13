package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

func TestSessionRoundTripSurvivesReopen(t *testing.T) {
	st, path := openTemp(t)
	ctx := context.Background()
	user := createUser(t, st, "Chris", core.RoleAdmin)
	expiry := time.Now().UTC().Add(time.Hour).Truncate(time.Millisecond)

	if err := st.PutSession(ctx, "hash-one", user.ID, expiry); err != nil {
		t.Fatalf("PutSession: %v", err)
	}
	st.Close()

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { reopened.Close() })

	gotID, gotExpiry, err := reopened.GetSession(ctx, "hash-one")
	if err != nil {
		t.Fatalf("GetSession after reopen: %v", err)
	}
	if gotID != user.ID {
		t.Fatalf("GetSession user = %d, want %d", gotID, user.ID)
	}
	if !gotExpiry.Equal(expiry) {
		t.Fatalf("GetSession expiry = %v, want %v", gotExpiry, expiry)
	}
}

func TestGetSessionMissingIsNotFound(t *testing.T) {
	st, _ := openTemp(t)
	if _, _, err := st.GetSession(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetSession(missing) = %v, want ErrNotFound", err)
	}
}

func TestDeleteSessionIsIdempotent(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()
	user := createUser(t, st, "Chris", core.RoleAdmin)
	if err := st.PutSession(ctx, "hash-one", user.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("PutSession: %v", err)
	}
	if err := st.DeleteSession(ctx, "hash-one"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, _, err := st.GetSession(ctx, "hash-one"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetSession after delete = %v, want ErrNotFound", err)
	}
	if err := st.DeleteSession(ctx, "hash-one"); err != nil {
		t.Fatalf("DeleteSession(again): %v", err)
	}
}

func TestDeleteSessionsForUserLeavesOthers(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()
	chris := createUser(t, st, "Chris", core.RoleAdmin)
	pat := createUser(t, st, "Pat", core.RoleMember)
	expiry := time.Now().Add(time.Hour)
	if err := st.PutSession(ctx, "chris-a", chris.ID, expiry); err != nil {
		t.Fatalf("PutSession chris-a: %v", err)
	}
	if err := st.PutSession(ctx, "chris-b", chris.ID, expiry); err != nil {
		t.Fatalf("PutSession chris-b: %v", err)
	}
	if err := st.PutSession(ctx, "pat-a", pat.ID, expiry); err != nil {
		t.Fatalf("PutSession pat-a: %v", err)
	}

	if err := st.DeleteSessionsForUser(ctx, chris.ID); err != nil {
		t.Fatalf("DeleteSessionsForUser: %v", err)
	}
	if _, _, err := st.GetSession(ctx, "chris-a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("chris-a survived: %v", err)
	}
	if _, _, err := st.GetSession(ctx, "chris-b"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("chris-b survived: %v", err)
	}
	if _, _, err := st.GetSession(ctx, "pat-a"); err != nil {
		t.Fatalf("pat-a was deleted: %v", err)
	}
}

func TestDeleteUserCascadesSessions(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()
	user := createUser(t, st, "Chris", core.RoleAdmin)
	if err := st.PutSession(ctx, "hash-one", user.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("PutSession: %v", err)
	}
	if err := st.DeleteUser(ctx, user.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, _, err := st.GetSession(ctx, "hash-one"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("session survived account deletion: %v", err)
	}
}

func TestDeleteExpiredSessionsKeepsLiveOnes(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()
	user := createUser(t, st, "Chris", core.RoleAdmin)
	now := time.Now().UTC()
	if err := st.PutSession(ctx, "dead", user.ID, now.Add(-time.Minute)); err != nil {
		t.Fatalf("PutSession dead: %v", err)
	}
	if err := st.PutSession(ctx, "live", user.ID, now.Add(time.Hour)); err != nil {
		t.Fatalf("PutSession live: %v", err)
	}
	if err := st.DeleteExpiredSessions(ctx, now); err != nil {
		t.Fatalf("DeleteExpiredSessions: %v", err)
	}
	if _, _, err := st.GetSession(ctx, "dead"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session survived: %v", err)
	}
	if _, _, err := st.GetSession(ctx, "live"); err != nil {
		t.Fatalf("live session was deleted: %v", err)
	}
}
