//go:build !windows

package ds4api

import "github.com/ebitengine/purego"

func openDynamicLibrary(path string) (uintptr, error) {
	return purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
}
