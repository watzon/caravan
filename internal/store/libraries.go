package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/watzon/caravan/internal/core"
)

// ListLibraries returns every library ordered by id, which is the order 0012
// seeded them in: Movies first, then Series.
func (s *Store) ListLibraries(ctx context.Context) ([]core.Library, error) {
	models := make([]libraryStoreModel, 0)
	if err := s.db.NewSelect().Model(&models).Order("id ASC").Scan(ctx); err != nil {
		return nil, fmt.Errorf("store: list libraries: %w", err)
	}
	out := make([]core.Library, 0, len(models))
	for i := range models {
		out = append(out, models[i].coreLibrary())
	}
	return out, nil
}

// GetLibrary returns the library with the given id, or ErrNotFound.
func (s *Store) GetLibrary(ctx context.Context, id int64) (*core.Library, error) {
	var model libraryStoreModel
	err := s.db.NewSelect().Model(&model).Where("id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: library %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get library %d: %w", id, err)
	}
	library := model.coreLibrary()
	return &library, nil
}

// GetLibraryByKind returns the DEFAULT library of the given kind, or
// ErrNotFound when no library of that kind exists at all.
//
// Before 0022 kind identified a library outright; now it identifies the
// default one, which is what every call site written under the old rule
// means by the lookup. A kind whose default flag was somehow lost falls back
// to the lowest id, so a by-kind caller never fails while a library of the
// kind exists.
func (s *Store) GetLibraryByKind(ctx context.Context, kind string) (*core.Library, error) {
	var model libraryStoreModel
	err := s.db.NewSelect().Model(&model).
		Where("kind = ?", kind).
		Order("is_default DESC", "id ASC").
		Limit(1).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: library of kind %q: %w", kind, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get library of kind %q: %w", kind, err)
	}
	library := model.coreLibrary()
	return &library, nil
}

// GetDefaultLibrary is GetLibraryByKind under its post-0022 name. New code
// should say what it means; the old name survives for the call sites written
// when kind was the whole mapping.
func (s *Store) GetDefaultLibrary(ctx context.Context, kind string) (*core.Library, error) {
	return s.GetLibraryByKind(ctx, kind)
}

// ListLibrariesByKind returns every library of one kind ordered by id, the
// default first among equals only by virtue of usually being oldest — callers
// that need the default ask GetDefaultLibrary.
func (s *Store) ListLibrariesByKind(ctx context.Context, kind string) ([]core.Library, error) {
	models := make([]libraryStoreModel, 0)
	if err := s.db.NewSelect().Model(&models).
		Where("kind = ?", kind).
		Order("id ASC").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("store: list libraries of kind %q: %w", kind, err)
	}
	out := make([]core.Library, 0, len(models))
	for i := range models {
		out = append(out, models[i].coreLibrary())
	}
	return out, nil
}

// Library deletion refusals. Each is a distinct error because the API answers
// each with a different message, and the difference matters to the user: one
// asks them to move items first, the others say the row is structural.
var (
	// ErrLibraryNotEmpty refuses to delete a library that still owns items.
	ErrLibraryNotEmpty = errors.New("store: library still has items")
	// ErrLibraryIsDefault refuses to delete a kind's default library —
	// demote it first, so every by-kind lookup keeps an answer.
	ErrLibraryIsDefault = errors.New("store: library is its kind's default")
)

// CreateLibrary inserts a new library and writes back the assigned id. The
// caller (the API layer) validates the kind, the provider and the root path —
// this function only owns what the schema owns: root uniqueness, and the DLNA
// tree version, which must advance when a visible container appears.
//
// A library is born ACTIVE, whatever the caller left in the field. Dormancy is
// a later and deliberate act on a library that exists — there is no form that
// creates one already switched off, and a Go zero value silently meaning "off"
// would make every caller written before the column existed create libraries
// nobody can see. Restriction is the opposite: it IS a create-time decision
// (an adult library is born restricted), so it is passed through.
func (s *Store) CreateLibrary(ctx context.Context, l *core.Library) error {
	chain, err := normalizeChain(l)
	if err != nil {
		return fmt.Errorf("store: create library %q: %w", l.Name, err)
	}
	l.Active = true
	model := libraryStoreModelFromCore(l, chain)
	if err := s.db.NewInsert().Model(model).Returning("id").Scan(ctx); err != nil {
		return fmt.Errorf("store: create library %q: %w", l.Name, err)
	}
	l.ID = model.ID
	if l.DLNAVisible {
		return s.bumpDLNAUpdateID(ctx)
	}
	return nil
}

// DeleteLibrary removes an empty, non-default library. The guards live here
// rather than in the API so no second caller can forget one: a library that
// still owns items is ErrLibraryNotEmpty (with items stranded nowhere), and the
// kind's default is ErrLibraryIsDefault.
//
// An adult library is deleted under those two guards and no others. It used to
// have a third, because the module switch owned the row and promised that
// switching off destroyed nothing; `active` keeps that promise now, and it is
// the deliberate "off". Once a library is an ordinary object, a kind-shaped
// exception here would only mean the one shelf an owner cannot tidy away.
func (s *Store) DeleteLibrary(ctx context.Context, id int64) error {
	lib, err := s.GetLibrary(ctx, id)
	if err != nil {
		return err
	}
	if lib.IsDefault {
		return ErrLibraryIsDefault
	}
	n, err := s.CountLibraryItems(ctx, id)
	if err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("%w: %d", ErrLibraryNotEmpty, n)
	}
	if _, err := s.db.NewDelete().Model((*libraryStoreModel)(nil)).Where("id = ?", id).Exec(ctx); err != nil {
		return fmt.Errorf("store: delete library %d: %w", id, err)
	}
	if lib.DLNAVisible {
		return s.bumpDLNAUpdateID(ctx)
	}
	return nil
}

// CountLibraryItems reports how many movies and series name the library as
// theirs. It is the emptiness check DeleteLibrary runs and the item_count the
// Libraries screen renders, so both always agree.
func (s *Store) CountLibraryItems(ctx context.Context, id int64) (int64, error) {
	var n int64
	err := s.db.NewSelect().
		ColumnExpr("(SELECT COUNT(*) FROM movies WHERE library_id = ?) + (SELECT COUNT(*) FROM series WHERE library_id = ?)", id, id).
		Scan(ctx, &n)
	if err != nil {
		return 0, fmt.Errorf("store: count items of library %d: %w", id, err)
	}
	return n, nil
}

// SetDefaultLibrary makes the given library its kind's default. Clear-then-set
// in one transaction: the partial unique index admits at most one default per
// kind, so the old flag must be gone before the new one lands.
func (s *Store) SetDefaultLibrary(ctx context.Context, id int64) error {
	lib, err := s.GetLibrary(ctx, id)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: set default library %d: %w", id, err)
	}
	defer tx.Rollback()
	if _, err := tx.NewUpdate().Model((*libraryStoreModel)(nil)).
		Set("is_default = ?", false).
		Where("kind = ?", lib.Kind).
		Where("is_default = ?", true).
		Exec(ctx); err != nil {
		return fmt.Errorf("store: set default library %d: %w", id, err)
	}
	if _, err := tx.NewUpdate().Model((*libraryStoreModel)(nil)).
		Set("is_default = ?", true).
		Where("id = ?", id).
		Exec(ctx); err != nil {
		return fmt.Errorf("store: set default library %d: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: set default library %d: %w", id, err)
	}
	return nil
}

// UpdateLibrary rewrites the mutable fields of an existing library. Kind is
// not among them: it is the library's identity and what items are mapped by.
// Neither is is_default — that is SetDefaultLibrary's transactional job.
// Updating an absent library is ErrNotFound.
//
// Flipping dlna_visible also advances SettingDLNAUpdateID, because that flag is
// the one library field the DLNA content tree is built from: a television
// caches the tree against SystemUpdateID, so a library that vanished from it
// while the counter stood still is one the TV keeps showing. Doing it here
// rather than in the caller is what makes the two impossible to get out of
// step.
//
// `active` advances it too, but only for a library dlna_visible was already on
// for. The DLNA rule is `active AND dlna_visible`, so deactivating a shared
// library removes its container and reactivating puts it back — both are tree
// changes a cached client must be told about. For a library nobody shares, the
// tree did not contain the container either way and the counter has nothing to
// report.
func (s *Store) UpdateLibrary(ctx context.Context, l *core.Library) error {
	prev, err := s.GetLibrary(ctx, l.ID)
	if err != nil {
		return err
	}
	chain, err := normalizeChain(l)
	if err != nil {
		return fmt.Errorf("store: update library %d: %w", l.ID, err)
	}
	model := libraryStoreModelFromCore(l, chain)
	res, err := s.db.NewUpdate().Model(model).
		Column("name", "icon", "root_path", "dlna_visible", "route_torrent", "route_usenet",
			"quality_profile_id", "provider", "providers", "active", "restricted").
		WherePK().
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("store: update library %d: %w", l.ID, err)
	}
	if err := affectedOne(res, "library", l.ID); err != nil {
		return err
	}
	if prev.DLNAVisible != l.DLNAVisible || (prev.Active != l.Active && l.DLNAVisible) {
		return s.bumpDLNAUpdateID(ctx)
	}
	return nil
}

// bumpDLNAUpdateID advances the ContentDirectory tree version by one.
//
// The increment is done in SQL rather than read-modify-write in Go so two
// concurrent toggles cannot both read 3 and both write 4, which would leave a
// client that had already cached 4 believing it was up to date. An absent key
// reads as 1 (see SettingDLNAUpdateID), so the first bump lands on 2.
func (s *Store) bumpDLNAUpdateID(ctx context.Context) error {
	setting := &settingModel{Key: SettingDLNAUpdateID, Value: "2", UpdatedAt: formatTime(now())}
	_, err := s.db.NewInsert().Model(setting).
		On("CONFLICT (key) DO UPDATE").
		Set("value = CAST(CAST(value AS INTEGER) + 1 AS TEXT)").
		Set("updated_at = EXCLUDED.updated_at").
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("store: bump dlna update id: %w", err)
	}
	return nil
}

// ListLibraryIndexers returns the override rows stored for one library. It is
// the raw table, not the resolved set: an indexer with no row here is missing
// from the result and is enabled with its own categories. Callers rendering
// the per-library indexer matrix list the indexers separately and treat a
// missing row as the default; callers running a search want
// ResolveLibrarySettings instead.
func (s *Store) ListLibraryIndexers(ctx context.Context, libraryID int64) ([]core.LibraryIndexer, error) {
	models := make([]libraryIndexerStoreModel, 0)
	if err := s.db.NewSelect().Model(&models).
		Where("library_id = ?", libraryID).
		Order("indexer_id ASC").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("store: list indexers of library %d: %w", libraryID, err)
	}
	out := make([]core.LibraryIndexer, 0, len(models))
	for i := range models {
		li := core.LibraryIndexer{LibraryID: models[i].LibraryID, IndexerID: models[i].IndexerID, Enabled: models[i].Enabled}
		if models[i].Categories != nil && *models[i].Categories != "" {
			if err := json.Unmarshal([]byte(*models[i].Categories), &li.Categories); err != nil {
				return nil, fmt.Errorf("store: decode categories of library %d indexer %d: %w",
					li.LibraryID, li.IndexerID, err)
			}
		}
		out = append(out, li)
	}
	return out, nil
}

// SetLibraryIndexer writes one (library, indexer) override, replacing any
// previous one. Writing enabled with nil categories is equivalent to having no
// row at all, so a user can always get back to the default.
func (s *Store) SetLibraryIndexer(ctx context.Context, li *core.LibraryIndexer) error {
	var categories *string
	if li.Categories != nil {
		b, err := json.Marshal(li.Categories)
		if err != nil {
			return fmt.Errorf("store: encode categories of library %d indexer %d: %w",
				li.LibraryID, li.IndexerID, err)
		}
		encoded := string(b)
		categories = &encoded
	}
	model := &libraryIndexerStoreModel{
		LibraryID: li.LibraryID, IndexerID: li.IndexerID, Enabled: li.Enabled, Categories: categories,
	}
	_, err := s.db.NewInsert().Model(model).
		On("CONFLICT (library_id, indexer_id) DO UPDATE").
		Set("enabled = EXCLUDED.enabled").
		Set("categories = EXCLUDED.categories").
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("store: set library %d indexer %d: %w", li.LibraryID, li.IndexerID, err)
	}
	return nil
}

// ResolveLibrarySettings returns the effective settings for one library: its
// own value where it set one, the global default everywhere else (PLAN phase 8
// task 2).
//
// This is the only place the fallback rule lives, and the set of settings it
// covers is closed — routing, DLNA visibility, the default quality profile,
// and the indexer set with its per-pair categories. Every other setting stays
// global, so nothing else has to learn that libraries exist.
func (s *Store) ResolveLibrarySettings(ctx context.Context, libraryID int64) (*core.LibrarySettings, error) {
	lib, err := s.GetLibrary(ctx, libraryID)
	if err != nil {
		return nil, err
	}
	settings, err := s.AllSettings(ctx)
	if err != nil {
		return nil, err
	}
	// Globally disabled indexers are not a library's decision to reverse: a
	// disabled indexer is out of every fan-out, and the per-library rows only
	// ever narrow that set further.
	indexers, err := s.ListEnabledIndexers(ctx)
	if err != nil {
		return nil, err
	}
	overrides, err := s.ListLibraryIndexers(ctx, lib.ID)
	if err != nil {
		return nil, err
	}
	byIndexer := make(map[int64]core.LibraryIndexer, len(overrides))
	for _, o := range overrides {
		byIndexer[o.IndexerID] = o
	}

	resolved := &core.LibrarySettings{
		LibraryID:        lib.ID,
		Kind:             lib.Kind,
		RouteTorrent:     overrideOrGlobal(lib.RouteTorrent, settings[SettingRouteTorrent]),
		RouteUsenet:      overrideOrGlobal(lib.RouteUsenet, settings[SettingRouteUsenet]),
		DLNAVisible:      lib.DLNAVisible,
		QualityProfileID: lib.QualityProfileID,
		Indexers:         []core.IndexerConfig{},
	}
	for _, ix := range indexers {
		o, ok := byIndexer[ix.ID]
		if ok && !o.Enabled {
			continue
		}
		switch {
		case ok && o.Categories != nil:
			ix.Categories = o.Categories
		default:
			ix.Categories = DefaultLibraryCategories(lib.Kind, ix.Categories)
		}
		resolved.Indexers = append(resolved.Indexers, ix)
	}
	return resolved, nil
}

// DefaultLibraryCategories is what a library asks an indexer for when nobody
// has written it an override — given the library's kind and the indexer's own
// categories.
//
// For every kind but one the answer is the indexer's own list, and for them
// that is right: an indexer is configured with the categories its owner wants
// Caravan to search, and a movie or television search wants exactly those.
//
// The adult library cannot inherit them, because by construction they are the
// movie and television ones. An install that enables the module has an indexer
// configured for 2000/5000 and a brand new Adult library with no override row,
// so inheriting would send every scene search out under `cat=5000,2000` — and
// that fails SILENTLY rather than loudly. indexer.parseTitle selects the
// date-based scene parser only for a 6000-series result, so everything such a
// search returns parses with a zero scene date and is then dropped by
// searchScene's date match: the job records "no release found" forever, on an
// indexer that carries the scene (PLAN phase 9 task 3).
//
// So the adult fallback is the adult category block itself. An indexer already
// narrowed to specific adult subcategories keeps exactly those — that is its
// owner naming which flavours of XXX this install wants — and one with no adult
// category at all gets the parent 6000, which is what "XXX" is. Either way an
// adult search sends only 6000-series categories, the invariant the whole
// module rests on. An explicit per-library override still wins outright: it is
// the owner's word, and phase 8's Libraries screen is where it is given.
//
// It is exported because the Libraries screen renders the per-library matrix
// from ListLibraryIndexers — the raw table, where "no row" is a hole — and has
// to fill that hole with the same answer a search would use, or the screen
// would show a default the searches do not run.
func DefaultLibraryCategories(kind string, own []int) []int {
	if kind != core.LibraryKindAdult {
		return own
	}
	adult := make([]int, 0, len(own))
	for _, id := range own {
		if core.IsAdultCategory(id) {
			adult = append(adult, id)
		}
	}
	if len(adult) == 0 {
		return []int{core.AdultCategoryBase}
	}
	return adult
}

// ResolveLibrarySettingsForItem resolves the effective settings of the library
// an item names, falling back to the kind's default library when it names one
// that has vanished, or names none at all.
//
// Naming none is no longer an item's state — migration 0011 stamped the rows
// that carried a zero — but it is still a caller's: work that has a kind and no
// row in hand passes 0 deliberately, which is what the fallback is for.
func (s *Store) ResolveLibrarySettingsForItem(ctx context.Context, libraryID int64, kind string) (*core.LibrarySettings, error) {
	if libraryID != 0 {
		settings, err := s.ResolveLibrarySettings(ctx, libraryID)
		if err == nil {
			return settings, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}
	return s.ResolveLibrarySettingsByKind(ctx, kind)
}

// ResolveLibrarySettingsByKind resolves the settings of the library that items
// of the given core.LibraryKind* belong to.
//
// Kind is the whole item -> library mapping, so this is the entire lookup a
// search does: a movie search resolves the movie library, an episode search the
// tv library, and neither needs to know a library id exists.
func (s *Store) ResolveLibrarySettingsByKind(ctx context.Context, kind string) (*core.LibrarySettings, error) {
	lib, err := s.GetLibraryByKind(ctx, kind)
	if err != nil {
		return nil, err
	}
	return s.ResolveLibrarySettings(ctx, lib.ID)
}

// ResolveItemQualityProfileByLibrary returns the effective profile for one
// library item: the profile the item names, its library's default when it
// names none, and the store-wide default when neither answers.
//
// It is the library step ResolveQualityProfile deliberately has no notion of.
// Every scoring site goes through here (or the by-kind wrapper below) rather
// than through ResolveQualityProfile directly, because a library default
// nobody reads is a setting that saves, renders as an override, and changes
// nothing (PLAN phase 8 task 2).
//
// libraryID 0 — a caller with a kind and no row in hand, see
// ResolveItemQualityProfile below — or a library that has vanished falls
// through to kind: the kind's default library answers.
func (s *Store) ResolveItemQualityProfileByLibrary(ctx context.Context, libraryID int64, kind string, itemProfileID int64) (*core.QualityProfile, error) {
	if itemProfileID > 0 {
		p, err := s.GetQualityProfile(ctx, itemProfileID)
		if err == nil {
			return p, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}
	var (
		lib *core.Library
		err error
	)
	if libraryID != 0 {
		lib, err = s.GetLibrary(ctx, libraryID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}
	if lib == nil {
		lib, err = s.GetLibraryByKind(ctx, kind)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return s.ResolveQualityProfile(ctx, 0)
			}
			return nil, err
		}
	}
	return s.ResolveQualityProfile(ctx, lib.QualityProfileID)
}

// ResolveItemQualityProfile is the by-kind form of the resolution above, for
// call sites that have no item row in hand (a kind-scoped sweep, a default).
// It reads the kind's DEFAULT library, which is the whole of what "this kind's
// settings" can mean with no row to ask.
func (s *Store) ResolveItemQualityProfile(ctx context.Context, kind string, itemProfileID int64) (*core.QualityProfile, error) {
	return s.ResolveItemQualityProfileByLibrary(ctx, 0, kind, itemProfileID)
}

// overrideOrGlobal is the whole fallback rule: the library answers when it has
// an answer, the global setting answers when it does not.
func overrideOrGlobal(library, global string) string {
	if library != "" {
		return library
	}
	return global
}

// normalizeChain settles l's two provider columns against each other and
// returns the encoded `providers` value to store.
//
// Both columns are written from this one result, by every writer, so they
// cannot disagree: `provider` is defined as the chain's head, and a row whose
// head contradicts its list would make two readers of the same library reach
// two different providers. It also fills a chain in from a caller that only
// set the head, which is every caller written before 0024.
func normalizeChain(l *core.Library) (string, error) {
	chain := l.Providers
	if len(chain) == 0 && l.Provider != "" {
		chain = []string{l.Provider}
	}
	l.Providers = chain
	if len(chain) == 0 {
		// A library nobody assigned a provider. Empty rather than "[]" so it
		// reads back the same as a row the migration left alone.
		l.Provider = ""
		return "", nil
	}
	l.Provider = chain[0]
	b, err := json.Marshal(chain)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func libraryStoreModelFromCore(l *core.Library, providers string) *libraryStoreModel {
	return &libraryStoreModel{
		ID: l.ID, Kind: l.Kind, Name: l.Name, Icon: l.Icon, RootPath: l.RootPath, DLNAVisible: l.DLNAVisible,
		RouteTorrent: stringPointer(l.RouteTorrent), RouteUsenet: stringPointer(l.RouteUsenet),
		QualityProfileID: int64Pointer(l.QualityProfileID), Provider: l.Provider, Providers: providers,
		IsDefault: l.IsDefault, Active: l.Active, Restricted: l.Restricted,
	}
}

func (m *libraryStoreModel) coreLibrary() core.Library {
	l := core.Library{
		ID: m.ID, Kind: m.Kind, Name: m.Name, Icon: m.Icon, RootPath: m.RootPath, DLNAVisible: m.DLNAVisible,
		Provider: m.Provider, IsDefault: m.IsDefault, Active: m.Active, Restricted: m.Restricted,
	}
	if m.RouteTorrent != nil {
		l.RouteTorrent = *m.RouteTorrent
	}
	if m.RouteUsenet != nil {
		l.RouteUsenet = *m.RouteUsenet
	}
	if m.QualityProfileID != nil {
		l.QualityProfileID = *m.QualityProfileID
	}
	// A chain that will not decode is treated as absent rather than as an
	// error: `provider` is the head and still answers, so a library with a
	// mangled list keeps working exactly as it did before 0024 instead of
	// making the whole Libraries screen unreadable.
	if m.Providers != "" {
		if err := json.Unmarshal([]byte(m.Providers), &l.Providers); err != nil {
			l.Providers = nil
		}
	}
	return l
}

func stringPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func int64Pointer(value int64) *int64 {
	if value == 0 {
		return nil
	}
	return &value
}
