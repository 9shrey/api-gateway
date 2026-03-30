package circuitbreaker

import (
	"errors"
	"log/slog"
	"sync"
	"time"
)

// State represents the current state of the circuit breaker.
type State int

const (
	StateClosed   State = iota // normal operation — requests flow through
	StateOpen                  // circuit tripped — requests fail fast
	StateHalfOpen              // testing recovery — limited requests allowed
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// ErrCircuitOpen is returned when the circuit breaker is open and
// requests are being rejected without reaching the backend.
var ErrCircuitOpen = errors.New("circuit breaker is open")

// Config holds the circuit breaker parameters.
type Config struct {
	// FailureThreshold is the number of consecutive failures before opening the circuit.
	FailureThreshold int

	// SuccessThreshold is the number of consecutive successes in half-open state
	// required to close the circuit again.
	SuccessThreshold int

	// OpenTimeout is how long the circuit stays open before transitioning to half-open.
	OpenTimeout time.Duration
}

// DefaultConfig returns sensible defaults for a circuit breaker.
func DefaultConfig() Config {
	return Config{
		FailureThreshold: 5,
		SuccessThreshold: 2,
		OpenTimeout:      30 * time.Second,
	}
}

// CircuitBreaker implements the circuit breaker pattern.
// It tracks failures per backend and opens the circuit when the failure
// threshold is reached, preventing requests to unhealthy backends.
//
// State transitions:
//
//	Closed → Open:     when consecutive failures >= FailureThreshold
//	Open → Half-Open:  after OpenTimeout elapses
//	Half-Open → Closed: when consecutive successes >= SuccessThreshold
//	Half-Open → Open:  on any failure
type CircuitBreaker struct {
	cfg Config

	mu                sync.RWMutex
	state             State
	consecutiveFailures int
	consecutiveSuccesses int
	lastFailureTime   time.Time
	name              string // for logging (usually the backend URL)
}

// New creates a new circuit breaker with the given config and name.
func New(name string, cfg Config) *CircuitBreaker {
	return &CircuitBreaker{
		cfg:   cfg,
		state: StateClosed,
		name:  name,
	}
}

// Allow checks if a request is allowed through the circuit breaker.
// Returns nil if the request can proceed, ErrCircuitOpen if it should be rejected.
func (cb *CircuitBreaker) Allow() error {
	cb.mu.RLock()
	state := cb.state
	lastFailure := cb.lastFailureTime
	cb.mu.RUnlock()

	switch state {
	case StateClosed:
		return nil

	case StateOpen:
		// Check if the open timeout has elapsed → transition to half-open
		if time.Since(lastFailure) > cb.cfg.OpenTimeout {
			cb.mu.Lock()
			// Double-check after acquiring write lock
			if cb.state == StateOpen {
				cb.state = StateHalfOpen
				cb.consecutiveSuccesses = 0
				slog.Info("circuit breaker → half-open",
					"backend", cb.name,
				)
			}
			cb.mu.Unlock()
			return nil
		}
		return ErrCircuitOpen

	case StateHalfOpen:
		// Allow limited requests through to test recovery
		return nil
	}

	return nil
}

// RecordSuccess records a successful request to the backend.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.consecutiveFailures = 0

	switch cb.state {
	case StateHalfOpen:
		cb.consecutiveSuccesses++
		if cb.consecutiveSuccesses >= cb.cfg.SuccessThreshold {
			cb.state = StateClosed
			cb.consecutiveSuccesses = 0
			slog.Info("circuit breaker → closed (recovered)",
				"backend", cb.name,
			)
		}
	case StateClosed:
		// Already healthy, nothing to do
	}
}

// RecordFailure records a failed request to the backend.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.lastFailureTime = time.Now()
	cb.consecutiveSuccesses = 0

	switch cb.state {
	case StateClosed:
		cb.consecutiveFailures++
		if cb.consecutiveFailures >= cb.cfg.FailureThreshold {
			cb.state = StateOpen
			slog.Warn("circuit breaker → open (threshold reached)",
				"backend", cb.name,
				"failures", cb.consecutiveFailures,
			)
		}

	case StateHalfOpen:
		// Any failure in half-open sends us back to open
		cb.state = StateOpen
		cb.consecutiveFailures = cb.cfg.FailureThreshold // reset to threshold
		slog.Warn("circuit breaker → open (half-open test failed)",
			"backend", cb.name,
		)
	}
}

// State returns the current circuit breaker state.
func (cb *CircuitBreaker) State() State {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}
