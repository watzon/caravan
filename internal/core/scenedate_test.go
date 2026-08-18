package core

import (
	"testing"
	"time"
)

func TestSceneDayDelta(t *testing.T) {
	utc := time.UTC
	day := func(y int, m time.Month, d int) time.Time {
		return time.Date(y, m, d, 0, 0, 0, 0, utc)
	}

	tests := []struct {
		name     string
		a, b     time.Time
		wantDays int
		wantOK   bool
	}{
		{name: "same day", a: day(2022, time.March, 14), b: day(2022, time.March, 14), wantDays: 0, wantOK: true},
		{name: "a is one day later", a: day(2022, time.March, 15), b: day(2022, time.March, 14), wantDays: 1, wantOK: true},
		{name: "a is one day earlier", a: day(2022, time.March, 13), b: day(2022, time.March, 14), wantDays: -1, wantOK: true},
		{name: "month boundary", a: day(2022, time.April, 1), b: day(2022, time.March, 31), wantDays: 1, wantOK: true},
		{name: "year boundary", a: day(2023, time.January, 1), b: day(2022, time.December, 31), wantDays: 1, wantOK: true},
		{name: "two days apart", a: day(2022, time.March, 16), b: day(2022, time.March, 14), wantDays: 2, wantOK: true},
		{
			name:     "same calendar day across clock times",
			a:        time.Date(2022, time.March, 14, 23, 0, 0, 0, utc),
			b:        time.Date(2022, time.March, 14, 1, 0, 0, 0, utc),
			wantDays: 0,
			wantOK:   true,
		},
		{name: "zero a", a: time.Time{}, b: day(2022, time.March, 14), wantOK: false},
		{name: "zero b", a: day(2022, time.March, 14), b: time.Time{}, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := SceneDayDelta(tt.a, tt.b)
			if ok != tt.wantOK {
				t.Fatalf("SceneDayDelta ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if got != tt.wantDays {
				t.Errorf("SceneDayDelta = %d, want %d", got, tt.wantDays)
			}
		})
	}
}

func TestSameSceneDayAndNearbySceneDay(t *testing.T) {
	released := time.Date(2022, time.March, 14, 0, 0, 0, 0, time.UTC)

	if !SameSceneDay(released, released.Add(3*time.Hour)) {
		t.Error("same calendar day must match")
	}
	if SameSceneDay(released, released.AddDate(0, 0, 1)) {
		t.Error("the next day must not be the same scene day")
	}
	if SameSceneDay(released, time.Time{}) {
		t.Error("a zero date matches nothing")
	}

	if !NearbySceneDay(released, released) {
		t.Error("the exact day is nearby")
	}
	if !NearbySceneDay(released, released.AddDate(0, 0, -1)) {
		t.Error("the previous day is nearby")
	}
	if !NearbySceneDay(released, released.AddDate(0, 0, 1)) {
		t.Error("the next day is nearby")
	}
	if NearbySceneDay(released, released.AddDate(0, 0, 2)) {
		t.Error("two days away is not nearby")
	}
	if NearbySceneDay(released, time.Time{}) {
		t.Error("a zero date is not nearby")
	}
}
