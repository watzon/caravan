package download

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

// fakeEngine is a core.Engine that records what it was asked to add and knows
// exactly the download ids it was seeded with.
type fakeEngine struct {
	name string
	// added is every release this engine was handed, so a misroute is visible
	// as a release in the wrong engine rather than only as a missing one.
	added []core.Release
	// holds are the download ids this engine claims.
	holds map[core.DownloadID]bool
	// listErr makes List fail, standing in for an unreachable client.
	listErr error
	paused  []core.DownloadID
	removed []core.DownloadID
}

func newFakeEngine(name string, holds ...core.DownloadID) *fakeEngine {
	e := &fakeEngine{name: name, holds: map[core.DownloadID]bool{}}
	for _, id := range holds {
		e.holds[id] = true
	}
	return e
}

func (e *fakeEngine) Add(_ context.Context, r core.Release, _ core.AddOpts) (core.DownloadID, error) {
	e.added = append(e.added, r)
	id := core.DownloadID(e.name + ":" + r.Title)
	e.holds[id] = true
	return id, nil
}

func (e *fakeEngine) Status(_ context.Context, id core.DownloadID) (*core.DownloadStatus, error) {
	if !e.holds[id] {
		return nil, ErrNotFound
	}
	return &core.DownloadStatus{ID: id, Name: e.name}, nil
}

func (e *fakeEngine) List(context.Context) ([]core.DownloadStatus, error) {
	if e.listErr != nil {
		return nil, e.listErr
	}
	out := []core.DownloadStatus{}
	for id := range e.holds {
		out = append(out, core.DownloadStatus{ID: id, Name: e.name})
	}
	return out, nil
}

func (e *fakeEngine) Pause(_ context.Context, id core.DownloadID) error {
	if !e.holds[id] {
		return ErrNotFound
	}
	e.paused = append(e.paused, id)
	return nil
}

func (e *fakeEngine) Resume(context.Context, core.DownloadID) error { return nil }

// Remove succeeds for an id this engine never had, the way qBittorrent's
// delete does. It is what makes "try each engine in turn" unsafe and ownership
// lookup necessary.
func (e *fakeEngine) Remove(_ context.Context, id core.DownloadID, _ bool) error {
	e.removed = append(e.removed, id)
	delete(e.holds, id)
	return nil
}

func (e *fakeEngine) Close() error { return nil }

type pollActivity struct {
	mu      sync.Mutex
	active  int
	maximum int
}

func (a *pollActivity) begin() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.active++
	if a.active > a.maximum {
		a.maximum = a.active
	}
}

func (a *pollActivity) end() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.active--
}

func (a *pollActivity) max() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.maximum
}

type controlledListEngine struct {
	*fakeEngine
	entered   chan<- string
	completed chan<- string
	release   <-chan struct{}
	statuses  []core.DownloadStatus
	listErr   error
	activity  *pollActivity
}

func (e *controlledListEngine) List(ctx context.Context) ([]core.DownloadStatus, error) {
	if e.activity != nil {
		e.activity.begin()
		defer e.activity.end()
	}
	if e.entered != nil {
		e.entered <- e.name
	}
	if e.release != nil {
		select {
		case <-e.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if e.completed != nil {
		e.completed <- e.name
	}
	return e.statuses, e.listErr
}

type listCallResult struct {
	statuses []core.DownloadStatus
	err      error
}

func awaitListCall(t *testing.T, result <-chan listCallResult) listCallResult {
	t.Helper()
	select {
	case got := <-result:
		return got
	case <-time.After(2 * time.Second):
		t.Fatal("Router.List did not return")
		return listCallResult{}
	}
}

func awaitString(t *testing.T, values <-chan string) string {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for poll signal")
		return ""
	}
}

func awaitError(t *testing.T, values <-chan error) error {
	t.Helper()
	select {
	case err := <-values:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reported poll error")
		return nil
	}
}

// staticTable is a download.Table over a fixed set of routes.
func staticTable(routes ...Route) Table {
	return func(context.Context) ([]Route, error) { return routes, nil }
}

// The acceptance criterion: a release reaches the engine configured for its
// protocol, whatever the configuration is, and a protocol nothing is
// configured for is rejected in words the user can act on rather than sent
// to an engine that does not speak it.
func TestRouterDispatchesOnReleaseProtocol(t *testing.T) {
	ctx := context.Background()

	torrentRelease := core.Release{Title: "Movie.2020.1080p", Protocol: core.ProtocolTorrent}
	usenetRelease := core.Release{Title: "Movie.2020.1080p.nzb", Protocol: core.ProtocolUsenet}

	tests := []struct {
		name string
		// torrent and usenet are the configured defaults; "" means unconfigured.
		torrent string
		usenet  string
		release core.Release
		// want is the engine name the release must land on, or "" when the
		// grab must be rejected.
		want string
	}{
		{
			name:    "stock caravan sends torrents to the embedded engine",
			torrent: EngineName,
			release: torrentRelease,
			want:    EngineName,
		},
		{
			name:    "a usenet release with no usenet client is rejected",
			torrent: EngineName,
			release: usenetRelease,
		},
		{
			name:    "a configured usenet client takes usenet releases",
			torrent: EngineName,
			usenet:  "sabnzbd",
			release: usenetRelease,
			want:    "sabnzbd",
		},
		{
			name:    "a torrent release still goes to the torrent engine when usenet is configured",
			torrent: EngineName,
			usenet:  "sabnzbd",
			release: torrentRelease,
			want:    EngineName,
		},
		{
			name:    "the torrent default can be an external client",
			torrent: "qbittorrent",
			usenet:  "sabnzbd",
			release: torrentRelease,
			want:    "qbittorrent",
		},
		{
			name:    "a usenet release is rejected rather than sent to the torrent engine",
			torrent: "qbittorrent",
			release: usenetRelease,
		},
		{
			name:    "a torrent release is rejected when only usenet is configured",
			usenet:  "sabnzbd",
			release: torrentRelease,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engines := map[string]*fakeEngine{}
			var routes []Route
			for _, r := range []struct{ name, protocol string }{
				{tt.torrent, core.ProtocolTorrent},
				{tt.usenet, core.ProtocolUsenet},
			} {
				if r.name == "" {
					continue
				}
				engine := newFakeEngine(r.name)
				engines[r.name] = engine
				routes = append(routes, Route{Name: r.name, Protocol: r.protocol, Engine: engine})
			}
			router := NewRouter(staticTable(routes...))

			id, err := router.Add(ctx, tt.release, core.AddOpts{})

			if tt.want == "" {
				if !errors.Is(err, ErrNoEngine) {
					t.Fatalf("Add = (%q, %v), want ErrNoEngine", id, err)
				}
				// The rejection has to say what to configure; a reason the
				// user cannot act on is a silent drop with extra steps. Both
				// protocols have a built-in engine now, so the hint names the
				// storage root that engine needs rather than an external
				// client the user does not have to install.
				if !strings.Contains(err.Error(), "storage root") {
					t.Errorf("rejection reason = %q, want it to name what to configure", err)
				}
				for name, engine := range engines {
					if len(engine.added) != 0 {
						t.Errorf("rejected release reached engine %q", name)
					}
				}
				if got := router.EngineNameFor(ctx, tt.release.Protocol); got != "" {
					t.Errorf("EngineNameFor(%q) = %q, want \"\"", tt.release.Protocol, got)
				}
				return
			}

			if err != nil {
				t.Fatalf("Add: %v", err)
			}
			for name, engine := range engines {
				if name == tt.want {
					if len(engine.added) != 1 || engine.added[0].Title != tt.release.Title {
						t.Errorf("engine %q got %v, want the release", name, engine.added)
					}
					continue
				}
				if len(engine.added) != 0 {
					t.Errorf("engine %q got %v, want nothing", name, engine.added)
				}
			}
			// The download row records the engine that actually holds it,
			// which is what addresses the download afterwards.
			if got := router.EngineNameFor(ctx, tt.release.Protocol); got != tt.want {
				t.Errorf("EngineNameFor(%q) = %q, want %q", tt.release.Protocol, got, tt.want)
			}
		})
	}
}

// A download is addressed by the engine that holds it, never by the engine
// that is currently the default. Trying engines in turn would let a delete
// land on the wrong client — some clients treat an unknown handle as a
// successful no-op — while the real one kept the data.
func TestRouterAddressesTheEngineHoldingTheDownload(t *testing.T) {
	ctx := context.Background()
	embedded := newFakeEngine(EngineName, "torrent-1")
	sab := newFakeEngine("sabnzbd", "SABnzbd_nzo_abc")
	router := NewRouter(staticTable(
		Route{Name: EngineName, Protocol: core.ProtocolTorrent, Engine: embedded},
		Route{Name: "sabnzbd", Protocol: core.ProtocolUsenet, Engine: sab},
	))

	if err := router.Remove(ctx, "SABnzbd_nzo_abc", true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(embedded.removed) != 0 {
		t.Errorf("remove reached the wrong engine: %v", embedded.removed)
	}
	if len(sab.removed) != 1 {
		t.Errorf("sabnzbd removed = %v, want the download", sab.removed)
	}

	if err := router.Pause(ctx, "torrent-1"); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if len(embedded.paused) != 1 || len(sab.paused) != 0 {
		t.Errorf("pause landed on embedded=%v sabnzbd=%v", embedded.paused, sab.paused)
	}

	// A handle nobody holds is still a 404's worth of not-found, exactly as it
	// was before there was a router.
	if _, err := router.Status(ctx, "nobody-has-this"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Status(unknown) = %v, want ErrNotFound", err)
	}
}

// One unreachable client must not blank the queue or stall the import watcher
// for the engines that are working.
func TestRouterListSurvivesAnUnreachableClient(t *testing.T) {
	ctx := context.Background()
	embedded := newFakeEngine(EngineName, "torrent-1")
	sab := newFakeEngine("sabnzbd", "SABnzbd_nzo_abc")
	sab.listErr = errors.New("dial tcp: connection refused")
	router := NewRouter(staticTable(
		Route{Name: EngineName, Protocol: core.ProtocolTorrent, Engine: embedded},
		Route{Name: "sabnzbd", Protocol: core.ProtocolUsenet, Engine: sab},
	))

	got, err := router.List(ctx)
	if err != nil {
		t.Fatalf("List with one client down: %v", err)
	}
	if len(got) != 1 || got[0].ID != "torrent-1" {
		t.Errorf("List = %v, want the reachable engine's downloads", got)
	}

	// Nothing reachable at all is a real failure.
	embedded.listErr = errors.New("engine closed")
	if _, err := router.List(ctx); err == nil {
		t.Error("List with every client down = nil error, want a failure")
	}
}

func TestRouterListPollsConcurrently(t *testing.T) {
	const routeCount = 3
	entered := make(chan string, routeCount)
	release := make(chan struct{})
	activity := &pollActivity{}
	routes := make([]Route, 0, routeCount)
	names := []string{"first", "second", "third"}
	for i, name := range names {
		id := core.DownloadID(name)
		routes = append(routes, Route{
			Name: name,
			Engine: &controlledListEngine{
				fakeEngine: newFakeEngine(name),
				entered:    entered,
				release:    release,
				statuses:   []core.DownloadStatus{{ID: id}},
				activity:   activity,
			},
			IDPrefix: string(rune('a'+i)) + ".",
		})
	}
	router := NewRouter(staticTable(routes...))
	result := make(chan listCallResult, 1)
	go func() {
		statuses, err := router.List(context.Background())
		result <- listCallResult{statuses: statuses, err: err}
	}()

	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	seen := make(map[string]int, routeCount)
	for i := 0; i < routeCount; i++ {
		select {
		case name := <-entered:
			seen[name]++
		case <-timer.C:
			close(release)
			awaitListCall(t, result)
			t.Fatalf("only %d of %d route polls started before release", i, routeCount)
		}
	}
	for _, name := range names {
		if seen[name] != 1 {
			t.Fatalf("route %q entered %d times, want once", name, seen[name])
		}
	}
	close(release)

	got := awaitListCall(t, result)
	if got.err != nil {
		t.Fatalf("List: %v", got.err)
	}
	if len(got.statuses) != routeCount {
		t.Fatalf("List returned %d statuses, want %d", len(got.statuses), routeCount)
	}
	for i, status := range got.statuses {
		wantID := core.DownloadID(string(rune('a'+i)) + "." + names[i])
		if status.ID != wantID || status.Engine != names[i] {
			t.Errorf("status %d = %+v, want id %q from %q", i, status, wantID, names[i])
		}
	}
	if activity.max() <= 1 {
		t.Errorf("maximum active polls = %d, want greater than 1", activity.max())
	}
}

func TestRouterListPreservesRouteOrder(t *testing.T) {
	t.Run("statuses", func(t *testing.T) {
		entered := make(chan string, 3)
		completed := make(chan string, 3)
		releases := []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})}
		names := []string{"first", "second", "third"}
		routes := make([]Route, 0, len(names))
		for i, name := range names {
			routes = append(routes, Route{
				Name: name,
				Engine: &controlledListEngine{
					fakeEngine: newFakeEngine(name),
					entered:    entered,
					completed:  completed,
					release:    releases[i],
					statuses:   []core.DownloadStatus{{ID: "item"}},
				},
				IDPrefix: string(rune('a'+i)) + ".",
			})
		}
		router := NewRouter(staticTable(routes...))
		result := make(chan listCallResult, 1)
		go func() {
			statuses, err := router.List(context.Background())
			result <- listCallResult{statuses: statuses, err: err}
		}()

		for range routes {
			awaitString(t, entered)
		}
		for i := len(releases) - 1; i >= 0; i-- {
			close(releases[i])
			if got := awaitString(t, completed); got != names[i] {
				t.Fatalf("completed route = %q, want %q", got, names[i])
			}
		}

		got := awaitListCall(t, result)
		if got.err != nil {
			t.Fatalf("List: %v", got.err)
		}
		wantIDs := []core.DownloadID{"a.item", "b.item", "c.item"}
		if len(got.statuses) != len(wantIDs) {
			t.Fatalf("List returned %d statuses, want %d", len(got.statuses), len(wantIDs))
		}
		for i, status := range got.statuses {
			if status.ID != wantIDs[i] || status.Engine != names[i] {
				t.Errorf("status %d = %+v, want id %q from %q", i, status, wantIDs[i], names[i])
			}
		}
	})

	t.Run("errors", func(t *testing.T) {
		entered := make(chan string, 3)
		completed := make(chan string, 3)
		releases := []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})}
		names := []string{"first", "second", "third"}
		routes := make([]Route, 0, len(names))
		for i, name := range names {
			routes = append(routes, Route{
				Name: name,
				Engine: &controlledListEngine{
					fakeEngine: newFakeEngine(name),
					entered:    entered,
					completed:  completed,
					release:    releases[i],
					listErr:    errors.New(name + " failed"),
				},
			})
		}
		router := NewRouter(staticTable(routes...))
		result := make(chan listCallResult, 1)
		go func() {
			statuses, err := router.List(context.Background())
			result <- listCallResult{statuses: statuses, err: err}
		}()

		for range routes {
			awaitString(t, entered)
		}
		for i := len(releases) - 1; i >= 0; i-- {
			close(releases[i])
			if got := awaitString(t, completed); got != names[i] {
				t.Fatalf("completed route = %q, want %q", got, names[i])
			}
		}

		got := awaitListCall(t, result)
		if got.err == nil {
			t.Fatal("List with every route failed = nil error")
		}
		message := got.err.Error()
		first := strings.Index(message, "first: first failed")
		second := strings.Index(message, "second: second failed")
		third := strings.Index(message, "third: third failed")
		if first < 0 || second <= first || third <= second {
			t.Errorf("joined error order = %q, want first, second, third", message)
		}
	})
}

func TestRouterListHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	entered := make(chan string, 1)
	reported := make(chan error, 2)
	router := NewRouter(staticTable(Route{
		Name: "blocked",
		Engine: &controlledListEngine{
			fakeEngine: newFakeEngine("blocked"),
			entered:    entered,
			release:    make(chan struct{}),
		},
		Report: func(err error) { reported <- err },
	}))
	result := make(chan listCallResult, 1)
	go func() {
		statuses, err := router.List(ctx)
		result <- listCallResult{statuses: statuses, err: err}
	}()

	awaitString(t, entered)
	cancel()
	got := awaitListCall(t, result)
	if !errors.Is(got.err, context.Canceled) {
		t.Fatalf("List error = %v, want context cancellation", got.err)
	}
	if err := awaitError(t, reported); !errors.Is(err, context.Canceled) {
		t.Fatalf("reported error = %v, want context cancellation", err)
	}
	select {
	case err := <-reported:
		t.Fatalf("Report called more than once; extra error %v", err)
	default:
	}
}

func TestRouterListReturnsPartialResultsAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	entered := make(chan string, 1)
	reported := make(chan error, 2)
	blocked := &controlledListEngine{
		fakeEngine: newFakeEngine("blocked"),
		entered:    entered,
		release:    make(chan struct{}),
	}
	router := NewRouter(staticTable(
		Route{Name: "healthy", Engine: newFakeEngine("healthy", "ready")},
		Route{Name: "blocked", Engine: blocked, Report: func(err error) { reported <- err }},
	))
	result := make(chan listCallResult, 1)
	go func() {
		statuses, err := router.List(ctx)
		result <- listCallResult{statuses: statuses, err: err}
	}()

	awaitString(t, entered)
	cancel()
	got := awaitListCall(t, result)
	if got.err != nil {
		t.Fatalf("List with one successful route: %v", got.err)
	}
	if len(got.statuses) != 1 || got.statuses[0].ID != "ready" || got.statuses[0].Engine != "healthy" {
		t.Fatalf("List = %+v, want healthy route status", got.statuses)
	}
	if err := awaitError(t, reported); !errors.Is(err, context.Canceled) {
		t.Fatalf("reported error = %v, want context cancellation", err)
	}
	select {
	case err := <-reported:
		t.Fatalf("Report called more than once; extra error %v", err)
	default:
	}
}

// A route with no protocol takes no new work but still owns what it holds:
// moving the usenet default to another client must not strand the downloads
// the old one is still running.
func TestRouterKeepsNonDefaultEnginesAddressable(t *testing.T) {
	ctx := context.Background()
	embedded := newFakeEngine(EngineName)
	retired := newFakeEngine("nzbget", "42")
	current := newFakeEngine("sabnzbd")
	router := NewRouter(staticTable(
		Route{Name: EngineName, Protocol: core.ProtocolTorrent, Engine: embedded},
		Route{Name: "sabnzbd", Protocol: core.ProtocolUsenet, Engine: current},
		Route{Name: "nzbget", Engine: retired},
	))

	if _, err := router.Add(ctx, core.Release{Title: "New", Protocol: core.ProtocolUsenet}, core.AddOpts{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if len(retired.added) != 0 {
		t.Errorf("a route with no protocol took new work: %v", retired.added)
	}
	if len(current.added) != 1 {
		t.Errorf("the usenet default got %v, want the release", current.added)
	}

	status, err := router.Status(ctx, "42")
	if err != nil {
		t.Fatalf("Status on the retired client's download: %v", err)
	}
	if status.Name != "nzbget" {
		t.Errorf("Status = %q, want the retired client's download", status.Name)
	}
}

// The optional extensions have to keep working through the router, and an
// engine that does not implement one has to say so in a way the API can turn
// into a 400 rather than a 502.
func TestRouterDelegatesOptionalExtensions(t *testing.T) {
	ctx := context.Background()
	plain := newFakeEngine("sabnzbd", "SABnzbd_nzo_abc")
	router := NewRouter(staticTable(Route{Name: "sabnzbd", Protocol: core.ProtocolUsenet, Engine: plain}))

	if _, err := router.Insight(ctx, "SABnzbd_nzo_abc"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Insight on an engine without it = %v, want ErrUnsupported", err)
	}
	if err := router.SetDownloadRates(ctx, "SABnzbd_nzo_abc", 1, 1); !errors.Is(err, ErrUnsupported) {
		t.Errorf("SetDownloadRates on an engine without it = %v, want ErrUnsupported", err)
	}
	// Not-found still beats unsupported: the download has to exist first.
	if _, err := router.Insight(ctx, "nobody-has-this"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Insight(unknown) = %v, want ErrNotFound", err)
	}
}

// Two engines of the same kind hand out the same handles.
//
// NZBGet numbers downloads with small sequential integers, so a second NZBGet
// client — or one that was reinstalled and started counting again — has its own
// download "5". Caravan stores handles bare (`downloads.engine_id`) and looks
// grabs up by them (GetGrabByDownloadID), and Router.owner used to act on
// whichever engine answered first. That combination imports one client's
// payload against the other client's movie and overwrites its queue row.
//
// IDPrefix keeps them apart: every handle a client issues is namespaced by the
// `download_clients` row it came from, all the way through List, Add and every
// operation that addresses an existing download.
func TestRouterNamespacesCollidingHandlesPerEngine(t *testing.T) {
	ctx := context.Background()
	a := newFakeEngine("nzbget-a", "5")
	b := newFakeEngine("nzbget-b", "5")
	router := NewRouter(staticTable(
		Route{Name: "nzbget", Protocol: core.ProtocolUsenet, Engine: a, IDPrefix: "c1."},
		Route{Name: "nzbget", Engine: b, IDPrefix: "c2."},
	))

	// The queue shows two distinct downloads, not one shadowing the other.
	statuses, err := router.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	ids := map[core.DownloadID]string{}
	for _, s := range statuses {
		ids[s.ID] = s.Name
	}
	if len(ids) != 2 || ids["c1.5"] != "nzbget-a" || ids["c2.5"] != "nzbget-b" {
		t.Fatalf("List = %v, want c1.5 on nzbget-a and c2.5 on nzbget-b", ids)
	}

	// Status resolves to the right client, and reports back the handle it was
	// asked with — that handle is what the queue row and the grab are keyed on.
	got, err := router.Status(ctx, "c2.5")
	if err != nil {
		t.Fatalf("Status(c2.5): %v", err)
	}
	if got.Name != "nzbget-b" {
		t.Fatalf("Status(c2.5) answered from %q, want nzbget-b", got.Name)
	}
	if got.ID != "c2.5" {
		t.Fatalf("Status(c2.5).ID = %q, want the namespaced handle back", got.ID)
	}

	// Pause acts on the second client only, with that client's own bare handle.
	if err := router.Pause(ctx, "c2.5"); err != nil {
		t.Fatalf("Pause(c2.5): %v", err)
	}
	if len(a.paused) != 0 {
		t.Fatalf("nzbget-a paused %v, want the operation to have missed it entirely", a.paused)
	}
	if len(b.paused) != 1 || b.paused[0] != "5" {
		t.Fatalf("nzbget-b paused %v, want its own handle 5", b.paused)
	}

	// Remove is the dangerous one: it succeeds on an engine that never held the
	// id, so landing on the wrong client destroys the wrong data.
	if err := router.Remove(ctx, "c1.5", true); err != nil {
		t.Fatalf("Remove(c1.5): %v", err)
	}
	if len(b.removed) != 0 {
		t.Fatalf("nzbget-b removed %v, want the removal to have gone to nzbget-a only", b.removed)
	}
	if len(a.removed) != 1 || a.removed[0] != "5" {
		t.Fatalf("nzbget-a removed %v, want its own handle 5", a.removed)
	}
}

// Add hands back the namespaced handle, because that is what gets written to
// the grab and the `downloads` row and used to address the download forever
// after. A bare handle stored there would collide with the other client's.
func TestRouterAddReturnsANamespacedHandle(t *testing.T) {
	ctx := context.Background()
	client := newFakeEngine("nzbget-a")
	router := NewRouter(staticTable(
		Route{Name: "nzbget", Protocol: core.ProtocolUsenet, Engine: client, IDPrefix: "c7."},
	))

	id, err := router.Add(ctx, core.Release{Title: "Movie.2020", Protocol: core.ProtocolUsenet}, core.AddOpts{})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if id != "c7.nzbget-a:Movie.2020" {
		t.Fatalf("Add returned %q, want it namespaced by the client row", id)
	}
	// And the handle it returned is one the router can resolve again.
	if _, err := router.Status(ctx, id); err != nil {
		t.Fatalf("Status(%q): %v", id, err)
	}
}

// The embedded engine is deliberately not namespaced: info hashes are already
// globally unique, and prefixing them would orphan every `downloads` row
// written before external clients existed. Its handles must keep resolving
// untouched even with namespaced clients alongside it.
func TestRouterLeavesEmbeddedHandlesBare(t *testing.T) {
	ctx := context.Background()
	embedded := newFakeEngine(EngineName, "hash_embedded")
	client := newFakeEngine("nzbget", "5")
	router := NewRouter(staticTable(
		Route{Name: EngineName, Protocol: core.ProtocolTorrent, Engine: embedded},
		Route{Name: "nzbget", Protocol: core.ProtocolUsenet, Engine: client, IDPrefix: "c1."},
	))

	statuses, err := router.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	seen := map[core.DownloadID]bool{}
	for _, s := range statuses {
		seen[s.ID] = true
	}
	if !seen["hash_embedded"] || !seen["c1.5"] {
		t.Fatalf("List = %v, want the embedded handle bare and the client's namespaced", seen)
	}

	if err := router.Pause(ctx, "hash_embedded"); err != nil {
		t.Fatalf("Pause(hash_embedded): %v", err)
	}
	if len(embedded.paused) != 1 || embedded.paused[0] != "hash_embedded" {
		t.Fatalf("embedded paused %v, want its own untouched handle", embedded.paused)
	}
}
