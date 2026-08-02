package convert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// FFmpeg is the pair of binaries a conversion drives. It is an interface so
// the queue's decisions — which strategy, and whether the output is good
// enough to replace the original — are testable without either binary
// installed, which is the difference between a test suite that runs in CI and
// one that gets skipped there (SPEC §8: ffmpeg is optional).
type FFmpeg interface {
	// Probe reads what the container claims about itself.
	Probe(ctx context.Context, path string) (Probe, error)
	// Run executes one ffmpeg command line, built by Args.
	Run(ctx context.Context, args ...string) error
}

// Detect finds ffmpeg and ffprobe on PATH, returning nil when either is
// missing.
//
// Both are required: without ffprobe there is no way to choose between a remux
// and a transcode, and guessing is exactly the thing that turns a two-second
// container swap into a two-hour re-encode. A nil result is the whole of the
// graceful degradation SPEC §8 asks for — the queue reports itself unavailable
// and the UI hides the affordance.
func Detect() FFmpeg {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		return nil
	}
	return &execTools{ffmpeg: ffmpeg, ffprobe: ffprobe}
}

// execTools drives the real binaries.
type execTools struct {
	ffmpeg  string
	ffprobe string
}

func (t *execTools) Run(ctx context.Context, args ...string) error {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, t.ffmpeg, args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, lastLines(stderr.String(), 3))
	}
	return nil
}

func (t *execTools) Probe(ctx context.Context, path string) (Probe, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, t.ffprobe,
		"-v", "error", "-print_format", "json", "-show_format", "-show_streams", path)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return Probe{}, fmt.Errorf("ffprobe: %w: %s", err, lastLines(stderr.String(), 3))
	}
	return ParseProbe(stdout.Bytes())
}

// probeJSON is the slice of `ffprobe -print_format json` this package reads.
type probeJSON struct {
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
	Streams []struct {
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
		PixFmt    string `json:"pix_fmt"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		Duration  string `json:"duration"`
	} `json:"streams"`
}

// ParseProbe reads ffprobe's JSON report. Exported so the parsing is testable
// against captured real output without running ffprobe.
//
// Only the first *video* stream is read: the profiles describe what a set
// decodes for the stream it will actually play. Every audio stream is kept,
// because a stream copy has to mux all of the ones it maps — the first one
// still decides the verdict (see Probe.AudioCodec), the rest decide what is
// safe to copy.
func ParseProbe(data []byte) (Probe, error) {
	var doc probeJSON
	if err := json.Unmarshal(data, &doc); err != nil {
		return Probe{}, fmt.Errorf("ffprobe: unreadable report: %w", err)
	}

	p := Probe{Duration: parseSeconds(doc.Format.Duration)}
	for _, s := range doc.Streams {
		switch s.CodecType {
		case "video":
			if p.VideoCodec != "" {
				continue
			}
			p.VideoCodec = s.CodecName
			p.BitDepth = bitDepthFromPixFmt(s.PixFmt)
			p.Width, p.Height = s.Width, s.Height
			if p.Duration == 0 {
				p.Duration = parseSeconds(s.Duration)
			}
		case "audio":
			p.AudioCodecs = append(p.AudioCodecs, s.CodecName)
		}
	}
	return p, nil
}

// parseSeconds reads ffprobe's duration, which is a decimal string and is
// "N/A" for containers that do not carry one.
func parseSeconds(raw string) float64 {
	seconds, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || seconds < 0 {
		return 0
	}
	return seconds
}

// lastLines keeps the tail of ffmpeg's stderr for the error message. The whole
// of it is a progress log; the last few lines are the complaint.
func lastLines(s string, n int) string {
	lines := []string{}
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "; ")
}
