package nzb_test

import (
	"testing"

	"github.com/watzon/caravan/internal/usenet/nzb"
)

func TestFilename(t *testing.T) {
	tests := []struct {
		name    string
		subject string
		want    string
	}{
		{
			name:    "quoted name wins",
			subject: `Some.Release-GRP [01/12] - "some.release-grp.part01.rar" yEnc (1/50)`,
			want:    "some.release-grp.part01.rar",
		},
		{
			name:    "quoted name with spaces",
			subject: `[1/3] - "My Movie (2019) 1080p.mkv" yEnc (1/200)`,
			want:    "My Movie (2019) 1080p.mkv",
		},
		{
			name:    "unquoted name after a leading counter",
			subject: `[1/9] - some.release.name.mkv yEnc (1/40)`,
			want:    "some.release.name.mkv",
		},
		{
			name:    "unquoted name with a description in front",
			subject: `Cool Release Pack (2020) [2/9] - cool.release.part2.rar yEnc (1/40)`,
			want:    "cool.release.part2.rar",
		},
		{
			name:    "parenthesised leading counter",
			subject: `(01/11) - release.vol000+01.par2 yEnc (1/3)`,
			want:    "release.vol000+01.par2",
		},
		{
			name:    "bare yEnc marker with no counter",
			subject: `[1/1] - archive.7z yEnc`,
			want:    "archive.7z",
		},
		{
			name:    "no extension anywhere falls back to the subject",
			subject: `just a subject line`,
			want:    "just a subject line",
		},
		{
			name:    "empty subject",
			subject: "   ",
			want:    "",
		},
		{
			name:    "empty quotes fall through to the unquoted rules",
			subject: `[1/1] - "" fallback.name.mkv yEnc (1/2)`,
			want:    "fallback.name.mkv",
		},
		{
			name:    "path separators are stripped",
			subject: `[1/1] - "../../etc/passwd" yEnc (1/1)`,
			want:    "....etcpasswd",
		},
		{
			name:    "windows path separators are stripped",
			subject: `[1/1] - "C:\windows\system32\evil.dll" yEnc (1/1)`,
			want:    "C:windowssystem32evil.dll",
		},
		{
			name:    "a bare dot-dot is not a filename",
			subject: `[1/1] - ".." yEnc (1/1)`,
			want:    "",
		},
		{
			name:    "trailing counter only, no yEnc marker",
			subject: `[1/1] - plain.file.txt`,
			want:    "plain.file.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nzb.Filename(tt.subject); got != tt.want {
				t.Errorf("Filename(%q) = %q, want %q", tt.subject, got, tt.want)
			}
		})
	}
}
