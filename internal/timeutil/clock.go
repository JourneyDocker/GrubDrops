package timeutil

// This file owns the 12/24-hour display clock format. Like Zone it is
// swappable at runtime so a setting change applies live without a restart,
// and the layout strings live here once instead of being hardcoded at every
// call site.

import (
	"sync/atomic"
	"time"
)

// Clock format identifiers. The default is 24-hour.
const (
	// Clock24 is the 24-hour clock (the default when the setting is unset).
	Clock24 = "24"
	// Clock12 is the 12-hour clock, suffixed with AM/PM.
	Clock12 = "12"
)

// The two timestamp shapes the UI renders. Only the clock portion varies
// with the 12/24-hour setting: the date part and the zone abbreviation are
// identical in both, so there is no date-format localization here by design.
const (
	// LayoutDateTime24 / LayoutDateTime12: date + clock + zone abbreviation.
	LayoutDateTime24 = "2006-01-02 15:04 MST"
	LayoutDateTime12 = "2006-01-02 3:04 PM MST"
	// LayoutClock24 / LayoutClock12: clock only, with seconds.
	LayoutClock24 = "15:04:05"
	LayoutClock12 = "3:04:05 PM"
)

// Clock holds the 12/24-hour display preference behind an atomic pointer so
// readers (request handlers formatting timestamps, templates) and the settings
// writer never race. Mirrors Zone: the zero value is usable and reports the
// default (24-hour).
type Clock struct {
	p atomic.Pointer[string]
}

// Display is the process-wide display clock. It is the single source of truth
// for time layouts — callers use the package-level FormatDateTime/FormatTime
// rather than hardcoding a layout string. The api package swaps it live when
// the setting changes.
var Display = NewClock(Clock24)

// NewClock returns a Clock set to format, falling back to Clock24 for
// anything that is not Clock12.
func NewClock(format string) *Clock {
	c := &Clock{}
	c.Set(format)
	return c
}

// Format returns the current format, Clock24 or Clock12. Never empty.
func (c *Clock) Format() string {
	if f := c.p.Load(); f != nil && *f == Clock12 {
		return Clock12
	}
	return Clock24
}

// Hour12 reports whether the 12-hour clock is in effect. This is what the
// template layer needs (for the browser clock's Intl option).
func (c *Clock) Hour12() bool { return c.Format() == Clock12 }

// Set swaps the current format live. Any value other than Clock12 is treated
// as Clock24, so a corrupt setting can never blank the clock format.
func (c *Clock) Set(format string) {
	v := Clock24
	if format == Clock12 {
		v = Clock12
	}
	c.p.Store(&v)
}

// DateTimeLayout returns the date+clock+zone layout for the current format.
func (c *Clock) DateTimeLayout() string {
	if c.Hour12() {
		return LayoutDateTime12
	}
	return LayoutDateTime24
}

// ClockLayout returns the seconds-precision clock-only layout for the current
// format.
func (c *Clock) ClockLayout() string {
	if c.Hour12() {
		return LayoutClock12
	}
	return LayoutClock24
}

// FormatDateTime renders t in loc as "date + clock + zone abbreviation" using
// the current format. A nil loc means UTC.
func (c *Clock) FormatDateTime(t time.Time, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	return t.In(loc).Format(c.DateTimeLayout())
}

// FormatClock renders t in loc as a seconds-precision clock using the current
// format. A nil loc means UTC.
func (c *Clock) FormatClock(t time.Time, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	return t.In(loc).Format(c.ClockLayout())
}

// Hour12 reports whether the process-wide display clock is 12-hour.
func Hour12() bool { return Display.Hour12() }

// FormatDateTime is the package-level shorthand for Display.FormatDateTime.
func FormatDateTime(t time.Time, loc *time.Location) string {
	return Display.FormatDateTime(t, loc)
}

// FormatClock is the package-level shorthand for Display.FormatClock.
func FormatClock(t time.Time, loc *time.Location) string {
	return Display.FormatClock(t, loc)
}
