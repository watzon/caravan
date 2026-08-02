package sabnzbd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/watzon/caravan/internal/clients"
	"github.com/watzon/caravan/internal/core"
)

// Number decodes a value SABnzbd may send either as a JSON number or as a
// quoted string.
//
// This is not defensiveness for its own sake: SABnzbd formats sizes with
// "%.2f" and percentages with "%s" into strings, keeps history byte counts as
// integers, and has moved fields between the two across versions. Declaring
// float64 would make a working install fail to decode after an upgrade.
type Number float64

// UnmarshalJSON accepts 12, "12", "12.34", "" and null.
func (n *Number) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if string(b) == "null" {
		*n = 0
		return nil
	}
	if len(b) >= 2 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		s = strings.TrimSpace(s)
		if s == "" {
			*n = 0
			return nil
		}
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return fmt.Errorf("sabnzbd: %q is not a number", s)
		}
		*n = Number(v)
		return nil
	}
	var v float64
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*n = Number(v)
	return nil
}

// Float returns the value as a float.
func (n Number) Float() float64 { return float64(n) }

// Int returns the value truncated to an integer, never negative.
func (n Number) Int() int64 {
	if n <= 0 {
		return 0
	}
	return int64(n)
}

// Queue is the answer to `mode=queue`. Only the fields Caravan reads are
// declared; SABnzbd sends thirty more.
type Queue struct {
	// Paused is the queue-wide pause switch, which is not the same as a job
	// being paused: it stops everything at once.
	Paused bool `json:"paused"`
	// KBPerSec is the queue-wide download rate in KiB/s. SABnzbd reports no
	// per-job rate — it downloads one job at a time — so this is the rate of
	// whichever job is currently transferring.
	KBPerSec Number `json:"kbpersec"`
	// Slots are the jobs still being transferred, in queue order.
	Slots []QueueSlot `json:"slots"`
}

// QueueSlot is one job SABnzbd is still transferring.
type QueueSlot struct {
	// NZOID is SABnzbd's identity for a job and therefore Caravan's
	// core.DownloadID. It survives the job's move from queue to history.
	NZOID string `json:"nzo_id"`
	// Filename is the display name.
	Filename string `json:"filename"`
	// Status is one of the status* constants below.
	Status string `json:"status"`
	// Category is the SABnzbd category, or "None".
	Category string `json:"cat"`
	// MB and MBLeft are the total and remaining size in MiB.
	MB     Number `json:"mb"`
	MBLeft Number `json:"mbleft"`
	// TimeLeft is "H:MM:SS", or "D:HH:MM:SS" past a day, and "0:00:00" when
	// SABnzbd has no estimate.
	TimeLeft string `json:"timeleft"`
	// Priority is the job's queue priority as a word ("Normal", "Force").
	Priority string `json:"priority"`
}

// HistorySlot is one job whose transfer has ended — finished, failed, or still
// being post-processed.
type HistorySlot struct {
	NZOID string `json:"nzo_id"`
	// Name is the display name; NZBName is the NZB file it came from.
	Name    string `json:"name"`
	NZBName string `json:"nzb_name"`
	// Status is one of the status* constants below.
	Status   string `json:"status"`
	Category string `json:"category"`
	// Storage is where the finished payload landed: an absolute path on
	// SABnzbd's machine, empty until post-processing has moved the job.
	Storage string `json:"storage"`
	// Path is the working directory, which is where a job that is still being
	// post-processed lives.
	Path string `json:"path"`
	// FailMessage is SABnzbd's own explanation of a failure, empty otherwise.
	FailMessage string `json:"fail_message"`
	// Bytes is the job's size and Downloaded is how much was actually
	// fetched — they differ on a failure, and on a success by the par2
	// overhead.
	Bytes      Number `json:"bytes"`
	Downloaded Number `json:"downloaded"`
	// Completed is a Unix timestamp.
	Completed int64 `json:"completed"`
}

// SABnzbd's job statuses, from sabnzbd/constants.py's Status class. The same
// vocabulary is used for queue and history rows, but two words mean different
// things depending on which list a row is in — see the two maps below.
const (
	statusIdle        = "Idle"
	statusQueued      = "Queued"
	statusDownloading = "Downloading"
	statusPaused      = "Paused"
	statusFetching    = "Fetching"
	statusGrabbing    = "Grabbing"
	statusPropagating = "Propagating"
	statusChecking    = "Checking"
	statusQuickCheck  = "QuickCheck"
	statusVerifying   = "Verifying"
	statusRepairing   = "Repairing"
	statusExtracting  = "Extracting"
	statusMoving      = "Moving"
	statusRunning     = "Running"
	statusCompleted   = "Completed"
	statusFailed      = "Failed"
	statusDeleted     = "Deleted"
)

// mebibyte is the unit SABnzbd's `mb` fields are in.
const mebibyte = 1 << 20

// kibibyte is the unit SABnzbd's `kbpersec` is in.
const kibibyte = 1 << 10

// queueStateMap collapses the statuses a *queued* job can carry onto Caravan's
// six. A job in the queue has not finished transferring, so none of these is
// ever completed.
//
// The judgement calls:
//
//   - Grabbing is downloading, not queued. It is the state a job sits in while
//     SABnzbd fetches the NZB from the link Caravan gave it, which is a
//     transfer that is already under way.
//   - Checking, Verifying, Repairing, Extracting, Moving and Running are
//     downloading: SABnzbd is still touching the files, and importing from
//     underneath a running unpack is how a library gets a half-copied file.
//   - Propagating is queued. The NZB is deliberately being held back until the
//     articles have spread across the provider's servers; nothing is moving.
var queueStateMap = map[string]core.DownloadState{
	statusIdle:        core.DownloadQueued,
	statusQueued:      core.DownloadQueued,
	statusPropagating: core.DownloadQueued,
	statusPaused:      core.DownloadPaused,
	statusGrabbing:    core.DownloadDownloading,
	statusFetching:    core.DownloadDownloading,
	statusDownloading: core.DownloadDownloading,
	statusChecking:    core.DownloadDownloading,
	statusQuickCheck:  core.DownloadDownloading,
	statusVerifying:   core.DownloadDownloading,
	statusRepairing:   core.DownloadDownloading,
	statusExtracting:  core.DownloadDownloading,
	statusMoving:      core.DownloadDownloading,
	statusRunning:     core.DownloadDownloading,
	statusCompleted:   core.DownloadCompleted,
	statusFailed:      core.DownloadFailed,
	statusDeleted:     core.DownloadFailed,
}

// historyStateMap collapses the statuses a *history* row can carry.
//
// It differs from queueStateMap in exactly one entry, and that entry is the
// reason there are two maps: in the history, Queued means "waiting for
// post-processing", not "waiting to download". Reporting it as queued would
// tell the queue UI a finished job had not started.
var historyStateMap = map[string]core.DownloadState{
	statusCompleted:  core.DownloadCompleted,
	statusFailed:     core.DownloadFailed,
	statusDeleted:    core.DownloadFailed,
	statusQueued:     core.DownloadDownloading,
	statusChecking:   core.DownloadDownloading,
	statusQuickCheck: core.DownloadDownloading,
	statusVerifying:  core.DownloadDownloading,
	statusRepairing:  core.DownloadDownloading,
	statusExtracting: core.DownloadDownloading,
	statusMoving:     core.DownloadDownloading,
	statusRunning:    core.DownloadDownloading,
	statusFetching:   core.DownloadDownloading,
}

// mapQueueState and mapHistoryState translate one SABnzbd status.
//
// Anything unrecognised — a status a future SABnzbd invents — becomes queued:
// it is the only state that claims nothing. Guessing failed would fail a
// healthy download and guessing completed would import an unfinished one.
func mapQueueState(s string) core.DownloadState { return lookupState(queueStateMap, s) }

func mapHistoryState(s string) core.DownloadState { return lookupState(historyStateMap, s) }

func lookupState(m map[string]core.DownloadState, s string) core.DownloadState {
	if got, ok := m[s]; ok {
		return got
	}
	return core.DownloadQueued
}

// errTextDeleted names a failure SABnzbd reports only as a status.
const errTextDeleted = "the job was deleted in SABnzbd"

// queueStatus converts one queued job into the snapshot the queue and the
// import watcher read.
//
// rate is the queue-wide byte rate: SABnzbd transfers one job at a time and
// reports no per-job speed, so the rate belongs to whichever job is actually
// downloading and is zero for the rest. Claiming the queue's speed for every
// queued job would show a queue of ten all moving at full speed.
func queueStatus(slot QueueSlot, rate int64) core.DownloadStatus {
	size := int64(slot.MB.Float() * mebibyte)
	left := int64(slot.MBLeft.Float() * mebibyte)
	done := size - left
	if done < 0 {
		done = 0
	}

	s := core.DownloadStatus{
		ID:         core.DownloadID(slot.NZOID),
		State:      mapQueueState(slot.Status),
		Name:       slot.Filename,
		BytesDone:  done,
		Size:       max(size, 0),
		ETASeconds: parseTimeLeft(slot.TimeLeft),
	}
	if size > 0 {
		s.Progress = clients.Clamp01(float64(done) / float64(size))
	}
	if s.State == core.DownloadDownloading {
		s.DownRate = max(rate, 0)
	}
	if s.State == core.DownloadFailed && slot.Status == statusDeleted {
		s.Error = errTextDeleted
	}
	// SavePath is deliberately empty: SABnzbd does not say where a job will
	// land until post-processing has moved it, and guessing would point the
	// importer at a temporary directory.
	return s
}

// historyStatus converts one history row.
//
// SavePath is SABnzbd's `storage` — an absolute path on SABnzbd's machine,
// which is the documented exception to the root-relative rule for external
// clients (docs/download-clients.md). It names the finished payload directly,
// the way the embedded engine's SavePath does. A row that is still being
// post-processed has no storage yet and falls back to its working directory,
// which is where the files actually are at that moment.
func historyStatus(slot HistorySlot) core.DownloadStatus {
	state := mapHistoryState(slot.Status)
	size := slot.Bytes.Int()

	done := slot.Downloaded.Int()
	switch {
	case done > size:
		// A success downloads more than the job's size: par2 blocks count too.
		done = size
	case done == 0 && state == core.DownloadCompleted:
		// Older history rows carry no `downloaded`. A completed job has all
		// of it by definition.
		done = size
	}

	s := core.DownloadStatus{
		ID:        core.DownloadID(slot.NZOID),
		State:     state,
		Name:      slot.Name,
		BytesDone: done,
		Size:      size,
		// The transfer is over, so there is no rate and no estimate left.
		ETASeconds: -1,
		SavePath:   slot.Storage,
	}
	if s.Name == "" {
		s.Name = slot.NZBName
	}
	if s.SavePath == "" {
		s.SavePath = slot.Path
	}
	if size > 0 {
		s.Progress = clients.Clamp01(float64(done) / float64(size))
	}
	if state == core.DownloadFailed {
		s.Error = slot.FailMessage
		if s.Error == "" {
			s.Error = fmt.Sprintf("SABnzbd reported %s for this job", slot.Status)
		}
	}
	return s
}

// parseTimeLeft turns SABnzbd's "H:MM:SS" (or "D:HH:MM:SS" past a day)
// estimate into seconds, normalized onto core.DownloadStatus's contract of -1
// for unknown.
//
// "0:00:00" is not "now": it is what SABnzbd prints for a paused job, a job it
// has no rate for, and a job with nothing left — none of which is an estimate.
func parseTimeLeft(s string) int64 {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) < 3 || len(parts) > 4 {
		return -1
	}
	fields := make([]int64, len(parts))
	for i, p := range parts {
		v, err := strconv.ParseInt(strings.TrimSpace(p), 10, 64)
		if err != nil || v < 0 {
			return -1
		}
		fields[i] = v
	}

	var total int64
	if len(fields) == 4 {
		total = fields[0] * 24 * 3600
		fields = fields[1:]
	}
	total += fields[0]*3600 + fields[1]*60 + fields[2]
	if total <= 0 {
		return -1
	}
	return total
}
