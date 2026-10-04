package server

import (
	"context"
	"errors"
	"io"
	"iter"
	"sync"
	"sync/atomic"

	"github.com/androiddrew/kokoro-run/internal/engine"
)

// Synthesizer streams one request's audio. *engine.Pipeline is one.
type Synthesizer interface {
	Stream(ctx context.Context, r engine.Request) iter.Seq2[engine.Chunk, error]
}

// Pool lends loaded synthesizers to requests: as many run at once as are
// loaded, and up to a fixed number wait for one. Replicas are added as they
// finish loading, so the server can listen while they load.
type Pool struct {
	replicas int
	admitted chan struct{}    // a token per running or waiting request
	idle     chan Synthesizer // loaded replicas not in use
	waiting  atomic.Int64     // admitted requests without a replica yet

	mu      sync.Mutex
	all     []Synthesizer
	failed  int
	loadErr error
	dead    chan struct{} // closed when every replica failed to load
}

// NewPool returns a pool for replicas synthesizers, with up to maxQueue
// requests waiting for one.
func NewPool(replicas, maxQueue int) *Pool {
	return &Pool{
		replicas: replicas,
		admitted: make(chan struct{}, replicas+maxQueue),
		idle:     make(chan Synthesizer, replicas),
		dead:     make(chan struct{}),
	}
}

// Add makes a loaded replica available.
func (p *Pool) Add(s Synthesizer) {
	p.mu.Lock()
	p.all = append(p.all, s)
	p.mu.Unlock()
	p.idle <- s
}

// Fail records that a replica couldn't be loaded. Once every replica has
// failed, waiting and new requests get ErrUnavailable.
func (p *Pool) Fail(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failed++
	p.loadErr = errors.Join(p.loadErr, err)
	if p.failed == p.replicas {
		close(p.dead)
	}
}

// Loaded is how many replicas are loaded.
func (p *Pool) Loaded() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.all)
}

// Waiting is how many admitted requests are waiting for a replica.
func (p *Pool) Waiting() int { return int(p.waiting.Load()) }

// ErrUnavailable means no replica could be loaded.
var ErrUnavailable = errors.New("the model could not be loaded")

var errQueueFull = errors.New("queue full")

// enter admits a request, or returns errQueueFull, then waits for a replica
// until ctx ends. On success the caller must call release when done.
func (p *Pool) enter(ctx context.Context) (s Synthesizer, release func(), err error) {
	select {
	case p.admitted <- struct{}{}:
	default:
		return nil, nil, errQueueFull
	}
	p.waiting.Add(1)
	defer p.waiting.Add(-1)
	select {
	case s = <-p.idle:
		return s, func() { p.idle <- s; <-p.admitted }, nil
	case <-p.dead:
		<-p.admitted
		p.mu.Lock()
		defer p.mu.Unlock()
		return nil, nil, errors.Join(ErrUnavailable, p.loadErr)
	case <-ctx.Done():
		<-p.admitted
		return nil, nil, ctx.Err()
	}
}

// Close closes every loaded replica that is an io.Closer. Call it only once
// no request is using the pool.
func (p *Pool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	var err error
	for _, s := range p.all {
		if c, ok := s.(io.Closer); ok {
			err = errors.Join(err, c.Close())
		}
	}
	p.all = nil
	return err
}
