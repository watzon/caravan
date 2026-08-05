package convert

import "testing"

func TestApplyProgressLine(t *testing.T) {
	var progress RunProgress

	tests := []struct {
		line       string
		recognized bool
		emit       bool
	}{
		{line: "out_time_us=30000000", recognized: true},
		{line: "speed=1.50x", recognized: true},
		{line: "progress=continue", recognized: true, emit: true},
		{line: "progress=end", recognized: true, emit: true},
		{line: "frame=900", recognized: true},
		{line: "conversion failed", recognized: false},
	}
	for _, tt := range tests {
		recognized, emit := applyProgressLine(tt.line, &progress)
		if recognized != tt.recognized || emit != tt.emit {
			t.Fatalf("applyProgressLine(%q) = (%t, %t), want (%t, %t)",
				tt.line, recognized, emit, tt.recognized, tt.emit)
		}
	}
	if progress.ProcessedSeconds != 30 {
		t.Fatalf("processed seconds = %v, want 30", progress.ProcessedSeconds)
	}
	if progress.Speed != 1.5 {
		t.Fatalf("speed = %v, want 1.5", progress.Speed)
	}
}

func TestLiveProgressEstimatesRemainingTime(t *testing.T) {
	progress := LiveProgress{
		Stage: ProgressStageConverting, DurationSeconds: 120,
		ProcessedSeconds: 30, Speed: 1.5,
	}
	if got := progress.Fraction(); got != 0.25 {
		t.Fatalf("Fraction = %v, want 0.25", got)
	}
	if got := progress.ETASeconds(); got != 60 {
		t.Fatalf("ETASeconds = %v, want 60", got)
	}

	progress.Stage = ProgressStageVerifying
	if got := progress.ETASeconds(); got != 0 {
		t.Fatalf("verifying ETASeconds = %v, want 0", got)
	}
	progress.Stage = ProgressStageConverting

	progress.ProcessedSeconds = 150
	if got := progress.Fraction(); got != 1 {
		t.Fatalf("overshoot Fraction = %v, want 1", got)
	}
	if got := progress.ETASeconds(); got != 0 {
		t.Fatalf("finished ETASeconds = %v, want 0", got)
	}

	progress = LiveProgress{ProcessedSeconds: 30, Speed: 1.5}
	if progress.Fraction() != 0 || progress.ETASeconds() != 0 {
		t.Fatalf("unknown duration produced an estimate: %+v", progress)
	}
}
