package sandbox

import (
	"errors"
	"testing"
	"time"
)

func TestBudgetMonitor_TokenLimit(t *testing.T) {
	bm := NewBudgetMonitor(BudgetConfig{
		MaxTokens: 1000,
	})

	if err := bm.Check(); err != nil {
		t.Fatalf("unexpected check error on new monitor: %v", err)
	}

	// 1. Record tokens below threshold
	if err := bm.RecordTokens(400); err != nil {
		t.Fatalf("unexpected error recording 400 tokens: %v", err)
	}
	if bm.CumulativeTokens() != 400 {
		t.Errorf("expected 400 tokens, got: %d", bm.CumulativeTokens())
	}
	if bm.IsTripped() {
		t.Errorf("expected monitor not to be tripped")
	}

	// 2. Record tokens still below threshold
	if err := bm.RecordTokens(500); err != nil {
		t.Fatalf("unexpected error recording 500 tokens: %v", err)
	}
	if bm.CumulativeTokens() != 900 {
		t.Errorf("expected 900 tokens, got: %d", bm.CumulativeTokens())
	}

	// 3. Record tokens breaching threshold (900 + 200 = 1100 > 1000)
	err := bm.RecordTokens(200)
	if err == nil {
		t.Fatalf("expected error when breaching token threshold")
	}
	if !errors.Is(err, ErrTokenBudgetExceeded) {
		t.Errorf("expected ErrTokenBudgetExceeded, got: %v", err)
	}
	if !bm.IsTripped() {
		t.Errorf("expected monitor to be tripped")
	}

	// 4. Subsequent Check() must fail fast with ErrCircuitBreakerTripped
	checkErr := bm.Check()
	if checkErr == nil {
		t.Fatalf("expected Check() to fail after trip")
	}
	if !errors.Is(checkErr, ErrCircuitBreakerTripped) {
		t.Errorf("expected ErrCircuitBreakerTripped, got: %v", checkErr)
	}

	// 5. Reset restores untripped state
	bm.Reset()
	if bm.IsTripped() {
		t.Errorf("expected monitor to be untripped after Reset()")
	}
	if bm.CumulativeTokens() != 0 {
		t.Errorf("expected 0 tokens after Reset(), got: %d", bm.CumulativeTokens())
	}
	if err := bm.Check(); err != nil {
		t.Errorf("expected Check() to succeed after Reset(): %v", err)
	}
}

func TestBudgetMonitor_DurationLimit(t *testing.T) {
	bm := NewBudgetMonitor(BudgetConfig{
		MaxDuration: 100 * time.Millisecond,
	})

	if err := bm.RecordDuration(50 * time.Millisecond); err != nil {
		t.Fatalf("unexpected error recording 50ms: %v", err)
	}
	if bm.IsTripped() {
		t.Errorf("expected monitor not to be tripped")
	}

	// Breach threshold
	err := bm.RecordDuration(60 * time.Millisecond)
	if err == nil {
		t.Fatalf("expected error when breaching duration threshold")
	}
	if !errors.Is(err, ErrDurationBudgetExceeded) {
		t.Errorf("expected ErrDurationBudgetExceeded, got: %v", err)
	}
	if !bm.IsTripped() {
		t.Errorf("expected monitor to be tripped")
	}

	// Check() fails
	if err := bm.Check(); !errors.Is(err, ErrCircuitBreakerTripped) {
		t.Errorf("expected ErrCircuitBreakerTripped, got: %v", err)
	}
}

func TestBudgetMonitor_ManualTripAndSnapshot(t *testing.T) {
	bm := NewBudgetMonitor(BudgetConfig{
		MaxTokens:   5000,
		MaxDuration: 10 * time.Second,
	})

	_ = bm.RecordTokens(250)
	_ = bm.RecordDuration(1 * time.Second)

	snap := bm.Snapshot()
	if snap.CumulativeTokens != 250 || snap.MaxTokens != 5000 {
		t.Errorf("unexpected tokens in snapshot: %+v", snap)
	}
	if snap.CumulativeDuration != 1*time.Second || snap.MaxDuration != 10*time.Second {
		t.Errorf("unexpected duration in snapshot: %+v", snap)
	}
	if snap.Tripped {
		t.Errorf("expected snapshot not tripped")
	}

	bm.Trip("operator emergency halt")
	if !bm.IsTripped() {
		t.Errorf("expected tripped after manual Trip()")
	}
	if bm.TripReason() != "operator emergency halt" {
		t.Errorf("unexpected trip reason: %s", bm.TripReason())
	}

	snap2 := bm.Snapshot()
	if !snap2.Tripped || snap2.TripReason != "operator emergency halt" {
		t.Errorf("unexpected snapshot after trip: %+v", snap2)
	}
}

func TestParseDurationString(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
		hasError bool
	}{
		{"", 0, false},
		{"0", 0, false},
		{"unlimited", 0, false},
		{"30s", 30 * time.Second, false},
		{"5m", 5 * time.Minute, false},
		{"1h", 1 * time.Hour, false},
		{"2h30m", 2*time.Hour + 30*time.Minute, false},
		{"invalid-duration", 0, true},
	}

	for _, tc := range tests {
		d, err := ParseDurationString(tc.input)
		if tc.hasError && err == nil {
			t.Errorf("expected error for input %q, got nil", tc.input)
		}
		if !tc.hasError && err != nil {
			t.Errorf("unexpected error for input %q: %v", tc.input, err)
		}
		if !tc.hasError && d != tc.expected {
			t.Errorf("for input %q: expected %v, got %v", tc.input, tc.expected, d)
		}
	}
}
