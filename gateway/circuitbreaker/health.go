package circuitbreaker

import (
	"sync"
	"sync/atomic"
)

// HealthTracker tracks backend health using exponential moving average
type HealthTracker struct {
	healthScore atomic.Int64 // 0-100 scale
	alpha       float64      // EMA decay factor
	mu          sync.Mutex
}

// NewHealthTracker creates a new health tracker
func NewHealthTracker(decay float64) *HealthTracker {
	ht := &HealthTracker{alpha: decay}
	ht.healthScore.Store(100) // Start healthy
	return ht
}

// RecordSuccess records a successful request
func (ht *HealthTracker) RecordSuccess() {
	ht.updateHealth(10)
}

// RecordFailure records a failed request
func (ht *HealthTracker) RecordFailure() {
	ht.updateHealth(-20)
}

// RecordTimeout records a timeout
func (ht *HealthTracker) RecordTimeout() {
	ht.updateHealth(-50)
}

// GetScore returns current health score (0-100)
func (ht *HealthTracker) GetScore() int {
	return int(ht.healthScore.Load())
}

func (ht *HealthTracker) updateHealth(delta int) {
	current := float64(ht.healthScore.Load())
	// Exponential moving average update
	newScore := current*ht.alpha + float64(delta)*(1-ht.alpha)
	
	// Clamp to [0, 100]
	if newScore < 0 {
		newScore = 0
	} else if newScore > 100 {
		newScore = 100
	}
	
	ht.healthScore.Store(int64(newScore))
}

// Reset resets the health score to 100
func (ht *HealthTracker) Reset() {
	ht.healthScore.Store(100)
}
