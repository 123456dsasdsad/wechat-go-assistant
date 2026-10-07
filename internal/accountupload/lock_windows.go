//go:build windows

package accountupload

import (
	"errors"
	"runtime"
	"syscall"
	"unsafe"
)

var kernel = syscall.NewLazyDLL("kernel32.dll")
var createMutex = kernel.NewProc("CreateMutexW")
var waitMutex = kernel.NewProc("WaitForSingleObject")
var releaseMutex = kernel.NewProc("ReleaseMutex")
var closeHandle = kernel.NewProc("CloseHandle")

// MutationLock shares the native maintenance runner's mutex across processes.
func MutationLock(_ string) (func(), bool, error) {
	// Windows mutex ownership belongs to the calling OS thread.
	runtime.LockOSThread()
	name, _ := syscall.UTF16PtrFromString(`Global\CampusStackMaintenance`)
	handle, _, _ := createMutex.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if handle == 0 {
		runtime.UnlockOSThread()
		return nil, false, errors.New("account_mutation_lock_unavailable")
	}
	state, _, _ := waitMutex.Call(handle, 0)
	if state == 258 {
		closeHandle.Call(handle)
		runtime.UnlockOSThread()
		return nil, false, nil
	}
	if state != 0 && state != 128 {
		closeHandle.Call(handle)
		runtime.UnlockOSThread()
		return nil, false, errors.New("account_mutation_lock_failed")
	}
	return func() { releaseMutex.Call(handle); closeHandle.Call(handle); runtime.UnlockOSThread() }, true, nil
}
