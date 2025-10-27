package metrics

import (
	"sync"
	"sync/atomic"
	"time"
)

// Collector collects and aggregates performance metrics
type Collector struct {
	// Request metrics
	totalRequests   atomic.Int64
	totalErrors     atomic.Int64
	cacheHits       atomic.Int64
	cacheMisses     atomic.Int64
	rateLimitHits   atomic.Int64
	
	// Latency metrics (in microseconds)
	latencySum      atomic.Int64
	latencyCount    atomic.Int64
	latencyMin      atomic.Int64
	latencyMax      atomic.Int64
	
	// Throughput
	currentRPS      atomic.Int64
	peakRPS         atomic.Int64
	
	// Status code counts
	status2xx       atomic.Int64
	status3xx       atomic.Int64
	status4xx       atomic.Int64
	status5xx       atomic.Int64
	
	// Timestamps
	startTime time.Time
	
	// Histograms
	mu                sync.RWMutex
	latencyHistogram  []int64 // Buckets: 0-1ms, 1-5ms, 5-10ms, 10-50ms, 50-100ms, 100ms+
	
	// Route-specific metrics
	routeMetrics     map[string]*RouteMetrics
	routeMetricsMu   sync.RWMutex
}

// RouteMetrics tracks per-route metrics
type RouteMetrics struct {
	Requests    int64
	Errors      int64
	AvgLatency  float64
	LatencySum  int64
}

// NewCollector creates a new metrics collector
func NewCollector() *Collector {
	return &Collector{
		startTime:         time.Now(),
		latencyHistogram:  make([]int64, 6),
		routeMetrics:      make(map[string]*RouteMetrics),
	}
}

// RecordRequest records a request with latency
func (c *Collector) RecordRequest(route string, latency time.Duration, statusCode int, fromCache bool) {
	// Update counters
	c.totalRequests.Add(1)
	
	if fromCache {
		c.cacheHits.Add(1)
	} else {
		c.cacheMisses.Add(1)
	}
	
	// Update status code counts
	switch {
	case statusCode >= 200 && statusCode < 300:
		c.status2xx.Add(1)
	case statusCode >= 300 && statusCode < 400:
		c.status3xx.Add(1)
	case statusCode >= 400 && statusCode < 500:
		c.status4xx.Add(1)
	case statusCode >= 500:
		c.status5xx.Add(1)
		c.totalErrors.Add(1)
	}
	
	// Update latency metrics
	latencyMicros := latency.Microseconds()
	c.updateLatencyMetrics(latencyMicros)
	
	// Update histogram
	c.updateHistogram(latencyMicros)
	
	// Update route-specific metrics
	c.updateRouteMetrics(route, latencyMicros)
}

// RecordRateLimit records a rate-limited request
func (c *Collector) RecordRateLimit() {
	c.rateLimitHits.Add(1)
}

// RecordCacheEvent records a cache hit/miss
func (c *Collector) RecordCacheEvent(isHit bool) {
	if isHit {
		c.cacheHits.Add(1)
	} else {
		c.cacheMisses.Add(1)
	}
}

func (c *Collector) updateLatencyMetrics(latencyMicros int64) {
	c.latencySum.Add(latencyMicros)
	c.latencyCount.Add(1)
	
	// Update min atomically
	current := c.latencyMin.Load()
	if current == 0 || latencyMicros < current {
		c.latencyMin.CompareAndSwap(current, latencyMicros)
	}
	
	// Update max atomically
	currentMax := c.latencyMax.Load()
	if latencyMicros > currentMax {
		c.latencyMax.CompareAndSwap(currentMax, latencyMicros)
	}
}

func (c *Collector) updateHistogram(latencyMicros int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	// 0-1ms, 1-5ms, 5-10ms, 10-50ms, 50-100ms, 100ms+
	millis := latencyMicros / 1000
	switch {
	case millis < 1:
		c.latencyHistogram[0]++
	case millis < 5:
		c.latencyHistogram[1]++
	case millis < 10:
		c.latencyHistogram[2]++
	case millis < 50:
		c.latencyHistogram[3]++
	case millis < 100:
		c.latencyHistogram[4]++
	default:
		c.latencyHistogram[5]++
	}
}

func (c *Collector) updateRouteMetrics(route string, latencyMicros int64) {
	c.routeMetricsMu.Lock()
	defer c.routeMetricsMu.Unlock()
	
	rm, exists := c.routeMetrics[route]
	if !exists {
		rm = &RouteMetrics{}
		c.routeMetrics[route] = rm
	}
	
	rm.Requests++
	rm.LatencySum += latencyMicros
	rm.AvgLatency = float64(rm.LatencySum) / float64(rm.Requests)
}

// GetStats returns current statistics
func (c *Collector) GetStats() map[string]interface{} {
	c.mu.RLock()
	c.routeMetricsMu.RLock()
	defer c.mu.RUnlock()
	defer c.routeMetricsMu.RUnlock()
	
	count := c.latencyCount.Load()
	avgLatency := float64(0)
	if count > 0 {
		avgLatency = float64(c.latencySum.Load()) / float64(count) / 1000.0 // Convert to ms
	}
	
	total := c.totalRequests.Load()
	cacheHits := c.cacheHits.Load()
	cacheHitRate := float64(0)
	if total > 0 {
		cacheHitRate = float64(cacheHits) / float64(total)
	}
	
	uptime := time.Since(c.startTime).Seconds()
	
	return map[string]interface{}{
		"uptime_seconds": uptime,
		"total_requests": total,
		"total_errors":   c.totalErrors.Load(),
		"error_rate":     float64(c.totalErrors.Load()) / float64(total),
		"cache_hit_rate": cacheHitRate,
		"rate_limit_hits": c.rateLimitHits.Load(),
		"latency_ms": map[string]interface{}{
			"avg":  avgLatency,
			"min":  float64(c.latencyMin.Load()) / 1000.0,
			"max":  float64(c.latencyMax.Load()) / 1000.0,
		},
		"latency_histogram": map[string]int64{
			"0-1ms":   c.latencyHistogram[0],
			"1-5ms":   c.latencyHistogram[1],
			"5-10ms":  c.latencyHistogram[2],
			"10-50ms": c.latencyHistogram[3],
			"50-100ms": c.latencyHistogram[4],
			"100ms+":  c.latencyHistogram[5],
		},
		"status_codes": map[string]int64{
			"2xx": c.status2xx.Load(),
			"3xx": c.status3xx.Load(),
			"4xx": c.status4xx.Load(),
			"5xx": c.status5xx.Load(),
		},
		"current_rps": c.currentRPS.Load(),
		"peak_rps":    c.peakRPS.Load(),
	}
}
