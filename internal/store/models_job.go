package store

import (
	"github.com/uptrace/bun"
	"github.com/watzon/caravan/internal/core"
)

type jobModel struct {
	bun.BaseModel `bun:"table:jobs,alias:job"`

	ID             int64 `bun:",pk,autoincrement"`
	Kind           string
	Payload        string
	State          string
	Attempts       int
	RunAfter       string
	LeaseExpiresAt string
	LastError      string
	CreatedAt      string
	UpdatedAt      string
}

func jobModelFromCore(j *core.Job) jobModel {
	return jobModel{
		ID: j.ID, Kind: j.Kind, Payload: j.Payload, State: j.State, Attempts: j.Attempts,
		RunAfter: formatTime(j.RunAfter), LeaseExpiresAt: formatTime(j.LeaseExpiresAt),
		LastError: j.LastError, CreatedAt: formatTime(j.CreatedAt), UpdatedAt: formatTime(j.UpdatedAt),
	}
}

func (m jobModel) core() core.Job {
	return core.Job{
		ID: m.ID, Kind: m.Kind, Payload: m.Payload, State: m.State, Attempts: m.Attempts,
		RunAfter: parseTime(m.RunAfter), LeaseExpiresAt: parseTime(m.LeaseExpiresAt),
		LastError: m.LastError, CreatedAt: parseTime(m.CreatedAt), UpdatedAt: parseTime(m.UpdatedAt),
	}
}
