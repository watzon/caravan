package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/watzon/caravan/internal/clients"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/download"
)

// clientRoute builds one external client's route, wiring the health tracker
// into it in both directions: what the last polls said (Unhealthy), and where
// this poll's outcome goes (Report).
//
// The embedded engine deliberately has neither. It is not a client, it cannot
// be unreachable, and a dead seedbox must leave it working exactly as before
// (PLAN phase 6 task 4).
func (p *engineProvider) clientRoute(pick routedEngine, protocol string) download.Route {
	return download.Route{
		Name:      pick.name,
		Protocol:  protocol,
		Engine:    pick.engine,
		IDPrefix:  clientIDPrefix(pick.id),
		Unhealthy: p.health.Reason(pick.id),
		Report:    func(err error) { p.observeClient(pick, err) },
		// Per row, not per kind: two SABnzbd clients are two machines with two
		// sets of connections, and one budget between them would cap the wrong
		// thing. The row id is the same stable identity the handle prefix uses.
		Method:    clientMethod(pick.id),
		Admission: p.admission,
	}
}

// observeClient feeds one poll outcome to the health tracker and records the
// transitions it causes. Only transitions reach the feed: a client that is
// down is down every poll, and the activity feed is for the user.
func (p *engineProvider) observeClient(pick routedEngine, err error) {
	switch p.health.Observe(pick.id, pick.label, pick.name, err) {
	case download.HealthDown:
		p.log.Warn("download client unreachable", "client", pick.label, "type", pick.name, "error", err)
		p.recordClientEvent(core.EventLevelWarn,
			fmt.Sprintf("Download client %s is unreachable", pick.label),
			fmt.Sprintf("%s: its downloads stop updating and new grabs routed to it are refused until it answers again", err))
	case download.HealthUp:
		p.log.Info("download client reachable again", "client", pick.label, "type", pick.name)
		p.recordClientEvent(core.EventLevelInfo,
			fmt.Sprintf("Download client %s is reachable again", pick.label),
			"its queue is being polled again")
	}
}

// recordClientEvent appends a health transition to the activity feed. It uses
// its own context because the poll's may already be cancelled by the time a
// failure is observed, and it swallows failures: an event is history, and
// losing one must never break the poll that produced it (SPEC §7).
func (p *engineProvider) recordClientEvent(level, message, detail string) {
	ctx, cancel := context.WithTimeout(context.Background(), clientEventTimeout)
	defer cancel()
	if err := p.adapter.st.InsertEvent(ctx, &core.Event{
		Level:    level,
		Category: "download",
		Message:  message,
		Detail:   detail,
	}); err != nil {
		p.log.Error("record download client event", "error", err)
	}
}

// UnhealthyDownloadClients implements api.DownloadClientHealthReporter, which
// is what puts the "client X unreachable" banner on screen.
func (p *engineProvider) UnhealthyDownloadClients() []core.DownloadClientHealth {
	return p.health.Unhealthy()
}

// routedEngine is one enabled client's engine plus what routes decides on.
type routedEngine struct {
	id int64
	// name is the backend's type ("qbittorrent"), which is what a download row
	// records; label is the user's own name for this client, which is what a
	// banner or an event has to say.
	name     string
	label    string
	protocol string
	engine   core.Engine
}

// syncClientEngines brings the external engine cache in line with the enabled
// rows: builds what is new or changed, and closes what is no longer enabled,
// no longer present, or was edited. Returns the live set keyed by row id.
func (p *engineProvider) syncClientEngines(configured []core.DownloadClientConfig) map[int64]routedEngine {
	p.mu.Lock()
	defer p.mu.Unlock()

	live := make(map[int64]routedEngine, len(configured))
	keep := make(map[int64]bool, len(configured))
	for _, cfg := range configured {
		t, ok := clients.Lookup(cfg.Type)
		if !ok {
			continue
		}
		fingerprint := clientFingerprint(cfg)
		cached, hit := p.external[cfg.ID]
		if hit && cached.fingerprint != fingerprint {
			p.closeClientEngine(cached)
			delete(p.external, cfg.ID)
			hit = false
		}
		if !hit {
			engine, err := p.newClientEngine(cfg)
			if err != nil {
				// Reported once: an unusable row must not log per poll, and
				// the download client's test button is where the user is told
				// about it in detail.
				p.reportLocked("build download client engine", fmt.Errorf("%s: %w", cfg.Name, err))
				continue
			}
			cached = &clientEngine{name: cfg.Type, fingerprint: fingerprint, engine: engine}
			p.external[cfg.ID] = cached
			p.log.Info("download client ready", "client", cfg.Name, "type", cfg.Type)
		}
		keep[cfg.ID] = true
		live[cfg.ID] = routedEngine{
			id: cfg.ID, name: cached.name, label: cfg.Name, protocol: t.Protocol, engine: cached.engine,
		}
	}
	for id, cached := range p.external {
		if keep[id] {
			continue
		}
		p.closeClientEngine(cached)
		delete(p.external, id)
	}
	// A client that is gone or switched off cannot be unreachable, and a
	// banner about one the user just deleted is a banner they cannot dismiss.
	p.health.Retain(keep)
	return live
}

// closeClientEngine shuts a cached engine down. A close that fails costs a
// session, never media, so it is logged rather than propagated.
func (p *engineProvider) closeClientEngine(c *clientEngine) {
	if err := c.engine.Close(); err != nil {
		p.log.Error("closing download client engine", "error", err, "type", c.name)
	}
}

// seedWaiting rebuilds, once per process, the set of downloads an external
// client is holding paused on Caravan's behalf.
//
// It is what makes a restart safe. The client cannot tell Caravan why a
// download is paused — it has one kind of paused — so without this every
// download that was waiting for a slot would come back reading as one a person
// paused, and nothing would ever start it again. The persisted row is the
// record: Caravan wrote "queued" for exactly these, and the handle carries the
// prefix that says which client row it belongs to.
func (p *engineProvider) seedWaiting(ctx context.Context, clients []core.DownloadClientConfig) {
	p.seedOnce.Do(func() {
		rows, err := p.adapter.st.ListDownloads(ctx)
		if err != nil {
			p.log.Warn("rebuilding the queued-download set", "err", err)
			return
		}
		for _, row := range rows {
			if row.State != core.DownloadQueued {
				continue
			}
			for _, c := range clients {
				if strings.HasPrefix(string(row.EngineID), clientIDPrefix(c.ID)) {
					p.admission.Wait(clientMethod(c.ID), row.EngineID)
					break
				}
			}
		}
	})
}

// registerClientWake teaches the coordinator how to start this client's queue
// moving again.
//
// The built-in engines hold their own waiting downloads and re-ask for
// themselves. A client cannot: what is waiting is paused inside it, and only
// Caravan knows that it was Caravan that paused it. So the wake takes as many
// waiting downloads as the caps now allow and unpauses each one at the client.
func (p *engineProvider) registerClientWake(pick routedEngine) {
	method := clientMethod(pick.id)
	engine := pick.engine
	p.admission.Register(method, func() {
		for _, id := range p.admission.TakeWaiting(method, nil) {
			native := strings.TrimPrefix(string(id), clientIDPrefix(pick.id))
			if err := engine.Resume(context.Background(), core.DownloadID(native)); err != nil {
				// The slot was granted and the client would not take it. Give
				// the slot back and put the download back in line, so the next
				// freed slot tries again — the one outcome that must not happen
				// here is a row that reads "queued" forever because the thing
				// that would have started it gave up silently.
				p.admission.Release(id)
				p.admission.Wait(method, id)
				p.log.Error("starting a queued download at its client",
					"client", pick.label, "download", id, "err", err)
				p.recordClientEvent(core.EventLevelWarn,
					fmt.Sprintf("Could not start a queued download at %s", pick.label),
					fmt.Sprintf("%s: it stays queued and Caravan will try again when the next download slot frees", err))
			}
		}
	})
}
