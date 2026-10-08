package main

import (
	"context"
	"time"
)

func watchCancellation(ctx context.Context, cancel context.CancelFunc, poll func() (bool, error)) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if stop, e := poll(); e == nil && stop {
				cancel()
				return
			}
		}
	}
}
