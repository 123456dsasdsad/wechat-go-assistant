//go:build !windows

package accountupload

import (
	"os"
	"path/filepath"
	"syscall"
)

func MutationLock(root string) (func(), bool, error) {
	f, e := os.OpenFile(filepath.Join(root, ".account-import.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, false, e
	}
	e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if e == syscall.EWOULDBLOCK {
		f.Close()
		return nil, false, nil
	}
	if e != nil {
		f.Close()
		return nil, false, e
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, true, nil
}
