//go:build windows

package models

import "golang.org/x/sys/windows"

// availableBytes reports the free space on path's volume that is usable by the
// calling user. GetDiskFreeSpaceEx's first output is the caller-available
// figure, which already accounts for any disk quota, unlike the volume-wide
// total that follows it.
func availableBytes(path string) (uint64, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var freeToCaller, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(pathPtr, &freeToCaller, &total, &free); err != nil {
		return 0, err
	}
	return freeToCaller, nil
}
