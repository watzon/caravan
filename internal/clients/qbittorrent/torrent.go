package qbittorrent

import (
	"github.com/watzon/caravan/internal/clients"
	"github.com/watzon/caravan/internal/core"
)

// Torrent is one entry of GET /api/v2/torrents/info. Only the fields Caravan
// reads are declared; qBittorrent sends forty more and adds to them every
// release, so the struct is deliberately a subset rather than a mirror.
type Torrent struct {
	// Hash is the info hash, lowercase hex. It is qBittorrent's identity for
	// a torrent and therefore Caravan's core.DownloadID.
	Hash string `json:"hash"`
	Name string `json:"name"`
	// State is one of the state* constants below.
	State string `json:"state"`
	// Progress is completion in [0,1].
	Progress float64 `json:"progress"`
	// DlSpeed and UpSpeed are current rates in bytes per second.
	DlSpeed int64 `json:"dlspeed"`
	UpSpeed int64 `json:"upspeed"`
	// ETA is seconds to completion, or etaInfinity when there is no estimate.
	ETA int64 `json:"eta"`
	// Ratio is uploaded over downloaded, capped by qBittorrent at 9999.
	Ratio float64 `json:"ratio"`
	// Size is the total size of the selected files; TotalSize counts the ones
	// that were deselected too.
	Size      int64 `json:"size"`
	TotalSize int64 `json:"total_size"`
	// Completed is how many bytes of Size have been written.
	Completed  int64 `json:"completed"`
	AmountLeft int64 `json:"amount_left"`
	// SavePath is the directory qBittorrent writes into; ContentPath is the
	// torrent's root folder, or the file itself for a single-file torrent.
	// Both are absolute paths on qBittorrent's machine, not Caravan's root.
	SavePath    string `json:"save_path"`
	ContentPath string `json:"content_path"`
	Category    string `json:"category"`
	// Tags is a comma-concatenated list.
	Tags string `json:"tags"`
	// AddedOn is a Unix timestamp.
	AddedOn int64 `json:"added_on"`
}

// File is one entry of GET /api/v2/torrents/files.
type File struct {
	// Index is the file's position in the torrent (WebAPI 2.8.2+; zero on
	// older servers, where the slice order is the index).
	Index int `json:"index"`
	// Name is the path of the file relative to the torrent's root.
	Name string `json:"name"`
	Size int64  `json:"size"`
	// Progress is completion in [0,1].
	Progress float64 `json:"progress"`
	// Priority is 0 (skipped), 1 (normal), 6 (high) or 7 (maximum).
	Priority int `json:"priority"`
}

// qBittorrent's torrent states, as serialized by
// src/webui/api/serialize/serialize_torrent.cpp.
//
// pausedDL and pausedUP are the pre-5.0 spellings of stoppedDL and stoppedUP.
// Both are accepted: qBittorrent 4.x is still widely deployed and the rename
// was a WebAPI 2.11 change, not a behaviour change.
const (
	stateError              = "error"
	stateMissingFiles       = "missingFiles"
	stateUploading          = "uploading"
	stateStoppedUP          = "stoppedUP"
	statePausedUP           = "pausedUP"
	stateQueuedUP           = "queuedUP"
	stateStalledUP          = "stalledUP"
	stateCheckingUP         = "checkingUP"
	stateForcedUP           = "forcedUP"
	stateAllocating         = "allocating"
	stateDownloading        = "downloading"
	stateMetaDL             = "metaDL"
	stateForcedMetaDL       = "forcedMetaDL"
	stateStoppedDL          = "stoppedDL"
	statePausedDL           = "pausedDL"
	stateQueuedDL           = "queuedDL"
	stateStalledDL          = "stalledDL"
	stateCheckingDL         = "checkingDL"
	stateForcedDL           = "forcedDL"
	stateCheckingResumeData = "checkingResumeData"
	stateMoving             = "moving"
	stateUnknown            = "unknown"
)

// etaInfinity is the sentinel qBittorrent reports (100 days in seconds) when it
// has no estimate: for a seeding torrent, or one with no peers.
const etaInfinity = 8640000

// stateMap collapses qBittorrent's twenty-odd states onto Caravan's six.
//
// The three judgement calls, since they decide when the import watcher fires:
//
//   - stoppedUP/pausedUP is *completed*, not paused. The transfer is finished;
//     the user only stopped it from seeding. Reporting it as paused would hide
//     a finished download from the importer forever.
//   - checkingResumeData, checkingDL and moving are *downloading* even when
//     the data is already complete. They mean qBittorrent is still touching
//     the files, and importing from underneath a running move is how a library
//     gets a half-copied file.
//   - checkingUP is *seeding*: the download finished and qBittorrent is
//     verifying it before announcing. The import watcher treats seeding as
//     importable, and a re-check that fails will surface as error next poll.
var stateMap = map[string]core.DownloadState{
	stateError:              core.DownloadFailed,
	stateMissingFiles:       core.DownloadFailed,
	stateUploading:          core.DownloadSeeding,
	stateQueuedUP:           core.DownloadSeeding,
	stateStalledUP:          core.DownloadSeeding,
	stateForcedUP:           core.DownloadSeeding,
	stateCheckingUP:         core.DownloadSeeding,
	stateStoppedUP:          core.DownloadCompleted,
	statePausedUP:           core.DownloadCompleted,
	stateStoppedDL:          core.DownloadPaused,
	statePausedDL:           core.DownloadPaused,
	stateQueuedDL:           core.DownloadQueued,
	stateAllocating:         core.DownloadDownloading,
	stateDownloading:        core.DownloadDownloading,
	stateMetaDL:             core.DownloadDownloading,
	stateForcedMetaDL:       core.DownloadDownloading,
	stateStalledDL:          core.DownloadDownloading,
	stateForcedDL:           core.DownloadDownloading,
	stateCheckingDL:         core.DownloadDownloading,
	stateCheckingResumeData: core.DownloadDownloading,
	stateMoving:             core.DownloadDownloading,
}

// mapState translates one qBittorrent state.
//
// "unknown", and any state a future qBittorrent invents, becomes queued: it is
// the only state that claims nothing. Guessing "failed" would fail a healthy
// download and guessing "completed" would import an unfinished one.
func mapState(s string) core.DownloadState {
	if got, ok := stateMap[s]; ok {
		return got
	}
	return core.DownloadQueued
}

// stateError texts. qBittorrent's torrents/info carries no error message, only
// the state, so the failure has to be named here.
const (
	errTextError        = "qBittorrent reported an error for this torrent"
	errTextMissingFiles = "qBittorrent cannot find this torrent's files"
)

// status converts one torrent into the snapshot the queue and the import
// watcher read.
//
// SavePath is qBittorrent's content path: an absolute path on qBittorrent's
// machine, which is the documented exception to the root-relative rule for
// external clients (docs/download-clients.md). It is the torrent's own root
// folder or file rather than the parent directory, so it names the payload
// exactly the way the embedded engine's SavePath does.
func status(t Torrent) core.DownloadStatus {
	s := core.DownloadStatus{
		ID:         core.DownloadID(t.Hash),
		State:      mapState(t.State),
		Name:       t.Name,
		Progress:   clients.Clamp01(t.Progress),
		BytesDone:  max(t.Completed, 0),
		Size:       max(t.Size, 0),
		DownRate:   max(t.DlSpeed, 0),
		UpRate:     max(t.UpSpeed, 0),
		ETASeconds: eta(t.ETA),
		Ratio:      max(t.Ratio, 0),
		SavePath:   t.ContentPath,
	}
	if s.SavePath == "" {
		s.SavePath = t.SavePath
	}
	switch t.State {
	case stateError:
		s.Error = errTextError
	case stateMissingFiles:
		s.Error = errTextMissingFiles
	}
	return s
}

// eta normalizes qBittorrent's estimate onto core.DownloadStatus's contract of
// -1 for unknown. Zero is not "now": it is what a torrent with nothing left to
// download reports.
func eta(v int64) int64 {
	if v <= 0 || v >= etaInfinity {
		return -1
	}
	return v
}
