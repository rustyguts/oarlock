package engine

import (
	"testing"
	"time"

	"github.com/robfig/cron/v3"
)

// TestLatestOccurrence covers the most-recent-activation-in-window computation
// that drives the sweep, including the none-in-window case.
func TestLatestOccurrence(t *testing.T) {
	everyMinute, err := cron.ParseStandard("* * * * *")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	every5, err := cron.ParseStandard("*/5 * * * *")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// now sits at 10:05:30; the most recent whole minute is 10:05:00.
	now := time.Date(2026, 7, 6, 10, 5, 30, 0, time.UTC)
	occ, ok := latestOccurrence(everyMinute, now, 90*time.Second)
	if !ok {
		t.Fatalf("expected an occurrence for every-minute schedule")
	}
	if want := time.Date(2026, 7, 6, 10, 5, 0, 0, time.UTC); !occ.Equal(want) {
		t.Fatalf("occurrence = %s, want %s", occ, want)
	}
	if occ.After(now) {
		t.Fatalf("occurrence %s must be <= now %s", occ, now)
	}

	// every-5-minutes at 10:05:30 → most recent boundary is 10:05:00.
	occ5, ok := latestOccurrence(every5, now, 90*time.Second)
	if !ok {
		t.Fatalf("expected an occurrence for every-5-min schedule")
	}
	if want := time.Date(2026, 7, 6, 10, 5, 0, 0, time.UTC); !occ5.Equal(want) {
		t.Fatalf("every-5 occurrence = %s, want %s", occ5, want)
	}

	// None-in-window: every-5-minutes at 10:02:00 — the last boundary (10:00:00)
	// is >90s ago, and the next (10:05:00) is in the future.
	quiet := time.Date(2026, 7, 6, 10, 2, 0, 0, time.UTC)
	if occ, ok := latestOccurrence(every5, quiet, 90*time.Second); ok {
		t.Fatalf("expected no occurrence in window, got %s", occ)
	}

	// Boundary exactly at now counts (half-open (now-lookback, now]).
	onBoundary := time.Date(2026, 7, 6, 10, 5, 0, 0, time.UTC)
	occB, ok := latestOccurrence(everyMinute, onBoundary, 90*time.Second)
	if !ok || !occB.Equal(onBoundary) {
		t.Fatalf("boundary occurrence = %s ok=%v, want %s true", occB, ok, onBoundary)
	}
}
