package core

import "time"

// SceneDateSlackDays is how far a parsed release date may drift from a
// scene's stored air date and still be treated as that scene.
//
// One day covers the usual split: a studio publishes in local time, the
// metadata box stores a UTC calendar day, and the indexer names the file
// with the other day's digits. Whisparr hit the same drift (issue #109)
// and only fixed ingest conversion; the filename and the stored day can
// still disagree after that.
const SceneDateSlackDays = 1

// SceneDayDelta is the signed UTC calendar-day difference a minus b.
// ok is false when either instant is zero.
func SceneDayDelta(a, b time.Time) (days int, ok bool) {
	if a.IsZero() || b.IsZero() {
		return 0, false
	}
	ay, am, ad := a.UTC().Date()
	by, bm, bd := b.UTC().Date()
	aa := time.Date(ay, am, ad, 0, 0, 0, 0, time.UTC)
	bb := time.Date(by, bm, bd, 0, 0, 0, 0, time.UTC)
	return int(aa.Sub(bb) / (24 * time.Hour)), true
}

// SameSceneDay reports whether two instants fall on the same UTC calendar day.
func SameSceneDay(a, b time.Time) bool {
	days, ok := SceneDayDelta(a, b)
	return ok && days == 0
}

// NearbySceneDay reports whether two instants fall within SceneDateSlackDays
// UTC calendar days of each other, including the same day.
func NearbySceneDay(a, b time.Time) bool {
	days, ok := SceneDayDelta(a, b)
	if !ok {
		return false
	}
	if days < 0 {
		days = -days
	}
	return days <= SceneDateSlackDays
}
