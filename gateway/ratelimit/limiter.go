package ratelimit

import (
	"sync"
	"time"
)

// Limiter implements hybrid token bucket + sliding window rate limiting
type Limiter struct {
	shards []*shard
	numShards int
}

type shard struct {
	tokens    map[string]*clientState
	mu        sync.RWMutex
	refillRate int64 // tokens per minute
	burstSize  int64
}

type clientState struct {
	tokens     float64
	lastRefill int64 // Unix timestamp
	window     []int64 // sliding window timestamps
}

// NewLimiter creates a new rate limiter with sharding
func NewLimiter(numShards, defaultRate, burstSize int) *Limiter {
	shards := make([]*shard, numShards)
	for i := 0; i < numShards; i++ {
		shards[i] = &shard{
			tokens:     make(map[string]*clientState),
			refillRate: int64(defaultRate),
			burstSize:  int64(burstSize),
		}
	}
	return &Limiter{shards: shards, numShards: numShards}
}

// Allow checks if a request is allowed, returns (allowed, remaining, resetTime)
func (l *Limiter) Allow(clientKey string, customRate int) (bool, int64, int64) {
	shardIdx := l.getShardIdx(clientKey)
	shard := l.shards[shardIdx]

	if customRate > 0 {
		shard.refillRate = int64(customRate)
	}

	shard.mu.Lock()
	defer shard.mu.Unlock()

	state, exists := shard.tokens[clientKey]
	now := time.Now().Unix()

	if !exists {
		state = &clientState{
			tokens:     float64(shard.burstSize),
			lastRefill: now,
			window:     []int64{},
		}
		shard.tokens[clientKey] = state
	}

	// Clean old window entries (older than 1 minute)
	cutoff := now - 60
	keep := state.window[:0]
	for _, ts := range state.window {
		if ts >= cutoff {
			keep = append(keep, ts)
		}
	}
	state.window = keep
	
	// Refill tokens based on time elapsed
	timeElapsed := now - state.lastRefill
	if timeElapsed > 0 {
		tokensToAdd := float64(shard.refillRate) * float64(timeElapsed) / 60.0
		state.tokens = min(state.tokens+tokensToAdd, float64(shard.burstSize))
		state.lastRefill = now
	}

	// Check if we have tokens
	if state.tokens >= 1.0 {
		state.tokens -= 1.0
		state.window = append(state.window, now)
		
		remaining := int64(state.tokens)
		resetTime := now + 60 // reset in 1 minute
		
		return true, remaining, resetTime
	}

	// Calculate reset time based on token deficit
	remaining := int64(state.tokens)
	deficit := 1.0 - state.tokens
	secondsUntilReset := int64((deficit / float64(shard.refillRate)) * 60)
	resetTime := now + secondsUntilReset

	return false, remaining, resetTime
}

func (l *Limiter) getShardIdx(key string) int {
	hash := uint64(0)
	for _, c := range key {
		hash = hash*31 + uint64(c)
	}
	return int(hash) % l.numShards
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
