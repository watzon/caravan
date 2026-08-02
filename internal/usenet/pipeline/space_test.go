package pipeline

import (
	"errors"
	"testing"
)

func TestCheckSpace(t *testing.T) {
	const dir = "/downloads/rel"
	cases := []struct {
		name string
		need int64
		free func(string) (int64, error)
		want error
	}{
		{
			name: "room to spare",
			need: 1_000,
			free: func(string) (int64, error) { return 2_000, nil },
		},
		{
			name: "exactly enough",
			need: 1_000,
			free: func(string) (int64, error) { return 1_000, nil },
		},
		{
			name: "one byte short",
			need: 1_000,
			free: func(string) (int64, error) { return 999, nil },
			want: ErrInsufficientSpace,
		},
		{
			// A resumed download that has everything already asks for
			// nothing, and a check for nothing is not a check.
			name: "nothing wanted",
			need: 0,
			free: func(string) (int64, error) { return 0, nil },
		},
		{
			// A check that cannot run must not block a download that would
			// have worked.
			name: "unmeasurable filesystem",
			need: 1 << 40,
			free: func(string) (int64, error) { return 0, errSpaceUnsupported },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSpace(dir, tc.need, tc.free)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("checkSpace = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("checkSpace = %v, want %v", err, tc.want)
			}
			var se *SpaceError
			if !errors.As(err, &se) || se.Path != dir || se.Need != tc.need {
				t.Fatalf("SpaceError = %+v", se)
			}
		})
	}
}

// The platform implementation has to answer for a real directory, or the
// preflight silently never runs in production.
func TestFreeSpaceMeasuresARealDirectory(t *testing.T) {
	free, err := FreeSpace(t.TempDir())
	if errors.Is(err, errSpaceUnsupported) {
		t.Skip("free space is not measurable on this platform")
	}
	if err != nil {
		t.Fatalf("FreeSpace: %v", err)
	}
	if free <= 0 {
		t.Fatalf("FreeSpace = %d, want a positive number of bytes", free)
	}
}
