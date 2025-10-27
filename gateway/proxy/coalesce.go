package proxy

import (
	"sync"
	"time"
)

// Coalescer deduplicates identical concurrent requests
type Coalescer struct {
	groups sync.Map // map[string]*CoalesceGroup
	ttl    time.Duration
}

// CoalesceGroup represents a group of requests waiting for the same response
type CoalesceGroup struct {
	mu       sync.Mutex
	waiters  []chan *Response
	response *Response
	done     bool
	created  time.Time
}

// Response wraps the actual response with metadata
type Response struct {
	StatusCode int
	Headers    map[string][]string
	Body       []byte
	Err        error
}

// NewCoalescer creates a new request coalescer
func NewCoalescer(ttl time.Duration) *Coalescer {
	return &Coalescer{ttl: ttl}
}

// Do coalesces requests with the same key
func (c *Coalescer) Do(key string, fn func() (*Response, error)) (*Response, error) {
	group, _ := c.groups.LoadOrStore(key, &CoalesceGroup{
		created: time.Now(),
		waiters: []chan *Response{},
	})

	g := group.(*CoalesceGroup)
	g.mu.Lock()

	if g.done {
		// Already completed, return immediately
		g.mu.Unlock()
		return g.response, g.response.Err
	}

	// Check if we're the first waiter (the one who will execute)
	shouldExecute := len(g.waiters) == 0
	if shouldExecute {
		// We're the executor
		g.waiters = append(g.waiters, make(chan *Response, 1))
		g.mu.Unlock()

		// Execute the function
		resp, err := fn()

		// Notify all waiters
		g.mu.Lock()
		g.done = true
		if resp == nil {
			resp = &Response{Err: err}
		} else {
			resp.Err = err
		}
		g.response = resp

		// Broadcast to all waiters
		for _, waiter := range g.waiters {
			waiter <- resp
			close(waiter)
		}
		g.mu.Unlock()

		// Clean up after TTL
		time.AfterFunc(c.ttl, func() {
			c.groups.Delete(key)
		})

		return resp, err
	}

	// We're a waiter
	waitChan := make(chan *Response, 1)
	g.waiters = append(g.waiters, waitChan)
	g.mu.Unlock()

	// Wait for the response
	select {
	case resp := <-waitChan:
		return resp, resp.Err
	case <-time.After(60 * time.Second):
		// Timeout
		return nil, &CoalesceTimeoutError{}
	}
}

// CoalesceTimeoutError indicates timeout waiting for coalesced response
type CoalesceTimeoutError struct{}

func (e *CoalesceTimeoutError) Error() string {
	return "request coalescing timeout"
}
