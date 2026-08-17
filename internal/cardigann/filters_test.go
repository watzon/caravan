package cardigann

import (
	"strings"
	"testing"
	"time"
)

func TestDateParseFilterTranslatesBoundedDotNetLayouts(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		format string
		want   string
	}{
		{
			name:   "numeric offset",
			value:  "2026-08-15 12:34:56 +00:00",
			format: "yyyy-MM-dd HH:mm:ss zzz",
			want:   "2026-08-15T12:34:56Z",
		},
		{
			name:   "english month and meridiem",
			value:  "Aug 5 2026 03:04 PM",
			format: "MMM d yyyy hh:mm tt",
			want:   "2026-08-05T15:04:00Z",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := applyFilters(tt.value, []filterBlock{{Name: "dateparse", Args: tt.format}})
			if err != nil {
				t.Fatalf("applyFilters: %v", err)
			}
			if got != tt.want {
				t.Fatalf("dateparse = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDateParseFilterRejectsUnknownLayoutTokens(t *testing.T) {
	filters := []filterBlock{{Name: "dateparse", Args: "yyyy-QQ-dd"}}
	if err := validateFilters(filters); err == nil || !strings.Contains(err.Error(), "dateparse format") {
		t.Fatalf("validateFilters error = %v, want dateparse format rejection", err)
	}
}

func TestDateParseFilterInfersYearAndRollsFutureDatesBack(t *testing.T) {
	now := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	got, err := applyDateParseFilterAt("12/31 23:45", "MM/dd HH:mm", now)
	if err != nil {
		t.Fatalf("applyDateParseFilterAt: %v", err)
	}
	if got != "2025-12-31T23:45:00Z" {
		t.Fatalf("yearless date = %q", got)
	}
}

func TestQueryStringFilterExtractsDecodedParameter(t *testing.T) {
	got, err := applyFilters("browse.php?cat=42&name=hello%20world", []filterBlock{{Name: "querystring", Args: "name"}})
	if err != nil {
		t.Fatalf("applyFilters: %v", err)
	}
	if got != "hello world" {
		t.Fatalf("querystring = %q, want decoded value", got)
	}
}

func TestQueryStringFilterRejectsBlankParameter(t *testing.T) {
	if err := validateFilters([]filterBlock{{Name: "querystring", Args: ""}}); err == nil {
		t.Fatal("validateFilters accepted blank querystring parameter")
	}
}

func TestURLencodeFilterEscapesCredentialForQueryValue(t *testing.T) {
	got, err := applyFilters("key with + and &", []filterBlock{{Name: "urlencode"}})
	if err != nil {
		t.Fatalf("applyFilters: %v", err)
	}
	if got != "key+with+%2B+and+%26" {
		t.Fatalf("urlencode = %q", got)
	}
}

func TestFuzzyTimeFilterResolvesRelativeDayAndDuration(t *testing.T) {
	now := time.Date(2026, 8, 15, 18, 0, 0, 0, time.UTC)
	tests := []struct {
		value string
		want  string
	}{
		{value: "Today 12:30 +08:00", want: "2026-08-15T04:30:00Z"},
		{value: "Yesterday at 23:15 +08:00", want: "2026-08-14T15:15:00Z"},
		{value: "Yesterday", want: "2026-08-14T18:00:00Z"},
		{value: "Today", want: "2026-08-15T18:00:00Z"},
		{value: "2 hours ago", want: "2026-08-15T16:00:00Z"},
	}
	for _, test := range tests {
		got, err := applyFuzzyTimeFilterAt(test.value, now)
		if err != nil {
			t.Fatalf("applyFuzzyTimeFilterAt(%q): %v", test.value, err)
		}
		if got != test.want {
			t.Fatalf("fuzzytime(%q) = %q, want %q", test.value, got, test.want)
		}
	}
}

func TestDiacriticsAndTimeAgoFiltersUseCardigannSemantics(t *testing.T) {
	got, err := applyFilters("Crème brûlée", []filterBlock{{Name: "diacritics", Args: "replace"}})
	if err != nil || got != "Creme brulee" {
		t.Fatalf("diacritics = %q, %v", got, err)
	}
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	got, err = applyTimeAgoFilterAt("2 days ago", now)
	if err != nil || got != "2026-08-13T12:00:00Z" {
		t.Fatalf("timeago = %q, %v", got, err)
	}
}

func TestValidFilenameFilterRemovesUnsafePathCharacters(t *testing.T) {
	got, err := applyFilters(`Bad:/\Name?* `, []filterBlock{{Name: "validfilename"}})
	if err != nil || got != "Bad___Name__" {
		t.Fatalf("validfilename = %q, %v", got, err)
	}
}
