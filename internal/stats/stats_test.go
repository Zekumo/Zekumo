package stats

import (
	"testing"
	"time"
)

func TestDayKeyOffsetUsesCalendarDaysAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("timezone data unavailable: %v", err)
	}
	svc := &Service{Loc: loc}
	// Noon on the first day after the spring-forward transition.
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, loc)
	if got := svc.dayKeyOffset(now, -1); got != "2026-03-08" {
		t.Fatalf("previous local day = %s", got)
	}
	if got := svc.dayKeyOffset(now, -2); got != "2026-03-07" {
		t.Fatalf("two local days ago = %s", got)
	}
}

func TestRetentionHorizonsFinalizeAfterTargetDay(t *testing.T) {
	want := map[string]retentionSpec{
		"d1":  {horizon: 1, minAge: 2},
		"d7":  {horizon: 7, minAge: 8},
		"d30": {horizon: 30, minAge: 31},
	}
	for name, expected := range want {
		got, ok := retentionSpecs[name]
		if !ok || got != expected {
			t.Fatalf("%s spec = %+v (present=%v), want %+v", name, got, ok, expected)
		}
		if got.minAge != got.horizon+1 {
			t.Fatalf("%s finalizes before its target day is complete: %+v", name, got)
		}
	}
}
