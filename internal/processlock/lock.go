// Package processlock provides a kernel-owned singleton lock. A stale pathname
// after a crash is harmless; the kernel releases ownership on process exit.
package processlock

import (
	"os"
	"sync"
)

func Acquire(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = lock(f); err != nil {
		f.Close()
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { unlock(f); f.Close() }) }, nil
}
