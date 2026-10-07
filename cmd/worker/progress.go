package main

import (
	"context"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"sync"
	"time"
)

// The sampler never waits for HTTP. Full snapshots coalesce delta bursts and
// retries retain their sequence so a timeout cannot duplicate or reorder text.
type relayProgress struct {
	mu        sync.Mutex
	latest    jobs.ProgressUpdate
	sent      uint64
	transport *relaySteering
	parent    context.Context
	cancel    context.CancelFunc
	done      chan struct{}
}

func newRelayProgress(parent context.Context, relay, key string, task jobs.Task) *relayProgress {
	ctx, cancel := context.WithCancel(parent)
	p := &relayProgress{latest: jobs.ProgressUpdate{ID: task.ID, Lease: task.Lease}, transport: &relaySteering{url: relay, key: key, task: task}, parent: parent, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(p.done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.send(ctx)
			}
		}
	}()
	return p
}
func (p *relayProgress) Publish(text string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(text) > 256<<10 || text == p.latest.Text {
		return
	}
	p.latest.Sequence++
	p.latest.Text = text
}
func (p *relayProgress) send(parent context.Context) {
	p.mu.Lock()
	snapshot := p.latest
	sent := p.sent
	p.mu.Unlock()
	if snapshot.Sequence == 0 || snapshot.Sequence <= sent {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	_, e := p.transport.call(ctx, "/jobs/progress", snapshot, nil)
	if e == nil {
		p.mu.Lock()
		if snapshot.Sequence > p.sent {
			p.sent = snapshot.Sequence
		}
		p.mu.Unlock()
	}
}
func (p *relayProgress) Close() { p.cancel(); <-p.done; p.send(p.parent) }
