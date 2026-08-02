package store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

func TestUsenetServerCRUD(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	if _, err := st.GetUsenetServer(ctx, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetUsenetServer(absent) = %v, want ErrNotFound", err)
	}
	list, err := st.ListUsenetServers(ctx)
	if err != nil {
		t.Fatalf("ListUsenetServers: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("ListUsenetServers on a fresh db = %v, want empty", list)
	}

	srv := core.UsenetServerConfig{
		Name:           "Eweka",
		Host:           "news.eweka.nl",
		Port:           563,
		TLS:            true,
		Username:       "user",
		Password:       "secret",
		MaxConnections: 20,
		Priority:       10,
		Enabled:        true,
	}
	if err := st.UpsertUsenetServer(ctx, &srv); err != nil {
		t.Fatalf("UpsertUsenetServer: %v", err)
	}
	if srv.ID == 0 {
		t.Fatal("UpsertUsenetServer did not write back an ID")
	}

	got, err := st.GetUsenetServer(ctx, srv.ID)
	if err != nil {
		t.Fatalf("GetUsenetServer: %v", err)
	}
	if !reflect.DeepEqual(*got, srv) {
		t.Errorf("GetUsenetServer = %+v, want %+v", *got, srv)
	}

	// Update in place: same id, no second row, and the credential the caller
	// carried is what comes back.
	srv.Name = "Eweka (renamed)"
	srv.Password = "rotated"
	srv.TLS = false
	srv.Port = 119
	srv.Enabled = false
	if err := st.UpsertUsenetServer(ctx, &srv); err != nil {
		t.Fatalf("UpsertUsenetServer update: %v", err)
	}
	list, err = st.ListUsenetServers(ctx)
	if err != nil {
		t.Fatalf("ListUsenetServers: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListUsenetServers = %+v, want one row after update", list)
	}
	if !reflect.DeepEqual(list[0], srv) {
		t.Errorf("ListUsenetServers[0] = %+v, want %+v", list[0], srv)
	}

	if err := st.DeleteUsenetServer(ctx, srv.ID); err != nil {
		t.Fatalf("DeleteUsenetServer: %v", err)
	}
	if _, err := st.GetUsenetServer(ctx, srv.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetUsenetServer after delete = %v, want ErrNotFound", err)
	}
}

// Updating a row that is not there must not quietly insert one: the HTTP layer
// turns ErrNotFound into a 404.
func TestUpsertUsenetServerMissingIDIsNotFound(t *testing.T) {
	cfg := core.UsenetServerConfig{ID: 404, Name: "ghost", Host: "news.example", Port: 563, TLS: true}
	st, _ := openTemp(t)

	if err := st.UpsertUsenetServer(context.Background(), &cfg); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpsertUsenetServer(missing id) = %v, want ErrNotFound", err)
	}
}

// The engine asks for servers in failover order, so the order the store hands
// them back is part of the contract: lowest priority first, name breaking the
// tie, disabled rows absent entirely.
func TestListEnabledUsenetServersOrdersByPriority(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	for _, c := range []core.UsenetServerConfig{
		{Name: "backup", Host: "b.example", Port: 563, TLS: true, Priority: 50, Enabled: true},
		{Name: "primary", Host: "a.example", Port: 563, TLS: true, Priority: 5, Enabled: true},
		{Name: "off", Host: "c.example", Port: 563, TLS: true, Priority: 1, Enabled: false},
		{Name: "aaa-tie", Host: "d.example", Port: 563, TLS: true, Priority: 50, Enabled: true},
	} {
		cfg := c
		if err := st.UpsertUsenetServer(ctx, &cfg); err != nil {
			t.Fatalf("UpsertUsenetServer(%s): %v", c.Name, err)
		}
	}

	enabled, err := st.ListEnabledUsenetServers(ctx)
	if err != nil {
		t.Fatalf("ListEnabledUsenetServers: %v", err)
	}
	var names []string
	for _, c := range enabled {
		names = append(names, c.Name)
	}
	want := []string{"primary", "aaa-tie", "backup"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("enabled servers = %v, want %v", names, want)
	}

	// The disabled row keeps its configuration; it is only skipped.
	all, err := st.ListUsenetServers(ctx)
	if err != nil {
		t.Fatalf("ListUsenetServers: %v", err)
	}
	if len(all) != 4 {
		t.Errorf("ListUsenetServers = %d rows, want all 4 including the disabled one", len(all))
	}
}

// usenet_servers.name is unique, which is what lets the HTTP layer answer a
// duplicate with 409 instead of a 500.
func TestUpsertUsenetServerRejectsDuplicateName(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	first := core.UsenetServerConfig{Name: "dup", Host: "a.example", Port: 563, TLS: true, Enabled: true}
	if err := st.UpsertUsenetServer(ctx, &first); err != nil {
		t.Fatalf("UpsertUsenetServer: %v", err)
	}
	second := core.UsenetServerConfig{Name: "dup", Host: "b.example", Port: 563, TLS: true, Enabled: true}
	if err := st.UpsertUsenetServer(ctx, &second); err == nil {
		t.Fatal("UpsertUsenetServer with a duplicate name = nil, want a constraint failure")
	}
}

// A row written with the column defaults still describes a dialable server:
// the zero port and zero connection cap mean "the protocol default", not
// "port 0". The API resolves them before storing, but a hand-edited row or a
// future writer must still round-trip through the same resolution.
func TestUsenetServerZeroPortMeansProtocolDefault(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	tls := core.UsenetServerConfig{Name: "tls", Host: "a.example", TLS: true, Enabled: true}
	plain := core.UsenetServerConfig{Name: "plain", Host: "b.example", Enabled: true}
	for _, c := range []*core.UsenetServerConfig{&tls, &plain} {
		if err := st.UpsertUsenetServer(ctx, c); err != nil {
			t.Fatalf("UpsertUsenetServer(%s): %v", c.Name, err)
		}
	}

	got, err := st.GetUsenetServer(ctx, tls.ID)
	if err != nil {
		t.Fatalf("GetUsenetServer: %v", err)
	}
	if got.Port != 0 {
		t.Errorf("stored port = %d, want the 0 that was written", got.Port)
	}
	if got.ResolvedPort() != core.UsenetDefaultTLSPort {
		t.Errorf("ResolvedPort() = %d, want %d", got.ResolvedPort(), core.UsenetDefaultTLSPort)
	}
	if got.ResolvedMaxConnections() != core.UsenetDefaultMaxConnections {
		t.Errorf("ResolvedMaxConnections() = %d, want %d",
			got.ResolvedMaxConnections(), core.UsenetDefaultMaxConnections)
	}

	got, err = st.GetUsenetServer(ctx, plain.ID)
	if err != nil {
		t.Fatalf("GetUsenetServer: %v", err)
	}
	if got.ResolvedPort() != core.UsenetDefaultPort {
		t.Errorf("plaintext ResolvedPort() = %d, want %d", got.ResolvedPort(), core.UsenetDefaultPort)
	}
}
