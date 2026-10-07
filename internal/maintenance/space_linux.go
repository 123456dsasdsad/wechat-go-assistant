package maintenance

import "syscall"

func availableSpace(path string) (int64, error) {
	var s syscall.Statfs_t
	e := syscall.Statfs(path, &s)
	return int64(s.Bavail) * int64(s.Bsize), e
}
