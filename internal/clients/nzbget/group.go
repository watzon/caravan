package nzbget

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/watzon/caravan/internal/clients"
	"github.com/watzon/caravan/internal/core"
)

// Group is one entry of `listgroups`: a download NZBGet is still working on.
// Only the fields Caravan reads are declared; NZBGet sends fifty more and adds
// to them every release, so the struct is deliberately a subset.
type Group struct {
	// NZBID is NZBGet's identity for a download and therefore, spelled as a
	// string, Caravan's core.DownloadID.
	NZBID int64 `json:"NZBID"`
	// NZBName is the display name, without path or extension.
	NZBName string `json:"NZBName"`
	// Kind is "NZB" or "URL".
	Kind string `json:"Kind"`
	// Status is one of the status* constants below.
	Status   string `json:"Status"`
	Category string `json:"Category"`
	// DestDir is where NZBGet is assembling the download; FinalDir is where a
	// post-processing script moved it, when one did. Both are absolute paths
	// on NZBGet's machine, not Caravan's root.
	DestDir  string `json:"DestDir"`
	FinalDir string `json:"FinalDir"`
	// NZBGet splits every 64-bit byte count into two unsigned 32-bit halves,
	// because its RPC protocol predates 64-bit integers in XML-RPC.
	FileSizeLo      uint32 `json:"FileSizeLo"`
	FileSizeHi      uint32 `json:"FileSizeHi"`
	RemainingSizeLo uint32 `json:"RemainingSizeLo"`
	RemainingSizeHi uint32 `json:"RemainingSizeHi"`
	// Health is the download's article health in permille; 1000 is perfect.
	Health int `json:"Health"`
}

// HistoryItem is one entry of `history`: a download NZBGet has finished with.
type HistoryItem struct {
	NZBID int64 `json:"NZBID"`
	// Name is the display name, without path or extension.
	Name string `json:"Name"`
	// Kind is "NZB", "URL" or "DUP".
	Kind string `json:"Kind"`
	// Status is a "CATEGORY/DETAIL" pair — see mapHistoryState.
	Status   string `json:"Status"`
	Category string `json:"Category"`
	// DestDir is where the download was assembled; FinalDir is where
	// post-processing moved it, when it did. FinalDir wins when both are set.
	DestDir  string `json:"DestDir"`
	FinalDir string `json:"FinalDir"`
	// FileSize is the download's size; DownloadedSize is how much was fetched,
	// which is larger on a success because par2 blocks count too.
	FileSizeLo       uint32 `json:"FileSizeLo"`
	FileSizeHi       uint32 `json:"FileSizeHi"`
	DownloadedSizeLo uint32 `json:"DownloadedSizeLo"`
	DownloadedSizeHi uint32 `json:"DownloadedSizeHi"`
	// ParStatus and UnpackStatus say which stage failed when Status does not
	// spell it out.
	ParStatus    string `json:"ParStatus"`
	UnpackStatus string `json:"UnpackStatus"`
	// HistoryTime is a Unix timestamp.
	HistoryTime int64 `json:"HistoryTime"`
}

// NZBGet's queue statuses, from ListGroupsXmlCommand::DetectStatus in
// daemon/remote/XmlRpc.cpp.
const (
	statusQueued   = "QUEUED"
	statusPaused   = "PAUSED"
	statusDownload = "DOWNLOADING"
	statusFetching = "FETCHING"

	// The post-processing stages. A group carrying one of these has finished
	// transferring but NZBGet is still working on its files.
	statusPPQueued           = "PP_QUEUED"
	statusLoadingPars        = "LOADING_PARS"
	statusVerifyingSources   = "VERIFYING_SOURCES"
	statusRepairing          = "REPAIRING"
	statusVerifyingRepaired  = "VERIFYING_REPAIRED"
	statusRenaming           = "RENAMING"
	statusUnpacking          = "UNPACKING"
	statusMoving             = "MOVING"
	statusPostUnpackRenaming = "POST_UNPACK_RENAMING"
	statusExecutingScript    = "EXECUTING_SCRIPT"
	statusPPFinished         = "PP_FINISHED"
	statusQSQueued           = "QS_QUEUED"
	statusQSExecuting        = "QS_EXECUTING"
)

// NZBGet's history status categories: the part of a "CATEGORY/DETAIL" status
// before the slash.
const (
	historySuccess = "SUCCESS"
	historyWarning = "WARNING"
	historyFailure = "FAILURE"
	historyDeleted = "DELETED"
)

// groupStateMap collapses the statuses a queued download can carry onto
// Caravan's six. A group in the queue is never completed: NZBGet moves it to
// the history the moment it is done with it.
//
// The judgement call is the whole post-processing block. Repairing, unpacking,
// moving and running a script are all *downloading*, because NZBGet is still
// touching the files — importing from underneath a running unpack is how a
// library gets a half-copied file. A PAR repair that fails does not appear
// here at all; it lands in the history as FAILURE/PAR.
var groupStateMap = map[string]core.DownloadState{
	statusQueued:   core.DownloadQueued,
	statusPaused:   core.DownloadPaused,
	statusDownload: core.DownloadDownloading,
	// Fetching an NZB from a URL someone else queued.
	statusFetching:           core.DownloadDownloading,
	statusPPQueued:           core.DownloadDownloading,
	statusLoadingPars:        core.DownloadDownloading,
	statusVerifyingSources:   core.DownloadDownloading,
	statusRepairing:          core.DownloadDownloading,
	statusVerifyingRepaired:  core.DownloadDownloading,
	statusRenaming:           core.DownloadDownloading,
	statusUnpacking:          core.DownloadDownloading,
	statusMoving:             core.DownloadDownloading,
	statusPostUnpackRenaming: core.DownloadDownloading,
	statusExecutingScript:    core.DownloadDownloading,
	statusPPFinished:         core.DownloadDownloading,
	statusQSQueued:           core.DownloadDownloading,
	statusQSExecuting:        core.DownloadDownloading,
}

// mapGroupState translates one queue status.
//
// Anything unrecognised — a post-processing stage a future NZBGet invents —
// becomes queued: it is the only state that claims nothing. Guessing failed
// would fail a healthy download and guessing completed would import one that
// is still being unpacked.
func mapGroupState(s string) core.DownloadState {
	if got, ok := groupStateMap[s]; ok {
		return got
	}
	return core.DownloadQueued
}

// mapHistoryState translates one history status.
//
// NZBGet spells these as "CATEGORY/DETAIL" — SUCCESS/ALL, FAILURE/PAR,
// DELETED/MANUAL — and it has added details in most releases. Only the
// category is read, so a detail nobody has seen yet still maps correctly.
//
// The two decisions:
//
//   - WARNING is completed. NZBGet's warnings are complaints about
//     post-processing over data it did finish fetching (a script exited
//     non-zero, a par check was skipped). Calling them failures would block
//     the import of a download that is sitting there, and an import that finds
//     nothing usable already parks itself.
//   - DELETED is failed. A download the user or a duplicate check removed did
//     not deliver what it was grabbed for, and failed is the state that lets
//     the grab be retried.
func mapHistoryState(s string) core.DownloadState {
	category, _, _ := strings.Cut(strings.ToUpper(strings.TrimSpace(s)), "/")
	switch category {
	case historySuccess, historyWarning:
		return core.DownloadCompleted
	case historyFailure, historyDeleted:
		return core.DownloadFailed
	default:
		return core.DownloadQueued
	}
}

// size combines one of NZBGet's split 64-bit byte counts.
func size(hi, lo uint32) int64 {
	return int64(hi)<<32 | int64(lo)
}

// id spells an NZBID the way core.DownloadID does.
func id(nzbID int64) core.DownloadID {
	return core.DownloadID(strconv.FormatInt(nzbID, 10))
}

// parseID reads a core.DownloadID back into an NZBID.
func parseID(v core.DownloadID) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(string(v)), 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// groupStatus converts one queued download into the snapshot the queue and the
// import watcher read.
//
// rate is the server-wide byte rate: NZBGet transfers one download at a time
// and reports no per-group speed, so the rate belongs to whichever group is
// actually downloading and is zero for the rest.
//
// SavePath is NZBGet's FinalDir, falling back to DestDir — an absolute path on
// NZBGet's machine, which is the documented exception to the root-relative
// rule for external clients (docs/download-clients.md).
func groupStatus(g Group, rate int64) core.DownloadStatus {
	total := size(g.FileSizeHi, g.FileSizeLo)
	left := size(g.RemainingSizeHi, g.RemainingSizeLo)
	done := total - left
	if done < 0 {
		done = 0
	}

	s := core.DownloadStatus{
		ID:        id(g.NZBID),
		State:     mapGroupState(g.Status),
		Name:      g.NZBName,
		BytesDone: done,
		Size:      total,
		// NZBGet publishes no estimate; one is derived below when there is
		// enough to derive it from.
		ETASeconds: -1,
		SavePath:   g.FinalDir,
	}
	if s.SavePath == "" {
		s.SavePath = g.DestDir
	}
	if total > 0 {
		s.Progress = clients.Clamp01(float64(done) / float64(total))
	}
	if s.State == core.DownloadDownloading && rate > 0 {
		s.DownRate = rate
		if left > 0 {
			s.ETASeconds = left / rate
		}
	}
	return s
}

// historyStatus converts one history row.
func historyStatus(h HistoryItem) core.DownloadStatus {
	state := mapHistoryState(h.Status)
	total := size(h.FileSizeHi, h.FileSizeLo)

	done := size(h.DownloadedSizeHi, h.DownloadedSizeLo)
	switch {
	case done > total:
		// A success downloads more than the job's size: par2 blocks count too.
		done = total
	case done == 0 && state == core.DownloadCompleted:
		done = total
	}

	s := core.DownloadStatus{
		ID:        id(h.NZBID),
		State:     state,
		Name:      h.Name,
		BytesDone: done,
		Size:      total,
		// The transfer is over, so there is no rate and no estimate left.
		ETASeconds: -1,
		SavePath:   h.FinalDir,
	}
	if s.SavePath == "" {
		s.SavePath = h.DestDir
	}
	if total > 0 {
		s.Progress = clients.Clamp01(float64(done) / float64(total))
	}
	if state == core.DownloadFailed {
		s.Error = failureText(h)
	}
	return s
}

// failureText names a failure NZBGet reports only as a status code.
//
// The status is quoted rather than translated: "FAILURE/PAR" is the exact word
// the user will find in NZBGet's own history, and inventing a sentence for
// each of the dozen details would go stale by the next release.
func failureText(h HistoryItem) string {
	base := fmt.Sprintf("NZBGet reported %s for this download", h.Status)
	switch {
	case h.ParStatus == "FAILURE":
		return base + " (par repair failed)"
	case h.UnpackStatus == "FAILURE":
		return base + " (unpack failed)"
	case h.UnpackStatus == "PASSWORD":
		return base + " (the archive is password protected)"
	case h.UnpackStatus == "SPACE":
		return base + " (not enough disk space)"
	default:
		return base
	}
}
