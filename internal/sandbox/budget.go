package sandbox

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	ErrTokenBudgetExceeded    = errors.New("cumulative token budget exceeded")
	ErrDurationBudgetExceeded = errors.New("cumulative execution duration budget exceeded")
	ErrCircuitBreakerTripped  = errors.New("budget circuit breaker is tripped; further execution halted")
)

// BudgetConfig specifies upper limits for cumulative resource consumption.
type BudgetConfig struct {
	MaxTokens   int64         // Max cumulative tokens scrubbed (0 = unlimited)
	MaxDuration time.Duration // Max cumulative execution wall-clock time (0 = unlimited)
}

// BudgetSnapshot provides an immutable reading of consumption and circuit breaker state.
type BudgetSnapshot struct {
	CumulativeTokens   int64         `json:"cumulative_tokens"`
	MaxTokens          int64         `json:"max_tokens"`
	CumulativeDuration time.Duration `json:"cumulative_duration_ms"`
	MaxDuration        time.Duration `json:"max_duration_ms"`
	Tripped            bool          `json:"tripped"`
	TripReason         string        `json:"trip_reason,omitempty"`
}

// BudgetMonitor tracks cumulative token usage and execution duration, enforcing circuit breakers.
type BudgetMonitor struct {
	cfg        BudgetConfig
	mu         sync.RWMutex
	tokens     int64
	duration   time.Duration
	tripped    bool
	tripReason string
}

// NewBudgetMonitor initializes a new thread-safe budget monitor.
func NewBudgetMonitor(cfg BudgetConfig) *BudgetMonitor {
	return &BudgetMonitor{
		cfg: cfg,
	}
}

// Check returns ErrCircuitBreakerTripped if the circuit breaker is already tripped.
func (b *BudgetMonitor) Check() error {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.tripped {
		return fmt.Errorf("%w: %s", ErrCircuitBreakerTripped, b.tripReason)
	}
	return nil
}

// RecordTokens increments cumulative tokens and trips the circuit breaker if MaxTokens is breached.
func (b *BudgetMonitor) RecordTokens(count int) error {
	if count <= 0 {
		return nil
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.tokens += int64(count)

	if b.cfg.MaxTokens > 0 && b.tokens > b.cfg.MaxTokens && !b.tripped {
		b.tripped = true
		b.tripReason = fmt.Sprintf("cumulative token limit breached (%d > %d tokens)", b.tokens, b.cfg.MaxTokens)
		return fmt.Errorf("%w: %s", ErrTokenBudgetExceeded, b.tripReason)
	}

	return nil
}

// RecordDuration increments cumulative duration and trips the circuit breaker if MaxDuration is breached.
func (b *BudgetMonitor) RecordDuration(d time.Duration) error {
	if d <= 0 {
		return nil
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.duration += d

	if b.cfg.MaxDuration > 0 && b.duration > b.cfg.MaxDuration && !b.tripped {
		b.tripped = true
		b.tripReason = fmt.Sprintf("cumulative execution duration limit breached (%s > %s)", b.duration, b.cfg.MaxDuration)
		return fmt.Errorf("%w: %s", ErrDurationBudgetExceeded, b.tripReason)
	}

	return nil
}

// Trip manually trips the circuit breaker with a specified reason.
func (b *BudgetMonitor) Trip(reason string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.tripped = true
	b.tripReason = reason
}

// Reset clears the counters and untrips the circuit breaker.
func (b *BudgetMonitor) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.tokens = 0
	b.duration = 0
	b.tripped = false
	b.tripReason = ""
}

// IsTripped returns true if the circuit breaker has been tripped.
func (b *BudgetMonitor) IsTripped() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.tripped
}

// TripReason returns the reason why the circuit breaker was tripped.
func (b *BudgetMonitor) TripReason() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.tripReason
}

// CumulativeTokens returns the total tokens tracked.
func (b *BudgetMonitor) CumulativeTokens() int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.tokens
}

// CumulativeDuration returns the total execution duration tracked.
func (b *BudgetMonitor) CumulativeDuration() time.Duration {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.duration
}

// Snapshot returns an immutable point-in-time telemetry snapshot.
func (b *BudgetMonitor) Snapshot() BudgetSnapshot {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return BudgetSnapshot{
		CumulativeTokens:   b.tokens,
		MaxTokens:          b.cfg.MaxTokens,
		CumulativeDuration: b.duration,
		MaxDuration:        b.cfg.MaxDuration,
		Tripped:            b.tripped,
		TripReason:         b.tripReason,
	}
}

// ParseDurationString parses duration strings like "30s", "5m", "1h", "2h30m".
// Empty or "0" returns 0 without error.
func ParseDurationString(val string) (time.Duration, error) {
	val = strings.TrimSpace(val)
	if val == "" || val == "0" || strings.ToLower(val) == "unlimited" {
		return 0, nil
	}
	return time.ParseDuration(val)
}
