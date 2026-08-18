package automation

// The adult library's own durable job: the deferred catalogue walk that
// POST /adult/sites queues instead of doing inline (core.JobSyncSite).
//
// The walk itself lives in internal/library, which this package deliberately
// does not import — same arrangement as the metadata refresh and the Jellyfin
// handoff, and for the same reason: the queue's semantics (leases, backoff,
// at-least-once) belong here, and the filesystem-and-provider half belongs
// there. cmd/caravan is where the two are joined.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
	"github.com/watzon/caravan/internal/wanted"
)

// SiteWalker files one site's whole scene catalogue as season and episode rows.
// It is library.Manager.SyncSite, named as a function so this package does not
// have to know about the manager.
type SiteWalker func(ctx context.Context, seriesID int64) error

// SyncSiteHandler builds the core.JobSyncSite handler around a walker.
//
// The search fan-out is the tail rather than a job of its own, and the order is
// the point: the wanted list is computed from episode rows, so a search queued
// before the walk would have nothing to name. Riding it on this job is what
// makes "add, monitor and start searching" mean the same thing for a site as it
// does for a series, now that the site's scenes arrive a minute later.
//
// A walk that fails fails the job, so the queue's backoff retries it. The
// searches are not retried separately: they are queued by the same dedupe every
// other search goes through, and the next backlog sweep queues anything this
// missed.
func SyncSiteHandler(walk SiteWalker) Handler {
	return func(ctx context.Context, st *store.Store, payload json.RawMessage) error {
		var input core.JobSyncSitePayload
		if err := json.Unmarshal(payload, &input); err != nil || input.SeriesID <= 0 {
			return fmt.Errorf("decode %s payload", core.JobSyncSite)
		}
		if err := walk(ctx, input.SeriesID); err != nil {
			return fmt.Errorf("walk site %d: %w", input.SeriesID, err)
		}
		if !input.SearchNow {
			return nil
		}
		return EnqueueSeriesSearches(ctx, st, input.SeriesID)
	}
}

// EnqueueSeriesSearches queues one search_episode job per missing episode of a
// series that has no active download, deduped against jobs already open.
//
// Searchable is the filter, not "every episode": an unmonitored site has no
// searchable scenes, so "start searching immediately" on an unmonitored add
// correctly queues nothing rather than erroring. That combination is not
// offered in the UI, but the queue is not the place to find out.
func EnqueueSeriesSearches(ctx context.Context, st *store.Store, seriesID int64) error {
	lists, err := wanted.ComputeSearchable(ctx, st)
	if err != nil {
		return fmt.Errorf("compute wanted releases: %w", err)
	}
	for _, episode := range lists.Episodes {
		if episode.SeriesID != seriesID {
			continue
		}
		payload, err := json.Marshal(core.JobSearchEpisodePayload{EpisodeID: episode.ID})
		if err != nil {
			return fmt.Errorf("encode episode search payload: %w", err)
		}
		if err := enqueueIfMissing(ctx, st, core.JobSearchEpisode, string(payload)); err != nil {
			return err
		}
	}
	return nil
}
