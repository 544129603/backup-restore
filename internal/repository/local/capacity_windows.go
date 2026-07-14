//go:build windows

package local

import "golang.org/x/sys/windows"

func availableBytes(root string) (int64, bool, error) {
	path, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return 0, false, err
	}
	var freeAvailable, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(path, &freeAvailable, &total, &totalFree); err != nil {
		return 0, false, err
	}
	if freeAvailable > uint64(^uint64(0)>>1) {
		return int64(^uint64(0) >> 1), true, nil
	}
	return int64(freeAvailable), true, nil
}
