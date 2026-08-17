package store

import (
	"database/sql"
	"encoding/json"

	"github.com/uptrace/bun"
	"github.com/watzon/caravan/internal/core"
)

type releaseModel struct {
	bun.BaseModel `bun:"table:releases,alias:release"`

	ID          int64 `bun:",pk,autoincrement"`
	IndexerID   int64
	IndexerName string
	GUID        string
	Title       string
	DownloadURL string
	InfoURL     string
	InfoHash    string
	Protocol    string
	Size        int64
	Seeders     int
	Leechers    int
	PublishedAt string
	Parsed      string
	SeenAt      string
	Categories  string
	Attributes  string
}

func releaseModelFromCore(r *core.Release, parsed, categories, attributes string) releaseModel {
	return releaseModel{
		ID: r.ID, IndexerID: r.IndexerID, IndexerName: r.Indexer, GUID: r.GUID,
		Title: r.Title, DownloadURL: r.DownloadURL, InfoHash: r.InfoHash,
		Protocol: r.Protocol, Size: r.Size, Seeders: r.Seeders, Leechers: r.Leechers,
		PublishedAt: formatTime(r.PublishedAt), Parsed: parsed, SeenAt: formatTime(now()),
		Categories: categories, Attributes: attributes,
	}
}

func (m releaseModel) core() core.Release {
	out := core.Release{
		ID: m.ID, IndexerID: m.IndexerID, Indexer: m.IndexerName, Title: m.Title,
		GUID: m.GUID, DownloadURL: m.DownloadURL, InfoHash: m.InfoHash,
		Protocol: m.Protocol, Size: m.Size, Seeders: m.Seeders, Leechers: m.Leechers,
		PublishedAt: parseTime(m.PublishedAt),
	}
	if m.Parsed != "" {
		_ = json.Unmarshal([]byte(m.Parsed), &out.Parsed)
	}
	if m.Categories != "" {
		_ = json.Unmarshal([]byte(m.Categories), &out.Categories)
	}
	if m.Attributes != "" {
		_ = json.Unmarshal([]byte(m.Attributes), &out.Attributes)
		out.Attributes = core.NormalizeReleaseAttributes(out.Attributes)
	}
	return out
}

type unmatchedFileModel struct {
	bun.BaseModel `bun:"table:unmatched_files,alias:unmatched_file"`

	ID        int64 `bun:",pk,autoincrement"`
	Path      string
	Size      int64
	Parsed    string
	Reason    string
	SeenAt    string
	LibraryID sql.NullInt64
}

func unmatchedFileModelFromCore(u *core.UnmatchedFile, parsed string) unmatchedFileModel {
	return unmatchedFileModel{
		ID: u.ID, Path: u.Path, Size: u.Size, Parsed: parsed, Reason: u.Reason,
		SeenAt: formatTime(u.SeenAt), LibraryID: sql.NullInt64{Int64: u.LibraryID, Valid: u.LibraryID != 0},
	}
}

func (m unmatchedFileModel) core() core.UnmatchedFile {
	out := core.UnmatchedFile{
		ID: m.ID, Path: m.Path, Size: m.Size, Reason: m.Reason,
		LibraryID: m.LibraryID.Int64, SeenAt: parseTime(m.SeenAt),
	}
	if m.Parsed != "" {
		_ = json.Unmarshal([]byte(m.Parsed), &out.Parsed)
	}
	return out
}
