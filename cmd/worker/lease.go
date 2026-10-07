package main

import (
	"context"
	"time"
)

func maintainLease(ctx context.Context, cancel context.CancelFunc, renew func() (int, error)) {
	timer := time.NewTicker(30 * time.Second)
	defer timer.Stop()
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			status, err := renew()
			if err == nil {
				failures = 0
				continue
			}
			failures++
			if status == 409 || failures >= 6 {
				cancel()
				return
			}
		}
	}
}
