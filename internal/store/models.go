package store

import "github.com/uptrace/bun"

// settingModel is the database representation of one application setting.
// Domain-facing methods deliberately expose the value rather than this row.
type settingModel struct {
	bun.BaseModel `bun:"table:settings,alias:setting"`

	Key       string `bun:",pk"`
	Value     string `bun:",notnull"`
	UpdatedAt string `bun:",notnull"`
}
