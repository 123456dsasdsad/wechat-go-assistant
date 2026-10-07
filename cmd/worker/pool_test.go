package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkerParallelismConfiguration(t *testing.T) {
	for _, value := range []int{0, 1, 4, 16} {
		got, err := workerParallelism(value)
		want := value
		if want == 0 {
			want = 4
		}
		if err != nil || got != want {
			t.Fatal(value, got, err)
		}
	}
	for _, value := range []int{-1, 17} {
		if _, err := workerParallelism(value); err == nil {
			t.Fatal("accepted invalid limit", value)
		}
	}
}

func TestWorkerPoolStartsAllSlotsAndJoinsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, 4)
	done := make(chan error, 1)
	var active, exited atomic.Int32
	go func() {
		done <- runWorkerPool(ctx, 4, func(ctx context.Context) error {
			active.Add(1)
			started <- struct{}{}
			<-ctx.Done()
			active.Add(-1)
			exited.Add(1)
			return nil
		})
	}()
	for i := 0; i < 4; i++ {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("pool serialized independent slots")
		}
	}
	if active.Load() != 4 {
		t.Fatal("wrong slot count", active.Load())
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("pool did not stop")
	}
	if active.Load() != 0 || exited.Load() != 4 {
		t.Fatal("pool returned before all slots stopped")
	}
}

func TestWorkerPoolFatalErrorCancelsOtherSlots(t *testing.T) {
	var entered, exited atomic.Int32
	failure := errors.New("fatal worker fixture")
	started := make(chan struct{}, 3)
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- runWorkerPool(context.Background(), 3, func(ctx context.Context) error {
			index := entered.Add(1)
			defer exited.Add(1)
			started <- struct{}{}
			if index == 1 {
				<-release
				return failure
			}
			<-ctx.Done()
			return nil
		})
	}()
	for i := 0; i < 3; i++ {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("slots not started")
		}
	}
	close(release)
	select {
	case err := <-done:
		if !errors.Is(err, failure) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("fatal error left workers running")
	}
	if exited.Load() != 3 {
		t.Fatal("fatal exit did not join all slots")
	}
}
