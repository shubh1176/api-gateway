package proxy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"gateway/cache"
	"gateway/circuitbreaker"
	"gateway/config"
	"gateway/metrics"
	"gateway/ratelimit"
)

// ProxyHandler handles HTTP requests
type ProxyHandler struct {
	client      *http.Client
	limiter    *ratelimit.Limiter
	breaker    *circuitbreaker.Breaker
	cache      *cache.Cache
	coalescer  *Coalescer
	collector  *metrics.Collector
	cfg        *config.Config
	cfgVersion int64
}

// NewProxyHandler creates a new proxy handler
func NewProxyHandler(cfg *config.Config, limiter *ratelimit.Limiter, 
	breaker *circuitbreaker.Breaker, cache *cache.Cache, 
	coalescer *Coalescer, collector *metrics.Collector) *ProxyHandler {
	
	transport := &http.Transport{
		MaxIdleConns:        cfg.ConnectionPool.MaxIdle,
		MaxIdleConnsPerHost: cfg.ConnectionPool.MaxIdle,
		IdleConnTimeout:     time.Duration(cfg.ConnectionPool.IdleTimeout) * time.Second,
	}

	return &ProxyHandler{
		client: &http.Client{
			Transport: transport,
			Timeout:   time.Duration(cfg.Timeouts.TotalSeconds) * time.Second,
		},
		limiter:   limiter,
		breaker:   breaker,
		cache:     cache,
		coalescer: coalescer,
		collector: collector,
		cfg:       cfg,
	}
}

// ServeHTTP handles HTTP requests
func (p *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	
	// Update configuration if needed
	cfg := config.GetConfig()
	if cfg != nil && cfg != p.cfg {
		p.cfg = cfg
	}

	// Find matching route
	route := p.findRoute(r.URL.Path, r.Method)
	if route == nil {
		http.Error(w, "Route not found", http.StatusNotFound)
		p.collector.RecordRequest(r.URL.Path, time.Since(start), http.StatusNotFound, false)
		return
	}

	// Rate limiting
	if p.cfg.RateLimit.Enabled {
		allowed, remaining, resetTime := p.limiter.Allow(p.getClientKey(r), route.RateLimit)
		if !allowed {
			w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", route.RateLimit))
			w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
			w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", resetTime))
			w.Header().Set("Retry-After", fmt.Sprintf("%d", resetTime-time.Now().Unix()))
			http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
			p.collector.RecordRateLimit()
			return
		}
	}

	// Check cache for GET requests
	fromCache := false
	if r.Method == http.MethodGet && route.EnableCache && p.cfg.Cache.Enabled {
		key := cache.Hash(r.Method, r.URL.Path, r.URL.RawQuery, nil)
		if cachedResp, ok := p.cache.Get(key); ok {
			// Serve from cache
			for k, v := range cachedResp.Headers {
				for _, val := range v {
					w.Header().Add(k, val)
				}
			}
			w.WriteHeader(cachedResp.StatusCode)
			w.Write(cachedResp.Body)
			p.collector.RecordRequest(route.Path, time.Since(start), cachedResp.StatusCode, true)
			return
		}
	}

	// Request coalescing for GET requests
	if r.Method == http.MethodGet {
		coalesceKey := fmt.Sprintf("%s:%s:%s", r.Method, r.URL.Path, r.URL.RawQuery)
		
		resp, err := p.coalescer.Do(coalesceKey, func() (*Response, error) {
			return p.forwardRequest(r, route)
		})
		
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			p.collector.RecordRequest(route.Path, time.Since(start), http.StatusBadGateway, fromCache)
			return
		}

		// Write response
		for k, v := range resp.Headers {
			for _, val := range v {
				w.Header().Add(k, val)
			}
		}
		w.WriteHeader(resp.StatusCode)
		w.Write(resp.Body)

		// Cache response if eligible
		if route.EnableCache && p.cfg.Cache.Enabled && resp.StatusCode == http.StatusOK {
			key := cache.Hash(r.Method, r.URL.Path, r.URL.RawQuery, nil)
			p.cache.Set(key, &cache.Response{
				StatusCode: resp.StatusCode,
				Headers:    resp.Headers,
				Body:       resp.Body,
			}, p.cfg.Cache.TTLSeconds)
		}

		p.collector.RecordRequest(route.Path, time.Since(start), resp.StatusCode, fromCache)
		return
	}

	// Non-GET requests: no coalescing
	resp, err := p.forwardRequest(r, route)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		p.collector.RecordRequest(route.Path, time.Since(start), http.StatusBadGateway, fromCache)
		return
	}

	// Write response
	for k, v := range resp.Headers {
		for _, val := range v {
			w.Header().Add(k, val)
		}
	}
	w.WriteHeader(resp.StatusCode)
	w.Write(resp.Body)

	p.collector.RecordRequest(route.Path, time.Since(start), resp.StatusCode, fromCache)
}

func (p *ProxyHandler) forwardRequest(r *http.Request, route *config.RouteConfig) (*Response, error) {
	backend, err := url.Parse(route.Backend)
	if err != nil {
		return nil, fmt.Errorf("invalid backend URL: %w", err)
	}

	// Create proxy request
	target := backend.ResolveReference(r.URL)
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), r.Body)
	if err != nil {
		return nil, err
	}

	// Copy headers
	for k, v := range r.Header {
		req.Header[k] = v
	}

	// Remove hop-by-hop headers
	req.Header.Del("Connection")
	req.Header.Del("Keep-Alive")
	req.Header.Del("Proxy-Authenticate")
	req.Header.Del("Proxy-Authorization")
	req.Header.Del("Upgrade")

	// Circuit breaker check
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(route.Timeout)*time.Second)
	defer cancel()

	var resp *http.Response
	if p.cfg.CircuitBreaker.Enabled {
		err = p.breaker.Execute(ctx, func() error {
			var err error
			resp, err = p.client.Do(req)
			return err
		})
	} else {
		resp, err = p.client.Do(req)
	}

	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Convert headers
	headers := make(map[string][]string)
	for k, v := range resp.Header {
		headers[k] = v
	}

	return &Response{
		StatusCode: resp.StatusCode,
		Headers:    headers,
		Body:       body,
	}, nil
}

func (p *ProxyHandler) findRoute(path, method string) *config.RouteConfig {
	for _, route := range p.cfg.Routes {
		if path == route.Path {
			// Check method
			for _, m := range route.Methods {
				if m == method {
					return &route
				}
			}
		}
	}
	return nil
}

func (p *ProxyHandler) getClientKey(r *http.Request) string {
	// Use IP address for key
	return r.RemoteAddr
}
