package nntp

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/usenet/nntptest"
)

// recordingSleep replaces the backoff wait so a test can assert the delays
// without spending them. No test in this package sleeps to synchronise.
func recordingSleep() (func(context.Context, time.Duration) error, func() []time.Duration) {
	var mu sync.Mutex
	var delays []time.Duration
	sleep := func(ctx context.Context, d time.Duration) error {
		mu.Lock()
		delays = append(delays, d)
		mu.Unlock()
		return ctx.Err()
	}
	return sleep, func() []time.Duration {
		mu.Lock()
		defer mu.Unlock()
		return append([]time.Duration(nil), delays...)
	}
}

func newMulti(t *testing.T, opts Options, servers ...ServerConfig) *MultiPool {
	t.Helper()
	m, err := NewMultiPool(servers, opts)
	if err != nil {
		t.Fatalf("NewMultiPool: %v", err)
	}
	t.Cleanup(func() { m.Close() })
	return m
}

// A backup account exists precisely for the article the main provider dropped.
func TestFetchBodyFailsOverOnAMissingArticle(t *testing.T) {
	primary, pcfg, opts := newFake(t, nntptest.Options{})
	backup, bcfg, _ := newFake(t, nntptest.Options{})
	pcfg.Name, pcfg.Priority, pcfg.ID = "primary", 1, 1
	bcfg.Name, bcfg.Priority, bcfg.ID = "backup", 2, 2
	backup.Add(testMessageID, sampleBody)

	m := newMulti(t, opts, pcfg, bcfg)
	body, err := m.FetchBody(context.Background(), testMessageID)
	if err != nil {
		t.Fatalf("FetchBody: %v", err)
	}
	if !bytes.Equal(body, sampleBody) {
		t.Fatalf("body = %q", body)
	}
	// One ask each: a 430 is never retried on the server that gave it.
	if got := primary.Stats().Bodies; got != 1 {
		t.Fatalf("primary answered %d BODY commands, want 1", got)
	}
	if got := backup.Stats().Bodies; got != 1 {
		t.Fatalf("backup answered %d BODY commands, want 1", got)
	}
}

func TestFetchBodyTriesServersInPriorityOrder(t *testing.T) {
	low, lcfg, opts := newFake(t, nntptest.Options{})
	high, hcfg, _ := newFake(t, nntptest.Options{})
	lcfg.Name, lcfg.Priority, lcfg.ID = "block-account", 10, 1
	hcfg.Name, hcfg.Priority, hcfg.ID = "unlimited", 1, 2
	low.Add(testMessageID, sampleBody)
	high.Add(testMessageID, sampleBody)

	// Registered in the wrong order on purpose: priority decides, not input.
	m := newMulti(t, opts, lcfg, hcfg)
	if got := m.Servers()[0].Name; got != "unlimited" {
		t.Fatalf("first server = %q, want the lowest priority number", got)
	}
	if _, err := m.FetchBody(context.Background(), testMessageID); err != nil {
		t.Fatalf("FetchBody: %v", err)
	}
	if got := high.Stats().Bodies; got != 1 {
		t.Fatalf("priority-1 server answered %d BODY commands, want 1", got)
	}
	if got := low.Stats().Bodies; got != 0 {
		t.Fatalf("priority-10 server answered %d BODY commands, want 0", got)
	}
}

// Missing everywhere is par2's problem, and the caller can only tell by asking
// this question, so the sentinel has to survive the aggregate error.
func TestFetchBodyMissingOnEveryServerIsArticleNotFound(t *testing.T) {
	_, acfg, opts := newFake(t, nntptest.Options{})
	_, bcfg, _ := newFake(t, nntptest.Options{})
	acfg.Name, acfg.Priority, acfg.ID = "a", 1, 1
	bcfg.Name, bcfg.Priority, bcfg.ID = "b", 2, 2

	m := newMulti(t, opts, acfg, bcfg)
	_, err := m.FetchBody(context.Background(), testMessageID)
	if !errors.Is(err, ErrArticleNotFound) {
		t.Fatalf("err = %v, want ErrArticleNotFound", err)
	}
	var fe *FetchError
	if !errors.As(err, &fe) {
		t.Fatalf("err = %v (%T), want *FetchError", err, err)
	}
	if !fe.NotFound() {
		t.Fatalf("NotFound() = false for %v", err)
	}
	if len(fe.Attempts) != 2 {
		t.Fatalf("attempts = %+v, want one per server", fe.Attempts)
	}
	if fe.MessageID != testMessageID {
		t.Fatalf("MessageID = %q, want %q", fe.MessageID, testMessageID)
	}
}

// The distinction this test protects is the important one: an article nobody
// could be asked about is unknown, not missing. Calling it missing would hand
// par2 a hole it cannot fill and lose a download to a network blip.
func TestFetchBodyUnreachableServerIsNotArticleNotFound(t *testing.T) {
	down, dcfg, opts := newFake(t, nntptest.Options{})
	_, ocfg, _ := newFake(t, nntptest.Options{})
	dcfg.Name, dcfg.Priority, dcfg.ID = "down", 1, 1
	ocfg.Name, ocfg.Priority, ocfg.ID = "other", 2, 2
	down.SetFault(nntptest.Fault{Bodies: 100, Mode: nntptest.FaultDropBeforeStatus})

	sleep, delays := recordingSleep()
	opts.Sleep = sleep
	opts.Retry = Retry{Attempts: 2, Base: time.Millisecond, Max: time.Millisecond}

	m := newMulti(t, opts, dcfg, ocfg)
	_, err := m.FetchBody(context.Background(), testMessageID)
	if err == nil {
		t.Fatal("FetchBody succeeded with no server holding the article")
	}
	if errors.Is(err, ErrArticleNotFound) {
		t.Fatalf("err = %v: an unreachable server must not read as a missing article", err)
	}
	var fe *FetchError
	if !errors.As(err, &fe) || fe.NotFound() {
		t.Fatalf("err = %v, want a *FetchError that is not NotFound", err)
	}
	if len(fe.Attempts) != 2 {
		t.Fatalf("attempts = %+v, want one per server", fe.Attempts)
	}
	// The broken server was retried; the one that answered 430 was not.
	if got := delays(); len(got) != 1 {
		t.Fatalf("backoff waits = %v, want exactly one (only the transient failure retries)", got)
	}
}

func TestFetchBodyRetriesTransientFailuresWithCappedBackoff(t *testing.T) {
	s, cfg, opts := newFake(t, nntptest.Options{})
	s.Add(testMessageID, sampleBody)
	s.SetFault(nntptest.Fault{Bodies: 3, Mode: nntptest.FaultDropBeforeStatus})

	sleep, delays := recordingSleep()
	opts.Sleep = sleep
	opts.Retry = Retry{Attempts: 4, Base: 10 * time.Millisecond, Max: 20 * time.Millisecond}

	m := newMulti(t, opts, cfg)
	body, err := m.FetchBody(context.Background(), testMessageID)
	if err != nil {
		t.Fatalf("FetchBody: %v", err)
	}
	if !bytes.Equal(body, sampleBody) {
		t.Fatalf("body = %q", body)
	}
	want := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 20 * time.Millisecond}
	got := delays()
	if len(got) != len(want) {
		t.Fatalf("backoff waits = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("backoff waits = %v, want %v", got, want)
		}
	}
}

func TestFetchBodyGivesUpAfterTheAttemptBudget(t *testing.T) {
	s, cfg, opts := newFake(t, nntptest.Options{})
	s.Add(testMessageID, sampleBody)
	s.SetFault(nntptest.Fault{Bodies: 100, Mode: nntptest.FaultStatus, Code: 400})

	sleep, _ := recordingSleep()
	opts.Sleep = sleep
	opts.Retry = Retry{Attempts: 3, Base: time.Millisecond, Max: time.Millisecond}

	m := newMulti(t, opts, cfg)
	if _, err := m.FetchBody(context.Background(), testMessageID); err == nil {
		t.Fatal("FetchBody succeeded against a server that always fails")
	}
	if got := s.Stats().Bodies; got != 3 {
		t.Fatalf("server saw %d BODY commands, want the 3-attempt budget", got)
	}
}

// A 5xx is the server saying it will not, so spending the retry budget on it
// only delays the failover.
func TestFetchBodyDoesNotRetryAPermanentRefusal(t *testing.T) {
	s, cfg, opts := newFake(t, nntptest.Options{})
	s.SetFault(nntptest.Fault{Bodies: 100, Mode: nntptest.FaultStatus, Code: 502})

	sleep, delays := recordingSleep()
	opts.Sleep = sleep

	m := newMulti(t, opts, cfg)
	if _, err := m.FetchBody(context.Background(), testMessageID); err == nil {
		t.Fatal("FetchBody succeeded against a refusing server")
	}
	if got := s.Stats().Bodies; got != 1 {
		t.Fatalf("server saw %d BODY commands, want 1", got)
	}
	if got := delays(); len(got) != 0 {
		t.Fatalf("backoff waits = %v, want none", got)
	}
}

func TestFetchBodyStopsWhenTheCallerCancels(t *testing.T) {
	s, cfg, opts := newFake(t, nntptest.Options{})
	s.Add(testMessageID, sampleBody)

	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	s.SetBodyHook(func(string) {
		once.Do(func() { close(entered) })
		<-release
	})
	defer close(release)

	m := newMulti(t, opts, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := m.FetchBody(ctx, testMessageID)
		done <- err
	}()

	<-entered
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if errors.Is(err, ErrArticleNotFound) {
			t.Fatalf("err = %v: a cancelled fetch is not a missing article", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("FetchBody did not return after the context was cancelled")
	}
}

func TestNewMultiPoolSkipsDisabledServers(t *testing.T) {
	_, on, opts := newFake(t, nntptest.Options{})
	_, off, _ := newFake(t, nntptest.Options{})
	on.Name, on.ID = "on", 1
	off.Name, off.ID, off.Enabled = "off", 2, false

	m := newMulti(t, opts, on, off)
	servers := m.Servers()
	if len(servers) != 1 || servers[0].Name != "on" {
		t.Fatalf("servers = %+v, want only the enabled one", servers)
	}
	// SPEC §12: nothing that leaves this package carries a credential.
	if servers[0].Username != "" || servers[0].Password != "" {
		t.Fatalf("servers = %+v, want the credentials cleared", servers)
	}
	if stats := m.Stats(); len(stats) != 1 || stats[0].Server != "on" {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestNewMultiPoolWithoutAnEnabledServer(t *testing.T) {
	_, cfg, opts := newFake(t, nntptest.Options{})
	cfg.Enabled = false

	for _, tc := range []struct {
		name    string
		servers []ServerConfig
	}{
		{"none configured", nil},
		{"all disabled", []ServerConfig{cfg}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewMultiPool(tc.servers, opts); !errors.Is(err, ErrNoServers) {
				t.Fatalf("err = %v, want ErrNoServers", err)
			}
		})
	}
}

func TestNewMultiPoolRejectsAnInvalidServer(t *testing.T) {
	_, cfg, opts := newFake(t, authOptions())
	cfg.Password = "line\r\nbreak"

	_, err := NewMultiPool([]ServerConfig{cfg}, opts)
	if err == nil {
		t.Fatal("NewMultiPool accepted a password containing a line break")
	}
	if got := err.Error(); strings.Contains(got, "line\r\nbreak") {
		t.Fatalf("error text leaked the credential: %q", got)
	}
}

func TestRetryDelayDoublesAndCaps(t *testing.T) {
	r := Retry{Attempts: 6, Base: 100 * time.Millisecond, Max: 400 * time.Millisecond}.normalized()
	want := []time.Duration{0, 0, 100, 200, 400, 400, 400}
	for attempt, ms := range want {
		got := r.delay(attempt)
		if got != ms*time.Millisecond {
			t.Fatalf("delay(%d) = %v, want %v", attempt, got, ms*time.Millisecond)
		}
	}

	// A zero Retry is the documented default, not an infinite loop.
	d := Retry{}.normalized()
	if d.Attempts != DefaultRetryAttempts || d.Base != DefaultRetryBase || d.Max != DefaultRetryMax {
		t.Fatalf("normalized zero Retry = %+v", d)
	}
	// A negative Base disables waiting without disabling retrying.
	n := Retry{Attempts: 3, Base: -1}.normalized()
	if n.delay(2) != 0 || n.Attempts != 3 {
		t.Fatalf("negative base: delay(2) = %v, attempts = %d", n.delay(2), n.Attempts)
	}
}

// The pipeline's use of FetchBodyFrom: a segment whose CRC failed came from
// server 0, so the second try must skip server 0 entirely rather than fetch
// the same damaged bytes again.
func TestFetchBodyFromSkipsTheServersBeforeIt(t *testing.T) {
	primary, pcfg, opts := newFake(t, nntptest.Options{})
	backup, bcfg, _ := newFake(t, nntptest.Options{})
	pcfg.Name, pcfg.Priority, pcfg.ID = "primary", 1, 1
	bcfg.Name, bcfg.Priority, bcfg.ID = "backup", 2, 2
	primary.Add(testMessageID, []byte("damaged"))
	backup.Add(testMessageID, sampleBody)

	m := newMulti(t, opts, pcfg, bcfg)
	body, server, err := m.FetchBodyFrom(context.Background(), testMessageID, 1)
	if err != nil {
		t.Fatalf("FetchBodyFrom: %v", err)
	}
	if server != 1 {
		t.Fatalf("server = %d, want 1", server)
	}
	if !bytes.Equal(body, sampleBody) {
		t.Fatalf("body = %q, want the backup's copy", body)
	}
	if got := primary.Stats().Bodies; got != 0 {
		t.Fatalf("primary answered %d BODY commands, want 0", got)
	}
	if got := backup.Stats().Bodies; got != 1 {
		t.Fatalf("backup answered %d BODY commands, want 1", got)
	}
}

// FetchBodyFrom reports which server answered so the caller knows where to
// resume from, and index 0 is exactly FetchBody.
func TestFetchBodyFromReportsTheAnsweringServer(t *testing.T) {
	_, pcfg, opts := newFake(t, nntptest.Options{})
	backup, bcfg, _ := newFake(t, nntptest.Options{})
	pcfg.Name, pcfg.Priority, pcfg.ID = "primary", 1, 1
	bcfg.Name, bcfg.Priority, bcfg.ID = "backup", 2, 2
	backup.Add(testMessageID, sampleBody)

	m := newMulti(t, opts, pcfg, bcfg)
	_, server, err := m.FetchBodyFrom(context.Background(), testMessageID, 0)
	if err != nil {
		t.Fatalf("FetchBodyFrom: %v", err)
	}
	if server != 1 {
		t.Fatalf("server = %d, want the backup that actually answered", server)
	}
}

// Running out of backups is "no server left to ask", never "the article is
// gone": only the second answer lets a caller hand the segment to par2.
func TestFetchBodyFromPastTheLastServerIsNotNotFound(t *testing.T) {
	server, cfg, opts := newFake(t, nntptest.Options{})
	server.Add(testMessageID, sampleBody)

	m := newMulti(t, opts, cfg)
	_, _, err := m.FetchBodyFrom(context.Background(), testMessageID, 1)
	if !errors.Is(err, ErrNoServers) {
		t.Fatalf("err = %v, want ErrNoServers", err)
	}
	if errors.Is(err, ErrArticleNotFound) {
		t.Fatal("exhausting the server list reported the article as missing")
	}
	if got := server.Stats().Bodies; got != 0 {
		t.Fatalf("server answered %d BODY commands, want 0", got)
	}
}
