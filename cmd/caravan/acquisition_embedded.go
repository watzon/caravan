package main

import (
	"context"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/download"
	"github.com/watzon/caravan/internal/usenet"
)

// embedded returns the built-in torrent engine, or nil when there is no
// storage root to build one under or building one failed. A failure is logged
// here, since it is the only place that knows the difference.
func (p *engineProvider) embedded() core.Engine {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.engine != nil {
		return p.engine
	}

	ctx := context.Background()
	root, err := p.adapter.StorageRoot(ctx)
	if err != nil {
		p.reportLocked("read storage root", err)
		return nil
	}
	if root == "" {
		return nil
	}
	settings, err := p.adapter.st.AllSettings(ctx)
	if err != nil {
		p.reportLocked("read engine settings", err)
		return nil
	}
	opts, err := engineOptions(settings, p.paused, p.log)
	if err != nil {
		p.reportLocked("read engine settings", err)
		return nil
	}
	opts.Store = downloadPersistence{st: p.adapter.st}
	opts.Admitter = p.admission
	// The caps have to be live before the engine restores its queue, or a
	// restart would start everything at once for the moment before the first
	// settings read.
	p.admission.SetCaps(capsFrom(settings))
	engine, err := download.NewEmbedded(root, opts)
	if err != nil {
		p.reportLocked("start download engine", err)
		return nil
	}

	p.engine = engine
	p.lastErr = ""
	p.log.Info("download engine ready", "root", root, "incomplete", download.IncompleteDir)
	return engine
}

// embeddedUsenet returns the built-in Usenet engine, or nil when there is no
// storage root to build one under or building one failed.
//
// It is built on exactly the terms the torrent engine is: lazily, because a
// first run has no storage root yet (SPEC §10.1), and once, because
// construction is also what resumes the queue. No configured news server is
// not a reason to withhold it — the engine still has to list, pause and remove
// whatever it already holds, and it tells the user to add a server when they
// try to grab.
func (p *engineProvider) embeddedUsenet() *usenet.Engine {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.news != nil {
		return p.news
	}

	ctx := context.Background()
	root, err := p.adapter.StorageRoot(ctx)
	if err != nil {
		p.reportLocked("read storage root", err)
		return nil
	}
	if root == "" {
		return nil
	}
	servers, err := p.adapter.st.ListEnabledUsenetServers(ctx)
	if err != nil {
		p.reportLocked("read usenet servers", err)
		return nil
	}
	engine, err := usenet.NewEngine(root, usenet.EngineOpts{
		Servers:  usenet.ServerConfigs(servers),
		Store:    downloadPersistence{st: p.adapter.st},
		Paused:   p.paused,
		Logger:   p.log,
		Admitter: p.admission,
	})
	if err != nil {
		p.reportLocked("start usenet engine", err)
		return nil
	}

	p.news = engine
	p.log.Info("usenet engine ready", "root", root, "servers", len(servers))
	return engine
}

// syncUsenetServers pushes the enabled `usenet_servers` rows into a running
// engine.
//
// This is how a settings change propagates without a restart, and it is
// deliberately the same shape as syncClientEngines: the routing table is
// rebuilt per operation, so re-reading the rows here is what makes "add a
// backup provider" take effect on the next queue poll. SetServers compares a
// fingerprint and does nothing when the configuration is unchanged, so the
// common case costs one small query and no dropped connections.
func (p *engineProvider) syncUsenetServers(ctx context.Context, engine *usenet.Engine) {
	servers, err := p.adapter.st.ListEnabledUsenetServers(ctx)
	if err != nil {
		p.mu.Lock()
		p.reportLocked("read usenet servers", err)
		p.mu.Unlock()
		return
	}
	if err := engine.SetServers(usenet.ServerConfigs(servers)); err != nil {
		p.mu.Lock()
		p.reportLocked("apply usenet servers", err)
		p.mu.Unlock()
	}
}

// reportLocked logs a construction failure, skipping a repeat of the one
// already reported. Must be called with p.mu held.
func (p *engineProvider) reportLocked(msg string, err error) {
	if err.Error() == p.lastErr {
		return
	}
	p.lastErr = err.Error()
	p.log.Error(msg, "error", err)
}

// ApplyEngineSettings updates rates and seeding targets on an existing engine.
// Listen port and connection count are ClientConfig fields, so they apply when
// the next engine starts rather than disrupting active peer connections.
func (p *engineProvider) ApplyEngineSettings(ctx context.Context, settings map[string]string) error {
	// The caps reach the coordinator whether or not an engine has been built:
	// it is the thing every engine will ask, and a cap saved before the first
	// grab has to be in force when that grab arrives.
	p.admission.SetCaps(capsFrom(settings))

	opts, err := engineOptions(settings, p.paused, p.log)
	if err != nil {
		return err
	}
	p.mu.Lock()
	engine := p.engine
	p.mu.Unlock()
	if engine == nil {
		return nil
	}
	if err := engine.SetGlobalRates(ctx, opts.MaxDownKBps, opts.MaxUpKBps); err != nil {
		return err
	}
	return engine.SetSeedingTargets(opts.SeedRatio, opts.SeedDays)
}

// capsFrom reads the concurrency ceilings out of the settings map.
//
// Absent or unparseable is zero, which is unlimited: a cap is the one setting
// where guessing high is safe and guessing low stops downloads, so anything
// this cannot read means "no ceiling" rather than a number nobody chose. The
// API validated these on the way in; this is the read side.
// applyCaps pushes the whole concurrency configuration — the settings keys and
// every client's own column — into the coordinator, and makes sure each client
// has a way to be told that one of its slots has freed.
//
// It runs on every settings save and every routing rebuild, because a client's
// cap lives on its row and a row can change without the settings map moving.
func (p *engineProvider) applyCaps(ctx context.Context, settings map[string]string, clients []core.DownloadClientConfig) {
	caps := capsFrom(settings)
	for _, c := range clients {
		caps.Method[clientMethod(c.ID)] = c.MaxConcurrent
	}
	p.admission.SetCaps(caps)
}
