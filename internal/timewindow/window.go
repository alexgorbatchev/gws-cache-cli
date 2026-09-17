// Package timewindow parses the lookback and lookahead window expressions
// accepted by the gws-cache command line ("4w", "30d", "2026-06-01", "all").
package timewindow

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Kind distinguishes the three shapes a window expression can take. Keeping
// "unbounded" as its own kind is what lets callers tell "the user asked for
// everything" apart from "the user did not narrow the window", which a bare
// epoch-seconds return value cannot express.
type Kind int

const (
	// Unbounded is the "all" window: no bound is applied in either direction.
	Unbounded Kind = iota
	// Relative is a duration measured from the current time, such as "4w".
	Relative
	// Absolute is a calendar date, such as "2026-06-01".
	Absolute
)

// String implements fmt.Stringer so test failures and progress messages read
// clearly instead of printing raw integers.
func (k Kind) String() string {
	switch k {
	case Unbounded:
		return "unbounded"
	case Relative:
		return "relative"
	case Absolute:
		return "absolute"
	default:
		return "invalid"
	}
}

const (
	day   = 24 * time.Hour
	week  = 7 * day
	month = 30 * day
	year  = 365 * day
)

// dateLayout is the only absolute form accepted, matching Gmail's own
// "after:" query syntax so the two cannot drift apart.
const dateLayout = "2006-01-02"

// Window is a parsed window expression. The zero value is Unbounded, which is
// the correct default for a filter that has not been set.
type Window struct {
	Kind     Kind
	Duration time.Duration
	At       time.Time
}

// Parse converts a window expression into a Window. An empty string and "all"
// both mean unbounded.
func Parse(expr string) (Window, error) {
	trimmed := strings.TrimSpace(expr)
	if trimmed == "" || strings.EqualFold(trimmed, "all") {
		return Window{Kind: Unbounded}, nil
	}

	if at, err := time.Parse(dateLayout, trimmed); err == nil {
		return Window{Kind: Absolute, At: at.UTC()}, nil
	}

	unit := trimmed[len(trimmed)-1]
	value, err := strconv.Atoi(trimmed[:len(trimmed)-1])
	if err != nil {
		return Window{}, fmt.Errorf("invalid window %q: expected a duration such as 4w, a date such as 2026-06-01, or \"all\"", expr)
	}
	if value < 0 {
		return Window{}, fmt.Errorf("invalid window %q: duration must not be negative", expr)
	}

	var unitDuration time.Duration
	switch unit {
	case 'h':
		unitDuration = time.Hour
	case 'd':
		unitDuration = day
	case 'w':
		unitDuration = week
	case 'm':
		unitDuration = month
	case 'y':
		unitDuration = year
	default:
		return Window{}, fmt.Errorf("invalid window %q: unknown duration unit %q", expr, string(unit))
	}

	return Window{Kind: Relative, Duration: time.Duration(value) * unitDuration}, nil
}

// Start returns the lower bound of the window. The second result is false when
// the window is unbounded and no lower bound should be applied at all.
func (w Window) Start(now time.Time) (time.Time, bool) {
	switch w.Kind {
	case Relative:
		return now.Add(-w.Duration).UTC(), true
	case Absolute:
		return w.At.UTC(), true
	default:
		return time.Time{}, false
	}
}

// End returns the upper bound of the window, treating a relative duration as a
// lookahead rather than a lookback. The second result is false when the window
// is unbounded.
func (w Window) End(now time.Time) (time.Time, bool) {
	switch w.Kind {
	case Relative:
		return now.Add(w.Duration).UTC(), true
	case Absolute:
		return w.At.UTC(), true
	default:
		return time.Time{}, false
	}
}

// StartUnix returns Start as Unix epoch seconds, the form the store layer uses
// for date predicates.
func (w Window) StartUnix(now time.Time) (int64, bool) {
	start, ok := w.Start(now)
	if !ok {
		return 0, false
	}
	return start.Unix(), true
}
