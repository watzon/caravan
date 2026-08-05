package convert

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// fakeFFmpeg stands in for the two binaries. Every conversion decision and the
// verification that guards the library is exercised through it, so the suite
// runs identically on a machine with no ffmpeg installed — which is the
// configuration SPEC §8 says must keep working.
type fakeFFmpeg struct {
	// probes answers Probe by absolute path; anything unlisted is an error,
	// which is what an unreadable file looks like.
	probes map[string]Probe
	// runs records every command line, in order.
	runs [][]string
	// onRun writes the output file. The default copies the input, which is
	// what a successful stream copy looks like from the filesystem's side.
	onRun func(t *fakeFFmpeg, args []string) error
	// runErr fails the ffmpeg invocation outright.
	runErr error
	// progressUpdates are reported before the output file is written.
	progressUpdates []RunProgress
}

func (f *fakeFFmpeg) Probe(_ context.Context, path string) (Probe, error) {
	p, ok := f.probes[path]
	if !ok {
		return Probe{}, fmt.Errorf("fake ffprobe: nothing known about %s", path)
	}
	return p, nil
}

func (f *fakeFFmpeg) Run(_ context.Context, report func(RunProgress), args ...string) error {
	f.runs = append(f.runs, args)
	for _, update := range f.progressUpdates {
		if report != nil {
			report(update)
		}
	}
	if f.runErr != nil {
		return f.runErr
	}
	if f.onRun != nil {
		return f.onRun(f, args)
	}
	return writeOutput(args, "converted bytes")
}

// writeOutput materialises the file ffmpeg would have written. The output is
// the last argument and the input the one after -i, exactly as Args builds it.
func writeOutput(args []string, contents string) error {
	return os.WriteFile(args[len(args)-1], []byte(contents), 0o644)
}

func outputPath(args []string) string { return args[len(args)-1] }

// newTestService wires a service over a real sqlite database and a real
// temporary storage root, so path handling and row updates are exercised for
// real; only ffmpeg is faked.
func newTestService(t *testing.T, tools FFmpeg) (*Service, *store.Store, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "caravan.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	root := t.TempDir()
	svc := New(st, func(context.Context) (string, error) { return root, nil }, tools, nil)
	return svc, st, root
}

// seedFile writes a library file on disk and its media_files row.
func seedFile(t *testing.T, st *store.Store, root, rel string, tags core.MediaFile) *core.MediaFile {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(abs, []byte("original bytes"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	f := tags
	f.Path = rel
	f.Size = int64(len("original bytes"))
	if err := st.UpsertMediaFile(context.Background(), &f); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}
	return &f
}

func queue(t *testing.T, st *store.Store, fileID int64, sourcePath string) *core.Conversion {
	t.Helper()
	c := &core.Conversion{MediaFileID: fileID, SourcePath: sourcePath, Status: core.ConversionQueued}
	if err := st.CreateConversion(context.Background(), c); err != nil {
		t.Fatalf("CreateConversion: %v", err)
	}
	return c
}

func handle(t *testing.T, svc *Service, conv *core.Conversion) error {
	t.Helper()
	payload, err := json.Marshal(Payload{ConversionID: conv.ID})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return svc.Handle(context.Background(), nil, payload)
}

// TestRemuxReplacesTheLibraryFile is the acceptance criterion for PLAN phase 4
// task 4: remuxing an incompatible file produces a TV-safe file and the
// library record updates to it.
func TestRemuxReplacesTheLibraryFile(t *testing.T) {
	tools := &fakeFFmpeg{probes: map[string]Probe{}}
	svc, st, root := newTestService(t, tools)
	ctx := context.Background()

	const rel = "library/Movies/Big Buck Bunny (2008)/Big Buck Bunny (2008).mkv"
	file := seedFile(t, st, root, rel, core.MediaFile{
		MovieID: 7, Quality: core.Quality1080p, Codec: "x264", Audio: "AAC",
	})
	sourceAbs := filepath.Join(root, filepath.FromSlash(rel))
	// Streams the safe profile accepts, in a container it does not.
	tools.probes[sourceAbs] = Probe{Duration: 600, VideoCodec: "h264", BitDepth: 8, Width: 1920, Height: 1080, AudioCodecs: []string{"aac"}}
	tools.onRun = func(f *fakeFFmpeg, args []string) error {
		// The remuxed file probes the same, because a stream copy is a copy.
		f.probes[outputPath(args)] = f.probes[sourceAbs]
		return writeOutput(args, "remuxed bytes")
	}

	conv := queue(t, st, file.ID, file.Path)
	if err := handle(t, svc, conv); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	// The strategy was the cheap one, and it ran exactly once.
	if len(tools.runs) != 1 {
		t.Fatalf("ffmpeg ran %d times, want 1", len(tools.runs))
	}
	if joined := strings.Join(tools.runs[0], " "); !strings.Contains(joined, "-c copy") {
		t.Fatalf("remux did not stream-copy: %v", tools.runs[0])
	}

	const wantRel = "library/Movies/Big Buck Bunny (2008)/Big Buck Bunny (2008).mp4"
	updated, err := st.GetMediaFile(ctx, file.ID)
	if err != nil {
		t.Fatalf("GetMediaFile: %v", err)
	}
	if updated.Path != wantRel {
		t.Fatalf("library path = %q, want %q", updated.Path, wantRel)
	}
	if updated.Size != int64(len("remuxed bytes")) {
		t.Fatalf("library size = %d, want the converted file's size", updated.Size)
	}
	// A stream copy changes nothing about the streams, so the tags must not
	// be rewritten into a lie.
	if updated.Codec != "x264" || updated.Audio != "AAC" {
		t.Fatalf("remux rewrote stream tags: codec %q audio %q", updated.Codec, updated.Audio)
	}

	// The new file is on disk and the original is gone.
	if got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(wantRel))); err != nil || string(got) != "remuxed bytes" {
		t.Fatalf("converted file on disk = %q, %v", got, err)
	}
	if _, err := os.Stat(sourceAbs); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the original is still on disk: %v", err)
	}

	done, err := st.GetConversion(ctx, conv.ID)
	if err != nil {
		t.Fatalf("GetConversion: %v", err)
	}
	if done.Status != core.ConversionDone || done.Strategy != core.ConvertStrategyRemux {
		t.Fatalf("conversion = %+v, want done/remux", done)
	}
	if done.OutputPath != wantRel {
		t.Fatalf("output path = %q, want %q", done.OutputPath, wantRel)
	}
	// SPEC §1.2 pillar 3: nothing this package stores may be absolute.
	if filepath.IsAbs(done.OutputPath) || strings.Contains(done.OutputPath, root) {
		t.Fatalf("stored an absolute path: %q", done.OutputPath)
	}
}

func TestConversionReportsLiveProgress(t *testing.T) {
	tools := &fakeFFmpeg{
		probes:          map[string]Probe{},
		progressUpdates: []RunProgress{{ProcessedSeconds: 30, Speed: 2}},
	}
	svc, st, root := newTestService(t, tools)
	ctx := context.Background()

	const rel = "library/Movies/Progress (2026)/Progress (2026).mkv"
	file := seedFile(t, st, root, rel, core.MediaFile{
		MovieID: 8, Quality: core.Quality1080p, Codec: "x264", Audio: "AAC",
	})
	sourceAbs := filepath.Join(root, filepath.FromSlash(rel))
	tools.probes[sourceAbs] = Probe{
		Duration: 120, VideoCodec: "h264", BitDepth: 8,
		Width: 1920, Height: 1080, AudioCodecs: []string{"aac"},
	}
	conv := queue(t, st, file.ID, file.Path)
	tools.onRun = func(f *fakeFFmpeg, args []string) error {
		live, ok := svc.Progress(conv.ID)
		if !ok {
			t.Fatal("conversion has no live progress while ffmpeg is running")
		}
		if live.Stage != ProgressStageConverting || live.StartedAt.IsZero() {
			t.Fatalf("live progress = %+v, want converting with a start time", live)
		}
		if live.Fraction() != 0.25 || live.ETASeconds() != 45 {
			t.Fatalf("live progress = %+v, want 25%% with 45s remaining", live)
		}
		running, err := st.GetConversion(ctx, conv.ID)
		if err != nil {
			t.Fatalf("GetConversion: %v", err)
		}
		if running.Status != core.ConversionRunning ||
			running.Strategy != core.ConvertStrategyRemux {
			t.Fatalf("stored conversion = %+v, want running remux", running)
		}
		f.probes[outputPath(args)] = f.probes[sourceAbs]
		return writeOutput(args, "remuxed bytes")
	}

	if err := handle(t, svc, conv); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if _, ok := svc.Progress(conv.ID); ok {
		t.Fatal("finished conversion kept stale live progress")
	}
}

func TestTranscodeRewritesStreamTags(t *testing.T) {
	tools := &fakeFFmpeg{probes: map[string]Probe{}}
	svc, st, root := newTestService(t, tools)

	const rel = "library/Movies/Sintel (2010)/Sintel (2010).mkv"
	file := seedFile(t, st, root, rel, core.MediaFile{
		MovieID: 3, Quality: core.Quality2160p, Codec: "x265", Audio: "DTS",
	})
	sourceAbs := filepath.Join(root, filepath.FromSlash(rel))
	tools.probes[sourceAbs] = Probe{Duration: 900, VideoCodec: "hevc", BitDepth: 10, Width: 3840, Height: 2160, AudioCodecs: []string{"dts"}}
	if err := st.SetSettings(t.Context(), map[string]string{
		store.SettingConvertVideoPreset:      "slow",
		store.SettingConvertVideoCRF:         "18",
		store.SettingConvertAudioBitrateKbps: "256",
	}); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}
	tools.onRun = func(f *fakeFFmpeg, args []string) error {
		f.probes[outputPath(args)] = Probe{Duration: 900, VideoCodec: "h264", BitDepth: 8, Width: 1920, Height: 1080, AudioCodecs: []string{"aac"}}
		return writeOutput(args, "transcoded bytes")
	}

	conv := queue(t, st, file.ID, file.Path)
	if err := handle(t, svc, conv); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	joined := strings.Join(tools.runs[0], " ")
	for _, want := range []string{
		"-c:v libx264 -preset slow -crf 18",
		"-c:a aac -b:a 256k",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("ffmpeg args %q do not contain %q", joined, want)
		}
	}
	updated, err := st.GetMediaFile(context.Background(), file.ID)
	if err != nil {
		t.Fatalf("GetMediaFile: %v", err)
	}
	// A re-encode does change the streams, so the tags must follow.
	if updated.Codec != "h264" || updated.Audio != "AAC" {
		t.Fatalf("codec %q audio %q, want h264/AAC", updated.Codec, updated.Audio)
	}
	// Including the resolution. A row still claiming 2160p keeps failing the
	// profile check on a file that is now 1080p, so the library would badge it
	// NEEDS CONVERT and offer the button forever.
	if updated.Quality != core.Quality1080p {
		t.Fatalf("quality = %q, want %q after a downscaling transcode", updated.Quality, core.Quality1080p)
	}
}

// os.Rename replaces its destination without a word. A movie directory that
// holds both an .mkv and an .mp4 is ordinary (internal/dlna serves it), and
// converting the .mkv must not silently destroy the .mp4 beside it — nor delete
// the row and the episode links behind it.
func TestConversionRefusesToOverwriteAnotherLibraryFile(t *testing.T) {
	tools := &fakeFFmpeg{probes: map[string]Probe{}}
	svc, st, root := newTestService(t, tools)
	ctx := context.Background()

	const (
		sourceRel = "library/Movies/X (2020)/X (2020).mkv"
		victimRel = "library/Movies/X (2020)/X (2020).mp4"
	)
	victim := seedFile(t, st, root, victimRel, core.MediaFile{MovieID: 8})
	file := seedFile(t, st, root, sourceRel, core.MediaFile{MovieID: 8})
	sourceAbs := filepath.Join(root, filepath.FromSlash(sourceRel))
	victimAbs := filepath.Join(root, filepath.FromSlash(victimRel))
	// Streams the safe profile accepts in a container it does not: a remux,
	// whose target path is exactly where the victim already lives.
	tools.probes[sourceAbs] = Probe{Duration: 600, VideoCodec: "h264", BitDepth: 8, Width: 1920, Height: 1080, AudioCodecs: []string{"aac"}}
	tools.onRun = func(f *fakeFFmpeg, args []string) error {
		f.probes[outputPath(args)] = f.probes[sourceAbs]
		return writeOutput(args, "remuxed bytes")
	}

	conv := queue(t, st, file.ID, file.Path)
	if err := handle(t, svc, conv); err == nil {
		t.Fatal("a conversion that would overwrite another library file must fail")
	}

	if got, err := os.ReadFile(victimAbs); err != nil || string(got) != "original bytes" {
		t.Fatalf("the file at the target path = %q, %v; want it untouched", got, err)
	}
	if _, err := st.GetMediaFile(ctx, victim.ID); err != nil {
		t.Fatalf("the other file's row was destroyed: %v", err)
	}
	// The source is intact and still where the library says it is.
	if _, err := os.Stat(sourceAbs); err != nil {
		t.Fatalf("the source was disturbed: %v", err)
	}
	unchanged, err := st.GetMediaFile(ctx, file.ID)
	if err != nil {
		t.Fatalf("GetMediaFile: %v", err)
	}
	if unchanged.Path != sourceRel {
		t.Fatalf("library moved to %q despite a refused conversion", unchanged.Path)
	}
	if leftovers := tempFiles(t, filepath.Dir(sourceAbs)); len(leftovers) > 0 {
		t.Fatalf("temporary files left behind: %v", leftovers)
	}
}

// A conversion cancelled while the worker was claiming it must not go on to
// rewrite the file: the claim is conditional, so the loser of the race finds
// out rather than writing over the winner.
func TestCancelledConversionIsNotClaimed(t *testing.T) {
	tools := &fakeFFmpeg{probes: map[string]Probe{}}
	svc, st, root := newTestService(t, tools)
	ctx := context.Background()

	const rel = "library/Movies/W (2019)/W (2019).mkv"
	file := seedFile(t, st, root, rel, core.MediaFile{MovieID: 11})
	sourceAbs := filepath.Join(root, filepath.FromSlash(rel))
	tools.probes[sourceAbs] = Probe{Duration: 600, VideoCodec: "h264", BitDepth: 8, Width: 1920, Height: 1080, AudioCodecs: []string{"aac"}}

	conv := queue(t, st, file.ID, file.Path)
	// The HTTP cancel lands. conv is the handler's stale read from before it.
	cancelled, err := st.TransitionConversion(ctx, conv.ID, core.ConversionCancelled, core.ConversionQueued)
	if err != nil {
		t.Fatalf("TransitionConversion: %v", err)
	}
	if !cancelled {
		t.Fatal("a queued conversion must be cancellable")
	}

	if err := handle(t, svc, conv); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(tools.runs) != 0 {
		t.Fatalf("ffmpeg ran on a cancelled conversion: %v", tools.runs)
	}
	got, err := st.GetConversion(ctx, conv.ID)
	if err != nil {
		t.Fatalf("GetConversion: %v", err)
	}
	if got.Status != core.ConversionCancelled {
		t.Fatalf("status = %q, want %q: the handler wrote over the cancel", got.Status, core.ConversionCancelled)
	}
	if _, err := os.Stat(sourceAbs); err != nil {
		t.Fatalf("the file was replaced despite the cancel: %v", err)
	}
}

func TestCompatibleFileIsNotTouched(t *testing.T) {
	tools := &fakeFFmpeg{probes: map[string]Probe{}}
	svc, st, root := newTestService(t, tools)

	const rel = "library/Movies/Tears of Steel (2012)/Tears of Steel (2012).mp4"
	file := seedFile(t, st, root, rel, core.MediaFile{MovieID: 1})
	sourceAbs := filepath.Join(root, filepath.FromSlash(rel))
	tools.probes[sourceAbs] = Probe{Duration: 700, VideoCodec: "h264", BitDepth: 8, Width: 1920, Height: 1080, AudioCodecs: []string{"aac"}}

	conv := queue(t, st, file.ID, file.Path)
	if err := handle(t, svc, conv); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if len(tools.runs) != 0 {
		t.Fatalf("ffmpeg ran on a compatible file: %v", tools.runs)
	}
	done, err := st.GetConversion(context.Background(), conv.ID)
	if err != nil {
		t.Fatalf("GetConversion: %v", err)
	}
	if done.Status != core.ConversionDone || done.Strategy != core.ConvertStrategyNone {
		t.Fatalf("conversion = %+v, want done/none", done)
	}
	if _, err := os.Stat(sourceAbs); err != nil {
		t.Fatalf("the file was disturbed: %v", err)
	}
}

// TestTruncatedOutputKeepsTheOriginal is the failure that would cost media: an
// ffmpeg that exits 0 having written a short file.
func TestTruncatedOutputKeepsTheOriginal(t *testing.T) {
	tools := &fakeFFmpeg{probes: map[string]Probe{}}
	svc, st, root := newTestService(t, tools)

	const rel = "library/Movies/Elephants Dream (2006)/Elephants Dream (2006).mkv"
	file := seedFile(t, st, root, rel, core.MediaFile{MovieID: 9})
	sourceAbs := filepath.Join(root, filepath.FromSlash(rel))
	tools.probes[sourceAbs] = Probe{Duration: 654, VideoCodec: "h264", BitDepth: 8, Width: 1920, Height: 1080, AudioCodecs: []string{"aac"}}
	tools.onRun = func(f *fakeFFmpeg, args []string) error {
		f.probes[outputPath(args)] = Probe{Duration: 12, VideoCodec: "h264"}
		return writeOutput(args, "truncated")
	}

	conv := queue(t, st, file.ID, file.Path)
	err := handle(t, svc, conv)
	if err == nil {
		t.Fatal("a truncated output must fail the job so the queue retries it")
	}

	if _, statErr := os.Stat(sourceAbs); statErr != nil {
		t.Fatalf("the original was removed despite a failed conversion: %v", statErr)
	}
	unchanged, getErr := st.GetMediaFile(context.Background(), file.ID)
	if getErr != nil {
		t.Fatalf("GetMediaFile: %v", getErr)
	}
	if unchanged.Path != rel {
		t.Fatalf("library moved to %q despite a failed conversion", unchanged.Path)
	}
	failed, getErr := st.GetConversion(context.Background(), conv.ID)
	if getErr != nil {
		t.Fatalf("GetConversion: %v", getErr)
	}
	if failed.Status != core.ConversionFailed || failed.Error == "" {
		t.Fatalf("conversion = %+v, want failed with a reason", failed)
	}
	// Nothing half-written may survive into the library directory.
	if leftovers := tempFiles(t, filepath.Dir(sourceAbs)); len(leftovers) > 0 {
		t.Fatalf("temporary files left behind: %v", leftovers)
	}
}

// TestRetryAfterACrashConverges covers the crash-mid-conversion case: the row
// is still "running" and a stale temp file is on disk, and the redelivered job
// must clean up and finish rather than trip over itself.
func TestRetryAfterACrashConverges(t *testing.T) {
	tools := &fakeFFmpeg{probes: map[string]Probe{}}
	svc, st, root := newTestService(t, tools)
	ctx := context.Background()

	const rel = "library/Movies/Cosmos Laundromat (2015)/Cosmos Laundromat (2015).mkv"
	file := seedFile(t, st, root, rel, core.MediaFile{MovieID: 4})
	sourceAbs := filepath.Join(root, filepath.FromSlash(rel))
	tools.probes[sourceAbs] = Probe{Duration: 720, VideoCodec: "h264", BitDepth: 8, Width: 1920, Height: 1080, AudioCodecs: []string{"aac"}}
	tools.onRun = func(f *fakeFFmpeg, args []string) error {
		f.probes[outputPath(args)] = f.probes[sourceAbs]
		return writeOutput(args, "remuxed bytes")
	}

	conv := queue(t, st, file.ID, file.Path)
	// Exactly the state a kill -9 mid-encode leaves behind.
	conv.Status = core.ConversionRunning
	if err := st.UpdateConversion(ctx, conv); err != nil {
		t.Fatalf("UpdateConversion: %v", err)
	}
	stale := filepath.Join(filepath.Dir(sourceAbs), fmt.Sprintf("%s%d.mp4", tempPrefix, conv.ID))
	if err := os.WriteFile(stale, []byte("half an encode"), 0o644); err != nil {
		t.Fatalf("write stale temp: %v", err)
	}

	if err := handle(t, svc, conv); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	updated, err := st.GetMediaFile(ctx, file.ID)
	if err != nil {
		t.Fatalf("GetMediaFile: %v", err)
	}
	if !strings.HasSuffix(updated.Path, ".mp4") {
		t.Fatalf("library path = %q, want the converted file", updated.Path)
	}
	if leftovers := tempFiles(t, filepath.Dir(sourceAbs)); len(leftovers) > 0 {
		t.Fatalf("temporary files left behind: %v", leftovers)
	}
}

func TestFinishedConversionIsANoOpOnRedelivery(t *testing.T) {
	for _, status := range []string{core.ConversionDone, core.ConversionCancelled} {
		t.Run(status, func(t *testing.T) {
			tools := &fakeFFmpeg{probes: map[string]Probe{}}
			svc, st, root := newTestService(t, tools)

			file := seedFile(t, st, root, "library/Movies/X (2020)/X (2020).mkv", core.MediaFile{MovieID: 2})
			conv := queue(t, st, file.ID, file.Path)
			conv.Status = status
			if err := st.UpdateConversion(context.Background(), conv); err != nil {
				t.Fatalf("UpdateConversion: %v", err)
			}

			if err := handle(t, svc, conv); err != nil {
				t.Fatalf("Handle: %v", err)
			}
			if len(tools.runs) != 0 {
				t.Fatalf("ffmpeg ran for a %s conversion: %v", status, tools.runs)
			}
		})
	}
}

func TestMissingLibraryFileIsTerminal(t *testing.T) {
	tools := &fakeFFmpeg{probes: map[string]Probe{}}
	svc, st, _ := newTestService(t, tools)

	conv := queue(t, st, 4242, "library/Movies/Gone (1999)/Gone (1999).mkv")
	// Nil, not an error: retrying cannot bring a deleted row back, so the job
	// must complete rather than burn its five attempts.
	if err := handle(t, svc, conv); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	failed, err := st.GetConversion(context.Background(), conv.ID)
	if err != nil {
		t.Fatalf("GetConversion: %v", err)
	}
	if failed.Status != core.ConversionFailed {
		t.Fatalf("status = %q, want failed", failed.Status)
	}
}

func TestWithoutFFmpegNothingRuns(t *testing.T) {
	svc, st, root := newTestService(t, nil)
	if svc.Available() {
		t.Fatal("a service with no tools must not report itself available")
	}

	file := seedFile(t, st, root, "library/Movies/Y (2021)/Y (2021).mkv", core.MediaFile{MovieID: 5})
	conv := queue(t, st, file.ID, file.Path)
	if err := handle(t, svc, conv); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	failed, err := st.GetConversion(context.Background(), conv.ID)
	if err != nil {
		t.Fatalf("GetConversion: %v", err)
	}
	if failed.Status != core.ConversionFailed || !strings.Contains(failed.Error, "ffmpeg") {
		t.Fatalf("conversion = %+v, want failed naming ffmpeg", failed)
	}
}

func TestFFmpegFailureIsRetryable(t *testing.T) {
	tools := &fakeFFmpeg{probes: map[string]Probe{}, runErr: errors.New("ffmpeg: Invalid data found")}
	svc, st, root := newTestService(t, tools)

	const rel = "library/Movies/Z (2022)/Z (2022).mkv"
	file := seedFile(t, st, root, rel, core.MediaFile{MovieID: 6})
	tools.probes[filepath.Join(root, filepath.FromSlash(rel))] = Probe{
		Duration: 100, VideoCodec: "h264", BitDepth: 8, Width: 1920, Height: 1080, AudioCodecs: []string{"aac"},
	}

	conv := queue(t, st, file.ID, file.Path)
	// An error back to the runner is what buys the job its backoff and retry.
	if err := handle(t, svc, conv); err == nil {
		t.Fatal("an ffmpeg failure must fail the job")
	}
	failed, err := st.GetConversion(context.Background(), conv.ID)
	if err != nil {
		t.Fatalf("GetConversion: %v", err)
	}
	if failed.Status != core.ConversionFailed || failed.Error == "" {
		t.Fatalf("conversion = %+v, want failed with a reason", failed)
	}
}

// tempFiles lists the in-progress markers left in dir.
func tempFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), tempPrefix) {
			out = append(out, e.Name())
		}
	}
	return out
}
