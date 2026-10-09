package workerpermits

import (
	"context"
	"testing"
	"time"
)

func TestWaitingTaskReleasesAndReacquiresExecutionSlot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	p := New(1)
	a, e := p.Acquire(ctx)
	if e != nil {
		t.Fatal(e)
	}
	a.Bind("a", "lease-a")
	if e = a.Pause("question"); e != nil {
		t.Fatal(e)
	}
	if e = a.Pause("question"); e != nil {
		t.Fatal(e)
	}
	b, e := p.Acquire(ctx)
	if e != nil {
		t.Fatal(e)
	}
	b.Bind("b", "lease-b")
	resumed := make(chan error, 1)
	go func() { resumed <- a.Resume(ctx, "question") }()
	select {
	case e := <-resumed:
		t.Fatal("A resumed while B executes", e)
	case <-time.After(40 * time.Millisecond):
	}
	b.Release()
	select {
	case e := <-resumed:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("resume blocked")
	}
	p.mu.Lock()
	used := p.used
	p.mu.Unlock()
	if used != 1 {
		t.Fatal("permit accounting", used)
	}
	a.Release()
	if e = a.Pause("new"); e == nil {
		t.Fatal("stale ticket accepted")
	}
}
func TestParallelQuestionsDoNotResumeUntilAllAnswered(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	p := New(1)
	a, _ := p.Acquire(ctx)
	a.Bind("a", "lease")
	a.Pause("q1")
	a.Pause("q2")
	defer a.Release()
	result := make(chan error, 1)
	go func() { result <- a.Resume(ctx, "q1") }()
	select {
	case <-result:
		t.Fatal("unanswered q2 bypassed")
	case <-time.After(30 * time.Millisecond):
	}
	if e := a.Resume(ctx, "q2"); e != nil {
		t.Fatal(e)
	}
	if e := <-result; e != nil {
		t.Fatal(e)
	}
}
func TestCancelWakesBlockedResumeAndAcquire(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := New(1)
	a, _ := p.Acquire(ctx)
	a.Bind("a", "lease")
	a.Pause("q")
	b, _ := p.Acquire(ctx)
	defer b.Release()
	result := make(chan error, 1)
	go func() { result <- a.Resume(ctx, "q") }()
	time.Sleep(20 * time.Millisecond)
	a.Release()
	select {
	case e := <-result:
		if e == nil {
			t.Fatal("closed ticket resumed")
		}
	case <-time.After(time.Second):
		t.Fatal("release failed to wake")
	}
	cancel()
	if _, e := p.Acquire(ctx); e == nil {
		t.Fatal("canceled acquire accepted")
	}
}
