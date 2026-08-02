package convert

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// TestRealFFmpegRemuxAndTranscode is the only test that shells out. It is
// skipped when ffmpeg is absent, because SPEC §8 makes ffmpeg optional and a
// suite that needs it would be a suite that does not run in CI.
//
// What it buys over the faked tests: proof that the command lines in Args are
// accepted by a real ffmpeg and that the resulting file probes the way Decide
// assumed it would. The faked tests own the decisions; this one owns the
// vocabulary.
func TestRealFFmpegRemuxAndTranscode(t *testing.T) {
	tools := Detect()
	if tools == nil {
		t.Skip("ffmpeg and ffprobe are not on PATH")
	}
	ctx := context.Background()
	dir := t.TempDir()

	// A two-second HEVC 10-bit clip in MKV: incompatible with the safe
	// profile on the video stream, so the fallback has to fire.
	source := filepath.Join(dir, "source.mkv")
	generate(t, ctx, source, "libx265", "yuv420p10le")

	probe, err := tools.Probe(ctx, source)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if probe.VideoCodec != "hevc" || probe.BitDepth != 10 {
		t.Fatalf("probe = %+v, want 10-bit hevc", probe)
	}

	safe := core.ResolveTVProfile(core.TVProfileSafe)
	plan := Decide(safe, probe, "mkv")
	if plan.Strategy != core.ConvertStrategyTranscode {
		t.Fatalf("strategy = %q, want transcode", plan.Strategy)
	}

	out := filepath.Join(dir, "out.mp4")
	if err := tools.Run(ctx, Args(plan, source, out)...); err != nil {
		t.Fatalf("transcode: %v", err)
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatalf("stat output: %v", err)
	}
	outProbe, err := tools.Probe(ctx, out)
	if err != nil {
		t.Fatalf("probe output: %v", err)
	}
	if err := Verify(probe, outProbe, info.Size()); err != nil {
		t.Fatalf("the transcode did not verify: %v", err)
	}
	// The whole point: the result is what the profile accepts.
	if verdict := safe.Check(ProbeTags(outProbe, "mp4")); verdict.Verdict != core.TVCompatCompatible {
		t.Fatalf("transcoded file is %q for the safe profile: %v", verdict.Verdict, verdict.Reasons)
	}

	// Now the cheap path: H.264 8-bit + AAC in MKV needs only a container swap.
	remuxSource := filepath.Join(dir, "remux.mkv")
	generate(t, ctx, remuxSource, "libx264", "yuv420p")
	remuxProbe, err := tools.Probe(ctx, remuxSource)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	remuxPlan := Decide(safe, remuxProbe, "mkv")
	if remuxPlan.Strategy != core.ConvertStrategyRemux {
		t.Fatalf("strategy = %q, want remux (%v)", remuxPlan.Strategy, remuxPlan.Reasons)
	}

	remuxed := filepath.Join(dir, "remuxed.mp4")
	if err := tools.Run(ctx, Args(remuxPlan, remuxSource, remuxed)...); err != nil {
		t.Fatalf("remux: %v", err)
	}
	remuxInfo, err := os.Stat(remuxed)
	if err != nil {
		t.Fatalf("stat remuxed: %v", err)
	}
	remuxedProbe, err := tools.Probe(ctx, remuxed)
	if err != nil {
		t.Fatalf("probe remuxed: %v", err)
	}
	if err := Verify(remuxProbe, remuxedProbe, remuxInfo.Size()); err != nil {
		t.Fatalf("the remux did not verify: %v", err)
	}
	if verdict := safe.Check(ProbeTags(remuxedProbe, "mp4")); verdict.Verdict != core.TVCompatCompatible {
		t.Fatalf("remuxed file is %q for the safe profile: %v", verdict.Verdict, verdict.Reasons)
	}
}

// generate writes a short synthetic clip with the given video encoder.
func generate(t *testing.T, ctx context.Context, path, encoder, pixFmt string) {
	t.Helper()
	cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=320x240:rate=10",
		"-f", "lavfi", "-i", "sine=duration=2:frequency=440",
		"-c:v", encoder, "-pix_fmt", pixFmt, "-c:a", "aac", "-shortest", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("this ffmpeg cannot produce a %s fixture: %v\n%s", encoder, err, out)
	}
}
