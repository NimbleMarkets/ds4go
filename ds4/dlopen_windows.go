//go:build windows

package ds4

import "syscall"

func openDynamicLibrary(path string) (uintptr, error) {
	dll, err := syscall.LoadDLL(path)
	if err != nil {
		return 0, err
	}
	return uintptr(dll.Handle), nil
}
