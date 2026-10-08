package files

import "errors"

// This is free space kept for the OS and existing services, not a file size cap.
const ReserveDiskBytes int64 = 512 << 20

// DiskBudget exposes actual free space after the reserve to durable library storage.
func DiskBudget(path string) (int64, error) { return diskBudget(path) }

func diskBudget(path string) (int64, error) {
	available, err := availableDisk(path)
	if err != nil || available <= ReserveDiskBytes {
		return 0, errors.New("attachment_storage_full")
	}
	return available - ReserveDiskBytes, nil
}
