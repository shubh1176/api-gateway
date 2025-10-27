package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gateway/cache"
	"gateway/circuitbreaker"
	"gateway/config"
	"gateway/metrics"
	"gateway/proxy"
	"gateway/ratelimit"
)

func main() {
	// Load configuration
	cfg, err := config.LoadConfig("../config/gateway.json")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	config.SetConfig(cfg)

	// Watch for config changes
	if err := config.WatchConfig("../config/gateway.json"); err != nil {
		log.Printf("Warning: failed to watch config: %v", err)
	}

	// Initialize components
	limiter := ratelimit.NewLimiter(
		cfg.RateLimit.NumShards,
		cfg.RateLimit.DefaultRate,
		cfg.RateLimit.BurstSize,
	)

	breaker := circuitbreaker.NewBreaker(
		cfg.CircuitBreaker.FailureThreshold,
		cfg.CircuitBreaker.SuccessThreshold,
		cfg.CircuitBreaker.TimeoutSeconds,
		cfg.CircuitBreaker.HealthDecay,
	)

	c := cache.NewCache(1000, cfg.Cache.MaxSize)
	
	coalescer := proxy.NewCoalescer(60 * time.Second)
	
	collector := metrics.NewCollector()

	// Create proxy handler
	proxyHandler := proxy.NewProxyHandler(cfg, limiter, breaker, c, coalescer, collector)

	// Setup server
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		healthHandler(breaker)(w, r)
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		metricsHandler(collector, breaker)(w, r)
	})
	mux.Handle("/", proxyHandler)

	server := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      mux,
		ReadTimeout:  time.Duration(cfg.Timeouts.ReadSeconds) * time.Second,
		WriteTimeout: time.Duration(cfg.Timeouts.WriteSeconds) * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start server in goroutine
	go func() {
		log.Printf("Starting gateway on %s", cfg.ListenAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exited")
}

func healthHandler(breaker *circuitbreaker.Breaker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		status := "healthy"
		if breaker.GetState() == circuitbreaker.StateOpen {
			status = "degraded"
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":    status,
			"timestamp": time.Now().Unix(),
		})
	}
}

func metricsHandler(collector *metrics.Collector, breaker *circuitbreaker.Breaker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stats := collector.GetStats()
		breakerStats := breaker.Stats()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"metrics": stats,
			"circuit_breaker": breakerStats,
		})
	}
}
