package store

import (
	"context"
	"fmt"
)

// ListLibraryAccess returns the user ids granted access to one library, in id
// order.
//
// Ids and nothing else. The screen that renders this list wants a username and
// a role beside each row, but those live on `users` and the API already lists
// every account to draw the checklist, joining them here would make the store
// answer a question about accounts while pretending to answer one about a
// library, and would hand the caller a second, staler copy of every user row.
//
// The list is meaningful only alongside Library.Restricted: on an unrestricted
// library a roster is a note about who would be admitted if it were ever
// restricted, not a statement about who can see it today.
func (s *Store) ListLibraryAccess(ctx context.Context, libraryID int64) ([]int64, error) {
	out := []int64{}
	if err := s.db.NewSelect().Model((*libraryAccessStoreModel)(nil)).Column("user_id").
		Where("library_id = ?", libraryID).OrderExpr("user_id").Scan(ctx, &out); err != nil {
		return nil, fmt.Errorf("store: list access of library %d: %w", libraryID, err)
	}
	return out, nil
}

// ListLibraryAccessForUser returns the set of libraries one account holds a
// grant on.
//
// A set rather than a list because that is how it is read: a request asks "may
// this session see library N" once per library it is about to render, and a
// caller building a map per request would build the same map every time. It is
// one of the two queries the API's per-request gate runs, which is what bounds
// the cost of per-library filtering to a constant.
//
// User id 0 (the API-key credential and the open install, both of which
// authenticate as an admin) holds nothing and is not special-cased: there is no
// users row 0 to grant, and LibraryVisible never asks about a grant for an
// admin.
func (s *Store) ListLibraryAccessForUser(ctx context.Context, userID int64) (map[int64]bool, error) {
	var ids []int64
	if err := s.db.NewSelect().Model((*libraryAccessStoreModel)(nil)).Column("library_id").
		Where("user_id = ?", userID).Scan(ctx, &ids); err != nil {
		return nil, fmt.Errorf("store: list library access of user %d: %w", userID, err)
	}
	out := map[int64]bool{}
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// SetLibraryAccess writes a library's whole access decision at once: the
// restricted flag and the roster that goes with it, in one transaction.
//
// Wholesale replacement rather than per-user toggles, and that is the point of
// the signature. Restricting a library and naming its members are one decision;
// done as two writes there is a window in which the library is restricted to
// nobody, and a member watching the screen during it sees a shelf disappear
// that was never meant to leave. One statement per concept, one transaction for
// the pair.
//
// Restricting also clears dlna_visible in the same write. DLNA has no accounts:
// anything advertised on the tree is readable by every device on the LAN, so
// "restricted to two people" and "shared with the whole house" cannot both be
// true, and of the two it is the restriction that was just asked for. Re-sharing
// afterwards is a second, deliberate act on the Reach card — which is why
// unrestricting does NOT put the flag back: nobody asked for the LAN to see it
// again, and silently re-advertising a library would be exactly the surprise
// clearing it was there to prevent.
func (s *Store) SetLibraryAccess(ctx context.Context, libraryID int64, restricted bool, userIDs []int64) error {
	lib, err := s.GetLibrary(ctx, libraryID)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: set access of library %d: %w", libraryID, err)
	}
	defer tx.Rollback()

	update := tx.NewUpdate().Model((*libraryStoreModel)(nil)).Where("id = ?", libraryID)
	if restricted {
		_, err = update.Set("restricted = ?", true).Set("dlna_visible = ?", false).Exec(ctx)
	} else {
		_, err = update.Set("restricted = ?", false).Exec(ctx)
	}
	if err != nil {
		return fmt.Errorf("store: set access of library %d: %w", libraryID, err)
	}

	if _, err := tx.NewDelete().Model((*libraryAccessStoreModel)(nil)).
		Where("library_id = ?", libraryID).Exec(ctx); err != nil {
		return fmt.Errorf("store: set access of library %d: %w", libraryID, err)
	}
	grants := make([]libraryAccessStoreModel, 0, len(userIDs))
	for _, userID := range userIDs {
		grants = append(grants, libraryAccessStoreModel{LibraryID: libraryID, UserID: userID})
	}
	if len(grants) > 0 {
		if _, err := tx.NewInsert().Model(&grants).On("CONFLICT DO NOTHING").Exec(ctx); err != nil {
			return fmt.Errorf("store: grant users on library %d: %w", libraryID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: set access of library %d: %w", libraryID, err)
	}
	// The container leaves the DLNA tree the moment restriction clears the flag,
	// so a client caching against SystemUpdateID has to be told (see
	// UpdateLibrary for the whole rule).
	if restricted && lib.DLNAVisible {
		return s.bumpDLNAUpdateID(ctx)
	}
	return nil
}

// SetLibraryActive flips one library's master switch.
//
// It is its own writer rather than a field on UpdateLibrary's round trip
// because it is its own decision: everything else that method writes is a
// setting, and this is whether the library exists for anybody at all. Nothing
// is deleted, the rows, the files and the grants all wait for it to come back
// on.
//
// The DLNA update id advances under UpdateLibrary's condition, and for its
// reason: the tree carries a container for a library only while `active AND
// dlna_visible`, so this is a tree change exactly when the library is shared.
func (s *Store) SetLibraryActive(ctx context.Context, id int64, active bool) error {
	lib, err := s.GetLibrary(ctx, id)
	if err != nil {
		return err
	}
	res, err := s.db.NewUpdate().Model((*libraryStoreModel)(nil)).Set("active = ?", active).
		Where("id = ?", id).Exec(ctx)
	if err != nil {
		return fmt.Errorf("store: set library %d active: %w", id, err)
	}
	if err := affectedOne(res, "library", id); err != nil {
		return err
	}
	if lib.Active != active && lib.DLNAVisible {
		return s.bumpDLNAUpdateID(ctx)
	}
	return nil
}

// AnyActiveLibraryOfKind reports whether the install holds at least one library
// of the given kind that is switched on.
//
// It is the zero-traffic guard in its general form: the question "is this
// module reachable at all" used to be a settings lookup, and with the switch
// living on the library rows it becomes this. A caller that gets false must do
// nothing at all (not degrade, not ask a provider, not advertise a container)
// which is what keeps "off" meaning absent rather than merely hidden.
func (s *Store) AnyActiveLibraryOfKind(ctx context.Context, kind string) (bool, error) {
	exists, err := s.db.NewSelect().Model((*libraryStoreModel)(nil)).
		Where("kind = ?", kind).Where("active = ?", true).Exists(ctx)
	if err != nil {
		return false, fmt.Errorf("store: any active library of kind %q: %w", kind, err)
	}
	return exists, nil
}
