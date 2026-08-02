// Package convert runs the optional convert-for-TV queue (SPEC §8): the
// ffmpeg-backed jobs that make a file the target set can actually decode.
//
// Two rules shape everything here.
//
// Remux first. A container swap is a stream copy — seconds, byte-identical
// video — so it is what runs whenever the streams themselves are fine. A full
// transcode is slow and lossy, so it is the explicit fallback and never the
// first attempt. Which one applies is not a second opinion: it is the
// needs-remux/incompatible split core.TVProfile.Check already draws.
//
// The original is the last thing to go. ffmpeg writes a temporary file beside
// the original (same directory, therefore same filesystem, therefore the
// rename is atomic-ish), the output is probed and measured against the source,
// and only then does the library row move and the old file get removed. A
// crash at any point leaves a stale temp file and a conversion still marked
// running, which the next attempt cleans up and redoes — the same
// at-least-once contract as every other job (SPEC §7).
package convert

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/parse"
	"github.com/watzon/caravan/internal/store"
)

// JobKind is the durable job kind the queue runs on.
const JobKind = "convert"

// EventCategory tags the activity-feed entries a conversion writes.
const EventCategory = "convert"

// Payload is the job payload: the conversion row holds everything else, so a
// redelivered job cannot disagree with the queue about what it is doing.
type Payload struct {
	ConversionID int64 `json:"conversion_id"`
}

// tempPrefix marks the in-progress output. It is derived from the conversion
// id so a crashed attempt's leftovers are found by the retry rather than
// accumulating (the dot keeps it out of the library scanner's way).
const tempPrefix = ".caravan-convert-"

// RootFunc resolves the storage root in force right now. It is a function
// rather than a captured string for the same reason the library adapter reads
// it per call: the root is editable from the settings screen at runtime.
type RootFunc func(ctx context.Context) (string, error)

// Service owns the conversion job handler.
type Service struct {
	st    *store.Store
	root  RootFunc
	tools FFmpeg
	log   *slog.Logger
}

// New builds the service. tools may be nil, which is what Detect returns when
// ffmpeg is not installed; the service then reports itself unavailable and
// refuses to queue work rather than failing jobs one by one.
func New(st *store.Store, root RootFunc, tools FFmpeg, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{st: st, root: root, tools: tools, log: log}
}

// Available reports whether ffmpeg and ffprobe were both found. The HTTP layer
// surfaces this in GET /system/status so the UI can hide the Convert
// affordance instead of offering a button that always fails.
func (s *Service) Available() bool { return s != nil && s.tools != nil }

// Handle runs one conversion. It matches automation.Handler; the store
// argument is ignored because the service holds its own handle.
//
// Idempotency: the conversion row is the state, not the job. A redelivered job
// for a conversion that already finished (or was cancelled) is a no-op, and a
// redelivered job for one that was mid-flight starts over from the source file,
// which is still there because nothing removes it before the output is verified.
//
// A transcode can outlive the job lease by hours, which is why this kind runs
// on a worker of its own (automation.WithDedicatedWorker): that worker is a
// single goroutine, so it cannot claim a second conversion while this one
// blocks, and its lease is long enough that the general worker's reclaim sweep
// does not hand a running conversion back to the pending pool. Two workers on
// this kind is the assumption that would need revisiting, not something this
// handler defends against on its own.
func (s *Service) Handle(ctx context.Context, _ *store.Store, payload json.RawMessage) error {
	var p Payload
	if err := json.Unmarshal(payload, &p); err != nil {
		// A payload nothing can decode will never decode. Failing it forever
		// would just fill the activity feed with the same error five times.
		s.log.Error("convert: undecodable job payload", "error", err)
		return nil
	}

	conv, err := s.st.GetConversion(ctx, p.ConversionID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if conv.Status == core.ConversionDone || conv.Status == core.ConversionCancelled {
		return nil
	}

	if !s.Available() {
		return s.abandon(ctx, conv, errors.New("ffmpeg is not installed"))
	}

	file, err := s.st.GetMediaFile(ctx, conv.MediaFileID)
	if errors.Is(err, store.ErrNotFound) {
		// The library forgot the file (deleted, or replaced by an upgrade).
		// Retrying cannot bring it back, so this is a terminal outcome rather
		// than a job failure.
		return s.abandon(ctx, conv, errors.New("the file is no longer in the library"))
	}
	if err != nil {
		return err
	}

	// Claim the row rather than overwrite it: a cancel that lands between the
	// read above and this write must win, and it can only win if this write
	// asks whether the row is still the one it read.
	claimed, err := s.st.TransitionConversion(ctx, conv.ID, core.ConversionRunning,
		core.ConversionQueued, core.ConversionRunning)
	if err != nil {
		return err
	}
	if !claimed {
		// Cancelled (or finished) under us. The job is done either way.
		return nil
	}
	conv.Status = core.ConversionRunning
	conv.Error = ""

	if err := s.run(ctx, conv, file); err != nil {
		return s.fail(ctx, conv, err)
	}
	return nil
}

// run does the work, leaving status bookkeeping to Handle.
func (s *Service) run(ctx context.Context, conv *core.Conversion, file *core.MediaFile) error {
	root, err := s.root(ctx)
	if err != nil {
		return err
	}
	if root == "" {
		return errors.New("no storage root is configured")
	}
	sourceAbs := abs(root, file.Path)

	profile := s.profile(ctx)
	conv.ProfileID = profile.ID

	sourceProbe, err := s.tools.Probe(ctx, sourceAbs)
	if err != nil {
		return err
	}

	plan := Decide(profile, sourceProbe, parse.Container(file.Path))
	conv.Strategy = plan.Strategy
	if plan.Strategy == core.ConvertStrategyNone {
		conv.Status = core.ConversionDone
		conv.OutputPath = file.Path
		if err := s.st.UpdateConversion(ctx, conv); err != nil {
			return err
		}
		return s.event(ctx, core.EventLevelInfo, file,
			fmt.Sprintf("Nothing to convert: %s already plays on the %s profile", path.Base(file.Path), profile.Name), "")
	}

	// Beside the original: same directory means same filesystem, which is what
	// makes the final rename a metadata operation rather than a second copy.
	tempAbs := filepath.Join(filepath.Dir(sourceAbs), fmt.Sprintf("%s%d.%s", tempPrefix, conv.ID, plan.Container))
	// A previous attempt that crashed mid-encode left this behind; ffmpeg's -y
	// would overwrite it anyway, but removing it first keeps a failed run from
	// leaving a half file that looks like a result.
	if err := os.Remove(tempAbs); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove stale temporary file: %w", err)
	}
	defer func() {
		if err := os.Remove(tempAbs); err != nil && !errors.Is(err, fs.ErrNotExist) {
			s.log.Warn("convert: leftover temporary file", "path", tempAbs, "error", err)
		}
	}()

	if err := s.tools.Run(ctx, Args(plan, sourceAbs, tempAbs)...); err != nil {
		return err
	}

	info, err := os.Stat(tempAbs)
	if err != nil {
		return fmt.Errorf("converted file is missing: %w", err)
	}
	outputProbe, err := s.tools.Probe(ctx, tempAbs)
	if err != nil {
		return err
	}
	if err := Verify(sourceProbe, outputProbe, info.Size()); err != nil {
		return err
	}

	targetRel := swapExt(file.Path, plan.Container)
	targetAbs := abs(root, targetRel)
	// A same-container transcode writes over its own source. Anything else must
	// land on empty ground: os.Rename replaces its destination silently, so a
	// second file that happens to sit at the target path — a movie with both an
	// .mkv and an .mp4, which the library and the DLNA browse both support —
	// would be destroyed, along with the row and the episode links behind it.
	if targetRel != file.Path {
		switch _, err := os.Stat(targetAbs); {
		case err == nil:
			return fmt.Errorf("another file already occupies %s", targetRel)
		case !errors.Is(err, fs.ErrNotExist):
			return fmt.Errorf("check the conversion target: %w", err)
		}
	}
	if err := os.Rename(tempAbs, targetAbs); err != nil {
		return fmt.Errorf("install converted file: %w", err)
	}

	// A row left at the target path describes a file that is no longer there
	// (the stat above proved the path was free); leaving it would collide with
	// the unique path index.
	if targetRel != file.Path {
		if err := s.st.DeleteMediaFileByPath(ctx, targetRel); err != nil {
			return err
		}
	}
	quality, codec, audio := convertedTags(plan, file, outputProbe)
	if err := s.st.UpdateMediaFileConverted(ctx, file.ID, targetRel, info.Size(), quality, codec, audio); err != nil {
		return err
	}
	// Only now, with the library pointing at the new file, is the original
	// safe to remove.
	if targetRel != file.Path {
		if err := os.Remove(abs(root, file.Path)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			s.log.Warn("convert: could not remove the original", "path", file.Path, "error", err)
		}
	}

	conv.Status = core.ConversionDone
	conv.OutputPath = targetRel
	conv.Error = ""
	if err := s.st.UpdateConversion(ctx, conv); err != nil {
		return err
	}

	verb := "Remuxed"
	if plan.Strategy == core.ConvertStrategyTranscode {
		verb = "Transcoded"
	}
	return s.event(ctx, core.EventLevelInfo, file,
		fmt.Sprintf("%s %s for the %s profile", verb, path.Base(file.Path), profile.Name),
		strings.Join(append([]string{"now at " + targetRel}, plan.Reasons...), "; "))
}

// convertedTags is the quality, codec and audio the library row must carry now
// that the file has been rewritten.
//
// Only the streams that were actually re-encoded are re-tagged, and they are
// re-tagged from the output's own probe rather than from the plan's intent: the
// file on disk is the truth, and a row that still claims the source's tags is a
// row the compatibility badge keeps condemning. A downscale is the case that
// makes this load-bearing — a 2160p row on a 1080p file would offer the Convert
// button forever, on a file that is already compatible.
func convertedTags(plan Plan, file *core.MediaFile, out Probe) (quality, codec, audio string) {
	quality, codec, audio = file.Quality, file.Codec, file.Audio
	if plan.Strategy != core.ConvertStrategyTranscode {
		return quality, codec, audio
	}
	if !plan.VideoCopy {
		codec = videoTag(out.VideoCodec)
		if q := qualityOf(out.Width, out.Height); q != "" {
			quality = q
		}
	}
	if !plan.AudioCopy {
		if tag := audioTag(out.AudioCodec()); tag != "" {
			audio = tag
		}
	}
	return quality, codec, audio
}

// fail records a failed attempt and hands the error back to the job runner, so
// the queue's own backoff decides when to try again.
func (s *Service) fail(ctx context.Context, conv *core.Conversion, cause error) error {
	conv.Status = core.ConversionFailed
	conv.Error = cause.Error()
	if err := s.st.UpdateConversion(ctx, conv); err != nil {
		s.log.Error("convert: record failure", "conversion", conv.ID, "error", err)
	}
	if err := s.event(ctx, core.EventLevelWarn, nil,
		fmt.Sprintf("Conversion of %s failed", path.Base(conv.SourcePath)), cause.Error()); err != nil {
		s.log.Error("convert: record failure event", "conversion", conv.ID, "error", err)
	}
	return cause
}

// abandon records a failure retrying cannot fix and completes the job. The
// conversion stays visible as failed; the queue simply stops working on it.
func (s *Service) abandon(ctx context.Context, conv *core.Conversion, cause error) error {
	_ = s.fail(ctx, conv, cause)
	return nil
}

// profile resolves the active TV profile, falling back to the safe default.
// A conversion aimed at the wrong profile is recoverable; one that refuses to
// run because a preference row is missing is just broken.
func (s *Service) profile(ctx context.Context) core.TVProfile {
	id, err := s.st.GetSetting(ctx, store.SettingTVProfile)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.log.Error("convert: read tv profile", "error", err)
	}
	return core.ResolveTVProfile(id)
}

func (s *Service) event(ctx context.Context, level string, file *core.MediaFile, message, detail string) error {
	ev := &core.Event{
		Level:    level,
		Category: EventCategory,
		Message:  message,
		Detail:   detail,
	}
	if file != nil {
		ev.MovieID = file.MovieID
	}
	return s.st.InsertEvent(ctx, ev)
}

// abs resolves a storage-root-relative path. Stored paths use forward slashes
// (SPEC §1.2 pillar 3), so they are converted before touching the filesystem.
func abs(root, rel string) string {
	return filepath.Join(root, filepath.FromSlash(rel))
}

// swapExt replaces a relative path's extension with container. It keeps the
// forward slashes the stored form uses.
func swapExt(rel, container string) string {
	ext := path.Ext(rel)
	return strings.TrimSuffix(rel, ext) + "." + container
}
