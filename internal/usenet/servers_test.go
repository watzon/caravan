package usenet

import (
	"reflect"
	"testing"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/usenet/nntp"
)

func TestServerConfigCarriesEveryField(t *testing.T) {
	got := ServerConfig(core.UsenetServerConfig{
		ID: 7, Name: "Eweka", Host: "news.eweka.nl", Port: 563, TLS: true,
		Username: "user", Password: "secret", MaxConnections: 20,
		Priority: 5, Enabled: true,
	})
	want := nntp.ServerConfig{
		ID: 7, Name: "Eweka", Host: "news.eweka.nl", Port: 563, TLS: true,
		Username: "user", Password: "secret", MaxConnections: 20,
		Priority: 5, Enabled: true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ServerConfig = %+v, want %+v", got, want)
	}
	// The credential has to survive: this is the value that authenticates.
	if got.Password != "secret" {
		t.Errorf("Password = %q, want it carried through to the dialler", got.Password)
	}
}

// A row written with the column defaults still has to dial somewhere, so the
// conversion is where 0 stops meaning "port zero".
func TestServerConfigResolvesDefaults(t *testing.T) {
	tests := []struct {
		name     string
		in       core.UsenetServerConfig
		wantPort int
	}{
		{"tls default port", core.UsenetServerConfig{Host: "a.example", TLS: true}, nntp.DefaultTLSPort},
		{"plaintext default port", core.UsenetServerConfig{Host: "a.example"}, nntp.DefaultPort},
		{"explicit port wins", core.UsenetServerConfig{Host: "a.example", TLS: true, Port: 9119}, 9119},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ServerConfig(tt.in)
			if got.Port != tt.wantPort {
				t.Errorf("Port = %d, want %d", got.Port, tt.wantPort)
			}
			if got.MaxConnections != nntp.DefaultMaxConnections {
				t.Errorf("MaxConnections = %d, want the default %d",
					got.MaxConnections, nntp.DefaultMaxConnections)
			}
		})
	}
}

// The engine builds its pool from ListEnabledUsenetServers, whose order is the
// failover order. The conversion must not disturb it.
func TestServerConfigsPreservesOrder(t *testing.T) {
	in := []core.UsenetServerConfig{
		{Name: "primary", Host: "a.example", Priority: 5, Enabled: true},
		{Name: "backup", Host: "b.example", Priority: 50, Enabled: true},
	}
	got := ServerConfigs(in)
	if len(got) != 2 || got[0].Name != "primary" || got[1].Name != "backup" {
		t.Fatalf("ServerConfigs = %+v, want priority order preserved", got)
	}
	// Every converted server is dialable, which is what NewMultiPool requires.
	for _, c := range got {
		if err := c.Validate(); err != nil {
			t.Errorf("Validate(%s) = %v, want a dialable configuration", c.Name, err)
		}
	}
}

func TestServerConfigsEmpty(t *testing.T) {
	if got := ServerConfigs(nil); len(got) != 0 {
		t.Errorf("ServerConfigs(nil) = %+v, want empty", got)
	}
}
