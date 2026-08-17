package cardigann

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const maxDateFormatBytes = 160

var (
	fuzzyAgoPattern  = regexp.MustCompile(`(?i)^\s*(\d+|a|an)\s+(second|minute|hour|day|week|month|year)s?\s+ago\s*$`)
	fuzzyZonePattern = regexp.MustCompile(`\s+([+-])(\d{2}):(\d{2})\s*$`)
)

func validateFuzzyTimeFilter(raw any) error {
	if raw != nil {
		return fmt.Errorf("fuzzytime does not accept arguments")
	}
	return nil
}

func applyDiacriticsFilter(value string, raw any) (string, error) {
	mode, ok := raw.(string)
	if !ok || !strings.EqualFold(strings.TrimSpace(mode), "replace") {
		return "", fmt.Errorf("diacritics expects replace")
	}
	var out strings.Builder
	for _, r := range norm.NFD.String(value) {
		if !unicode.Is(unicode.Mn, r) {
			out.WriteRune(r)
		}
	}
	return norm.NFC.String(out.String()), nil
}

func applyTimeAgoFilterAt(value string, now time.Time) (string, error) {
	return applyFuzzyTimeFilterAt(value, now)
}

func validateAllowedValuesFilter(raw any) error {
	values, ok := raw.(string)
	if !ok || strings.TrimSpace(values) == "" || len(values) > maxExtractedFieldBytes {
		return fmt.Errorf("validate requires a non-empty bounded string")
	}
	return nil
}

func applyAllowedValuesFilter(value string, raw any) (string, error) {
	if err := validateAllowedValuesFilter(raw); err != nil {
		return "", err
	}
	allowed := tokenizeValidatedValues(raw.(string))
	present := make(map[string]struct{})
	for _, token := range tokenizeValidatedValues(value) {
		present[token] = struct{}{}
	}
	result := make([]string, 0, len(allowed))
	seen := make(map[string]struct{})
	for _, token := range allowed {
		if _, ok := present[token]; !ok {
			continue
		}
		if _, duplicate := seen[token]; duplicate {
			continue
		}
		seen[token] = struct{}{}
		result = append(result, token)
	}
	return strings.Join(result, ", "), nil
}

func tokenizeValidatedValues(value string) []string {
	return strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		switch r {
		case ',', ' ', '/', ')', '(', '.', ';', '[', ']', '"', '|', ':':
			return true
		default:
			return false
		}
	})
}

func applyFuzzyTimeFilterAt(value string, now time.Time) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxExtractedFieldBytes {
		return "", fmt.Errorf("fuzzytime value must contain 1-%d bytes", maxExtractedFieldBytes)
	}
	if now.IsZero() {
		return "", fmt.Errorf("fuzzytime reference is zero")
	}

	location := now.Location()
	if match := fuzzyZonePattern.FindStringSubmatch(value); len(match) == 4 {
		hours, _ := strconv.Atoi(match[2])
		minutes, _ := strconv.Atoi(match[3])
		if hours > 23 || minutes > 59 {
			return "", fmt.Errorf("fuzzytime timezone is invalid")
		}
		offset := (hours*60 + minutes) * 60
		if match[1] == "-" {
			offset = -offset
		}
		location = time.FixedZone(match[0], offset)
		value = strings.TrimSpace(value[:len(value)-len(match[0])])
	}

	if strings.EqualFold(value, "now") {
		return now.UTC().Format(time.RFC3339), nil
	}
	if match := fuzzyAgoPattern.FindStringSubmatch(value); len(match) == 3 {
		amount := 1
		if match[1] != "a" && match[1] != "an" {
			parsed, err := strconv.Atoi(match[1])
			if err != nil || parsed < 0 || parsed > 1_000_000 {
				return "", fmt.Errorf("fuzzytime duration is invalid")
			}
			amount = parsed
		}
		var parsed time.Time
		switch strings.ToLower(match[2]) {
		case "second":
			parsed = now.Add(-time.Duration(amount) * time.Second)
		case "minute":
			parsed = now.Add(-time.Duration(amount) * time.Minute)
		case "hour":
			parsed = now.Add(-time.Duration(amount) * time.Hour)
		case "day":
			parsed = now.AddDate(0, 0, -amount)
		case "week":
			parsed = now.AddDate(0, 0, -7*amount)
		case "month":
			parsed = now.AddDate(0, -amount, 0)
		case "year":
			parsed = now.AddDate(-amount, 0, 0)
		}
		return parsed.UTC().Format(time.RFC3339), nil
	}

	normalized := strings.TrimSpace(strings.NewReplacer("(", "", ")", "", ",", " ", " at ", " ").Replace(value))
	fields := strings.Fields(normalized)
	if len(fields) == 0 {
		return "", fmt.Errorf("fuzzytime value %q is unsupported", value)
	}
	dayOffset := 0
	switch strings.ToLower(fields[0]) {
	case "today":
	case "yesterday":
		dayOffset = -1
	default:
		return "", fmt.Errorf("fuzzytime day %q is unsupported", fields[0])
	}
	// Sites such as TorrentDownload emit the bare day word with no clock.
	if len(fields) == 1 {
		return now.AddDate(0, 0, dayOffset).UTC().Format(time.RFC3339), nil
	}
	clock := strings.Join(fields[1:], " ")
	parsedClock, err := parseFuzzyClock(clock, location)
	if err != nil {
		return "", err
	}
	year, month, day := now.Date()
	parsed := time.Date(year, month, day+dayOffset, parsedClock.Hour(), parsedClock.Minute(), parsedClock.Second(), 0, location)
	return parsed.UTC().Format(time.RFC3339), nil
}

func parseFuzzyClock(value string, location *time.Location) (time.Time, error) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{"15:04:05", "15:04", "3:04PM", "3:04 PM", "3PM", "3 PM"} {
		if parsed, err := time.ParseInLocation(layout, strings.ToUpper(value), location); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("fuzzytime clock %q is unsupported", value)
}

func validateQueryStringFilter(raw any) error {
	name, ok := raw.(string)
	if !ok {
		return fmt.Errorf("querystring parameter must be a string")
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 256 || strings.ContainsAny(name, "&=#\r\n\x00") {
		return fmt.Errorf("querystring parameter must contain 1-256 safe bytes")
	}
	return nil
}

func applyQueryStringFilter(value string, raw any) (string, error) {
	if err := validateQueryStringFilter(raw); err != nil {
		return "", err
	}
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", fmt.Errorf("querystring URL: %w", err)
	}
	values, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return "", fmt.Errorf("querystring values: %w", err)
	}
	return values.Get(strings.TrimSpace(raw.(string))), nil
}

var dotNetDateTokens = []struct {
	source string
	layout string
}{
	{source: "dddd", layout: "Monday"},
	{source: "MMMM", layout: "January"},
	{source: "yyyy", layout: "2006"},
	{source: "ddd", layout: "Mon"},
	{source: "MMM", layout: "Jan"},
	{source: "zzz", layout: "-07:00"},
	{source: "yyy", layout: "2006"},
	{source: "yy", layout: "06"},
	{source: "MM", layout: "01"},
	{source: "dd", layout: "02"},
	{source: "HH", layout: "15"},
	{source: "hh", layout: "03"},
	{source: "mm", layout: "04"},
	{source: "ss", layout: "05"},
	{source: "tt", layout: "PM"},
	{source: "M", layout: "1"},
	{source: "d", layout: "2"},
	{source: "h", layout: "3"},
	{source: "m", layout: "4"},
	{source: "s", layout: "5"},
}

func validateDateParseFilter(raw any) error {
	format, ok := raw.(string)
	if !ok {
		return fmt.Errorf("dateparse format must be a string")
	}
	_, err := compileDotNetDateLayout(format)
	return err
}

func applyDateParseFilter(value string, raw any) (string, error) {
	return applyDateParseFilterAt(value, raw, time.Now().UTC())
}

func applyDateParseFilterAt(value string, raw any, now time.Time) (string, error) {
	format, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("dateparse format must be a string")
	}
	layout, err := compileDotNetDateLayout(format)
	if err != nil {
		return "", err
	}
	parseValue := strings.TrimSpace(value)
	if !strings.Contains(format, "yy") {
		layout = "2006 " + layout
		parseValue = strconv.Itoa(now.Year()) + " " + parseValue
	}
	parsed, err := time.ParseInLocation(layout, parseValue, time.UTC)
	if err != nil {
		return "", fmt.Errorf("dateparse value: %w", err)
	}
	if !strings.Contains(format, "yy") && parsed.After(now.UTC().Add(48*time.Hour)) {
		parsed = parsed.AddDate(-1, 0, 0)
	}
	return parsed.UTC().Format(time.RFC3339), nil
}

func compileDotNetDateLayout(format string) (string, error) {
	format = strings.TrimSpace(format)
	if format == "" || len(format) > maxDateFormatBytes {
		return "", fmt.Errorf("dateparse format must contain 1-%d bytes", maxDateFormatBytes)
	}
	var out strings.Builder
	out.Grow(len(format) + 16)
	for offset := 0; offset < len(format); {
		if format[offset] == '\\' {
			offset++
			if offset >= len(format) {
				return "", fmt.Errorf("dateparse format has a trailing escape")
			}
			r, size := utf8.DecodeRuneInString(format[offset:])
			if r == utf8.RuneError && size == 1 {
				return "", fmt.Errorf("dateparse format contains invalid UTF-8")
			}
			out.WriteRune(r)
			offset += size
			continue
		}
		if format[offset] == '\'' {
			end := strings.IndexByte(format[offset+1:], '\'')
			if end < 0 {
				return "", fmt.Errorf("dateparse format has an unterminated literal")
			}
			out.WriteString(format[offset+1 : offset+1+end])
			offset += end + 2
			continue
		}

		matched := false
		for _, token := range dotNetDateTokens {
			if strings.HasPrefix(format[offset:], token.source) {
				out.WriteString(token.layout)
				offset += len(token.source)
				matched = true
				break
			}
		}
		if matched {
			continue
		}

		r, size := utf8.DecodeRuneInString(format[offset:])
		if r == utf8.RuneError && size == 1 {
			return "", fmt.Errorf("dateparse format contains invalid UTF-8")
		}
		if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' {
			return "", fmt.Errorf("dateparse format contains unsupported token near %q", format[offset:])
		}
		out.WriteRune(r)
		offset += size
	}
	if out.Len() > maxDateFormatBytes*2 {
		return "", fmt.Errorf("dateparse compiled format exceeds size limit")
	}
	return out.String(), nil
}

func applyValidFilenameFilter(value string) string {
	value = strings.Map(func(char rune) rune {
		if char < 32 || strings.ContainsRune(`<>:"/\|?*`, char) {
			return '_'
		}
		return char
	}, value)
	return strings.TrimRight(value, " .")
}
