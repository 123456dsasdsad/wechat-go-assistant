package files

import (
	"errors"
	"math"
	"syscall"
	"unsafe"
)

var freeDisk = syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")

func availableDisk(path string) (int64, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var available uint64
	ok, _, err := freeDisk.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&available)), 0, 0)
	if ok == 0 {
		return 0, errors.New("disk_space_unavailable")
	}
	if available > math.MaxInt64 {
		return math.MaxInt64, nil
	}
	return int64(available), nil
}
