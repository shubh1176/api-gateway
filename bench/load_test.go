package main

import (
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	url := "http://localhost:8080/api/users"
	concurrency := 100
	duration := 10 * time.Second

	var totalRequests int64
	var totalErrors int64
	var totalLatency int64

	fmt.Printf("Load testing %s with concurrency=%d for %v\n", url, concurrency, duration)
	fmt.Println("Starting load test...")

	var wg sync.WaitGroup
	start := time.Now()
	deadline := start.Add(duration)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := &http.Client{Timeout: 10 * time.Second}

			for {
				if time.Now().After(deadline) {
					return
				}

				reqStart := time.Now()
				resp, err := client.Get(url)
				latency := time.Since(reqStart)

				atomic.AddInt64(&totalRequests, 1)
				atomic.AddInt64(&totalLatency, latency.Microseconds())

				if err != nil || resp.StatusCode >= 400 {
					atomic.AddInt64(&totalErrors, 1)
				}

				if resp != nil {
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}
			}
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)

	requests := atomic.LoadInt64(&totalRequests)
	errors := atomic.LoadInt64(&totalErrors)
	latencyTotal := atomic.LoadInt64(&totalLatency)

	reqPerSec := float64(requests) / elapsed.Seconds()
	avgLatency := float64(latencyTotal) / float64(requests) / 1000.0 // Convert to ms

	fmt.Printf("\nResults:\n")
	fmt.Printf("  Duration: %v\n", elapsed)
	fmt.Printf("  Total Requests: %d\n", requests)
	fmt.Printf("  Errors: %d (%.2f%%)\n", errors, float64(errors)/float64(requests)*100)
	fmt.Printf("  Requests/sec: %.0f\n", reqPerSec)
	fmt.Printf("  Avg Latency: %.2f ms\n", avgLatency)
}
