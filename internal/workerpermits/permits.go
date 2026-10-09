// Package workerpermits lets blocked human questions park a task without
// allowing more than the configured number of executing Codex turns.
package workerpermits

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sync"
)

type Pool struct {
	mu                    sync.Mutex
	limit, used, resumers int
	changed               chan struct{}
	tasks                 map[string]*Ticket
	URL, Key              string
}
type Ticket struct {
	pool                   *Pool
	held, closed, resuming bool
	job, lease             string
	questions              map[string]string
}

func New(limit int) *Pool {
	return &Pool{limit: limit, changed: make(chan struct{}), tasks: map[string]*Ticket{}}
}
func (p *Pool) wake() { close(p.changed); p.changed = make(chan struct{}) }
func (p *Pool) Acquire(ctx context.Context) (*Ticket, error) {
	for {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		p.mu.Lock()
		if p.used < p.limit && p.resumers == 0 {
			p.used++
			t := &Ticket{pool: p, held: true, questions: map[string]string{}}
			p.mu.Unlock()
			return t, nil
		}
		ch := p.changed
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ch:
		}
	}
}
func (t *Ticket) Bind(job, lease string) error {
	p := t.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	if t.closed || !t.held || job == "" || lease == "" {
		return errors.New("invalid_permit_binding")
	}
	if _, exists := p.tasks[job]; exists {
		return errors.New("task_already_bound")
	}
	t.job = job
	t.lease = lease
	p.tasks[job] = t
	return nil
}
func (t *Ticket) Pause(question string) error {
	p := t.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	if t.closed || question == "" || len(question) > 512 {
		return errors.New("invalid_permit_pause")
	}
	if _, ok := t.questions[question]; ok {
		return nil
	}
	if len(t.questions) >= 64 {
		return errors.New("too_many_permit_questions")
	}
	t.questions[question] = "paused"
	if t.resuming {
		t.resuming = false
		p.resumers--
	}
	if t.held {
		t.held = false
		p.used--
	}
	p.wake()
	return nil
}
func (t *Ticket) Resume(ctx context.Context, question string) error {
	p := t.pool
	for {
		if e := ctx.Err(); e != nil {
			return e
		}
		p.mu.Lock()
		if t.closed {
			p.mu.Unlock()
			return errors.New("stale_execution_permit")
		}
		if _, ok := t.questions[question]; !ok {
			p.mu.Unlock()
			return errors.New("unknown_permit_question")
		}
		changed := t.questions[question] != "resumed"
		t.questions[question] = "resumed"
		pending := false
		for _, state := range t.questions {
			if state == "paused" {
				pending = true
				break
			}
		}
		if !pending {
			if !t.held && !t.resuming {
				t.resuming = true
				p.resumers++
			}
			if t.held {
				p.mu.Unlock()
				return nil
			}
			if p.used < p.limit {
				p.used++
				t.held = true
				t.resuming = false
				p.resumers--
				p.wake()
				p.mu.Unlock()
				return nil
			}
		}
		if changed {
			p.wake()
		}
		ch := p.changed
		p.mu.Unlock()
		// A resume request can be retried after HTTP cancellation. The priority is
		// retained for the live ticket; Release is the task's authoritative cleanup.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ch:
		}
	}
}
func (t *Ticket) Release() {
	p := t.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	if t.closed {
		return
	}
	t.closed = true
	if t.held {
		p.used--
	}
	if t.resuming {
		p.resumers--
	}
	if p.tasks[t.job] == t {
		delete(p.tasks, t.job)
	}
	p.wake()
}

type operation struct {
	Job      string `json:"job"`
	Lease    string `json:"lease"`
	Question string `json:"question"`
	Action   string `json:"action"`
}

func (p *Pool) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" || r.URL.Path != "/permit" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+p.Key)) != 1 {
		http.Error(w, "unauthorized", 401)
		return
	}
	var v operation
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if d.Decode(&v) != nil {
		http.Error(w, "invalid", 400)
		return
	}
	p.mu.Lock()
	t, ok := p.tasks[v.Job]
	if ok && t.lease != v.Lease {
		ok = false
	}
	p.mu.Unlock()
	if !ok {
		http.Error(w, "stale_execution_permit", 409)
		return
	}
	var e error
	switch v.Action {
	case "pause":
		e = t.Pause(v.Question)
	case "resume":
		e = t.Resume(r.Context(), v.Question)
	default:
		e = errors.New("invalid_permit_action")
	}
	if e != nil {
		http.Error(w, "permit_operation_failed", 409)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}
func (p *Pool) Start(ctx context.Context) (func(), error) {
	var key [32]byte
	if _, e := rand.Read(key[:]); e != nil {
		return nil, e
	}
	p.Key = hex.EncodeToString(key[:])
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return nil, e
	}
	p.URL = "http://" + ln.Addr().String()
	server := &http.Server{Handler: p, ReadHeaderTimeout: 3e9}
	done := make(chan struct{})
	go func() { defer close(done); server.Serve(ln) }()
	go func() {
		select {
		case <-ctx.Done():
			server.Close()
		case <-done:
		}
	}()
	return func() { server.Close(); <-done }, nil
}
