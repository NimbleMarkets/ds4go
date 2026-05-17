package ds4api

import (
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

var (
	callbackMu        sync.Mutex
	callbackID        uintptr = 1
	tokenCallbacks            = map[uintptr]TokenEmitFunc{}
	doneCallbacks             = map[uintptr]GenerationDoneFunc{}
	progressCallbacks         = map[uintptr]ProgressFunc{}

	tokenEmitCallback = purego.NewCallback(func(ud uintptr, token int32) {
		callbackMu.Lock()
		fn := tokenCallbacks[ud]
		callbackMu.Unlock()
		if fn != nil {
			fn(int(token))
		}
	})
	doneCallback = purego.NewCallback(func(ud uintptr) {
		callbackMu.Lock()
		fn := doneCallbacks[ud]
		callbackMu.Unlock()
		if fn != nil {
			fn()
		}
	})
	progressCallback = purego.NewCallback(func(ud uintptr, event unsafe.Pointer, current int32, total int32) {
		callbackMu.Lock()
		fn := progressCallbacks[ud]
		callbackMu.Unlock()
		if fn != nil {
			fn(goString(event), int(current), int(total))
		}
	})
)

func nextCallbackID() uintptr {
	callbackID++
	if callbackID == 0 {
		callbackID = 1
	}
	return callbackID
}

func registerTokenCallback(fn TokenEmitFunc) uintptr {
	if fn == nil {
		return 0
	}
	callbackMu.Lock()
	defer callbackMu.Unlock()
	id := nextCallbackID()
	tokenCallbacks[id] = fn
	return id
}

func unregisterTokenCallback(id uintptr) {
	if id == 0 {
		return
	}
	callbackMu.Lock()
	delete(tokenCallbacks, id)
	callbackMu.Unlock()
}

func registerDoneCallback(fn GenerationDoneFunc) uintptr {
	if fn == nil {
		return 0
	}
	callbackMu.Lock()
	defer callbackMu.Unlock()
	id := nextCallbackID()
	doneCallbacks[id] = fn
	return id
}

func unregisterDoneCallback(id uintptr) {
	if id == 0 {
		return
	}
	callbackMu.Lock()
	delete(doneCallbacks, id)
	callbackMu.Unlock()
}

func registerProgressCallback(fn ProgressFunc) uintptr {
	if fn == nil {
		return 0
	}
	callbackMu.Lock()
	defer callbackMu.Unlock()
	id := nextCallbackID()
	progressCallbacks[id] = fn
	return id
}

func unregisterProgressCallback(id uintptr) {
	if id == 0 {
		return
	}
	callbackMu.Lock()
	delete(progressCallbacks, id)
	callbackMu.Unlock()
}
