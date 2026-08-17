package store

import "github.com/uptrace/bun"

// downloadClientModel is the database representation of an external download
// client. The core configuration remains persistence-agnostic.
type downloadClientModel struct {
	bun.BaseModel `bun:"table:download_clients,alias:download_client"`

	ID            int64  `bun:",pk,autoincrement"`
	Kind          string `bun:",notnull"`
	Name          string `bun:",notnull"`
	URL           string `bun:",notnull"`
	Username      string `bun:",notnull"`
	Password      string `bun:",notnull"`
	APIKey        string `bun:"api_key,notnull"`
	Category      string `bun:",notnull"`
	Priority      int    `bun:",notnull"`
	Enabled       bool   `bun:",notnull"`
	MaxConcurrent int    `bun:"max_concurrent,notnull"`
	CreatedAt     string `bun:"created_at,notnull"`
	UpdatedAt     string `bun:"updated_at,notnull"`
}

// indexerModel keeps categories as their JSON database text. Conversion at the
// store boundary lets legacy/default empty strings mean an empty category list
// while still reporting malformed JSON as a read error.
type indexerModel struct {
	bun.BaseModel `bun:"table:indexers,alias:indexer"`

	ID                  int64  `bun:",pk,autoincrement"`
	DefinitionID        string `bun:"definition_id,notnull"`
	Settings            string `bun:",notnull"`
	Name                string `bun:",notnull"`
	URL                 string `bun:",notnull"`
	APIKey              string `bun:"api_key,notnull"`
	Protocol            string `bun:",notnull"`
	Categories          string `bun:",notnull"`
	Priority            int    `bun:",notnull"`
	Enabled             bool   `bun:",notnull"`
	HealthError         string `bun:"health_error,notnull"`
	ConsecutiveFailures int    `bun:"consecutive_failures,notnull"`
	LastHealthAt        string `bun:"last_health_at,notnull"`
	CreatedAt           string `bun:"created_at,notnull"`
	UpdatedAt           string `bun:"updated_at,notnull"`
}

// usenetServerModel is the database representation of an NNTP server. The core
// configuration remains free of ORM-specific tags.
type usenetServerModel struct {
	bun.BaseModel `bun:"table:usenet_servers,alias:usenet_server"`

	ID             int64  `bun:",pk,autoincrement"`
	Name           string `bun:",notnull"`
	Host           string `bun:",notnull"`
	Port           int    `bun:",notnull"`
	TLS            bool   `bun:",notnull"`
	Username       string `bun:",notnull"`
	Password       string `bun:",notnull"`
	MaxConnections int    `bun:"max_connections,notnull"`
	Priority       int    `bun:",notnull"`
	Enabled        bool   `bun:",notnull"`
	CreatedAt      string `bun:"created_at,notnull"`
	UpdatedAt      string `bun:"updated_at,notnull"`
}
