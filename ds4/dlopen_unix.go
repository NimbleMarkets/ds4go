//go:build !windows

package ds4

import "github.com/ebitengine/purego"

func openDynamicLibrary(path string) (uintptr, error) {
	return purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
}
