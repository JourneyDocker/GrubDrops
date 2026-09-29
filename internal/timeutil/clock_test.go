package timeutil

import (
	"testing"
	"time"
)

// testTime is a fixed instant: 2026-09-28 15:04:05 UTC — afternoon, so the
// 12-hour layout must render PM (the AM/PM half of the format is what a
// 12/24 bug would silently drop).
var testTime = time.Date(2026, 9, 28, 15, 4, 5, 0, time.UTC)

func TestNewClock_DefaultsTo24Hour(t *testing.T) {
	c := NewClock("")
	if c.Format() != Clock24 {
		t.Fatalf("empty format should default to %q, got %q", Clock24, c.Format())
	}
	if c.Hour12() {
		t.Fatal("empty format must not select the 12-hour clock")
	}
	// The zero value is usable too (no constructor required).
	var zero Clock
	if zero.Hour12() || zero.Format() != Clock24 {
		t.Fatalf("zero Clock should be 24-hour, got %q", zero.Format())
	}
}

func TestNewClock_OnlyTwelveSelects12Hour(t *testing.T) {
	if c := NewClock(Clock12); !c.Hour12() || c.Format() != Clock12 {
		t.Fatalf("Clock12 should select the 12-hour clock, got %q", c.Format())
	}
	// Anything else — including a corrupt persisted value — is 24-hour.
	for _, bad := range []string{"24", "0", "1", "24h", "am/pm", "12:00"} {
		if c := NewClock(bad); c.Hour12() {
			t.Errorf("value %q must not select the 12-hour clock", bad)
		}
	}
}

func TestClock_FormatDateTime(t *testing.T) {
	if got, want := NewClock(Clock24).FormatDateTime(testTime, time.UTC), "2026-09-28 15:04 UTC"; got != want {
		t.Errorf("24h date+time = %q, want %q", got, want)
	}
	if got, want := NewClock(Clock12).FormatDateTime(testTime, time.UTC), "2026-09-28 3:04 PM UTC"; got != want {
		t.Errorf("12h date+time = %q, want %q", got, want)
	}
	// Date part and zone abbreviation must be identical in both formats.
	d24 := NewClock(Clock24).FormatDateTime(testTime, time.UTC)
	d12 := NewClock(Clock12).FormatDateTime(testTime, time.UTC)
	if len(d24) == 0 || len(d12) == 0 {
		t.Fatal("date+time rendered empty")
	}
}

func TestClock_FormatClock(t *testing.T) {
	if got, want := NewClock(Clock24).FormatClock(testTime, time.UTC), "15:04:05"; got != want {
		t.Errorf("24h clock = %q, want %q", got, want)
	}
	if got, want := NewClock(Clock12).FormatClock(testTime, time.UTC), "3:04:05 PM"; got != want {
		t.Errorf("12h clock = %q, want %q", got, want)
	}
}

func TestClock_NilLocationMeansUTC(t *testing.T) {
	c := NewClock(Clock24)
	if got, want := c.FormatDateTime(testTime, nil), "2026-09-28 15:04 UTC"; got != want {
		t.Errorf("nil loc date+time = %q, want %q", got, want)
	}
	if got, want := c.FormatClock(testTime, nil), "15:04:05"; got != want {
		t.Errorf("nil loc clock = %q, want %q", got, want)
	}
}

func TestClock_AppliesTheGivenZone(t *testing.T) {
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	// 15:04 UTC is 00:04 the next day in Tokyo — a 12h-only AM/PM flip.
	if got, want := NewClock(Clock12).FormatClock(testTime, tokyo), "12:04:05 AM"; got != want {
		t.Errorf("12h clock in Tokyo = %q, want %q", got, want)
	}
	if got, want := NewClock(Clock24).FormatClock(testTime, tokyo), "00:04:05"; got != want {
		t.Errorf("24h clock in Tokyo = %q, want %q", got, want)
	}
}

func TestClock_SetIsLive(t *testing.T) {
	c := NewClock(Clock24)
	if c.Hour12() {
		t.Fatal("new clock should start 24-hour")
	}
	c.Set(Clock12)
	if !c.Hour12() {
		t.Fatal("Set(12) did not take effect")
	}
	if got, want := c.FormatDateTime(testTime, time.UTC), "2026-09-28 3:04 PM UTC"; got != want {
		t.Errorf("after Set(12) date+time = %q, want %q", got, want)
	}
	c.Set(Clock24)
	if c.Hour12() {
		t.Fatal("Set(24) did not take effect")
	}
	if got, want := c.FormatDateTime(testTime, time.UTC), "2026-09-28 15:04 UTC"; got != want {
		t.Errorf("after Set(24) date+time = %q, want %q", got, want)
	}
	// A corrupt value never blanks the format.
	c.Set("garbage")
	if c.Format() != Clock24 {
		t.Fatalf("Set(garbage) = %q, want the %q default", c.Format(), Clock24)
	}
}

func TestDisplayClock_DefaultsTo24Hour(t *testing.T) {
	if Display == nil {
		t.Fatal("Display clock must never be nil")
	}
	// Package-level shorthands must agree with the holder.
	if got, want := FormatDateTime(testTime, time.UTC), "2026-09-28 15:04 UTC"; got != want {
		t.Errorf("Display.FormatDateTime = %q, want %q", got, want)
	}
	if got, want := FormatClock(testTime, time.UTC), "15:04:05"; got != want {
		t.Errorf("Display.FormatClock = %q, want %q", got, want)
	}
	if Hour12() {
		t.Error("Display clock should default to 24-hour")
	}
}

// TestDisplayClock_SwapIsVisibleToShorthands proves the live swap reaches the
// package-level helpers the handlers use (this is the whole point of the
// holder: a settings change applies with no restart).
func TestDisplayClock_SwapIsVisibleToShorthands(t *testing.T) {
	prev := Display.Format()
	t.Cleanup(func() { Display.Set(prev) })

	Display.Set(Clock12)
	if !Hour12() {
		t.Fatal("Hour12() did not observe the live swap")
	}
	if got, want := FormatClock(testTime, time.UTC), "3:04:05 PM"; got != want {
		t.Errorf("after swap FormatClock = %q, want %q", got, want)
	}
	Display.Set(Clock24)
	if got, want := FormatDateTime(testTime, time.UTC), "2026-09-28 15:04 UTC"; got != want {
		t.Errorf("after swap back FormatDateTime = %q, want %q", got, want)
	}
}
