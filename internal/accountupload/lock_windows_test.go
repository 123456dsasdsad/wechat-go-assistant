//go:build windows

package accountupload

import "testing"

func TestMutationLockCannotBeHeldByAnotherThreadAndIsReleased(t *testing.T) {
	release, ok, e := MutationLock("")
	if e != nil || !ok {
		t.Fatal("initial lock failed", e)
	}
	result := make(chan bool, 1)
	go func() {
		other, acquired, e := MutationLock("")
		if acquired {
			other()
		}
		result <- e == nil && !acquired
	}()
	if !<-result {
		release()
		t.Fatal("maintenance namespace not exclusive")
	}
	release()
	release, ok, e = MutationLock("")
	if e != nil || !ok {
		t.Fatal("mutex not released", e)
	}
	release()
}
