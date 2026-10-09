package main

import (
	"context"
	"errors"
)

func workerParallelism(value int) (int, error) {
	if value == 0 {
		return 4, nil
	}
	if value < 1 || value > 16 {
		return 0, errors.New("invalid_worker_parallelism")
	}
	return value, nil
}

// One process owns the thread store; every slot runs an independent claim loop.
// Fatal failures cancel and join all slots before the service can restart.
func runWorkerPool(parent context.Context, slots int, loop func(context.Context) error) error {
	if slots < 1 || slots > 16 {
		return errors.New("invalid_worker_parallelism")
	}
	return runElasticWorkerPool(parent, slots, loop)
}
func runElasticWorkerPool(parent context.Context, slots int, loop func(context.Context) error) error {
	if slots < 1 || slots > 48 {
		return errors.New("invalid_worker_pool_capacity")
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	results := make(chan error, slots)
	for i := 0; i < slots; i++ {
		go func() {
			err := loop(ctx)
			if err != nil {
				cancel()
			}
			results <- err
		}()
	}
	var first error
	for i := 0; i < slots; i++ {
		if err := <-results; first == nil && err != nil {
			first = err
		}
	}
	return first
}
