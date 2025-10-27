package circuitbreaker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// State represents circuit breaker state
type State int

const (
	StateClosed State = iota
	StateOpen
	StateHalfOpen
)

// Breaker implements a circuit breaker pattern with health scoring
type Breaker struct {
	state           atomic.Value // State
	failureCount    atomic.Int64
	successCount    atomic.Int64
	lastFailureTime atomic.Int64
	failureThreshold int
	successThreshold int
	timeoutSeconds   int64
	health          *HealthTracker
	mu              sync.RWMutex
}

// NewBreaker creates a new circuit breaker
func NewBreaker(failureThreshold, successThreshold, timeoutSeconds int, healthDecay float64) *Breaker {
	b := &Breaker{
		failureThreshold: failureThreshold,
		successThreshold: successThreshold,
		timeoutSeconds:   int64(timeoutSeconds),
		health:          NewHealthTracker(healthDecay),
	}
	b.state.Store(StateClosed)
	return b
}

// Execute executes a function through the circuit breaker
func (b *Breaker) Execute(ctx context.Context, fn func() error) error {
	if !b.Allow() {
		return errors.New("circuit breaker is open")
	}

	err := fn()
	
	if err != nil {
		b.RecordFailure()
	} else {
		b.RecordSuccess()
	}

	return err
}

// Allow checks if requests are allowed
func (b *Breaker) Allow() bool {
	state := b.state.Load().(State)
	
	switch state {
	case StateClosed:
		return true
	case StateOpen:
		if time.Now().Unix() >= b.lastFailureTime.Load()+b.timeoutSeconds {
			// Timeout expired, try half-open
			if b.state.CompareAndSwap(StateOpen, StateHalfOpen) {
				b.successCount.Store(0)
			}
			return true
		}
		return false
	case StateHalfOpen:
		return true
	default:
		return false
	}
}

// RecordSuccess records a successful request
func (b *Breaker) RecordSuccess() {
	b.health.RecordSuccess()
	success := b.successCount.Add(1)

	if b.state.Load().(State) == StateHalfOpen {
		if int(success) >= b.successThreshold {
			b.state.Store(StateClosed)
			b.failureCount.Store(0)
		}
	}
}

// RecordFailure records a failed request
func (b *Breaker) RecordFailure() {
	b.health.RecordFailure()
	failures := b.failureCount.Add(1)
	b.lastFailureTime.Store(time.Now().Unix())

	if int(failures) >= b.failureThreshold {
		b.state.Store(StateOpen)
	}
}

// GetState returns current state
func (b *Breaker) GetState() State {
	return b.state.Load().(State)
}

// GetHealthScore returns current health score
func (b *Breaker) GetHealthScore() int {
	return b.health.GetScore()
}

// Stats returns breaker statistics
func (b *Breaker) Stats() map[string]interface{} {
	state := b.state.Load().(State)
	stateStr := "closed"
	switch state {
	case StateOpen:
		stateStr = "open"
	case StateHalfOpen:
		stateStr = "half-open"
	}

	return map[string]interface{}{
		"state":        stateStr,
		"health_score": b.health.GetScore(),
		"failures":     b.failureCount.Load(),
		"successes":    b.successCount.Load(),
	}
}
