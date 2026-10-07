package files

import "errors"

// This is free space kept for the OS and existing services, not a file size cap.
const ReserveDiskBytes int64 = 512 << 20

func diskBudget(path string) (int64, error) {
	available, err := availableDisk(path)
	if err != nil || available <= ReserveDiskBytes {
		return 0, errors.New("attachment_storage_full")
	}
	return available - ReserveDiskBytes, nil
}
