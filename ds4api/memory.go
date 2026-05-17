package ds4api

import (
	"errors"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

const errBufSize = 4096

func cStringPointer(s string) ([]byte, unsafe.Pointer) {
	if s == "" {
		return nil, nil
	}
	b := append([]byte(s), 0)
	return b, unsafe.Pointer(&b[0])
}

func goString(ptr unsafe.Pointer) string {
	if ptr == nil {
		return ""
	}
	var n int
	for {
		if *(*byte)(unsafe.Add(ptr, n)) == 0 {
			break
		}
		n++
	}
	return string(unsafe.Slice((*byte)(ptr), n))
}

func errorBuffer() ([]byte, unsafe.Pointer, uintptr) {
	buf := make([]byte, errBufSize)
	return buf, unsafe.Pointer(&buf[0]), uintptr(len(buf))
}

func errorFromBuffer(op string, code int32, buf []byte) error {
	if code == 0 {
		return nil
	}
	msg := ""
	for i, b := range buf {
		if b == 0 {
			msg = string(buf[:i])
			break
		}
	}
	if msg == "" {
		return ds4Error(op, code)
	}
	return errors.New(op + ": " + msg)
}

type cRuntime struct {
	once   sync.Once
	err    error
	handle uintptr
	fopen  func(path string, mode string) uintptr
	fclose func(fp uintptr) int32
	malloc func(uintptr) unsafe.Pointer
	free   func(ptr unsafe.Pointer)
}

var libc cRuntime

func loadCRuntime() error {
	libc.once.Do(func() {
		handle, err := openCRuntime()
		if err != nil {
			libc.err = err
			return
		}
		libc.handle = handle
		purego.RegisterLibFunc(&libc.fopen, handle, "fopen")
		purego.RegisterLibFunc(&libc.fclose, handle, "fclose")
		purego.RegisterLibFunc(&libc.malloc, handle, "malloc")
		purego.RegisterLibFunc(&libc.free, handle, "free")
	})
	return libc.err
}

func cMalloc(size uintptr) unsafe.Pointer {
	if err := loadCRuntime(); err != nil {
		return nil
	}
	return libc.malloc(size)
}

func cFree(ptr unsafe.Pointer) {
	if ptr == nil {
		return
	}
	if err := loadCRuntime(); err == nil && libc.free != nil {
		libc.free(ptr)
	}
}

func cRuntimeName() string {
	switch runtime.GOOS {
	case "darwin":
		return "/usr/lib/libSystem.B.dylib"
	case "windows":
		return "msvcrt.dll"
	default:
		return "libc.so.6"
	}
}

func openCRuntime() (uintptr, error) {
	return openDynamicLibrary(cRuntimeName())
}

// File is an opaque C FILE* used by ds4 APIs that accept FILE pointers.
type File uintptr

// OpenFile opens a C FILE* with fopen for ds4 FILE*-based APIs.
func OpenFile(path, mode string) (File, error) {
	if err := loadCRuntime(); err != nil {
		return 0, err
	}
	fp := libc.fopen(path, mode)
	if fp == 0 {
		return 0, errors.New("ds4: fopen failed")
	}
	return File(fp), nil
}

// Close closes a C FILE* opened by OpenFile.
func (f File) Close() error {
	if f == 0 {
		return nil
	}
	if err := loadCRuntime(); err != nil {
		return err
	}
	if rc := libc.fclose(uintptr(f)); rc != 0 {
		return ds4Error("fclose", rc)
	}
	return nil
}
